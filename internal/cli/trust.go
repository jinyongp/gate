package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"text/tabwriter"

	"gate/internal/ca"
	"gate/internal/config"
	"gate/internal/expose"
	"gate/internal/paths"
	portx "gate/internal/port"
	"gate/internal/proxy"
	"gate/internal/registry"
	"gate/internal/ui"
)

var (
	trustAuthorityFunc   = func(authority *ca.CA) error { return authority.Trust() }
	untrustAuthorityFunc = func(authority *ca.CA) error { return authority.Untrust() }
	exposeProviderFor    = expose.For
	exposeSessionMu      sync.Mutex
	exposeSessionRoutes  = map[string]map[string]exposeSessionRoute{}
)

var tailscaleServePortPool = portx.Pool{Min: 10443, Max: 10999}

type exposeSessionRoute struct {
	Auth string
}

// Trust installs the root CA into the OS and browser trust stores.
func Trust(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("trust", flag.ContinueOnError)
	if handled, code := parseNoArgFlags(fs, "trust", args, stdout, stderr); handled {
		return code
	}
	unlock, code := acquireStateMutation(stderr, false)
	if code != ExitOK {
		return code
	}
	defer unlock()
	activity := startActivity(stderr, false, "preparing trust store")
	authority, err := ca.Load(paths.DataDir())
	if err != nil {
		activity.Stop()
		return fail(stderr, false, ExitError, "ca", err.Error())
	}
	activity.Complete("prepared trust store")
	if err := trustAuthorityFunc(authority); err != nil {
		if os.IsPermission(err) {
			return fail(stderr, false, ExitPerm, "permission", err.Error())
		}
		return fail(stderr, false, ExitError, "trust", err.Error())
	}
	printSuccess(stdout, "root CA trusted")
	printKV(stdout, "fingerprint", authority.Fingerprint())
	return ExitOK
}

// Untrust removes the root CA from the OS and browser trust stores.
func Untrust(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("untrust", flag.ContinueOnError)
	if handled, code := parseNoArgFlags(fs, "untrust", args, stdout, stderr); handled {
		return code
	}
	unlock, code := acquireStateMutation(stderr, false)
	if code != ExitOK {
		return code
	}
	defer unlock()
	activity := startActivity(stderr, false, "preparing trust store")
	authority, err := ca.LoadCertificate(paths.DataDir())
	if errors.Is(err, ca.ErrNotFound) {
		activity.Stop()
		printInfo(stdout, "root CA not found; nothing to untrust")
		return ExitOK
	}
	if err != nil {
		activity.Stop()
		return fail(stderr, false, ExitError, "ca", err.Error())
	}
	activity.Complete("prepared trust store")
	if err := untrustAuthorityFunc(authority); err != nil {
		if os.IsPermission(err) {
			return fail(stderr, false, ExitPerm, "permission", err.Error())
		}
		return fail(stderr, false, ExitError, "untrust", err.Error())
	}
	printSuccess(stdout, "root CA untrusted")
	printKV(stdout, "fingerprint", authority.Fingerprint())
	return ExitOK
}

// Ca dispatches `gate ca export`.
func Ca(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		sp := specFor("ca")
		WriteHelp(stdout, "ca", sp.Args, sp.Summary, nil)
		return ExitOK
	}
	if len(args) == 0 || args[0] != "export" {
		usageLine(stderr, "ca")
		return ExitUsage
	}
	fs := flag.NewFlagSet("ca export", flag.ContinueOnError)
	out := fs.String("out", "gate-root.crt", "output path")
	if handled, code := parseNoArgFlags(fs, "ca export", args[1:], stdout, stderr); handled {
		return code
	}
	authority, err := ca.Load(paths.DataDir())
	if err != nil {
		return fail(stderr, false, ExitError, "ca", err.Error())
	}
	fp, err := authority.Export(*out)
	if err != nil {
		return fail(stderr, false, ExitError, "export", err.Error())
	}
	printSuccess(stdout, "exported "+*out)
	printKV(stdout, "sha256", fp)
	return ExitOK
}

// Expose publishes a service beyond this machine via a provider.
func Expose(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "ls":
			return exposeLs(args[1:], stdout, stderr)
		case "stop":
			return exposeStop(args[1:], stdout, stderr)
		}
	}
	fs := flag.NewFlagSet("expose", flag.ContinueOnError)
	via := fs.String("via", "local", "provider: local|lan|cloudflared|tailscale")
	domain := fs.String("domain", "", "LAN .local domain override")
	auth := fs.String("auth", "", "require basic auth as user:pass")
	noAuth := fs.Bool("no-auth", false, "expose cloudflared without basic auth")
	jsonOut := fs.Bool("json", false, "emit JSON")
	scopeFlags := defineDaemonScopeFlags(fs, false)
	if handled, code := parseFlags(fs, "expose", args, stdout, stderr); handled {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return usageFail(stderr, *jsonOut, "expose")
	}
	providerName := normalizeExposeProvider(*via)
	routeAuth, err := exposeRouteAuth(providerName, *auth, *noAuth)
	if err != nil {
		return fail(stderr, *jsonOut, ExitUsage, "bad_auth", err.Error())
	}
	unlock, code := acquireStateMutation(stderr, *jsonOut)
	if code != ExitOK {
		return code
	}
	defer unlock()
	sel, err := registryScopeFromFlags(scopeFlags, false)
	if err != nil {
		return fail(stderr, *jsonOut, ExitUsage, "bad_scope", err.Error())
	}
	svc := rest[0]
	res, lerr := lookupScopedReservation(svc, sel)
	if lerr != nil {
		return fail(stderr, *jsonOut, lerr.Exit, lerr.Code, lerr.Message)
	}
	if !res.Active {
		return fail(stderr, *jsonOut, ExitError, "not_active", fmt.Sprintf("reservation %q is not active; run gate up first", svc))
	}
	exposeDomain, err := exposeDomainForProvider(providerName, res.Domain, *domain)
	if err != nil {
		return fail(stderr, *jsonOut, ExitUsage, "bad_domain", err.Error())
	}
	ref := listenerRefFor(res.ListenerPair())
	if providerName == expose.ProviderLAN && exposeDomain != res.Domain {
		reg, rerr := registryStore().Read()
		if rerr != nil {
			return fail(stderr, *jsonOut, ExitError, "registry", rerr.Error())
		}
		records, rerr := exposureStore().Read()
		if rerr != nil {
			return fail(stderr, *jsonOut, ExitError, "expose_store", rerr.Error())
		}
		if err := validateLANAliasAvailable(ref, reg, records, exposeDomain, res.Domain); err != nil {
			return fail(stderr, *jsonOut, ExitConflict, "domain_conflict", err.Error())
		}
	}
	servePort, err := exposeServePort(providerName, res)
	if err != nil {
		return fail(stderr, *jsonOut, ExitConflict, "port_conflict", err.Error())
	}
	provider, err := exposeProviderFor(providerName)
	if err != nil {
		return fail(stderr, *jsonOut, ExitUsage, "bad_provider", err.Error())
	}
	previousRecords, err := exposureStore().Read()
	if err != nil {
		_ = provider.Close()
		return fail(stderr, *jsonOut, ExitError, "expose_store", err.Error())
	}
	client := daemonClientForRef(ref)
	external := externalExposureProvider(providerName)
	listenerStatus, listenerStatusErr := client.Status()
	if external && listenerStatusErr != nil {
		_ = provider.Close()
		return fail(stderr, *jsonOut, ExitError, "daemon_not_running", "listener daemon is not running; run gate up -d first")
	}
	httpsAddr := ref.Pair.HTTPSAddr
	if listenerStatusErr == nil && listenerStatus.HTTPSAddr != "" {
		httpsAddr = listenerStatus.HTTPSAddr
	}
	if providerName == expose.ProviderLAN && !lanListenerReachable(httpsAddr) {
		_ = provider.Close()
		return fail(stderr, *jsonOut, ExitConflict, "listener_not_reachable", "LAN exposure requires a non-loopback HTTPS listener; restart the daemon with --https-addr :443 or a LAN address")
	}
	originURL := proxyURL(res.Domain, httpsAddr)
	publicURL := proxyURL(exposeDomain, httpsAddr)
	if external {
		for _, existing := range previousRecords {
			if !expose.SameKey(existing, expose.Record{Scope: exposureScope(res), Project: res.Project, Service: res.Service, Provider: providerName}) {
				continue
			}
			if existing.Pending != "" {
				_ = provider.Close()
				return fail(stderr, *jsonOut, ExitConflict, "exposure_pending", fmt.Sprintf("exposure has an incomplete %s transition; run %s, then retry", existing.Pending, exposureStopCommand(existing)))
			}
			status, statusErr := provider.Status(context.Background(), existing)
			if statusErr != nil {
				_ = provider.Close()
				return fail(stderr, *jsonOut, ExitError, "provider", statusErr.Error())
			}
			if status != expose.StatusDown {
				if providerName == expose.ProviderLAN && config.CanonicalDomain(exposurePublicHost(existing)) != config.CanonicalDomain(exposeDomain) {
					_ = provider.Close()
					return fail(stderr, *jsonOut, ExitConflict, "already_exposed", "existing LAN exposure uses a different domain; stop it before changing --domain")
				}
				return refreshExistingExposure(provider, existing, previousRecords, res, ref, routeAuth, svc, stdout, stderr, *jsonOut)
			}
			break
		}
	}
	provisional := expose.Record{
		Scope:       exposureScope(res),
		Project:     res.Project,
		Service:     res.Service,
		Provider:    providerName,
		PublicURL:   publicURL,
		Target:      res.Domain,
		OriginURL:   originURL,
		AuthEnabled: routeAuth != "",
		ServePort:   servePort,
	}
	txn := exposureTransaction{
		ref:             ref,
		previousRecords: previousRecords,
		stderr:          stderr,
		jsonOut:         *jsonOut,
	}
	if external {
		provisional.Pending = "start"
		if err := exposureStore().Upsert(provisional); err != nil {
			_ = provider.Close()
			return fail(stderr, *jsonOut, ExitError, "expose_store", err.Error())
		}
		txn.storeChanged = true
		txn.previousSession = snapshotExposeSession(ref.String())
		txn.sessionChanged = true
		applyExposeSession(ref.String(), nil, res.Domain, routeAuth)
		txn.routesChanged = true
		preRecords := upsertExposureRecord(previousRecords, provisional)
		if err := reloadExposureRoutesForRef(ref, preRecords, true, stderr, *jsonOut, "applying exposure policy", "applied exposure policy"); err != nil {
			if rollbackErr := txn.rollback(); rollbackErr != nil {
				_ = provider.Close()
				return fail(stderr, *jsonOut, ExitError, "rollback_failed", "expose failed and rollback failed: "+rollbackErr.Error())
			}
			_ = provider.Close()
			return fail(stderr, *jsonOut, ExitError, "reload_failed", err.Error())
		}
	}
	if routeAuth == "" && providerName != expose.ProviderLocal && !*jsonOut {
		printWarning(stderr, "exposing without --auth; anyone with the URL can reach your dev server")
	}
	var activity activityHandle
	if exposeActivityAllowed(providerName) {
		activity = startActivity(stderr, *jsonOut, "starting tunnel")
	}
	result, err := provider.Expose(context.Background(), exposeDomain, expose.Opts{
		Auth:      routeAuth,
		TargetURL: exposeTargetURL(providerName, originURL),
		PublicURL: publicURL,
		ServePort: servePort,
		OnStarted: func(started expose.Result) error {
			if !external {
				return nil
			}
			owned := provisional
			owned.PID = started.PID
			owned.Command = started.Command
			return exposureStore().Upsert(owned)
		},
	})
	if activity != nil {
		if err != nil {
			activity.Stop()
		} else {
			activity.Complete("started tunnel")
		}
	}
	if err != nil {
		cleanupExposureProvider(provider, provisional)
		if rollbackErr := txn.rollback(); rollbackErr != nil {
			return fail(stderr, *jsonOut, ExitError, "rollback_failed", "expose failed and rollback failed: "+rollbackErr.Error())
		}
		return fail(stderr, *jsonOut, ExitError, "expose_failed", err.Error())
	}

	record := provisional
	record.Pending = ""
	record.PublicURL = result.URL
	record.PID = result.PID
	record.Command = result.Command
	if err := exposureStore().Upsert(record); err != nil {
		cleanupExposureProvider(provider, record)
		if rollbackErr := txn.rollback(); rollbackErr != nil {
			return fail(stderr, *jsonOut, ExitError, "rollback_failed", "expose failed and rollback failed: "+rollbackErr.Error())
		}
		return fail(stderr, *jsonOut, ExitError, "expose_store", err.Error())
	}
	txn.storeChanged = true
	if client.IsRunning() {
		txn.routesChanged = true
		finalRecords := upsertExposureRecord(previousRecords, record)
		if err := reloadExposureRoutesForRef(ref, finalRecords, external, stderr, *jsonOut, "reloading routes", "reloaded routes"); err != nil {
			cleanupExposureProvider(provider, record)
			if rollbackErr := txn.rollback(); rollbackErr != nil {
				return fail(stderr, *jsonOut, ExitError, "rollback_failed", "expose failed and rollback failed: "+rollbackErr.Error())
			}
			return fail(stderr, *jsonOut, ExitError, "reload_failed", err.Error())
		}
	}

	if *jsonOut {
		out := map[string]any{"service": svc, "provider": providerName, "public_url": result.URL, "target": res.Domain}
		if res.Project != "" {
			out["project"] = res.Project
		} else {
			out["global"] = true
		}
		return writeJSON(stdout, out)
	}
	printSuccess(stdout, fmt.Sprintf("%s exposed via %s", displayReservationOwner(res), providerName))
	printKV(stdout, result.URL, res.Domain)
	return ExitOK
}

type exposeRow struct {
	Scope      string `json:"scope"`
	Project    string `json:"project,omitempty"`
	Service    string `json:"service"`
	Provider   string `json:"provider"`
	PublicURL  string `json:"public_url"`
	Target     string `json:"target"`
	Auth       bool   `json:"auth"`
	AuthStatus string `json:"auth_status"`
	Status     string `json:"status"`
}

func exposeLs(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("expose ls", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit JSON")
	via := fs.String("via", "", "filter provider")
	scopeFlags := defineDaemonScopeFlags(fs, true)
	if handled, code := parseNoArgFlags(fs, "expose ls", args, stdout, stderr); handled {
		return code
	}
	sel, err := registryScopeFromFlags(scopeFlags, true)
	if err != nil {
		return fail(stderr, *jsonOut, ExitUsage, "bad_scope", err.Error())
	}
	preserveLegacyProjectSelector(&sel, scopeFlags)
	if *via != "" && !validExposeProvider(*via) {
		return fail(stderr, *jsonOut, ExitUsage, "bad_provider", fmt.Sprintf("unknown provider %q", *via))
	}
	records, err := exposureStore().Read()
	if err != nil {
		return fail(stderr, *jsonOut, ExitError, "expose_store", err.Error())
	}
	reg, err := registryStore().Read()
	if err != nil {
		return fail(stderr, *jsonOut, ExitError, "registry", err.Error())
	}
	rows := make([]exposeRow, 0, len(records))
	for _, record := range records {
		if *via != "" && record.Provider != *via {
			continue
		}
		if !exposureRecordMatchesScope(record, sel) {
			continue
		}
		provider, err := exposeProviderFor(record.Provider)
		status := expose.StatusDown
		if err == nil {
			if got, serr := provider.Status(context.Background(), record); serr == nil {
				status = got
			}
		}
		rows = append(rows, exposeRow{
			Scope: record.Scope, Project: record.Project, Service: record.Service,
			Provider: record.Provider, PublicURL: record.PublicURL, Target: record.Target,
			Auth: record.AuthEnabled, AuthStatus: exposureAuthStatus(record, reg), Status: status,
		})
	}
	if *jsonOut {
		return writeJSON(stdout, map[string]any{"exposures": rows})
	}
	if len(rows) == 0 {
		printEmpty(stdout, "No exposures yet.", "No exposures.")
		return ExitOK
	}
	if richOut(stdout, false) {
		headers := []string{"SERVICE", "STATUS", "PROVIDER", "PUBLIC URL", "TARGET", "SCOPE", "AUTH"}
		data := make([][]string, 0, len(rows))
		for _, row := range rows {
			data = append(data, []string{
				row.Service, row.Status, row.Provider, row.PublicURL, row.Target, row.Scope, row.AuthStatus,
			})
		}
		fmt.Fprintln(stdout, ui.Render(headers, data))
		return ExitOK
	}
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SERVICE\tSTATUS\tPROVIDER\tPUBLIC URL\tTARGET\tSCOPE\tAUTH")
	for _, row := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", row.Service, row.Status, row.Provider, row.PublicURL, row.Target, row.Scope, row.AuthStatus)
	}
	_ = tw.Flush()
	return ExitOK
}

func exposureAuthStatus(record expose.Record, reg *registry.Registry) string {
	if !record.AuthEnabled {
		return "off"
	}
	res, ok := exposureRecordReservation(record, reg)
	if !ok {
		return "missing"
	}
	ref := listenerRefFor(res.ListenerPair())
	if routeAuthActive(ref, record.Target) {
		return "active"
	}
	exposeSessionMu.Lock()
	defer exposeSessionMu.Unlock()
	if session, ok := exposeSessionRoutes[ref.String()][record.Target]; ok && session.Auth != "" {
		return "active"
	}
	return "missing"
}

func routeAuthActive(ref listenerDaemonRef, domain string) bool {
	routes, err := daemonClientForRef(ref).Routes()
	if err != nil {
		return false
	}
	for _, route := range routes {
		if route.Domain == domain {
			return route.Auth
		}
	}
	return false
}

func exposureRecordReservation(record expose.Record, reg *registry.Registry) (registry.Reservation, bool) {
	project := ""
	if record.Scope == daemonScopeProject {
		project = record.Project
	}
	return reg.Get(registry.Key(project, record.Service))
}

func exposeStop(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("expose stop", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit JSON")
	via := fs.String("via", "", "provider")
	force := fs.Bool("force", false, "forget stale record")
	scopeFlags := defineDaemonScopeFlags(fs, false)
	if handled, code := parseFlags(fs, "expose stop", args, stdout, stderr); handled {
		return code
	}
	if len(fs.Args()) != 1 {
		return usageFail(stderr, *jsonOut, "expose stop")
	}
	unlock, code := acquireStateMutation(stderr, *jsonOut)
	if code != ExitOK {
		return code
	}
	defer unlock()
	sel, err := registryScopeFromFlags(scopeFlags, false)
	if err != nil {
		return fail(stderr, *jsonOut, ExitUsage, "bad_scope", err.Error())
	}
	preserveLegacyProjectSelector(&sel, scopeFlags)
	if *via != "" && !validExposeProvider(*via) {
		return fail(stderr, *jsonOut, ExitUsage, "bad_provider", fmt.Sprintf("unknown provider %q", *via))
	}
	service := fs.Args()[0]
	records, err := exposureStore().Read()
	if err != nil {
		return fail(stderr, *jsonOut, ExitError, "expose_store", err.Error())
	}
	var matches []expose.Record
	for _, record := range records {
		if record.Service != service || (*via != "" && record.Provider != *via) {
			continue
		}
		if exposureRecordMatchesScope(record, sel) {
			matches = append(matches, record)
		}
	}
	if len(matches) == 0 {
		return fail(stderr, *jsonOut, ExitError, "not_found", "no exposure record found")
	}
	if len(matches) > 1 && *via == "" {
		return fail(stderr, *jsonOut, ExitUsage, "ambiguous", "multiple providers match; pass --via")
	}
	record := matches[0]
	provider, err := exposeProviderFor(record.Provider)
	if err != nil {
		return fail(stderr, *jsonOut, ExitError, "provider", err.Error())
	}
	status, _ := provider.Status(context.Background(), record)
	skipProviderStop := status == expose.StatusDown && (*force || record.Pending != "")
	nextRecords := removeExposureRecordsAffectedByStop(records, record)
	pendingRecords := append([]expose.Record(nil), records...)
	for i := range pendingRecords {
		if expose.SameKey(pendingRecords[i], record) {
			pendingRecords[i].Pending = "stop"
		}
	}
	if err := exposureStore().Write(pendingRecords); err != nil {
		return fail(stderr, *jsonOut, ExitError, "expose_store", err.Error())
	}
	// Remove every route reachable by the provider before asking it to stop.
	// This keeps a slow or failing provider shutdown fail-closed.
	if err := reloadExposureRecordsTransitionBlocked(records, records, exposureStopBlockedDomains(records, record), stderr, *jsonOut); err != nil {
		_ = exposureStore().Write(records)
		return fail(stderr, *jsonOut, ExitError, "reload_failed", err.Error())
	}
	if !skipProviderStop {
		if err := provider.Stop(context.Background(), record, expose.StopOpts{Force: *force}); err != nil {
			storeErr := exposureStore().Write(records)
			if rollbackErr := reloadExposureRecordsTransition(records, records, stderr, *jsonOut); rollbackErr != nil {
				return fail(stderr, *jsonOut, ExitError, "rollback_failed", "provider stop failed and route rollback failed: "+rollbackErr.Error())
			}
			if storeErr != nil {
				return fail(stderr, *jsonOut, ExitError, "rollback_failed", "provider stop failed and exposure state rollback failed: "+storeErr.Error())
			}
			return fail(stderr, *jsonOut, ExitError, "stop_failed", err.Error())
		}
	}
	if err := exposureStore().Write(nextRecords); err != nil {
		return fail(stderr, *jsonOut, ExitError, "expose_store", err.Error())
	}
	if err := reloadExposureRecordsTransition(records, nextRecords, stderr, *jsonOut); err != nil {
		return fail(stderr, *jsonOut, ExitError, "reload_failed", err.Error())
	}
	if *jsonOut {
		return writeJSON(stdout, map[string]any{"removed": true, "service": service, "provider": record.Provider})
	}
	printSuccess(stdout, fmt.Sprintf("stopped exposure %s via %s", service, record.Provider))
	return ExitOK
}

func exposeActivityAllowed(via string) bool {
	return via == expose.ProviderCloudflared || via == expose.ProviderTailscale
}

func exposeRouteAuth(via, userpass string, noAuth bool) (string, error) {
	if noAuth && via != expose.ProviderCloudflared {
		return "", fmt.Errorf("--no-auth is only supported with --via cloudflared")
	}
	if userpass != "" && noAuth {
		return "", fmt.Errorf("--auth and --no-auth are mutually exclusive")
	}
	if via == expose.ProviderLocal && userpass != "" {
		return "", fmt.Errorf("--auth is not supported with --via local")
	}
	if userpass != "" {
		return proxy.NormalizeBasicAuth(userpass)
	}
	if via == expose.ProviderCloudflared && !noAuth {
		return "", fmt.Errorf("--via cloudflared requires --auth user:pass or --no-auth")
	}
	return "", nil
}

func exposeDomainForProvider(via, primary, override string) (string, error) {
	primary = config.CanonicalDomain(primary)
	override = config.CanonicalDomain(override)
	if override != "" {
		if via != expose.ProviderLAN {
			return "", fmt.Errorf("--domain is only supported with --via lan")
		}
		if err := config.ValidateDomain(override); err != nil {
			return "", err
		}
		if !strings.HasSuffix(override, ".local") {
			return "", fmt.Errorf("--domain for --via lan must end in .local")
		}
		return override, nil
	}
	if via == expose.ProviderLAN {
		derived := deriveLANDomain(primary)
		if err := config.ValidateDomain(derived); err != nil {
			return "", err
		}
		return derived, nil
	}
	return primary, nil
}

func exposeTargetURL(via, originURL string) string {
	switch via {
	case expose.ProviderCloudflared:
		return originURL
	case expose.ProviderTailscale:
		return strings.Replace(originURL, "https://", "https+insecure://", 1)
	default:
		return ""
	}
}

func lanListenerReachable(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if host == "" {
		return true
	}
	if zone := strings.LastIndexByte(host, '%'); zone >= 0 {
		host = host[:zone]
	}
	if strings.EqualFold(host, "localhost") {
		return false
	}
	ip := net.ParseIP(host)
	return ip == nil || (!ip.IsLoopback() && !ip.IsUnspecified())
}

func exposeServePort(via string, res registry.Reservation) (int, error) {
	if via != expose.ProviderTailscale {
		return 0, nil
	}
	records, err := exposureStore().Read()
	if err != nil {
		return 0, err
	}
	match := expose.Record{
		Scope:    exposureScope(res),
		Project:  res.Project,
		Service:  res.Service,
		Provider: expose.ProviderTailscale,
	}
	reserved := map[int]bool{80: true, 443: true}
	for _, record := range records {
		if record.Provider != expose.ProviderTailscale {
			continue
		}
		if !expose.SameKey(record, match) {
			return 0, fmt.Errorf("tailscale exposure already exists for %s; stop it before exposing another service", record.Service)
		}
		if record.ServePort > 0 {
			return record.ServePort, nil
		}
	}
	for _, record := range records {
		if record.ServePort <= 0 {
			continue
		}
		reserved[record.ServePort] = true
	}
	reg, err := registryStore().Read()
	if err != nil {
		return 0, err
	}
	for p := range reg.UsedPorts() {
		reserved[p] = true
	}
	p, err := portx.Allocate(tailscaleServePortPool, reserved)
	if err != nil {
		return 0, fmt.Errorf("no free tailscale serve port: %w", err)
	}
	return p, nil
}

func deriveLANDomain(primary string) string {
	primary = config.CanonicalDomain(primary)
	switch {
	case strings.HasSuffix(primary, ".local"):
		return primary
	case strings.HasSuffix(primary, ".localhost"):
		return strings.TrimSuffix(primary, ".localhost") + ".local"
	default:
		return primary + ".local"
	}
}

func validateLANAliasAvailable(ref listenerDaemonRef, reg *registry.Registry, records []expose.Record, alias, target string) error {
	alias = config.CanonicalDomain(alias)
	target = config.CanonicalDomain(target)
	if alias == target {
		return nil
	}
	for _, route := range activeRoutesForListener(reg, ref.Pair) {
		domain := config.CanonicalDomain(route.Domain)
		if domain == alias && domain != target {
			return fmt.Errorf("LAN domain %q conflicts with active route %q", alias, route.Domain)
		}
	}
	for _, record := range records {
		publicHost := config.CanonicalDomain(exposurePublicHost(record))
		if publicHost == "" || publicHost != alias {
			continue
		}
		res, ok := exposureRecordReservation(record, reg)
		if !ok || listenerRefFor(res.ListenerPair()).String() != ref.String() {
			continue
		}
		if config.CanonicalDomain(record.Target) != target {
			return fmt.Errorf("LAN domain %q conflicts with exposure for %q", alias, record.Target)
		}
	}
	return nil
}

func normalizeExposeProvider(via string) string {
	if via == "" {
		return expose.ProviderLocal
	}
	return via
}

func validExposeProvider(via string) bool {
	switch via {
	case expose.ProviderLocal, expose.ProviderLAN, expose.ProviderCloudflared, expose.ProviderTailscale:
		return true
	default:
		return false
	}
}

func externalExposureProvider(via string) bool {
	switch via {
	case expose.ProviderLAN, expose.ProviderCloudflared, expose.ProviderTailscale:
		return true
	default:
		return false
	}
}

type exposureTransaction struct {
	ref             listenerDaemonRef
	previousRecords []expose.Record
	previousSession map[string]exposeSessionRoute
	sessionChanged  bool
	routesChanged   bool
	storeChanged    bool
	stderr          io.Writer
	jsonOut         bool
}

func refreshExistingExposure(provider expose.Provider, existing expose.Record, previousRecords []expose.Record, res registry.Reservation, ref listenerDaemonRef, routeAuth, service string, stdout, stderr io.Writer, jsonOut bool) int {
	defer func() { _ = provider.Close() }()
	if existing.Target != res.Domain {
		return fail(stderr, jsonOut, ExitConflict, "already_exposed", "existing exposure targets a different route; stop it before exposing again")
	}
	updated := existing
	updated.AuthEnabled = routeAuth != ""
	if routeAuth == "" && existing.Provider != expose.ProviderLocal && !jsonOut {
		printWarning(stderr, "exposing without --auth; anyone with the URL can reach your dev server")
	}
	txn := exposureTransaction{
		ref:             ref,
		previousRecords: previousRecords,
		previousSession: snapshotExposeSession(ref.String()),
		sessionChanged:  true,
		routesChanged:   true,
		stderr:          stderr,
		jsonOut:         jsonOut,
	}
	applyExposeSession(ref.String(), nil, res.Domain, routeAuth)
	nextRecords := upsertExposureRecord(previousRecords, updated)
	if err := reloadExposureRoutesForRef(ref, nextRecords, true, stderr, jsonOut, "refreshing exposure policy", "refreshed exposure policy"); err != nil {
		if rollbackErr := txn.rollback(); rollbackErr != nil {
			return fail(stderr, jsonOut, ExitError, "rollback_failed", "expose refresh failed and rollback failed: "+rollbackErr.Error())
		}
		return fail(stderr, jsonOut, ExitError, "reload_failed", err.Error())
	}
	if err := exposureStore().Upsert(updated); err != nil {
		if rollbackErr := txn.rollback(); rollbackErr != nil {
			return fail(stderr, jsonOut, ExitError, "rollback_failed", "expose refresh failed and rollback failed: "+rollbackErr.Error())
		}
		return fail(stderr, jsonOut, ExitError, "expose_store", err.Error())
	}
	if jsonOut {
		out := map[string]any{"service": service, "provider": existing.Provider, "public_url": existing.PublicURL, "target": res.Domain, "refreshed": true}
		if res.Project != "" {
			out["project"] = res.Project
		} else {
			out["global"] = true
		}
		return writeJSON(stdout, out)
	}
	printSuccess(stdout, fmt.Sprintf("%s exposure refreshed via %s", displayReservationOwner(res), existing.Provider))
	printKV(stdout, existing.PublicURL, res.Domain)
	return ExitOK
}

func (t exposureTransaction) rollback() error {
	var errs []error
	if t.storeChanged {
		if err := exposureStore().Write(t.previousRecords); err != nil {
			errs = append(errs, fmt.Errorf("restore exposure store: %w", err))
		}
	}
	if t.sessionChanged {
		restoreExposeSession(t.ref.String(), t.previousSession)
	}
	if t.routesChanged {
		if err := reloadExposureRoutesForRef(t.ref, t.previousRecords, false, t.stderr, t.jsonOut, "restoring routes", "restored routes"); err != nil {
			errs = append(errs, fmt.Errorf("restore routes: %w", err))
		}
	}
	return errors.Join(errs...)
}

func reloadExposureRoutesForRef(ref listenerDaemonRef, records []expose.Record, requireRunning bool, stderr io.Writer, jsonOut bool, label, doneLabel string) error {
	if requireRunning && !daemonClientForRef(ref).IsRunning() {
		return errors.New("listener daemon stopped before exposure policy was applied")
	}
	activity := startActivity(stderr, jsonOut, label)
	err := registryStore().View(func(reg *registry.Registry) error {
		routes := activeRoutesForListener(reg, ref.Pair)
		var err error
		routes, err = applyExposureRecordSet(ref.String(), routes, records)
		if err != nil {
			return err
		}
		return setListenerRoutesFunc(ref, routes)
	})
	if err != nil {
		activity.Stop()
	} else {
		activity.Complete(doneLabel)
	}
	return err
}

func upsertExposureRecord(records []expose.Record, record expose.Record) []expose.Record {
	next := append([]expose.Record(nil), records...)
	for i := range next {
		if expose.SameKey(next[i], record) {
			next[i] = record
			return next
		}
	}
	return append(next, record)
}

func snapshotExposeSession(key string) map[string]exposeSessionRoute {
	exposeSessionMu.Lock()
	defer exposeSessionMu.Unlock()
	snapshot := map[string]exposeSessionRoute{}
	for domain, session := range exposeSessionRoutes[key] {
		snapshot[domain] = session
	}
	return snapshot
}

func restoreExposeSession(key string, snapshot map[string]exposeSessionRoute) {
	exposeSessionMu.Lock()
	defer exposeSessionMu.Unlock()
	if len(snapshot) == 0 {
		delete(exposeSessionRoutes, key)
		return
	}
	restored := make(map[string]exposeSessionRoute, len(snapshot))
	for domain, session := range snapshot {
		restored[domain] = session
	}
	exposeSessionRoutes[key] = restored
}

func cleanupExposureProvider(provider expose.Provider, record expose.Record) {
	_ = provider.Stop(context.Background(), record, expose.StopOpts{Force: true})
	_ = provider.Close()
}

func exposureStore() expose.Store {
	return expose.Store{Path: filepath.Join(paths.ConfigDir(), "exposures.json")}
}

func exposureScope(res registry.Reservation) string {
	if res.Project != "" {
		return daemonScopeProject
	}
	return daemonScopeGlobal
}

func exposureRecordMatchesScope(record expose.Record, sel registryScopeSelection) bool {
	if sel.All {
		return true
	}
	if sel.Scope.Kind == daemonScopeProject {
		return record.Scope == daemonScopeProject && record.Project == sel.Scope.Name
	}
	return record.Scope == daemonScopeGlobal && record.Project == ""
}

type exposureActiveError struct {
	message string
}

func (e *exposureActiveError) Error() string { return e.message }

type exposureStoreReadError struct {
	err error
}

func (e *exposureStoreReadError) Error() string { return e.err.Error() }
func (e *exposureStoreReadError) Unwrap() error { return e.err }

type exposureAliasConflictError struct {
	alias string
}

func (e *exposureAliasConflictError) Error() string {
	return fmt.Sprintf("LAN exposure alias %q conflicts with an active route domain; stop the exposure or choose another domain", e.alias)
}

func exposureGuardFailure(stderr io.Writer, jsonOut bool, err error) (int, bool) {
	var active *exposureActiveError
	if errors.As(err, &active) {
		return fail(stderr, jsonOut, ExitConflict, "exposure_active", active.Error()), true
	}
	var storeErr *exposureStoreReadError
	if errors.As(err, &storeErr) {
		return fail(stderr, jsonOut, ExitError, "expose_store", storeErr.Error()), true
	}
	return ExitOK, false
}

func ensureReservationsNotExposed(reservations []registry.Reservation) error {
	records, err := exposureStore().Read()
	if err != nil {
		return &exposureStoreReadError{err: err}
	}
	for _, res := range reservations {
		for _, record := range records {
			if !exposureRecordMatchesReservation(record, res) {
				continue
			}
			return &exposureActiveError{message: fmt.Sprintf("%s is exposed via %s; run %s before changing or removing its route", displayReservationOwner(res), record.Provider, exposureStopCommand(record))}
		}
	}
	return nil
}

func reservationHasExposure(res registry.Reservation, records []expose.Record) bool {
	for _, record := range records {
		if exposureRecordMatchesReservation(record, res) {
			return true
		}
	}
	return false
}

func exposureRecordMatchesReservation(record expose.Record, res registry.Reservation) bool {
	project := ""
	if record.Scope == daemonScopeProject {
		project = record.Project
	}
	return project == res.Project && record.Service == res.Service
}

func exposureStopCommand(record expose.Record) string {
	parts := []string{"gate", "expose", "stop"}
	if record.Scope == daemonScopeGlobal {
		parts = append(parts, "-g")
	} else if record.Project != "" {
		parts = append(parts, "-p", record.Project)
	}
	if strings.HasPrefix(record.Service, "-") {
		parts = append(parts, "--via", record.Provider, "--", record.Service)
	} else {
		parts = append(parts, record.Service, "--via", record.Provider)
	}
	return "`" + shellCommand(parts) + "`"
}

func applyExposureRecords(key string, routes []proxy.Route) ([]proxy.Route, error) {
	records, err := exposureStore().Read()
	if err != nil {
		return nil, err
	}
	return applyExposureRecordSet(key, routes, records)
}

func applyExposureRecordSet(key string, routes []proxy.Route, records []expose.Record) ([]proxy.Route, error) {
	baseDomains := make(map[string]bool, len(routes))
	for _, route := range routes {
		baseDomains[config.CanonicalDomain(route.Domain)] = true
	}
	for _, record := range records {
		if record.Provider != expose.ProviderLAN || record.Pending != "" {
			continue
		}
		alias := exposurePublicHost(record)
		if alias != "" && alias != config.CanonicalDomain(record.Target) && baseDomains[alias] {
			return nil, &exposureAliasConflictError{alias: alias}
		}
	}
	exposeSessionMu.Lock()
	defer exposeSessionMu.Unlock()
	sessions := exposeSessionRoutes[key]
	next := make([]proxy.Route, 0, len(routes))
	for _, original := range routes {
		route := original
		blocked := false
		var lanRecords []expose.Record
		for _, record := range records {
			if record.Target != route.Domain {
				continue
			}
			if record.Pending == "start" || record.Pending == "stop" {
				blocked = true
				break
			}
			if record.AuthEnabled {
				session, ok := sessions[route.Domain]
				if !ok || session.Auth == "" {
					blocked = true
					break
				}
				route.Auth = session.Auth
			}
			if record.Provider == expose.ProviderLAN && record.Pending == "" {
				lanRecords = append(lanRecords, record)
			}
		}
		if blocked {
			continue
		}
		if len(lanRecords) > 0 {
			route.Exposed = true
		}
		next = append(next, route)
		for _, record := range lanRecords {
			next = upsertExposureAlias(next, route, record)
		}
	}
	return next, nil
}

func reloadExposureRecordsTransition(previous, next []expose.Record, stderr io.Writer, jsonOut bool) error {
	return reloadExposureRecordsTransitionBlocked(previous, next, nil, stderr, jsonOut)
}

func reloadExposureRecordsTransitionBlocked(previous, next []expose.Record, blocked map[string]bool, stderr io.Writer, jsonOut bool) error {
	return registryStore().View(func(reg *registry.Registry) error {
		refs := []listenerDaemonRef{defaultListenerRef()}
		for _, key := range reg.Keys() {
			refs = appendListenerRef(refs, listenerRefFor(reg.Services[key].ListenerPair()))
		}
		var applied []listenerDaemonRef
		for _, ref := range refs {
			if !daemonClientForRef(ref).IsRunning() {
				continue
			}
			routes, err := applyExposureRecordSet(ref.String(), activeRoutesForListener(reg, ref.Pair), next)
			if err != nil {
				return err
			}
			if len(blocked) > 0 {
				filtered := routes[:0]
				for _, route := range routes {
					if blocked[config.CanonicalDomain(route.Domain)] {
						continue
					}
					filtered = append(filtered, route)
				}
				routes = filtered
			}
			activity := startActivity(stderr, jsonOut, "reloading routes")
			if err := setListenerRoutesFunc(ref, routes); err != nil {
				activity.Stop()
				var rollbackErrs []error
				failedOldRoutes, buildErr := applyExposureRecordSet(ref.String(), activeRoutesForListener(reg, ref.Pair), previous)
				if buildErr != nil {
					rollbackErrs = append(rollbackErrs, fmt.Errorf("build restore routes for %s: %w", ref.String(), buildErr))
				} else if rollbackErr := setListenerRoutesFunc(ref, failedOldRoutes); rollbackErr != nil {
					rollbackErrs = append(rollbackErrs, fmt.Errorf("restore %s: %w", ref.String(), rollbackErr))
				}
				for i := len(applied) - 1; i >= 0; i-- {
					appliedRef := applied[i]
					oldRoutes, buildErr := applyExposureRecordSet(appliedRef.String(), activeRoutesForListener(reg, appliedRef.Pair), previous)
					if buildErr != nil {
						rollbackErrs = append(rollbackErrs, fmt.Errorf("build restore routes for %s: %w", appliedRef.String(), buildErr))
					} else if rollbackErr := setListenerRoutesFunc(appliedRef, oldRoutes); rollbackErr != nil {
						rollbackErrs = append(rollbackErrs, fmt.Errorf("restore %s: %w", appliedRef.String(), rollbackErr))
					}
				}
				if rollbackErr := errors.Join(rollbackErrs...); rollbackErr != nil {
					return errors.Join(err, fmt.Errorf("route rollback failed: %w", rollbackErr))
				}
				return err
			}
			activity.Complete("reloaded routes")
			applied = append(applied, ref)
		}
		return nil
	})
}

func exposureStopBlockedDomains(records []expose.Record, match expose.Record) map[string]bool {
	blocked := map[string]bool{}
	for _, record := range records {
		if !expose.SameKey(record, match) {
			continue
		}
		blocked[config.CanonicalDomain(record.Target)] = true
		if alias := exposurePublicHost(record); alias != "" {
			blocked[config.CanonicalDomain(alias)] = true
		}
	}
	return blocked
}

func upsertExposureAlias(routes []proxy.Route, base proxy.Route, record expose.Record) []proxy.Route {
	alias := exposurePublicHost(record)
	if alias == "" || alias == base.Domain {
		return routes
	}
	base.Domain = alias
	base.Exposed = true
	base.ForwardHost = record.Target
	for i := range routes {
		if routes[i].Domain == alias {
			routes[i] = base
			return routes
		}
	}
	return append(routes, base)
}

func exposurePublicHost(record expose.Record) string {
	u, err := url.Parse(record.PublicURL)
	if err != nil {
		return ""
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "" || host == strings.TrimSuffix(strings.ToLower(record.Target), ".") {
		return ""
	}
	return host
}

func removeExposureRecord(records []expose.Record, match expose.Record) []expose.Record {
	next := make([]expose.Record, 0, len(records))
	for _, record := range records {
		if expose.SameKey(record, match) {
			continue
		}
		next = append(next, record)
	}
	return next
}

func removeExposureRecordsAffectedByStop(records []expose.Record, match expose.Record) []expose.Record {
	return removeExposureRecord(records, match)
}

func applyExposeSession(key string, routes []proxy.Route, domain, auth string) {
	exposeSessionMu.Lock()
	defer exposeSessionMu.Unlock()
	if exposeSessionRoutes[key] == nil {
		exposeSessionRoutes[key] = map[string]exposeSessionRoute{}
	}
	exposeSessionRoutes[key][domain] = exposeSessionRoute{Auth: auth}
	for i := range routes {
		session, ok := exposeSessionRoutes[key][routes[i].Domain]
		if !ok {
			continue
		}
		routes[i].Auth = session.Auth
	}
}

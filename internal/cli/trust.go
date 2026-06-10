package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
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
	if handled, code := parseFlags(fs, "trust", args, stdout, stderr); handled {
		return code
	}
	activity := startActivity(stderr, false, "preparing trust store")
	authority, err := ca.Load(paths.DataDir())
	if err != nil {
		activity.Stop()
		return fail(stderr, false, ExitError, "ca", err.Error())
	}
	activity.Complete()
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
	if handled, code := parseFlags(fs, "untrust", args, stdout, stderr); handled {
		return code
	}
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
	activity.Complete()
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
	if handled, code := parseFlags(fs, "ca export", args[1:], stdout, stderr); handled {
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
	sel, err := registryScopeFromFlags(scopeFlags, false)
	if err != nil {
		return fail(stderr, *jsonOut, ExitUsage, "bad_scope", err.Error())
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return usageFail(stderr, *jsonOut, "expose")
	}
	svc := rest[0]
	res, lerr := lookupScopedReservation(svc, sel)
	if lerr != nil {
		return fail(stderr, *jsonOut, lerr.Exit, lerr.Code, lerr.Message)
	}
	if !res.Active {
		return fail(stderr, *jsonOut, ExitError, "not_active", fmt.Sprintf("reservation %q is not active; run gate up first", svc))
	}
	providerName := normalizeExposeProvider(*via)
	routeAuth, err := exposeRouteAuth(providerName, *auth, *noAuth)
	if err != nil {
		return fail(stderr, *jsonOut, ExitUsage, "bad_auth", err.Error())
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
	if routeAuth == "" && providerName != expose.ProviderLocal {
		printWarning(stderr, "exposing without --auth; anyone with the URL can reach your dev server")
	}
	var activity activityHandle
	if exposeActivityAllowed(providerName) {
		activity = startActivity(stderr, *jsonOut, "starting tunnel")
	}
	result, err := provider.Expose(context.Background(), exposeDomain, expose.Opts{
		Auth:      routeAuth,
		TargetURL: exposeTargetURL(providerName, exposeDomain),
		ServePort: servePort,
	})
	if activity != nil {
		if err != nil {
			activity.Stop()
		} else {
			activity.Complete()
		}
	}
	if err != nil {
		return fail(stderr, *jsonOut, ExitError, "expose_failed", err.Error())
	}

	// External providers lift the loopback guard and can apply optional auth,
	// then the listener daemon is hot-reloaded. Auth is session-scoped: it lives
	// in the in-memory route table, not the persisted registry.
	record := expose.Record{
		Scope:       exposureScope(res),
		Project:     res.Project,
		Service:     res.Service,
		Provider:    providerName,
		PublicURL:   result.URL,
		Target:      res.Domain,
		AuthEnabled: routeAuth != "",
		ServePort:   servePort,
		PID:         result.PID,
		Command:     result.Command,
	}
	if err := exposureStore().Upsert(record); err != nil {
		cleanupExposureProvider(provider, record)
		return fail(stderr, *jsonOut, ExitError, "expose_store", err.Error())
	}
	client := daemonClientForRef(ref)
	if client.IsRunning() {
		reg, rerr := registryStore().Read()
		if rerr != nil {
			cleanupExposureProvider(provider, record)
			if rollbackErr := removeExposureRecordFromStore(record); rollbackErr != nil {
				return fail(stderr, *jsonOut, ExitError, "rollback_failed", "expose failed and rollback failed: "+rollbackErr.Error())
			}
			return fail(stderr, *jsonOut, ExitError, "registry", rerr.Error())
		}
		routes := activeRoutesForListener(reg, ref.Pair)
		if externalExposureProvider(providerName) {
			applyExposeSession(ref.String(), routes, res.Domain, routeAuth)
		}
		routes, err = applyExposureRecords(ref.String(), routes)
		if err != nil {
			cleanupExposureProvider(provider, record)
			if rollbackErr := removeExposureRecordFromStore(record); rollbackErr != nil {
				return fail(stderr, *jsonOut, ExitError, "rollback_failed", "expose failed and rollback failed: "+rollbackErr.Error())
			}
			return fail(stderr, *jsonOut, ExitError, "expose_store", err.Error())
		}
		activity := startActivity(stderr, *jsonOut, "reloading routes")
		serr := setListenerRoutesFunc(ref, routes)
		if serr != nil {
			activity.Stop()
			cleanupExposureProvider(provider, record)
			if rollbackErr := removeExposureRecordFromStore(record); rollbackErr != nil {
				return fail(stderr, *jsonOut, ExitError, "rollback_failed", "expose failed and rollback failed: "+rollbackErr.Error())
			}
			return fail(stderr, *jsonOut, ExitError, "reload_failed", serr.Error())
		}
		activity.Complete()
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
	if handled, code := parseFlags(fs, "expose ls", args, stdout, stderr); handled {
		return code
	}
	sel, err := registryScopeFromFlags(scopeFlags, true)
	if err != nil {
		return fail(stderr, *jsonOut, ExitUsage, "bad_scope", err.Error())
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
	sel, err := registryScopeFromFlags(scopeFlags, false)
	if err != nil {
		return fail(stderr, *jsonOut, ExitUsage, "bad_scope", err.Error())
	}
	if len(fs.Args()) != 1 {
		return usageFail(stderr, *jsonOut, "expose stop")
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
	skipProviderStop := status == expose.StatusDown && *force
	nextRecords := removeExposureRecordsAffectedByStop(records, record)
	if err := reloadExposureRecordsWith(nextRecords, stderr, *jsonOut); err != nil {
		return fail(stderr, *jsonOut, ExitError, "reload_failed", err.Error())
	}
	if err := exposureStore().Write(nextRecords); err != nil {
		if rollbackErr := reloadExposureRecordsWith(records, stderr, *jsonOut); rollbackErr != nil {
			return fail(stderr, *jsonOut, ExitError, "rollback_failed", "stop failed and rollback failed: "+rollbackErr.Error())
		}
		return fail(stderr, *jsonOut, ExitError, "expose_store", err.Error())
	}
	if !skipProviderStop {
		if err := provider.Stop(context.Background(), record, expose.StopOpts{Force: *force}); err != nil {
			if rollbackErr := restoreExposureRecords(records, stderr, *jsonOut); rollbackErr != nil {
				return fail(stderr, *jsonOut, ExitError, "rollback_failed", "stop failed and rollback failed: "+rollbackErr.Error())
			}
			return fail(stderr, *jsonOut, ExitError, "stop_failed", err.Error())
		}
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

func exposeTargetURL(via, domain string) string {
	if via != expose.ProviderTailscale {
		return ""
	}
	return "https+insecure://" + domain
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

func externalExposureProvider(via string) bool {
	switch via {
	case expose.ProviderLAN, expose.ProviderCloudflared, expose.ProviderTailscale:
		return true
	default:
		return false
	}
}

func cleanupExposureProvider(provider expose.Provider, record expose.Record) {
	_ = provider.Stop(context.Background(), record, expose.StopOpts{Force: true})
	_ = provider.Close()
}

func removeExposureRecordFromStore(record expose.Record) error {
	_, err := exposureStore().Delete(record)
	return err
}

func restoreExposureRecords(records []expose.Record, stderr io.Writer, jsonOut bool) error {
	if err := exposureStore().Write(records); err != nil {
		return err
	}
	return reloadExposureRecordsWith(records, stderr, jsonOut)
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

func applyExposureRecords(key string, routes []proxy.Route) ([]proxy.Route, error) {
	records, err := exposureStore().Read()
	if err != nil {
		return nil, err
	}
	return applyExposureRecordSet(key, routes, records), nil
}

func applyExposureRecordSet(key string, routes []proxy.Route, records []expose.Record) []proxy.Route {
	exposeSessionMu.Lock()
	defer exposeSessionMu.Unlock()
	sessions := exposeSessionRoutes[key]
	for i := range routes {
		for _, record := range records {
			if record.Target != routes[i].Domain {
				continue
			}
			if record.AuthEnabled {
				session, ok := sessions[routes[i].Domain]
				if !ok || session.Auth == "" {
					continue
				}
				routes[i].Auth = session.Auth
			}
			if !externalExposureProvider(record.Provider) {
				continue
			}
			routes[i].Exposed = true
			routes = upsertExposureAlias(routes, routes[i], record)
		}
	}
	return routes
}

func reloadExposureRecordsWith(records []expose.Record, stderr io.Writer, jsonOut bool) error {
	reg, err := registryStore().Read()
	if err != nil {
		return err
	}
	refs := []listenerDaemonRef{defaultListenerRef()}
	for _, key := range reg.Keys() {
		refs = appendListenerRef(refs, listenerRefFor(reg.Services[key].ListenerPair()))
	}
	for _, ref := range refs {
		if !daemonClientForRef(ref).IsRunning() {
			continue
		}
		routes := activeRoutesForListener(reg, ref.Pair)
		routes = applyExposureRecordSet(ref.String(), routes, records)
		activity := startActivity(stderr, jsonOut, "reloading routes")
		err := setListenerRoutesFunc(ref, routes)
		if err != nil {
			activity.Stop()
			return err
		}
		activity.Complete()
	}
	return nil
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
	if match.Provider != expose.ProviderTailscale {
		return removeExposureRecord(records, match)
	}
	next := make([]expose.Record, 0, len(records))
	for _, record := range records {
		if record.Provider == expose.ProviderTailscale {
			continue
		}
		next = append(next, record)
	}
	return next
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
		routes[i].Exposed = true
		routes[i].Auth = session.Auth
	}
}

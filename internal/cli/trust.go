package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"text/tabwriter"

	"gate/internal/ca"
	"gate/internal/expose"
	"gate/internal/paths"
	"gate/internal/proxy"
	"gate/internal/registry"
)

var (
	trustAuthorityFunc   = func(authority *ca.CA) error { return authority.Trust() }
	untrustAuthorityFunc = func(authority *ca.CA) error { return authority.Untrust() }
	exposeProviderFor    = expose.For
	exposeSessionMu      sync.Mutex
	exposeSessionRoutes  = map[string]map[string]exposeSessionRoute{}
)

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
	activity.Stop()
	if err != nil {
		return fail(stderr, false, ExitError, "ca", err.Error())
	}
	if err := trustAuthorityFunc(authority); err != nil {
		if os.IsPermission(err) {
			return fail(stderr, false, ExitPerm, "permission", err.Error())
		}
		return fail(stderr, false, ExitError, "trust", err.Error())
	}
	fmt.Fprintf(stdout, "root CA trusted\nfingerprint %s\n", authority.Fingerprint())
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
	activity.Stop()
	if errors.Is(err, ca.ErrNotFound) {
		fmt.Fprintln(stdout, "root CA not found; nothing to untrust")
		return ExitOK
	}
	if err != nil {
		return fail(stderr, false, ExitError, "ca", err.Error())
	}
	if err := untrustAuthorityFunc(authority); err != nil {
		if os.IsPermission(err) {
			return fail(stderr, false, ExitPerm, "permission", err.Error())
		}
		return fail(stderr, false, ExitError, "untrust", err.Error())
	}
	fmt.Fprintf(stdout, "root CA untrusted\nfingerprint %s\n", authority.Fingerprint())
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
	fmt.Fprintf(stdout, "exported %s\nsha256 %s\n", *out, fp)
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
	auth := fs.String("auth", "", "require basic auth as user:pass")
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
	provider, err := exposeProviderFor(*via)
	if err != nil {
		return fail(stderr, *jsonOut, ExitUsage, "bad_provider", err.Error())
	}
	if *auth == "" && *via != "local" {
		fmt.Fprintln(stderr, "warning: exposing without --auth; anyone with the URL can reach your dev server")
	}
	var activity activityHandle
	if exposeActivityAllowed(*via) {
		activity = startActivity(stderr, *jsonOut, "starting tunnel")
	}
	result, err := provider.Expose(context.Background(), res.Domain, expose.Opts{Auth: *auth})
	if activity != nil {
		activity.Stop()
	}
	if err != nil {
		return fail(stderr, *jsonOut, ExitError, "expose_failed", err.Error())
	}

	// Mark the route exposed (so non-loopback clients are allowed) and apply
	// optional auth, then hot-reload the listener daemon. Auth is session-scoped:
	// it lives in the in-memory route table, not the persisted registry.
	ref := listenerRefFor(res.ListenerPair())
	record := expose.Record{
		Scope:       exposureScope(res),
		Project:     res.Project,
		Service:     res.Service,
		Provider:    *via,
		PublicURL:   result.URL,
		Target:      res.Domain,
		AuthEnabled: *auth != "",
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
		applyExposeSession(ref.String(), routes, res.Domain, *auth)
		if err := applyExposureRecords(ref.String(), routes); err != nil {
			cleanupExposureProvider(provider, record)
			if rollbackErr := removeExposureRecordFromStore(record); rollbackErr != nil {
				return fail(stderr, *jsonOut, ExitError, "rollback_failed", "expose failed and rollback failed: "+rollbackErr.Error())
			}
			return fail(stderr, *jsonOut, ExitError, "expose_store", err.Error())
		}
		activity := startActivity(stderr, *jsonOut, "reloading routes")
		serr := setListenerRoutesFunc(ref, routes)
		activity.Stop()
		if serr != nil {
			cleanupExposureProvider(provider, record)
			if rollbackErr := removeExposureRecordFromStore(record); rollbackErr != nil {
				return fail(stderr, *jsonOut, ExitError, "rollback_failed", "expose failed and rollback failed: "+rollbackErr.Error())
			}
			return fail(stderr, *jsonOut, ExitError, "reload_failed", serr.Error())
		}
	}

	if *jsonOut {
		out := map[string]any{"service": svc, "provider": *via, "public_url": result.URL, "target": res.Domain}
		if res.Project != "" {
			out["project"] = res.Project
		} else {
			out["global"] = true
		}
		return writeJSON(stdout, out)
	}
	fmt.Fprintf(stdout, "%s exposed via %s\n  %s -> %s\n", displayReservationOwner(res), *via, result.URL, res.Domain)
	return ExitOK
}

type exposeRow struct {
	Scope     string `json:"scope"`
	Project   string `json:"project,omitempty"`
	Service   string `json:"service"`
	Provider  string `json:"provider"`
	PublicURL string `json:"public_url"`
	Target    string `json:"target"`
	Auth      bool   `json:"auth"`
	Status    string `json:"status"`
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
			Auth: record.AuthEnabled, Status: status,
		})
	}
	if *jsonOut {
		return writeJSON(stdout, map[string]any{"exposures": rows})
	}
	if len(rows) == 0 {
		fmt.Fprintln(stdout, "No exposures.")
		return ExitOK
	}
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SERVICE\tSTATUS\tPROVIDER\tPUBLIC URL\tTARGET\tSCOPE\tAUTH")
	for _, row := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%t\n", row.Service, row.Status, row.Provider, row.PublicURL, row.Target, row.Scope, row.Auth)
	}
	_ = tw.Flush()
	return ExitOK
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
	nextRecords := removeExposureRecord(records, record)
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
	fmt.Fprintf(stdout, "stopped exposure  %s via %s\n", service, record.Provider)
	return ExitOK
}

func exposeActivityAllowed(via string) bool {
	return via == expose.ProviderCloudflared || via == expose.ProviderTailscale
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

func applyExposureRecords(key string, routes []proxy.Route) error {
	records, err := exposureStore().Read()
	if err != nil {
		return err
	}
	applyExposureRecordSet(key, routes, records)
	return nil
}

func applyExposureRecordSet(key string, routes []proxy.Route, records []expose.Record) {
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
			routes[i].Exposed = true
		}
	}
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
		applyExposureRecordSet(ref.String(), routes, records)
		activity := startActivity(stderr, jsonOut, "reloading routes")
		err := setListenerRoutesFunc(ref, routes)
		activity.Stop()
		if err != nil {
			return err
		}
	}
	return nil
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

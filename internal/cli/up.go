package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gate/internal/config"
	"gate/internal/dns"
	"gate/internal/listener"
	"gate/internal/port"
	"gate/internal/proxy"
	"gate/internal/registry"
)

type upResult struct {
	Service     string `json:"service"`
	Domain      string `json:"domain"`
	Port        int    `json:"port"`
	URL         string `json:"url"`
	LoopbackURL string `json:"loopbackUrl"`
	Allocated   bool   `json:"allocated"`
}

type reloadUpResult struct {
	Reloaded           bool
	ActualHTTPSAddr    string
	RouteRestoreNeeded bool
	StartedPID         int
	Code               int
}

// Up reserves/allocates ports for the project, reflects DNS, and pushes the
// route table to a running daemon (or prints it when none is running).
func Up(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("up", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit JSON")
	dnsMode := fs.String("dns", "", "force DNS mode: localhost|hosts")
	startDaemon := fs.Bool("daemon", false, "start the background daemon before reloading routes")
	fs.BoolVar(startDaemon, "d", false, "start the background daemon before reloading routes")
	scopeFlags := defineDaemonScopeFlags(fs, false)
	httpsAddr := fs.String("https-addr", defaultDaemonHTTPSAddr, "daemon HTTPS listen address (with --daemon)")
	httpAddr := fs.String("http-addr", defaultDaemonHTTPAddr, "daemon HTTP listen address (with --daemon)")
	if handled, code := parseNoArgFlags(fs, "up", args, stdout, stderr); handled {
		return code
	}
	if *dnsMode != "" && *dnsMode != "localhost" && *dnsMode != "hosts" {
		return fail(stderr, *jsonOut, ExitUsage, "bad_dns", "dns must be localhost or hosts")
	}
	httpsAddrSet, httpAddrSet := flagSet(fs, "https-addr"), flagSet(fs, "http-addr")
	pair := listener.FromFlags(*httpsAddr, *httpAddr)
	if err := listener.Validate(pair, true); err != nil {
		return fail(stderr, *jsonOut, ExitUsage, "bad_listener", err.Error())
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

	if !sel.CurrentProjectSelected {
		return upExistingScope(sel, *dnsMode, *startDaemon, pair, httpsAddrSet, httpAddrSet, stdout, stderr, *jsonOut)
	}

	project, path := sel.CurrentProject, sel.CurrentProjectPath
	exposureRecords, err := exposureStore().Read()
	if err != nil {
		return fail(stderr, *jsonOut, ExitError, "expose_store", err.Error())
	}

	results := make([]upResult, 0, len(project.Services))
	var activated []registry.Reservation
	var previous []projectReservation
	var pruned []projectReservation
	var createdKeys []string
	err = registryStore().Update(func(reg *registry.Registry) error {
		removed, err := reg.Prune(configPathExists)
		if err != nil {
			return err
		}
		for _, res := range removed {
			if reservationHasExposure(res, exposureRecords) {
				if err := reg.Reserve(res); err != nil {
					return err
				}
				continue
			}
			pruned = append(pruned, projectReservation{Key: registry.Key(res.Project, res.Service), Reservation: res})
		}
		desired := make(map[string]bool, len(project.Services))
		for name := range project.Services {
			desired[registry.Key(project.Name, name)] = true
		}
		for _, key := range reg.Keys() {
			res := reg.Services[key]
			if res.ConfigPath == "" || !configPathsEquivalent(res.ConfigPath, path) || desired[key] {
				continue
			}
			if err := ensureReservationsNotExposed([]registry.Reservation{res}); err != nil {
				return err
			}
			reg.Release(key)
			pruned = append(pruned, projectReservation{Key: key, Reservation: res})
		}
		used := reg.UsedPorts()
		for _, name := range sortedServices(project) {
			svc := project.Services[name]
			p, allocated, aerr := resolvePort(reg, project.Name, name, svc, used)
			if aerr != nil {
				return aerr
			}
			used[p] = true
			res := registry.Reservation{
				Project: project.Name, Service: name, Domain: svc.Domain, Port: p,
				TLS: svc.TLS, DNS: dns.ModeFor(svc.Domain, *dnsMode),
				Active: true, ConfigPath: path,
			}
			res.SetListenerPair(pair)
			key := registry.Key(project.Name, name)
			if prev, ok := reg.Get(key); ok {
				if config.CanonicalDomain(prev.Domain) != config.CanonicalDomain(res.Domain) || !listener.Equivalent(prev.ListenerPair(), res.ListenerPair()) {
					if err := ensureReservationsNotExposed([]registry.Reservation{prev}); err != nil {
						return err
					}
				}
				previous = append(previous, projectReservation{Key: key, Reservation: prev})
			} else {
				createdKeys = append(createdKeys, key)
			}
			if rerr := reg.Reserve(res); rerr != nil {
				return rerr
			}
			results = append(results, upResult{Service: name, Domain: svc.Domain, Port: p, Allocated: allocated})
			activated = append(activated, res)
		}
		return nil
	})
	var ce *registry.ConflictError
	if errors.As(err, &ce) {
		return fail(stderr, *jsonOut, ExitConflict, "conflict", ce.Error())
	}
	if err != nil {
		if code, ok := exposureGuardFailure(stderr, *jsonOut, err); ok {
			return code
		}
		if errors.Is(err, port.ErrPoolExhausted) {
			return fail(stderr, *jsonOut, ExitConflict, "pool_exhausted", err.Error())
		}
		return fail(stderr, *jsonOut, ExitError, "up_failed", err.Error())
	}

	refs := append(listenerRefsForReservations(append(append([]projectReservation{}, previous...), pruned...)), listenerRefFor(pair))
	var ensured []registry.Reservation
	for _, res := range activated {
		if err := ensureDomainDNS(res.Domain, res.DNS, stderr, *jsonOut); err != nil {
			rollbackErr := rollbackCurrentProjectUp(previous, pruned, createdKeys, ensured, refs, stderr, *jsonOut)
			if rollbackErr != nil {
				return fail(stderr, *jsonOut, ExitError, "rollback_failed", "up failed and rollback failed: "+rollbackErr.Error())
			}
			if os.IsPermission(err) || errors.Is(err, os.ErrPermission) {
				return fail(stderr, *jsonOut, ExitPerm, "permission", err.Error())
			}
			return fail(stderr, *jsonOut, ExitError, "dns_failed", err.Error())
		}
		ensured = append(ensured, res)
	}

	reloadResult := reloadUpRoutes(listenerRefFor(pair), *startDaemon, pair, httpsAddrSet, httpAddrSet, stderr, *jsonOut)
	if reloadResult.Code != ExitOK {
		rollbackRefs := refs
		if !reloadResult.RouteRestoreNeeded {
			rollbackRefs = nil
		}
		rollbackErr := rollbackCurrentProjectUp(previous, pruned, createdKeys, ensured, rollbackRefs, stderr, *jsonOut)
		if rollbackErr != nil {
			return fail(stderr, *jsonOut, ExitError, "rollback_failed", "up failed and rollback failed: "+rollbackErr.Error())
		}
		return reloadResult.Code
	}
	if err := reloadOtherListenerRefs(refs, listenerRefFor(pair), stderr, *jsonOut); err != nil {
		rollbackErr := rollbackCurrentProjectUp(previous, pruned, createdKeys, ensured, refs, stderr, *jsonOut)
		if reloadResult.StartedPID != 0 {
			cleanupStartedDaemon(daemonClientForRef(listenerRefFor(pair)), listenerRefFor(pair), reloadResult.StartedPID)
		}
		if rollbackErr != nil {
			return fail(stderr, *jsonOut, ExitError, "rollback_failed", "up failed and rollback failed: "+rollbackErr.Error())
		}
		return fail(stderr, *jsonOut, ExitError, "reload_failed", err.Error())
	}
	stale := append(reservationsFromProjectReservations(previous), reservationsFromProjectReservations(pruned)...)
	if err := removeStaleReservationDNS(stale, stderr, *jsonOut); err != nil {
		rollbackErr := rollbackCurrentProjectUp(previous, pruned, createdKeys, ensured, refs, stderr, *jsonOut)
		if reloadResult.StartedPID != 0 {
			cleanupStartedDaemon(daemonClientForRef(listenerRefFor(pair)), listenerRefFor(pair), reloadResult.StartedPID)
		}
		if rollbackErr != nil {
			return fail(stderr, *jsonOut, ExitError, "rollback_failed", "up failed and rollback failed: "+rollbackErr.Error())
		}
		if os.IsPermission(err) || errors.Is(err, os.ErrPermission) {
			return fail(stderr, *jsonOut, ExitPerm, "permission", err.Error())
		}
		return fail(stderr, *jsonOut, ExitError, "dns_failed", err.Error())
	}

	populateUpResultURLs(results, reloadResult)
	if *jsonOut {
		return writeJSON(stdout, map[string]any{"project": project.Name, "reloaded": reloadResult.Reloaded, "services": results})
	}
	for _, r := range results {
		printRoute(stdout, project.Name+"/"+r.Service, r.URL, r.Port)
	}
	if reloadResult.Reloaded {
		printSuccess(stdout, "proxy reloaded")
	} else {
		printInfo(stderr, noDaemonRunningNote(pair, listenerRefFor(pair)))
	}
	return ExitOK
}

func executablePath() string {
	exe, err := os.Executable()
	if err != nil {
		return os.Args[0]
	}
	return exe
}

func configPathExists(path string) (bool, error) {
	if path == "" {
		return true, nil
	}
	info, err := os.Stat(path)
	if err == nil {
		return info.Mode().IsRegular(), nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	// Implicit cleanup during `up` must never make an unrelated project
	// unavailable because another config path cannot be inspected. Explicit
	// `gate prune` still reports this error through configFileExists.
	return true, nil
}

func proxyURL(domain, httpsAddr string) string {
	port := proxyPort(httpsAddr)
	if port == "" || port == "443" {
		return "https://" + domain
	}
	return "https://" + net.JoinHostPort(domain, port)
}

func proxyPort(addr string) string {
	if addr == "" {
		return ""
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return ""
	}
	if port == "" || port == "443" {
		return ""
	}
	return strings.TrimSpace(port)
}

func upExistingScope(sel registryScopeSelection, dnsMode string, startDaemon bool, pair listener.Pair, httpsAddrSet, httpAddrSet bool, stdout, stderr io.Writer, jsonOut bool) int {
	scope := sel.Scope
	var results []upResult
	var activated []registry.Reservation
	var previous []projectReservation
	err := registryStore().Update(func(reg *registry.Registry) error {
		removed := reservationsForScope(reg, sel)
		if len(removed) == 0 {
			return fmt.Errorf("no reservations for %s", scope.String())
		}
		previous = append(previous, removed...)
		for _, item := range removed {
			res := item.Reservation
			if !res.Active || !listener.Equivalent(res.ListenerPair(), pair) {
				if err := ensureReservationsNotExposed([]registry.Reservation{res}); err != nil {
					return err
				}
			}
		}
		for _, item := range removed {
			res := item.Reservation
			res.Active = true
			if dnsMode != "" {
				res.DNS = dns.ModeFor(res.Domain, dnsMode)
			} else if res.DNS == "" {
				res.DNS = dns.ModeFor(res.Domain, "")
			}
			res.SetListenerPair(pair)
			if err := reg.Reserve(res); err != nil {
				return err
			}
			results = append(results, upResult{Service: res.Service, Domain: res.Domain, Port: res.Port})
			activated = append(activated, res)
		}
		return nil
	})
	var ce *registry.ConflictError
	if errors.As(err, &ce) {
		return fail(stderr, jsonOut, ExitConflict, "conflict", ce.Error())
	}
	if err != nil {
		if code, ok := exposureGuardFailure(stderr, jsonOut, err); ok {
			return code
		}
		return fail(stderr, jsonOut, ExitError, "up_failed", err.Error())
	}
	var ensured []registry.Reservation
	for _, res := range activated {
		if err := ensureDomainDNS(res.Domain, res.DNS, stderr, jsonOut); err != nil {
			rollbackErr := rollbackScopedUp(previous, ensured, scope, true, stderr, jsonOut)
			if rollbackErr != nil {
				return fail(stderr, jsonOut, ExitError, "rollback_failed", "up failed and rollback failed: "+rollbackErr.Error())
			}
			if os.IsPermission(err) || errors.Is(err, os.ErrPermission) {
				return fail(stderr, jsonOut, ExitPerm, "permission", err.Error())
			}
			return fail(stderr, jsonOut, ExitError, "dns_failed", err.Error())
		}
		ensured = append(ensured, res)
	}
	refs := append(listenerRefsForReservations(previous), listenerRefFor(pair))
	reloadResult := reloadUpRoutes(listenerRefFor(pair), startDaemon, pair, httpsAddrSet, httpAddrSet, stderr, jsonOut)
	if reloadResult.Code != ExitOK {
		rollbackErr := rollbackScopedUp(previous, ensured, scope, reloadResult.RouteRestoreNeeded, stderr, jsonOut, listenerRefFor(pair))
		if rollbackErr != nil {
			return fail(stderr, jsonOut, ExitError, "rollback_failed", "up failed and rollback failed: "+rollbackErr.Error())
		}
		return reloadResult.Code
	}
	if err := reloadOtherListenerRefs(refs, listenerRefFor(pair), stderr, jsonOut); err != nil {
		rollbackErr := rollbackScopedUp(previous, ensured, scope, true, stderr, jsonOut, refs...)
		if reloadResult.StartedPID != 0 {
			cleanupStartedDaemon(daemonClientForRef(listenerRefFor(pair)), listenerRefFor(pair), reloadResult.StartedPID)
		}
		if rollbackErr != nil {
			return fail(stderr, jsonOut, ExitError, "rollback_failed", "up failed and rollback failed: "+rollbackErr.Error())
		}
		return fail(stderr, jsonOut, ExitError, "reload_failed", err.Error())
	}
	if err := removeStaleReservationDNS(reservationsFromProjectReservations(previous), stderr, jsonOut); err != nil {
		rollbackErr := rollbackScopedUp(previous, ensured, scope, true, stderr, jsonOut, refs...)
		if reloadResult.StartedPID != 0 {
			cleanupStartedDaemon(daemonClientForRef(listenerRefFor(pair)), listenerRefFor(pair), reloadResult.StartedPID)
		}
		if rollbackErr != nil {
			return fail(stderr, jsonOut, ExitError, "rollback_failed", "up failed and rollback failed: "+rollbackErr.Error())
		}
		if os.IsPermission(err) || errors.Is(err, os.ErrPermission) {
			return fail(stderr, jsonOut, ExitPerm, "permission", err.Error())
		}
		return fail(stderr, jsonOut, ExitError, "dns_failed", err.Error())
	}
	populateUpResultURLs(results, reloadResult)
	if jsonOut {
		out := map[string]any{"reloaded": reloadResult.Reloaded, "services": results}
		if scope.Kind == daemonScopeProject {
			out["project"] = scope.Name
		} else {
			out["global"] = true
		}
		return writeJSON(stdout, out)
	}
	for _, r := range results {
		printRoute(stdout, scope.String()+"/"+r.Service, r.URL, r.Port)
	}
	if reloadResult.Reloaded {
		printSuccess(stdout, "proxy reloaded")
	} else {
		printInfo(stderr, noDaemonRunningNote(pair, listenerRefFor(pair)))
	}
	return ExitOK
}

func populateUpResultURLs(results []upResult, reload reloadUpResult) {
	for i := range results {
		results[i].URL = displayDomainURL(results[i].Domain)
		if reload.Reloaded {
			results[i].URL = proxyURL(results[i].Domain, reload.ActualHTTPSAddr)
		}
		results[i].LoopbackURL = loopbackURL(results[i].Port)
	}
}

func noDaemonRunningNote(pair listener.Pair, ref listenerDaemonRef) string {
	hint := gateDaemonConflictHint(pair, ref)
	if hint == "" {
		return "note: no daemon running; start it with `gate daemon start`"
	}
	return "note: no daemon reachable in current gate state\n" + hint
}

func rollbackCurrentProjectUp(previous, pruned []projectReservation, createdKeys []string, ensured []registry.Reservation, refs []listenerDaemonRef, stderr io.Writer, jsonOut bool) error {
	var errs []error
	if err := registryStore().Update(func(r *registry.Registry) error {
		for _, key := range createdKeys {
			r.Release(key)
		}
		for _, item := range previous {
			if err := r.Reserve(item.Reservation); err != nil {
				return err
			}
		}
		for _, item := range pruned {
			if err := r.Reserve(item.Reservation); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		errs = append(errs, fmt.Errorf("restore registry: %w", err))
	}
	errs = append(errs, reconcileRollbackDNS(ensured, append(append([]projectReservation{}, previous...), pruned...), stderr, jsonOut))
	if err := setListenerRoutesForRefsWithActivity(uniqueListenerRefs(refs), stderr, jsonOut, "restoring routes"); err != nil {
		errs = append(errs, fmt.Errorf("restore daemon routes: %w", err))
	}
	return errors.Join(errs...)
}

func rollbackScopedUp(previous []projectReservation, ensured []registry.Reservation, scope daemonScope, restoreRoutes bool, stderr io.Writer, jsonOut bool, extraRefs ...listenerDaemonRef) error {
	var errs []error
	if err := registryStore().Update(func(r *registry.Registry) error {
		for _, item := range previous {
			if err := r.Reserve(item.Reservation); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		errs = append(errs, fmt.Errorf("restore registry: %w", err))
	}
	errs = append(errs, reconcileRollbackDNS(ensured, previous, stderr, jsonOut))
	if restoreRoutes {
		refs := listenerRefsForReservations(previous)
		if len(refs) == 0 && scope.Kind != "" {
			refs = []listenerDaemonRef{defaultListenerRef()}
		}
		refs = append(refs, extraRefs...)
		if err := setListenerRoutesForRefsWithActivity(uniqueListenerRefs(refs), stderr, jsonOut, "restoring routes"); err != nil {
			errs = append(errs, fmt.Errorf("restore daemon routes: %w", err))
		}
	}
	return errors.Join(errs...)
}

func uniqueListenerRefs(refs []listenerDaemonRef) []listenerDaemonRef {
	var out []listenerDaemonRef
	for _, ref := range refs {
		out = appendListenerRef(out, ref)
	}
	return out
}

func reservationsFromProjectReservations(items []projectReservation) []registry.Reservation {
	reservations := make([]registry.Reservation, 0, len(items))
	for _, item := range items {
		reservations = append(reservations, item.Reservation)
	}
	return reservations
}

func reloadOtherListenerRefs(refs []listenerDaemonRef, current listenerDaemonRef, stderr io.Writer, jsonOut bool) error {
	for _, ref := range uniqueListenerRefs(refs) {
		if ref.fileKey() == current.fileKey() {
			continue
		}
		if err := setListenerRoutesWithActivity(ref, stderr, jsonOut, "reloading routes"); err != nil {
			return err
		}
	}
	return nil
}

func removeStaleReservationDNS(candidates []registry.Reservation, stderr io.Writer, jsonOut bool) error {
	reg, err := registryStore().Read()
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, res := range candidates {
		if !res.Active || activeDNSBinding(reg, res) {
			continue
		}
		key := dnsBindingKey(res)
		if seen[key] {
			continue
		}
		seen[key] = true
		if err := removeDomainDNS(res.Domain, res.DNS, stderr, jsonOut); err != nil {
			return err
		}
	}
	return nil
}

func reconcileRollbackDNS(ensured []registry.Reservation, restored []projectReservation, stderr io.Writer, jsonOut bool) error {
	var errs []error
	reg, err := registryStore().Read()
	if err != nil {
		return fmt.Errorf("read restored registry: %w", err)
	}
	seen := map[string]bool{}
	for i := len(ensured) - 1; i >= 0; i-- {
		res := ensured[i]
		key := dnsBindingKey(res)
		if seen[key] || activeDNSBinding(reg, res) {
			continue
		}
		seen[key] = true
		if err := removeDomainDNS(res.Domain, res.DNS, stderr, jsonOut); err != nil {
			errs = append(errs, fmt.Errorf("remove DNS %s: %w", res.Domain, err))
		}
	}
	seen = map[string]bool{}
	for _, item := range restored {
		res := item.Reservation
		if !res.Active || !activeDNSBinding(reg, res) {
			continue
		}
		key := dnsBindingKey(res)
		if seen[key] {
			continue
		}
		seen[key] = true
		if err := ensureDomainDNS(res.Domain, res.DNS, stderr, jsonOut); err != nil {
			errs = append(errs, fmt.Errorf("restore DNS %s: %w", res.Domain, err))
		}
	}
	return errors.Join(errs...)
}

func activeDNSBinding(reg *registry.Registry, candidate registry.Reservation) bool {
	wantDomain := config.CanonicalDomain(candidate.Domain)
	wantMode := reservationDNSMode(candidate)
	for _, res := range reg.Services {
		if res.Active && config.CanonicalDomain(res.Domain) == wantDomain && reservationDNSMode(res) == wantMode {
			return true
		}
	}
	return false
}

func dnsBindingKey(res registry.Reservation) string {
	return config.CanonicalDomain(res.Domain) + "\x00" + reservationDNSMode(res)
}

func reservationDNSMode(res registry.Reservation) string {
	if res.DNS != "" {
		return res.DNS
	}
	return dns.ModeFor(res.Domain, "")
}

func reloadUpRoutes(ref listenerDaemonRef, startDaemon bool, pair listener.Pair, httpsAddrSet, httpAddrSet bool, stderr io.Writer, jsonOut bool) reloadUpResult {
	reloaded := false
	actualHTTPSAddr := ""
	startedPID := 0
	client := daemonClientForRef(ref)
	if startDaemon {
		if st, err := client.Status(); err == nil {
			if !daemonExplicitListenMatches(st, pair.HTTPSAddr, pair.HTTPAddr, httpsAddrSet, httpAddrSet) {
				msg := fmt.Sprintf("daemon already running on https %s · http %s; requested https %s · http %s; run `gate daemon stop` first",
					displayListenAddr(st.HTTPSAddr), displayListenAddr(st.HTTPAddr), pair.HTTPSAddr, pair.HTTPAddr)
				return reloadUpResult{Code: fail(stderr, jsonOut, ExitConflict, "daemon_start", msg)}
			}
		} else {
			if err := replaceScopedDaemonsForListener(pair); err != nil {
				return reloadUpResult{Code: fail(stderr, jsonOut, ExitError, "migration", err.Error())}
			}
			activity := startActivity(stderr, jsonOut, "starting daemon")
			result := startDaemonCommand(newDaemonServeCommand(executablePath(), ref.socketPath(), pair.HTTPSAddr, pair.HTTPAddr), client, ref)
			if result.Code != ExitOK {
				activity.Stop()
				return reloadUpResult{Code: failDaemonStart(stderr, jsonOut, result, pair, ref, "daemon_start")}
			}
			activity.Complete()
			startedPID = result.PID
		}
	}
	running := client.IsRunning()
	attempted := false
	var err error
	if running {
		attempted, err = setListenerRoutesForRefTracked(ref)
	} else {
		err = validateListenerRoutesForRef(ref)
	}
	if err != nil {
		if startedPID != 0 {
			cleanupStartedDaemon(client, ref, startedPID)
		}
		var aliasConflict *exposureAliasConflictError
		if errors.As(err, &aliasConflict) {
			return reloadUpResult{RouteRestoreNeeded: attempted, Code: fail(stderr, jsonOut, ExitConflict, "domain_conflict", aliasConflict.Error())}
		}
		code := "reload_failed"
		if !attempted {
			code = "expose_store"
		}
		return reloadUpResult{RouteRestoreNeeded: attempted, Code: fail(stderr, jsonOut, ExitError, code, err.Error())}
	}
	if running {
		if st, err := client.Status(); err == nil {
			actualHTTPSAddr = st.HTTPSAddr
		}
		reloaded = true
	}
	return reloadUpResult{Reloaded: reloaded, ActualHTTPSAddr: actualHTTPSAddr, StartedPID: startedPID, Code: ExitOK}
}

// Down deactivates the current project's routes (reservations are preserved)
// and removes its DNS entries.
func Down(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("down", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit JSON")
	scopeFlags := defineDaemonScopeFlags(fs, false)
	if handled, code := parseNoArgFlags(fs, "down", args, stdout, stderr); handled {
		return code
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
	if !sel.CurrentProjectSelected {
		return downExistingScope(sel, stdout, stderr, *jsonOut)
	}
	project := sel.CurrentProject
	path := sel.CurrentProjectPath

	var deactivated []registry.Reservation
	var previous []projectReservation
	var reloadRefs []listenerDaemonRef
	err = registryStore().Update(func(reg *registry.Registry) error {
		for _, key := range reg.Keys() {
			res := reg.Services[key]
			if res.Project != project.Name && (res.ConfigPath == "" || !configPathsEquivalent(res.ConfigPath, path)) {
				continue
			}
			previous = append(previous, projectReservation{Key: key, Reservation: res})
			reloadRefs = appendListenerRef(reloadRefs, listenerRefFor(res.ListenerPair()))
			if res.Active {
				deactivated = append(deactivated, res)
			}
			res.Active = false
			if err := reg.Reserve(res); err != nil {
				return err
			}
		}
		if err := ensureReservationsNotExposed(reservationsFromProjectReservations(previous)); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		if code, ok := exposureGuardFailure(stderr, *jsonOut, err); ok {
			return code
		}
		return fail(stderr, *jsonOut, ExitError, "down_failed", err.Error())
	}

	scope := projectDaemonScope(project.Name)
	for i, res := range deactivated {
		if err := removeDomainDNS(res.Domain, res.DNS, stderr, *jsonOut); err != nil {
			rollbackErr := restoreProjectDNS(reservationsFromRegistry(deactivated[:i]), stderr, *jsonOut)
			rollbackErr = errors.Join(rollbackErr, restoreReservations(previous, scope, stderr, *jsonOut))
			if rollbackErr != nil {
				return fail(stderr, *jsonOut, ExitError, "rollback_failed", "down failed and rollback failed: "+rollbackErr.Error())
			}
			if os.IsPermission(err) || errors.Is(err, os.ErrPermission) {
				return fail(stderr, *jsonOut, ExitPerm, "permission", err.Error())
			}
			return fail(stderr, *jsonOut, ExitError, "dns_failed", err.Error())
		}
	}
	for _, ref := range reloadRefs {
		if daemonClientForRef(ref).IsRunning() {
			if err := setListenerRoutesWithActivity(ref, stderr, *jsonOut, "reloading routes"); err != nil {
				rollbackErr := restoreProjectDNS(reservationsFromRegistry(deactivated), stderr, *jsonOut)
				rollbackErr = errors.Join(rollbackErr, restoreReservations(previous, scope, stderr, *jsonOut))
				if rollbackErr != nil {
					return fail(stderr, *jsonOut, ExitError, "rollback_failed", "down failed and rollback failed: "+rollbackErr.Error())
				}
				return fail(stderr, *jsonOut, ExitError, "reload_failed", err.Error())
			}
		}
	}
	if *jsonOut {
		return writeJSON(stdout, map[string]any{"project": project.Name, "down": true})
	}
	printSuccess(stdout, project.Name+" down (reservations preserved)")
	return ExitOK
}

func configPathsEquivalent(left, right string) bool {
	if left == "" || right == "" {
		return false
	}
	leftTarget, leftErr := config.ResolveFileTarget(left)
	rightTarget, rightErr := config.ResolveFileTarget(right)
	if leftErr == nil && rightErr == nil {
		return leftTarget == rightTarget
	}
	leftAbs, leftAbsErr := filepath.Abs(left)
	rightAbs, rightAbsErr := filepath.Abs(right)
	return leftAbsErr == nil && rightAbsErr == nil && filepath.Clean(leftAbs) == filepath.Clean(rightAbs)
}

func downExistingScope(sel registryScopeSelection, stdout, stderr io.Writer, jsonOut bool) int {
	scope := sel.Scope
	var deactivated []registry.Reservation
	var previous []projectReservation
	var reloadRefs []listenerDaemonRef
	err := registryStore().Update(func(reg *registry.Registry) error {
		items := reservationsForScope(reg, sel)
		if len(items) == 0 {
			return fmt.Errorf("no reservations for %s", scope.String())
		}
		previous = append(previous, items...)
		if err := ensureReservationsNotExposed(reservationsFromProjectReservations(items)); err != nil {
			return err
		}
		for _, item := range items {
			res := item.Reservation
			reloadRefs = appendListenerRef(reloadRefs, listenerRefFor(res.ListenerPair()))
			if res.Active {
				deactivated = append(deactivated, res)
			}
			res.Active = false
			if err := reg.Reserve(res); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		if code, ok := exposureGuardFailure(stderr, jsonOut, err); ok {
			return code
		}
		return fail(stderr, jsonOut, ExitError, "down_failed", err.Error())
	}
	for i, res := range deactivated {
		if err := removeDomainDNS(res.Domain, res.DNS, stderr, jsonOut); err != nil {
			rollbackErr := restoreProjectDNS(reservationsFromRegistry(deactivated[:i]), stderr, jsonOut)
			rollbackErr = errors.Join(rollbackErr, restoreReservations(previous, scope, stderr, jsonOut))
			if rollbackErr != nil {
				return fail(stderr, jsonOut, ExitError, "rollback_failed", "down failed and rollback failed: "+rollbackErr.Error())
			}
			if os.IsPermission(err) || errors.Is(err, os.ErrPermission) {
				return fail(stderr, jsonOut, ExitPerm, "permission", err.Error())
			}
			return fail(stderr, jsonOut, ExitError, "dns_failed", err.Error())
		}
	}
	for _, ref := range reloadRefs {
		if !daemonClientForRef(ref).IsRunning() {
			continue
		}
		if err := setListenerRoutesWithActivity(ref, stderr, jsonOut, "reloading routes"); err != nil {
			rollbackErr := restoreProjectDNS(reservationsFromRegistry(deactivated), stderr, jsonOut)
			rollbackErr = errors.Join(rollbackErr, restoreReservations(previous, scope, stderr, jsonOut))
			if rollbackErr != nil {
				return fail(stderr, jsonOut, ExitError, "rollback_failed", "down failed and rollback failed: "+rollbackErr.Error())
			}
			return fail(stderr, jsonOut, ExitError, "reload_failed", err.Error())
		}
	}
	if jsonOut {
		out := map[string]any{"down": true}
		if scope.Kind == daemonScopeProject {
			out["project"] = scope.Name
		} else {
			out["global"] = true
		}
		return writeJSON(stdout, out)
	}
	printSuccess(stdout, scope.String()+" down (reservations preserved)")
	return ExitOK
}

func printRoute(stdout io.Writer, owner, domain string, port int) {
	if richOut(stdout, false) {
		printKV(stdout, owner, fmt.Sprintf("%s -> :%d", domain, port))
		return
	}
	fmt.Fprintf(stdout, "%s  %s -> :%d\n", owner, domain, port)
}

func reservationsFromRegistry(reservations []registry.Reservation) []projectReservation {
	out := make([]projectReservation, 0, len(reservations))
	for _, res := range reservations {
		out = append(out, projectReservation{Key: registry.Key(res.Project, res.Service), Reservation: res})
	}
	return out
}

func resolvePort(reg *registry.Registry, project, name string, svc config.Service, used map[int]bool) (int, bool, error) {
	if svc.Port != 0 {
		if existing, ok := reg.Get(registry.Key(project, name)); ok && existing.Port == svc.Port {
			return existing.Port, false, nil
		}
		return svc.Port, true, nil
	}
	if existing, ok := reg.Get(registry.Key(project, name)); ok && existing.Port != 0 {
		return existing.Port, false, nil
	}
	p, err := port.Allocate(port.DefaultPool, used)
	return p, true, err
}

func activeRoutesForListener(reg *registry.Registry, pair listener.Pair) []proxy.Route {
	pair = listener.Normalize(pair)
	var rs []proxy.Route
	for _, k := range reg.Keys() {
		res := reg.Services[k]
		if !res.Active || res.Port == 0 {
			continue
		}
		if !listener.Equivalent(res.ListenerPair(), pair) {
			continue
		}
		rs = append(rs, proxy.Route{Domain: res.Domain, Upstream: fmt.Sprintf("127.0.0.1:%d", res.Port)})
	}
	return rs
}

func appendListenerRef(refs []listenerDaemonRef, ref listenerDaemonRef) []listenerDaemonRef {
	for _, existing := range refs {
		if existing.fileKey() == ref.fileKey() {
			return refs
		}
	}
	return append(refs, ref)
}

func sortedServices(p *config.Project) []string {
	names := make([]string, 0, len(p.Services))
	for n := range p.Services {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

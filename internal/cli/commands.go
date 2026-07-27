package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"gate/internal/config"
	"gate/internal/daemon"
	"gate/internal/dns"
	"gate/internal/fsutil"
	"gate/internal/listener"
	"gate/internal/port"
	"gate/internal/proxy"
	"gate/internal/registry"
	"gate/internal/ui"

	"golang.org/x/term"
)

// service is one row of `gate ls` output.
type service struct {
	Project     string `json:"project,omitempty"`
	Service     string `json:"service"`
	Domain      string `json:"domain"`
	Port        int    `json:"port"`
	DNS         string `json:"dns,omitempty"`
	Route       string `json:"route"`
	Upstream    string `json:"upstream"`
	Standalone  bool   `json:"standalone,omitempty"`
	URL         string `json:"url"`
	LoopbackURL string `json:"loopbackUrl"`
}

type projectReservation struct {
	Key string
	registry.Reservation
}

type portRow struct {
	Project    string `json:"project,omitempty"`
	Service    string `json:"service"`
	Domain     string `json:"domain"`
	Port       int    `json:"port"`
	Route      string `json:"route"`
	Upstream   string `json:"upstream"`
	Standalone bool   `json:"standalone,omitempty"`
}

type runDescriptor struct {
	Service     string            `json:"service"`
	Project     string            `json:"project,omitempty"`
	Standalone  bool              `json:"standalone,omitempty"`
	Domain      string            `json:"domain"`
	Port        int               `json:"port"`
	URL         string            `json:"url"`
	LoopbackURL string            `json:"loopbackUrl"`
	Route       string            `json:"route"`
	Upstream    string            `json:"upstream"`
	Env         map[string]string `json:"env"`
	EnvKeys     []string          `json:"envKeys"`
	Daemon      runDaemonStatus   `json:"daemon"`
	Diagnostics []runDiagnostic   `json:"diagnostics"`
}

type runDaemonStatus struct {
	Required  bool   `json:"required"`
	Running   bool   `json:"running"`
	Listener  string `json:"listener"`
	HTTPSAddr string `json:"httpsAddr,omitempty"`
	HTTPAddr  string `json:"httpAddr,omitempty"`
}

type runDiagnostic struct {
	Code             string                `json:"code"`
	Severity         string                `json:"severity"`
	Message          string                `json:"message"`
	SuggestedCommand string                `json:"suggestedCommand,omitempty"`
	Actions          []runDiagnosticAction `json:"actions,omitempty"`
}

type runDiagnosticAction struct {
	Label   string `json:"label"`
	Command string `json:"command,omitempty"`
}

type reservationLookupError struct {
	Exit    int
	Code    string
	Message string
}

var (
	selectDNSProvider     = dns.Select
	setListenerRoutesFunc = setListenerRoutes
	stdinIsTTYFunc        = stdinIsTTY
	routeHintEnabledFunc  = routeHintEnabled
	listenerStatusFunc    = func(ref listenerDaemonRef) (daemon.Status, error) { return daemonClientForRef(ref).Status() }
)

func liveness(p int) string {
	if p != 0 && port.IsLive(p) {
		return "live"
	}
	return "down"
}

func routeStatus(res registry.Reservation) string {
	if res.Active {
		return "active"
	}
	return "inactive"
}

func upstreamStatus(res registry.Reservation) string {
	return liveness(res.Port)
}

func displayDomainURL(domain string) string {
	return "https://" + domain
}

type listenerRouteInfo struct {
	running   bool
	httpsAddr string
	httpAddr  string
}

type routeURLResolver struct {
	listeners map[string]listenerRouteInfo
}

func newRouteURLResolver() *routeURLResolver {
	return &routeURLResolver{listeners: map[string]listenerRouteInfo{}}
}

func (r *routeURLResolver) listenerInfo(res registry.Reservation) listenerRouteInfo {
	ref := listenerRefFor(res.ListenerPair())
	if cached, ok := r.listeners[ref.String()]; ok {
		return cached
	}
	info := listenerRouteInfo{}
	st, err := listenerStatusFunc(ref)
	if err == nil && daemonStatusMatchesListener(st, ref.Pair) {
		info = listenerRouteInfo{running: true, httpsAddr: st.HTTPSAddr, httpAddr: st.HTTPAddr}
	}
	r.listeners[ref.String()] = info
	return info
}

func (r *routeURLResolver) URL(res registry.Reservation) string {
	info := r.listenerInfo(res)
	if !info.running {
		return displayDomainURL(res.Domain)
	}
	return proxyURL(res.Domain, info.httpsAddr)
}

func routeHintEnabled(w io.Writer) bool {
	ciValue := strings.ToLower(strings.TrimSpace(os.Getenv("CI")))
	if ciValue != "" && ciValue != "false" && ciValue != "0" {
		return false
	}
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

func currentProjectPath() (*config.Project, string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, "", err
	}
	path, err := config.Discover(cwd)
	if err != nil {
		return nil, "", err
	}
	p, err := config.Load(path)
	return p, path, err
}

func currentProject() (*config.Project, error) {
	p, _, err := currentProjectPath()
	return p, err
}

// Ls prints all reservations with live/down status.
func Ls(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("ls", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit JSON")
	scopeFlags := defineDaemonScopeFlags(fs, true)
	route := fs.String("route", "", "filter by route: active|inactive")
	upstream := fs.String("upstream", "", "filter by upstream: live|down")
	if handled, code := parseNoArgFlags(fs, "ls", args, stdout, stderr); handled {
		return code
	}
	sel, err := registryScopeFromFlags(scopeFlags, true)
	if err != nil {
		return fail(stderr, *jsonOut, ExitUsage, "bad_scope", err.Error())
	}
	if *route != "" && *route != "active" && *route != "inactive" {
		return fail(stderr, *jsonOut, ExitUsage, "bad_route", "route must be active or inactive")
	}
	if *upstream != "" && *upstream != "live" && *upstream != "down" {
		return fail(stderr, *jsonOut, ExitUsage, "bad_upstream", "upstream must be live or down")
	}

	reg, err := registryStore().Read()
	if err != nil {
		return fail(stderr, *jsonOut, ExitError, "registry_error", err.Error())
	}

	rows := make([]service, 0, len(reg.Services))
	urlResolver := newRouteURLResolver()
	for _, k := range reg.Keys() {
		res := reg.Services[k]
		if !reservationMatchesScope(res, sel) {
			continue
		}
		rowRoute := routeStatus(res)
		rowUpstream := upstreamStatus(res)
		if *route != "" && rowRoute != *route {
			continue
		}
		if *upstream != "" && rowUpstream != *upstream {
			continue
		}
		rows = append(rows, service{
			Project: res.Project, Service: res.Service, Domain: res.Domain,
			Port: res.Port, DNS: res.DNS, Route: rowRoute, Upstream: rowUpstream, Standalone: res.Standalone,
			URL: urlResolver.URL(res), LoopbackURL: loopbackURL(res.Port),
		})
	}

	if *jsonOut {
		return writeJSON(stdout, map[string]any{"services": rows})
	}
	if len(rows) == 0 {
		printEmpty(stdout, "No reservations yet — run `gate up` in a project or `gate add <service> <port>`.", "No reservations.")
		return ExitOK
	}
	if richOut(stdout, false) {
		headers := []string{"SERVICE", "DOMAIN", "PORT", "SCOPE", "ROUTE", "UPSTREAM"}
		data := make([][]string, 0, len(rows))
		for _, r := range rows {
			data = append(data, []string{
				r.Service, displayDomainURL(r.Domain), strconv.Itoa(r.Port), displayServiceScope(r), r.Route, statusDot(r.Upstream, true),
			})
		}
		fmt.Fprintln(stdout, ui.Render(headers, data))
		return ExitOK
	}
	color := isTTY(stdout)
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SERVICE\tDOMAIN\tPORT\tSCOPE\tROUTE\tUPSTREAM")
	for _, r := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\t%s\n", r.Service, displayDomainURL(r.Domain), r.Port, displayServiceScope(r), r.Route, statusDot(r.Upstream, color))
	}
	_ = tw.Flush()
	return ExitOK
}

func displayServiceScope(r service) string {
	if r.Project != "" {
		return r.Project
	}
	if r.Standalone {
		return daemonScopeGlobal
	}
	return "-"
}

// Port prints the reserved port for a service, or lists reserved ports when no
// service is passed.
func Port(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("port", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit JSON")
	scopeFlags := defineDaemonScopeFlags(fs, true)
	if handled, code := parseFlags(fs, "port", args, stdout, stderr); handled {
		return code
	}
	sel, err := registryScopeFromFlags(scopeFlags, true)
	if err != nil {
		return fail(stderr, *jsonOut, ExitUsage, "bad_scope", err.Error())
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return listPorts(stdout, stderr, *jsonOut, sel)
	}
	if len(rest) != 1 {
		return usageFail(stderr, *jsonOut, "port")
	}
	svc := rest[0]
	res, lerr := lookupScopedReservation(svc, sel)
	if lerr != nil {
		return fail(stderr, *jsonOut, lerr.Exit, lerr.Code, lerr.Message)
	}
	if *jsonOut {
		out := map[string]any{"service": res.Service, "port": res.Port}
		if res.Standalone {
			out["standalone"] = true
		}
		return writeJSON(stdout, out)
	}
	fmt.Fprintln(stdout, res.Port)
	return ExitOK
}

func listPorts(stdout, stderr io.Writer, jsonOut bool, sel registryScopeSelection) int {
	reg, err := registryStore().Read()
	if err != nil {
		return fail(stderr, jsonOut, ExitError, "registry_error", err.Error())
	}
	rows := make([]portRow, 0, len(reg.Services))
	for _, k := range reg.Keys() {
		res := reg.Services[k]
		if !reservationMatchesScope(res, sel) {
			continue
		}
		if res.Port == 0 {
			continue
		}
		rows = append(rows, portRow{
			Project:    res.Project,
			Service:    res.Service,
			Domain:     res.Domain,
			Port:       res.Port,
			Route:      routeStatus(res),
			Upstream:   upstreamStatus(res),
			Standalone: res.Standalone,
		})
	}
	if jsonOut {
		return writeJSON(stdout, map[string]any{"ports": rows})
	}
	if len(rows) == 0 {
		printEmpty(stdout, "No reserved ports yet — run `gate up` in a project or `gate add <service> <port>`.", "No reserved ports.")
		return ExitOK
	}
	if richOut(stdout, false) {
		headers := []string{"SERVICE", "PORT", "TARGET", "SCOPE", "ROUTE", "UPSTREAM"}
		data := make([][]string, 0, len(rows))
		for _, r := range rows {
			data = append(data, []string{
				r.Service, strconv.Itoa(r.Port), displayDomainURL(r.Domain), displayPortScope(r), r.Route, statusDot(r.Upstream, true),
			})
		}
		fmt.Fprintln(stdout, ui.Render(headers, data))
		return ExitOK
	}
	color := isTTY(stdout)
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SERVICE\tPORT\tTARGET\tSCOPE\tROUTE\tUPSTREAM")
	for _, r := range rows {
		fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\t%s\n", r.Service, r.Port, displayDomainURL(r.Domain), displayPortScope(r), r.Route, statusDot(r.Upstream, color))
	}
	_ = tw.Flush()
	return ExitOK
}

func displayPortScope(r portRow) string {
	if r.Project != "" {
		return r.Project
	}
	if r.Standalone {
		return daemonScopeGlobal
	}
	return "-"
}

// Add reserves a service/name -> domain -> port mapping in the selected scope.
func Add(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit JSON")
	hostFlag := fs.String("host", "", "service host label under project base")
	domainFlag := fs.String("domain", "", "full service domain")
	scopeFlags := defineDaemonScopeFlags(fs, false)
	if handled, code := parseFlags(fs, "add", args, stdout, stderr); handled {
		return code
	}
	rest := fs.Args()
	if len(rest) != 2 {
		return usageFail(stderr, *jsonOut, "add")
	}
	if *hostFlag != "" && *domainFlag != "" {
		return fail(stderr, *jsonOut, ExitUsage, "bad_service", "--host and --domain are mutually exclusive")
	}
	name := strings.TrimSpace(rest[0])
	if err := validateRegistryName(name, "service"); err != nil {
		return fail(stderr, *jsonOut, ExitUsage, "bad_service", err.Error())
	}
	domain := config.CanonicalDomain(*domainFlag)
	host := config.CanonicalHost(*hostFlag)
	if domain != "" {
		if err := config.ValidateDomain(domain); err != nil {
			return fail(stderr, *jsonOut, ExitUsage, "bad_domain", err.Error())
		}
	}
	p, err := strconv.Atoi(rest[1])
	if err != nil || p < 1 || p > 65535 {
		return fail(stderr, *jsonOut, ExitUsage, "bad_port", "port must be 1-65535")
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
	if sel.Scope.Kind == daemonScopeGlobal {
		if host != "" {
			return fail(stderr, *jsonOut, ExitUsage, "bad_service", "--host requires a project base")
		}
		if domain == "" {
			return fail(stderr, *jsonOut, ExitUsage, "bad_domain", "--domain is required for global reservations")
		}
		res := registry.Reservation{Service: name, Domain: domain, Port: p, TLS: config.TLSInternal, Standalone: true}
		res.Active = true
		res.DNS = dns.ModeFor(domain, "")
		return addStandalone(res, stdout, stderr, *jsonOut)
	}

	project := sel.CurrentProject
	path := sel.CurrentProjectPath
	if project == nil {
		_, path, err = projectConfigForName(sel.Scope.Name)
		if err != nil {
			return fail(stderr, *jsonOut, ExitError, "project", err.Error())
		}
	}
	configUnlock, err := config.LockFile(path)
	if err != nil {
		return fail(stderr, *jsonOut, ExitError, "config_lock", err.Error())
	}
	defer configUnlock()
	project, err = config.Load(path)
	if err != nil {
		return fail(stderr, *jsonOut, ExitError, "config", err.Error())
	}
	if sel.Scope.Kind == daemonScopeProject && project.Name != sel.Scope.Name {
		return fail(stderr, *jsonOut, ExitConflict, "project_changed", fmt.Sprintf("config %s now belongs to project %q", path, project.Name))
	}
	svc := config.Service{Domain: domain, Host: host, Port: p, TLS: config.TLSInternal}
	resolvedDomain, err := resolveConfigServiceDomain(project, name, svc)
	if err != nil {
		return fail(stderr, *jsonOut, ExitUsage, "bad_service", err.Error())
	}
	res := registry.Reservation{
		Project: project.Name, Service: name, Domain: resolvedDomain, Port: p,
		TLS: config.TLSInternal, ConfigPath: path,
	}
	key := registry.Key(project.Name, name)
	reg, err := registryStore().Read()
	if err != nil {
		return fail(stderr, *jsonOut, ExitError, "registry_error", err.Error())
	}
	previous, hadPrevious := reg.Get(key)
	if hadPrevious {
		if config.CanonicalDomain(previous.Domain) != config.CanonicalDomain(res.Domain) {
			if err := ensureReservationsNotExposed([]registry.Reservation{previous}); err != nil {
				if code, ok := exposureGuardFailure(stderr, *jsonOut, err); ok {
					return code
				}
				return fail(stderr, *jsonOut, ExitError, "exposure_guard", err.Error())
			}
		}
		res.Active = previous.Active
		res.SetListenerPair(previous.ListenerPair())
		if config.CanonicalDomain(previous.Domain) == config.CanonicalDomain(res.Domain) {
			res.DNS = previous.DNS
		} else {
			res.DNS = dns.ModeFor(res.Domain, "")
		}
		if res.Active && res.DNS == "" {
			res.DNS = dns.ModeFor(res.Domain, "")
		}
	}
	if err := registryStore().ReadReserve(res); err != nil {
		return addError(stderr, *jsonOut, err)
	}
	originalConfig, err := os.ReadFile(path)
	if err != nil {
		return fail(stderr, *jsonOut, ExitError, "config", err.Error())
	}
	originalInfo, err := os.Stat(path)
	if err != nil {
		return fail(stderr, *jsonOut, ExitError, "config", err.Error())
	}
	if err := config.UpsertService(path, name, svc); err != nil {
		return fail(stderr, *jsonOut, ExitError, "config", err.Error())
	}
	err = registryStore().Update(func(r *registry.Registry) error { return r.Reserve(res) })
	var ce *registry.ConflictError
	if errors.As(err, &ce) {
		if restoreErr := restoreProjectConfig(path, originalConfig, originalInfo.Mode().Perm()); restoreErr != nil {
			return fail(stderr, *jsonOut, ExitError, "rollback_failed", "service add failed and config rollback failed: "+restoreErr.Error())
		}
		return addError(stderr, *jsonOut, err)
	}
	if err != nil {
		if restoreErr := restoreProjectConfig(path, originalConfig, originalInfo.Mode().Perm()); restoreErr != nil {
			return fail(stderr, *jsonOut, ExitError, "rollback_failed", "service add failed and config rollback failed: "+restoreErr.Error())
		}
		return fail(stderr, *jsonOut, ExitError, "registry_error", err.Error())
	}
	if res.Active {
		reloadRefs := []listenerDaemonRef{listenerRefFor(res.ListenerPair())}
		if hadPrevious {
			reloadRefs = appendListenerRef(reloadRefs, listenerRefFor(previous.ListenerPair()))
		}
		if err := ensureDomainDNS(res.Domain, res.DNS, stderr, *jsonOut); err != nil {
			rollbackErr := rollbackProjectAdd(path, originalConfig, originalInfo.Mode().Perm(), key, previous, hadPrevious, reloadRefs, []registry.Reservation{res}, stderr, *jsonOut)
			if rollbackErr != nil {
				return fail(stderr, *jsonOut, ExitError, "rollback_failed", "service add failed and rollback failed: "+rollbackErr.Error())
			}
			if os.IsPermission(err) || errors.Is(err, os.ErrPermission) {
				return fail(stderr, *jsonOut, ExitPerm, "permission", err.Error())
			}
			return fail(stderr, *jsonOut, ExitError, "dns_failed", err.Error())
		}
		if code := reloadListenerRoutes(reloadRefs, stderr, *jsonOut); code != ExitOK {
			rollbackErr := rollbackProjectAdd(path, originalConfig, originalInfo.Mode().Perm(), key, previous, hadPrevious, reloadRefs, []registry.Reservation{res}, stderr, *jsonOut)
			if rollbackErr != nil {
				return fail(stderr, *jsonOut, ExitError, "rollback_failed", "service add failed and rollback failed: "+rollbackErr.Error())
			}
			return code
		}
		if hadPrevious {
			if err := removeStaleReservationDNS([]registry.Reservation{previous}, stderr, *jsonOut); err != nil {
				rollbackErr := rollbackProjectAdd(path, originalConfig, originalInfo.Mode().Perm(), key, previous, hadPrevious, reloadRefs, []registry.Reservation{res}, stderr, *jsonOut)
				if rollbackErr != nil {
					return fail(stderr, *jsonOut, ExitError, "rollback_failed", "DNS cleanup failed and rollback failed: "+rollbackErr.Error())
				}
				if os.IsPermission(err) || errors.Is(err, os.ErrPermission) {
					return fail(stderr, *jsonOut, ExitPerm, "permission", err.Error())
				}
				return fail(stderr, *jsonOut, ExitError, "dns_failed", err.Error())
			}
		}
	}
	if *jsonOut {
		return writeJSON(stdout, map[string]any{"project": project.Name, "service": res.Service, "domain": resolvedDomain, "port": p, "reserved": true})
	}
	printSuccess(stdout, fmt.Sprintf("reserved %s/%s  %s -> :%d", project.Name, name, resolvedDomain, p))
	return ExitOK
}

func resolveConfigServiceDomain(project *config.Project, name string, svc config.Service) (string, error) {
	if project == nil {
		return "", errors.New("current project is required")
	}
	next := &config.Project{
		Name:     project.Name,
		Base:     project.Base,
		EnvFiles: append([]string{}, project.EnvFiles...),
		Services: map[string]config.Service{},
	}
	for existingName, existingSvc := range project.Services {
		next.Services[existingName] = existingSvc
	}
	next.Services[name] = svc
	if err := next.Validate(); err != nil {
		return "", err
	}
	return next.ServiceDomain(name)
}

func addStandalone(res registry.Reservation, stdout, stderr io.Writer, jsonOut bool) int {
	key := registry.Key(res.Project, res.Service)
	reg, err := registryStore().Read()
	if err != nil {
		return fail(stderr, jsonOut, ExitError, "registry_error", err.Error())
	}
	previous, hadPrevious := reg.Get(key)
	if hadPrevious {
		sameDomain := config.CanonicalDomain(previous.Domain) == config.CanonicalDomain(res.Domain)
		if !sameDomain {
			if err := ensureReservationsNotExposed([]registry.Reservation{previous}); err != nil {
				if code, ok := exposureGuardFailure(stderr, jsonOut, err); ok {
					return code
				}
				return fail(stderr, jsonOut, ExitError, "exposure_guard", err.Error())
			}
		} else {
			res.DNS = previous.DNS
		}
		res.SetListenerPair(previous.ListenerPair())
	}
	if err := registryStore().ReadReserve(res); err != nil {
		return addError(stderr, jsonOut, err)
	}
	reloadRefs := []listenerDaemonRef{listenerRefFor(res.ListenerPair())}
	if hadPrevious {
		reloadRefs = appendListenerRef(reloadRefs, listenerRefFor(previous.ListenerPair()))
	}
	err = registryStore().Update(func(r *registry.Registry) error {
		if err := r.Reserve(res); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return addError(stderr, jsonOut, err)
	}
	if err := ensureDomainDNS(res.Domain, res.DNS, stderr, jsonOut); err != nil {
		rollbackErr := rollbackStandaloneAddDNS(key, previous, hadPrevious, reloadRefs, []registry.Reservation{res}, stderr, jsonOut)
		if rollbackErr != nil {
			return fail(stderr, jsonOut, ExitError, "rollback_failed", "service add failed and rollback failed: "+rollbackErr.Error())
		}
		if os.IsPermission(err) || errors.Is(err, os.ErrPermission) {
			return fail(stderr, jsonOut, ExitPerm, "permission", err.Error())
		}
		return fail(stderr, jsonOut, ExitError, "dns_failed", err.Error())
	}
	if code := reloadListenerRoutes(reloadRefs, stderr, jsonOut); code != ExitOK {
		if rollbackErr := rollbackStandaloneAddDNS(key, previous, hadPrevious, reloadRefs, []registry.Reservation{res}, stderr, jsonOut); rollbackErr != nil {
			return fail(stderr, jsonOut, ExitError, "rollback_failed", "service add failed and rollback failed: "+rollbackErr.Error())
		}
		return code
	}
	if hadPrevious {
		if err := removeStaleReservationDNS([]registry.Reservation{previous}, stderr, jsonOut); err != nil {
			rollbackErr := rollbackStandaloneAddDNS(key, previous, true, reloadRefs, []registry.Reservation{res}, stderr, jsonOut)
			if rollbackErr != nil {
				return fail(stderr, jsonOut, ExitError, "rollback_failed", "DNS cleanup failed and rollback failed: "+rollbackErr.Error())
			}
			if os.IsPermission(err) || errors.Is(err, os.ErrPermission) {
				return fail(stderr, jsonOut, ExitPerm, "permission", err.Error())
			}
			return fail(stderr, jsonOut, ExitError, "dns_failed", err.Error())
		}
	}
	if jsonOut {
		return writeJSON(stdout, map[string]any{"service": res.Service, "domain": res.Domain, "port": res.Port, "reserved": true, "standalone": true})
	}
	printSuccess(stdout, fmt.Sprintf("reserved %s  %s -> :%d", res.Service, res.Domain, res.Port))
	return ExitOK
}

// Rm removes one reservation from the selected scope.
func Rm(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("rm", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit JSON")
	scopeFlags := defineDaemonScopeFlags(fs, false)
	if handled, code := parseFlags(fs, "rm", args, stdout, stderr); handled {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return usageFail(stderr, *jsonOut, "rm")
	}
	name := rest[0]
	if strings.TrimSpace(name) == "" {
		return fail(stderr, *jsonOut, ExitUsage, "bad_service", "service is required")
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

	if sel.Scope.Kind == daemonScopeProject && sel.CurrentProjectSelected {
		return rmCurrentProjectService(sel, name, stdout, stderr, *jsonOut)
	}
	return rmScopedReservation(sel, name, stdout, stderr, *jsonOut)
}

func rmCurrentProjectService(sel registryScopeSelection, name string, stdout, stderr io.Writer, jsonOut bool) int {
	project := sel.CurrentProject
	path := sel.CurrentProjectPath
	if project == nil || path == "" {
		return fail(stderr, jsonOut, ExitError, "no_project", "current project is required")
	}
	configUnlock, err := config.LockFile(path)
	if err != nil {
		return fail(stderr, jsonOut, ExitError, "config_lock", err.Error())
	}
	defer configUnlock()
	project, err = config.Load(path)
	if err != nil {
		return fail(stderr, jsonOut, ExitError, "config", err.Error())
	}
	if project.Name != sel.Scope.Name {
		return fail(stderr, jsonOut, ExitConflict, "project_changed", fmt.Sprintf("config %s now belongs to project %q", path, project.Name))
	}
	if _, ok := project.Services[name]; !ok {
		return fail(stderr, jsonOut, ExitError, "no_service", fmt.Sprintf("no service %q in project", name))
	}
	originalConfig, err := os.ReadFile(path)
	if err != nil {
		return fail(stderr, jsonOut, ExitError, "config", err.Error())
	}
	originalInfo, err := os.Stat(path)
	if err != nil {
		return fail(stderr, jsonOut, ExitError, "config", err.Error())
	}

	reg, err := registryStore().Read()
	if err != nil {
		return fail(stderr, jsonOut, ExitError, "registry_error", err.Error())
	}
	key := registry.Key(project.Name, name)
	removedRes, hadReservation := reg.Get(key)
	owner := registry.Reservation{Project: project.Name, Service: name}
	if hadReservation {
		owner = removedRes
	}
	if err := ensureReservationsNotExposed([]registry.Reservation{owner}); err != nil {
		if code, ok := exposureGuardFailure(stderr, jsonOut, err); ok {
			return code
		}
		return fail(stderr, jsonOut, ExitError, "exposure_guard", err.Error())
	}
	reloadRefs := []listenerDaemonRef{defaultListenerRef()}
	if hadReservation {
		reloadRefs = []listenerDaemonRef{listenerRefFor(removedRes.ListenerPair())}
	}
	if err := config.RemoveService(path, name); err != nil {
		return fail(stderr, jsonOut, ExitError, "config", err.Error())
	}
	err = registryStore().Update(func(r *registry.Registry) error {
		r.Release(key)
		return nil
	})
	if err != nil {
		if restoreErr := restoreProjectConfig(path, originalConfig, originalInfo.Mode().Perm()); restoreErr != nil {
			return fail(stderr, jsonOut, ExitError, "rollback_failed", "service removal failed and config rollback failed: "+restoreErr.Error())
		}
		return fail(stderr, jsonOut, ExitError, "registry_error", err.Error())
	}
	if code := reloadListenerRoutes(reloadRefs, stderr, jsonOut); code != ExitOK {
		if rollbackErr := restoreProjectServiceRemoval(path, originalConfig, originalInfo.Mode().Perm(), key, removedRes, hadReservation, reloadRefs, stderr, jsonOut); rollbackErr != nil {
			return fail(stderr, jsonOut, ExitError, "rollback_failed", "service removal failed and rollback failed: "+rollbackErr.Error())
		}
		return code
	}
	if hadReservation {
		if err := removeDomainDNS(removedRes.Domain, removedRes.DNS, stderr, jsonOut); err != nil {
			rollbackErr := restoreProjectServiceRemoval(path, originalConfig, originalInfo.Mode().Perm(), key, removedRes, hadReservation, reloadRefs, stderr, jsonOut)
			if rollbackErr != nil {
				return fail(stderr, jsonOut, ExitError, "rollback_failed", "DNS removal failed and rollback failed: "+rollbackErr.Error())
			}
			if os.IsPermission(err) || errors.Is(err, os.ErrPermission) {
				return fail(stderr, jsonOut, ExitPerm, "permission", err.Error())
			}
			return fail(stderr, jsonOut, ExitError, "dns_failed", err.Error())
		}
	}
	if jsonOut {
		return writeJSON(stdout, map[string]any{"scope": daemonScopeProject, "project": project.Name, "service": name, "removed": true})
	}
	printSuccess(stdout, fmt.Sprintf("removed %s/%s", project.Name, name))
	return ExitOK
}

func rmScopedReservation(sel registryScopeSelection, name string, stdout, stderr io.Writer, jsonOut bool) int {
	projectName := ""
	if sel.Scope.Kind == daemonScopeProject {
		projectName = sel.Scope.Name
	}
	key := registry.Key(projectName, name)
	reg, err := registryStore().Read()
	if err != nil {
		return fail(stderr, jsonOut, ExitError, "registry_error", err.Error())
	}
	removedRes, found := reg.Get(key)
	if !found || !reservationMatchesScope(removedRes, sel) {
		return fail(stderr, jsonOut, ExitError, "not_found", fmt.Sprintf("no reservation for %q", name))
	}
	if err := ensureReservationsNotExposed([]registry.Reservation{removedRes}); err != nil {
		if code, ok := exposureGuardFailure(stderr, jsonOut, err); ok {
			return code
		}
		return fail(stderr, jsonOut, ExitError, "exposure_guard", err.Error())
	}
	scope := sel.Scope
	reloadRefs := []listenerDaemonRef{listenerRefFor(removedRes.ListenerPair())}
	err = registryStore().Update(func(r *registry.Registry) error {
		r.Release(key)
		return nil
	})
	if err != nil {
		return fail(stderr, jsonOut, ExitError, "registry_error", err.Error())
	}
	if code := reloadListenerRoutes(reloadRefs, stderr, jsonOut); code != ExitOK {
		if rollbackErr := restoreReservations([]projectReservation{{Key: key, Reservation: removedRes}}, scope, stderr, jsonOut); rollbackErr != nil {
			return fail(stderr, jsonOut, ExitError, "rollback_failed", "removal failed and rollback failed: "+rollbackErr.Error())
		}
		return code
	}
	if err := removeDomainDNS(removedRes.Domain, removedRes.DNS, stderr, jsonOut); err != nil {
		rollbackErr := restoreReservations([]projectReservation{{Key: key, Reservation: removedRes}}, scope, stderr, jsonOut)
		if rollbackErr != nil {
			return fail(stderr, jsonOut, ExitError, "rollback_failed", "removal failed and rollback failed: "+rollbackErr.Error())
		}
		if os.IsPermission(err) || errors.Is(err, os.ErrPermission) {
			return fail(stderr, jsonOut, ExitPerm, "permission", err.Error())
		}
		return fail(stderr, jsonOut, ExitError, "dns_failed", err.Error())
	}
	if jsonOut {
		out := map[string]any{"scope": scope.Kind, "service": name, "removed": true}
		if projectName != "" {
			out["project"] = projectName
		}
		return writeJSON(stdout, out)
	}
	printSuccess(stdout, "removed "+displayReservationOwner(removedRes))
	return ExitOK
}

func removeReservationsDNS(removed []projectReservation, scope daemonScope, stderr io.Writer, jsonOut bool) int {
	for i, item := range removed {
		res := item.Reservation
		if !res.Active {
			continue
		}
		if err := removeDomainDNS(res.Domain, res.DNS, stderr, jsonOut); err != nil {
			rollbackErr := restoreProjectDNS(removed[:i+1], stderr, jsonOut)
			rollbackErr = errors.Join(rollbackErr, restoreProjectReservations(removed, scope, stderr, jsonOut))
			if rollbackErr != nil {
				return fail(stderr, jsonOut, ExitError, "rollback_failed", "DNS removal failed and rollback failed: "+rollbackErr.Error())
			}
			if os.IsPermission(err) || errors.Is(err, os.ErrPermission) {
				return fail(stderr, jsonOut, ExitPerm, "permission", err.Error())
			}
			return fail(stderr, jsonOut, ExitError, "dns_failed", err.Error())
		}
	}
	return ExitOK
}

func restoreProjectDNS(removed []projectReservation, stderr io.Writer, jsonOut bool) error {
	var errs []error
	for _, item := range removed {
		res := item.Reservation
		if !res.Active {
			continue
		}
		if err := ensureDomainDNS(res.Domain, res.DNS, stderr, jsonOut); err != nil {
			errs = append(errs, fmt.Errorf("restore DNS %s: %w", res.Domain, err))
		}
	}
	return errors.Join(errs...)
}

func restoreProjectReservations(removed []projectReservation, scope daemonScope, stderr io.Writer, jsonOut bool) error {
	return restoreReservations(removed, scope, stderr, jsonOut)
}

func restoreProjectServiceRemoval(path string, originalConfig []byte, mode os.FileMode, key string, res registry.Reservation, hadReservation bool, refs []listenerDaemonRef, stderr io.Writer, jsonOut bool) error {
	var errs []error
	if err := restoreProjectConfig(path, originalConfig, mode); err != nil {
		errs = append(errs, fmt.Errorf("restore config: %w", err))
	}
	if hadReservation {
		errs = append(errs, restoreReservations([]projectReservation{{Key: key, Reservation: res}}, daemonScope{}, stderr, jsonOut))
	} else if err := setListenerRoutesForRefsWithActivity(refs, stderr, jsonOut, "restoring routes", "restored routes"); err != nil {
		errs = append(errs, fmt.Errorf("restore daemon routes: %w", err))
	}
	return errors.Join(errs...)
}

func restoreProjectAdd(path string, originalConfig []byte, mode os.FileMode, key string, res registry.Reservation, hadReservation bool, refs []listenerDaemonRef, stderr io.Writer, jsonOut bool) error {
	var errs []error
	if err := restoreProjectConfig(path, originalConfig, mode); err != nil {
		errs = append(errs, fmt.Errorf("restore config: %w", err))
	}
	if hadReservation {
		errs = append(errs, restoreReservations([]projectReservation{{Key: key, Reservation: res}}, daemonScope{}, stderr, jsonOut))
	} else {
		if err := registryStore().Update(func(r *registry.Registry) error {
			r.Release(key)
			return nil
		}); err != nil {
			errs = append(errs, fmt.Errorf("restore registry: %w", err))
		}
		if err := setListenerRoutesForRefsWithActivity(refs, stderr, jsonOut, "restoring routes", "restored routes"); err != nil {
			errs = append(errs, fmt.Errorf("restore daemon routes: %w", err))
		}
	}
	return errors.Join(errs...)
}

func restoreProjectConfig(path string, originalConfig []byte, mode os.FileMode) error {
	target, err := config.ResolveFileTarget(path)
	if err != nil {
		return err
	}
	return fsutil.WriteAtomic(target, originalConfig, mode)
}

func rollbackProjectAdd(path string, originalConfig []byte, mode os.FileMode, key string, previous registry.Reservation, hadPrevious bool, refs []listenerDaemonRef, attemptedDNS []registry.Reservation, stderr io.Writer, jsonOut bool) error {
	rollbackErr := restoreProjectAdd(path, originalConfig, mode, key, previous, hadPrevious, refs, stderr, jsonOut)
	restored := previousReservation(previous, hadPrevious)
	return errors.Join(rollbackErr, reconcileRollbackDNS(attemptedDNS, restored, stderr, jsonOut))
}

func rollbackStandaloneAddDNS(key string, previous registry.Reservation, hadPrevious bool, refs []listenerDaemonRef, attemptedDNS []registry.Reservation, stderr io.Writer, jsonOut bool) error {
	rollbackErr := rollbackStandaloneAdd(key, previous, hadPrevious, refs, stderr, jsonOut)
	restored := previousReservation(previous, hadPrevious)
	return errors.Join(rollbackErr, reconcileRollbackDNS(attemptedDNS, restored, stderr, jsonOut))
}

func previousReservation(previous registry.Reservation, ok bool) []projectReservation {
	if !ok {
		return nil
	}
	return []projectReservation{{Key: registry.Key(previous.Project, previous.Service), Reservation: previous}}
}

func rollbackStandaloneAdd(key string, previous registry.Reservation, hadPrevious bool, refs []listenerDaemonRef, stderr io.Writer, jsonOut bool) error {
	if hadPrevious {
		return restoreReservations([]projectReservation{{Key: key, Reservation: previous}}, daemonScope{}, stderr, jsonOut)
	}
	var errs []error
	if err := registryStore().Update(func(r *registry.Registry) error {
		r.Release(key)
		return nil
	}); err != nil {
		errs = append(errs, fmt.Errorf("remove registry: %w", err))
	}
	if err := setListenerRoutesForRefsWithActivity(refs, stderr, jsonOut, "restoring routes", "restored routes"); err != nil {
		errs = append(errs, fmt.Errorf("restore daemon routes: %w", err))
	}
	return errors.Join(errs...)
}

func restoreReservations(removed []projectReservation, scope daemonScope, stderr io.Writer, jsonOut bool) error {
	var errs []error
	if err := registryStore().Update(func(r *registry.Registry) error {
		for _, item := range removed {
			if err := r.Reserve(item.Reservation); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		errs = append(errs, fmt.Errorf("restore registry: %w", err))
	}
	refs := listenerRefsForReservations(removed)
	if len(refs) == 0 && scope.Kind != "" {
		refs = []listenerDaemonRef{defaultListenerRef()}
	}
	if err := setListenerRoutesForRefsWithActivity(refs, stderr, jsonOut, "restoring routes", "restored routes"); err != nil {
		errs = append(errs, fmt.Errorf("restore daemon routes: %w", err))
	}
	return errors.Join(errs...)
}

func reloadListenerRoutes(refs []listenerDaemonRef, stderr io.Writer, jsonOut bool) int {
	if err := setListenerRoutesForRefsWithActivity(refs, stderr, jsonOut, "reloading routes", "reloaded routes"); err != nil {
		var aliasConflict *exposureAliasConflictError
		if errors.As(err, &aliasConflict) {
			return fail(stderr, jsonOut, ExitConflict, "domain_conflict", aliasConflict.Error())
		}
		return fail(stderr, jsonOut, ExitError, "reload_failed", err.Error())
	}
	return ExitOK
}

func setListenerRoutesWithActivity(ref listenerDaemonRef, stderr io.Writer, jsonOut bool, label, doneLabel string) error {
	activity := startActivity(stderr, jsonOut, label)
	err := setListenerRoutesForRef(ref)
	if err != nil {
		activity.Stop()
	} else {
		activity.Complete(doneLabel)
	}
	return err
}

func setListenerRoutesForRefsWithActivity(refs []listenerDaemonRef, stderr io.Writer, jsonOut bool, label, doneLabel string) error {
	for _, ref := range refs {
		if err := setListenerRoutesWithActivity(ref, stderr, jsonOut, label, doneLabel); err != nil {
			return err
		}
	}
	return nil
}

func setListenerRoutesForRef(ref listenerDaemonRef) error {
	_, err := setListenerRoutesForRefTracked(ref)
	return err
}

func validateListenerRoutesForRef(ref listenerDaemonRef) error {
	return registryStore().View(func(reg *registry.Registry) error {
		_, err := applyExposureRecords(ref.String(), activeRoutesForListener(reg, ref.Pair))
		return err
	})
}

func setListenerRoutesForRefTracked(ref listenerDaemonRef) (bool, error) {
	attempted := false
	err := registryStore().View(func(reg *registry.Registry) error {
		routes := activeRoutesForListener(reg, ref.Pair)
		routes, err := applyExposureRecords(ref.String(), routes)
		if err != nil {
			return err
		}
		attempted = true
		return setListenerRoutesFunc(ref, routes)
	})
	return attempted, err
}

func setListenerRoutes(ref listenerDaemonRef, routes []proxy.Route) error {
	client := daemonClientForRef(ref)
	if !client.IsRunning() {
		return nil
	}
	return client.SetRoutes(routes)
}

func listenerRefsForReservations(reservations []projectReservation) []listenerDaemonRef {
	refs := make([]listenerDaemonRef, 0, len(reservations))
	for _, item := range reservations {
		refs = appendListenerRef(refs, listenerRefFor(item.ListenerPair()))
	}
	return refs
}

func ensureDomainDNS(domain, mode string, stderr io.Writer, jsonOut bool) error {
	provider := selectDNSProvider(domain, mode)
	return runDomainDNS(provider, domain, stderr, jsonOut, "updating DNS", "updated DNS", provider.Ensure)
}

func removeDomainDNS(domain, mode string, stderr io.Writer, jsonOut bool) error {
	provider := selectDNSProvider(domain, mode)
	return runDomainDNS(provider, domain, stderr, jsonOut, "updating DNS", "updated DNS", provider.Remove)
}

func runDomainDNS(provider dns.Provider, domain string, stderr io.Writer, jsonOut bool, label, doneLabel string, fn func(string) error) error {
	if hosts, ok := provider.(dns.Hosts); ok && hosts.Path == "/etc/hosts" && os.Getenv("GATE_ISOLATED_ROOT") != "" {
		return errors.New("isolated gate state cannot mutate the shared system hosts file; use a .localhost domain or run without GATE_ISOLATED_ROOT")
	}
	if !dnsActivityAllowed(provider) {
		return fn(domain)
	}
	activity := startActivity(stderr, jsonOut, label)
	err := fn(domain)
	if err != nil {
		activity.Stop()
	} else {
		activity.Complete(doneLabel)
	}
	return err
}

func dnsActivityAllowed(provider dns.Provider) bool {
	switch p := provider.(type) {
	case dns.Localhost:
		return false
	case dns.Hosts:
		return p.Path != "/etc/hosts"
	default:
		return true
	}
}

func addError(stderr io.Writer, jsonOut bool, err error) int {
	var ce *registry.ConflictError
	if errors.As(err, &ce) {
		if ce.Domain != "" {
			return fail(stderr, jsonOut, ExitConflict, "domain_conflict", ce.Error())
		}
		return fail(stderr, jsonOut, ExitConflict, "port_conflict", ce.Error())
	}
	return fail(stderr, jsonOut, ExitError, "registry_error", err.Error())
}

// Clear removes every reservation in the selected project or global scope.
func Clear(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("clear", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit JSON")
	yes := fs.Bool("yes", false, "skip confirmation")
	fs.BoolVar(yes, "y", false, "skip confirmation")
	scopeFlags := defineDaemonScopeFlags(fs, false)
	if handled, code := parseNoArgFlags(fs, "clear", args, stdout, stderr); handled {
		return code
	}
	sel, err := registryScopeFromFlags(scopeFlags, false)
	if err != nil {
		return fail(stderr, *jsonOut, ExitUsage, "bad_scope", err.Error())
	}
	preserveLegacyProjectSelector(&sel, scopeFlags)
	reg, err := registryStore().Read()
	if err != nil {
		return fail(stderr, *jsonOut, ExitError, "registry_error", err.Error())
	}
	removed := reservationsForScope(reg, sel)
	confirmedRemoved := append([]projectReservation(nil), removed...)
	scope := sel.Scope
	if !*yes {
		if code := confirmClear(sel, len(removed), stdout, stderr, *jsonOut); code != ExitOK {
			return code
		}
	}
	unlock, code := acquireStateMutation(stderr, *jsonOut)
	if code != ExitOK {
		return code
	}
	defer unlock()
	reg, err = registryStore().Read()
	if err != nil {
		return fail(stderr, *jsonOut, ExitError, "registry_error", err.Error())
	}
	removed = reservationsForScope(reg, sel)
	reloadRefs := listenerRefsForReservations(removed)
	if !reflect.DeepEqual(confirmedRemoved, removed) {
		return fail(stderr, *jsonOut, ExitConflict, "state_changed", "reservations changed while clear was waiting; review and retry")
	}
	if err := ensureReservationsNotExposed(reservationsFromProjectReservations(removed)); err != nil {
		if code, ok := exposureGuardFailure(stderr, *jsonOut, err); ok {
			return code
		}
		return fail(stderr, *jsonOut, ExitError, "exposure_guard", err.Error())
	}

	err = registryStore().Update(func(r *registry.Registry) error {
		for _, item := range removed {
			r.Release(item.Key)
		}
		return nil
	})
	if err != nil {
		return fail(stderr, *jsonOut, ExitError, "registry_error", err.Error())
	}
	if code := reloadListenerRoutes(reloadRefs, stderr, *jsonOut); code != ExitOK {
		if err := restoreReservations(removed, scope, stderr, *jsonOut); err != nil {
			return fail(stderr, *jsonOut, ExitError, "rollback_failed", "clear failed and rollback failed: "+err.Error())
		}
		return code
	}
	if code := removeReservationsDNS(removed, scope, stderr, *jsonOut); code != ExitOK {
		return code
	}
	if *jsonOut {
		out := map[string]any{"scope": scope.Kind, "removed": true, "reservations": len(removed)}
		if scope.Kind == daemonScopeProject {
			out["project"] = scope.Name
		}
		return writeJSON(stdout, out)
	}
	printSuccess(stdout, fmt.Sprintf("removed %s (%d reservations)", clearScopeLabel(scope), len(removed)))
	return ExitOK
}

func confirmClear(sel registryScopeSelection, count int, stdout, stderr io.Writer, jsonOut bool) int {
	if jsonOut || !stdinIsTTYFunc() {
		return fail(stderr, jsonOut, ExitUsage, "confirmation_required", "pass -y to clear reservations")
	}
	label := fmt.Sprintf("Remove %s (%d reservations)?", clearScopeLabel(sel.Scope), count)
	confirmed, err := promptConfirm(bufio.NewReader(os.Stdin), stdout, label)
	if err != nil {
		return fail(stderr, false, ExitError, "confirm_failed", err.Error())
	}
	if !confirmed {
		return fail(stderr, false, ExitError, "cancelled", "clear cancelled")
	}
	return ExitOK
}

func clearScopeLabel(scope daemonScope) string {
	if scope.Kind == daemonScopeProject {
		return "project " + scope.Name
	}
	return "global reservations"
}

func preserveLegacyProjectSelector(sel *registryScopeSelection, flags daemonScopeFlags) {
	if flags.project == nil || !flags.project.set || flags.config != nil && flags.config.set {
		return
	}
	raw := flags.project.value
	if raw == strings.TrimSpace(raw) {
		return
	}
	sel.Scope = daemonScope{Kind: daemonScopeProject, Name: raw}
	sel.CurrentProject = nil
	sel.CurrentProjectPath = ""
	sel.CurrentProjectSelected = false
}

func stdinIsTTY() bool {
	info, err := os.Stdin.Stat()
	return err == nil && (info.Mode()&os.ModeCharDevice) != 0
}

// Prune garbage-collects reservations whose owning gate.toml no longer exists.
func Prune(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("prune", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit JSON")
	if handled, code := parseNoArgFlags(fs, "prune", args, stdout, stderr); handled {
		return code
	}
	unlock, code := acquireStateMutation(stderr, *jsonOut)
	if code != ExitOK {
		return code
	}
	defer unlock()
	var removed []registry.Reservation
	err := registryStore().Update(func(r *registry.Registry) error {
		var err error
		removed, err = r.Prune(configFileExists)
		if err != nil {
			return err
		}
		return ensureReservationsNotExposed(removed)
	})
	if err != nil {
		if code, ok := exposureGuardFailure(stderr, *jsonOut, err); ok {
			return code
		}
		return fail(stderr, *jsonOut, ExitError, "registry_error", err.Error())
	}
	items := make([]projectReservation, 0, len(removed))
	for _, res := range removed {
		items = append(items, projectReservation{Key: registry.Key(res.Project, res.Service), Reservation: res})
	}
	refs := listenerRefsForReservations(items)
	if code := reloadListenerRoutes(refs, stderr, *jsonOut); code != ExitOK {
		if rollbackErr := restoreReservations(items, daemonScope{}, stderr, *jsonOut); rollbackErr != nil {
			return fail(stderr, *jsonOut, ExitError, "rollback_failed", "prune failed and rollback failed: "+rollbackErr.Error())
		}
		return code
	}
	if code := removeReservationsDNS(items, daemonScope{}, stderr, *jsonOut); code != ExitOK {
		return code
	}
	if *jsonOut {
		out := make([]map[string]any, 0, len(removed))
		for _, res := range removed {
			out = append(out, map[string]any{"project": res.Project, "service": res.Service, "port": res.Port})
		}
		return writeJSON(stdout, map[string]any{"pruned": out})
	}
	printSuccess(stdout, fmt.Sprintf("pruned %d stale reservations", len(removed)))
	return ExitOK
}

// Env prints the environment gate would inject for a scoped service.
func Env(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("env", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit JSON")
	up := fs.Bool("up", false, "bring up the selected scope before printing env")
	scopeFlags := defineDaemonScopeFlags(fs, false)
	if handled, code := parseFlags(fs, "env", args, stdout, stderr); handled {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return usageFail(stderr, *jsonOut, "env")
	}
	sel, err := registryScopeFromFlags(scopeFlags, false)
	if err != nil {
		return fail(stderr, *jsonOut, ExitUsage, "bad_scope", err.Error())
	}
	if *up {
		if code := runUpBeforeExec(scopeFlags, stderr, *jsonOut); code != ExitOK {
			return code
		}
	}
	desc, lerr := buildRunDescriptor(rest[0], sel)
	if lerr != nil {
		return fail(stderr, *jsonOut, lerr.Exit, lerr.Code, lerr.Message)
	}
	if *jsonOut {
		return writeJSON(stdout, desc)
	}
	for _, pair := range envPairs(desc.Env) {
		fmt.Fprintln(stdout, pair)
	}
	return ExitOK
}

// Run executes `gate run <service> -- <cmd...>` with PORT injected.
func Run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	up := fs.Bool("up", false, "bring up the selected scope before running the child command")
	quiet := fs.Bool("quiet", false, "suppress gate route/status hints")
	scopeFlags := defineDaemonScopeFlags(fs, false)
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		sp := specFor("run")
		WriteHelp(stdout, "run", sp.Args, sp.Summary, collectFlags(fs))
		return ExitOK
	}

	sep := indexOf(args, "--")
	if len(args) < 1 || sep < 1 || sep+1 >= len(args) {
		usageLine(stderr, "run")
		return ExitUsage
	}
	if handled, code := parseFlags(fs, "run", args[:sep], stdout, stderr); handled {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		usageLine(stderr, "run")
		return ExitUsage
	}
	sel, err := registryScopeFromFlags(scopeFlags, false)
	if err != nil {
		return fail(stderr, false, ExitUsage, "bad_scope", err.Error())
	}
	svc := rest[0]
	cmd := args[sep+1:]

	if *up {
		if code := runUpBeforeExec(scopeFlags, stderr, false); code != ExitOK {
			return code
		}
	}

	desc, lerr := buildRunDescriptor(svc, sel)
	if lerr != nil {
		return fail(stderr, false, lerr.Exit, lerr.Code, lerr.Message)
	}
	if !*quiet && *up && routeHintEnabledFunc(stderr) {
		printRunRoute(stderr, desc)
	}
	return port.Exec(desc.Port, envPairs(desc.Env), cmd[0], cmd[1:], os.Stdin, stdout, stderr)
}

func printRunRoute(stderr io.Writer, desc runDescriptor) {
	fmt.Fprintf(stderr, "gate route  %s  %s  ->  %s\n", desc.Service, desc.URL, desc.LoopbackURL)
	for _, diagnostic := range desc.Diagnostics {
		printRunDiagnosticHint(stderr, diagnostic)
	}
}

func printRunDiagnosticHint(stderr io.Writer, diagnostic runDiagnostic) {
	if diagnostic.Severity == "" || diagnostic.Severity == "info" {
		return
	}
	fmt.Fprintf(stderr, "gate %s  %s: %s\n", diagnostic.Severity, diagnostic.Code, diagnostic.Message)
	for _, action := range diagnostic.Actions {
		if action.Command != "" {
			fmt.Fprintf(stderr, "gate action  %s: %s\n", action.Label, action.Command)
			continue
		}
		fmt.Fprintf(stderr, "gate action  %s\n", action.Label)
	}
}

func runUpBeforeExec(scopeFlags daemonScopeFlags, stderr io.Writer, jsonOut bool) int {
	args := []string{"--json"}
	if scopeFlags.global != nil && *scopeFlags.global {
		args = append(args, "--global")
	}
	if scopeFlags.project != nil && scopeFlags.project.set {
		args = append(args, "--project", scopeFlags.project.value)
	}
	if scopeFlags.config != nil && scopeFlags.config.set {
		args = append(args, "--config", scopeFlags.config.value)
	}

	var out, errb bytes.Buffer
	code := Up(args, &out, &errb)
	if code != ExitOK {
		if envelope, ok := parseErrorEnvelope(errb.Bytes()); ok {
			return fail(stderr, jsonOut, code, envelope.Error.Code, envelope.Error.Message)
		}
		if errb.Len() > 0 {
			return fail(stderr, jsonOut, code, "up_failed", strings.TrimSpace(errb.String()))
		}
		return fail(stderr, jsonOut, code, "up_failed", strings.TrimSpace(out.String()))
	}
	return code
}

func parseErrorEnvelope(data []byte) (errEnvelope, bool) {
	var envelope errEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return errEnvelope{}, false
	}
	return envelope, envelope.Error.Message != ""
}

func buildRunDescriptor(name string, sel registryScopeSelection) (runDescriptor, *reservationLookupError) {
	res, lerr := lookupScopedReservation(name, sel)
	if lerr != nil {
		return runDescriptor{}, lerr
	}
	urlResolver := newRouteURLResolver()
	env, err := runEnvMapForScope(sel, urlResolver)
	if err != nil {
		return runDescriptor{}, &reservationLookupError{Exit: ExitError, Code: "run_env", Message: err.Error()}
	}
	env["PORT"] = strconv.Itoa(res.Port)
	daemonStatus, diagnostics := runDescriptorDaemonStatus(res, urlResolver)
	return runDescriptor{
		Service:     res.Service,
		Project:     res.Project,
		Standalone:  res.Standalone,
		Domain:      res.Domain,
		Port:        res.Port,
		URL:         urlResolver.URL(res),
		LoopbackURL: loopbackURL(res.Port),
		Route:       routeStatus(res),
		Upstream:    upstreamStatus(res),
		Env:         env,
		EnvKeys:     sortedEnvKeys(env),
		Daemon:      daemonStatus,
		Diagnostics: diagnostics,
	}, nil
}

func runDescriptorDaemonStatus(res registry.Reservation, resolver *routeURLResolver) (runDaemonStatus, []runDiagnostic) {
	ref := listenerRefFor(res.ListenerPair())
	out := runDaemonStatus{
		Required:  res.Active,
		Listener:  ref.String(),
		HTTPSAddr: ref.Pair.HTTPSAddr,
		HTTPAddr:  ref.Pair.HTTPAddr,
	}
	diagnostics := []runDiagnostic{}
	info := resolver.listenerInfo(res)
	if info.running {
		out.Running = true
		if info.httpsAddr != "" {
			out.HTTPSAddr = info.httpsAddr
		}
		if info.httpAddr != "" {
			out.HTTPAddr = info.httpAddr
		}
		return out, diagnostics
	}
	if out.Required {
		command := gateUpDaemonCommand(res, ref)
		diagnostics = append(diagnostics, runDiagnostic{
			Code:             "daemon_not_running",
			Severity:         "fixable",
			Message:          "listener daemon is not running",
			SuggestedCommand: command,
			Actions: []runDiagnosticAction{{
				Label:   "Start listener daemon",
				Command: command,
			}},
		})
	}
	return out, diagnostics
}

func sortedEnvKeys(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func gateUpDaemonCommand(res registry.Reservation, ref listenerDaemonRef) string {
	args := []string{"gate", "up", "--daemon"}
	switch {
	case res.Standalone:
		args = append(args, "--global")
	case strings.TrimSpace(res.ConfigPath) != "":
		args = append(args, "--config", res.ConfigPath)
	case strings.TrimSpace(res.Project) != "":
		args = append(args, "--project", res.Project)
	}
	if !listener.Equivalent(ref.Pair, listener.DefaultPair()) {
		args = append(args, "--https-addr", ref.Pair.HTTPSAddr, "--http-addr", ref.Pair.HTTPAddr)
	}
	return shellCommand(args)
}

func shellCommand(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, shellQuote(arg))
	}
	return strings.Join(quoted, " ")
}

func shellQuote(arg string) string {
	if arg == "" {
		return "''"
	}
	if strings.IndexFunc(arg, shellUnsafeRune) == -1 {
		return arg
	}
	return "'" + strings.ReplaceAll(arg, "'", "'\"'\"'") + "'"
}

func shellUnsafeRune(r rune) bool {
	if r == '-' || r == '_' || r == '.' || r == '/' || r == ':' {
		return false
	}
	if r >= '0' && r <= '9' {
		return false
	}
	if r >= 'A' && r <= 'Z' {
		return false
	}
	if r >= 'a' && r <= 'z' {
		return false
	}
	return true
}

func runEnvMapForScope(sel registryScopeSelection, urlResolver *routeURLResolver) (map[string]string, error) {
	reg, err := registryStore().Read()
	if err != nil {
		return nil, err
	}
	reservations := reservationsForScope(reg, sel)
	sort.Slice(reservations, func(i, j int) bool {
		return reservations[i].Service < reservations[j].Service
	})
	env := map[string]string{}
	resByService := map[string]registry.Reservation{}
	for _, item := range reservations {
		res := item.Reservation
		if res.Port == 0 {
			continue
		}
		key := config.EnvServiceKey(res.Service)
		if existing, ok := env["GATE_"+key+"_PORT"]; ok {
			return nil, fmt.Errorf("duplicate gate env service key %q for %s and port %s", key, res.Service, existing)
		}
		loopbackURL := loopbackURL(res.Port)
		env["GATE_"+key+"_PORT"] = strconv.Itoa(res.Port)
		env["GATE_"+key+"_URL"] = loopbackURL
		env["GATE_"+key+"_ROUTE_URL"] = urlResolver.URL(res)
		resByService[res.Service] = res
	}
	project, hasProject, err := projectForRunEnv(sel, reservations)
	if err != nil {
		return nil, err
	}
	if hasProject {
		for name, svc := range project.Services {
			if len(svc.Env) == 0 && len(svc.RouteEnv) == 0 {
				continue
			}
			res, ok := resByService[name]
			if !ok || res.Port == 0 {
				return nil, fmt.Errorf("service %q publishes env names but has no reserved port", name)
			}
			loopbackURL := loopbackURL(res.Port)
			for _, envName := range svc.Env {
				env[envName] = loopbackURL
			}
			routeURL := urlResolver.URL(res)
			for _, envName := range svc.RouteEnv {
				env[envName] = routeURL
			}
		}
	}
	return env, nil
}

func loopbackURL(port int) string {
	return fmt.Sprintf("http://127.0.0.1:%d", port)
}

func envPairs(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+env[key])
	}
	return out
}

func projectForRunEnv(sel registryScopeSelection, reservations []projectReservation) (*config.Project, bool, error) {
	if sel.Scope.Kind != daemonScopeProject {
		return nil, false, nil
	}
	if sel.CurrentProject != nil {
		return sel.CurrentProject, true, nil
	}
	if project, _, err := currentProjectPath(); err == nil && project.Name == sel.Scope.Name {
		return project, true, nil
	} else if err != nil && !errors.Is(err, config.ErrNotFound) {
		return nil, false, err
	}
	for _, item := range reservations {
		path := strings.TrimSpace(item.ConfigPath)
		if path == "" {
			continue
		}
		project, err := config.Load(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, false, err
		}
		if project.Name == sel.Scope.Name {
			return project, true, nil
		}
	}
	return nil, false, nil
}

func indexOf(ss []string, want string) int {
	for i, s := range ss {
		if s == want {
			return i
		}
	}
	return -1
}

func configFileExists(p string) (bool, error) {
	info, err := os.Stat(p)
	if err == nil {
		return info.Mode().IsRegular(), nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

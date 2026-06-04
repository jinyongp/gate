package cli

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"gate/internal/config"
	"gate/internal/dns"
	"gate/internal/port"
	"gate/internal/proxy"
	"gate/internal/registry"
	"gate/internal/ui"
)

// service is one row of `gate ls` output.
type service struct {
	Project    string `json:"project"`
	Service    string `json:"service"`
	Domain     string `json:"domain"`
	Port       int    `json:"port"`
	DNS        string `json:"dns,omitempty"`
	Route      string `json:"route"`
	Upstream   string `json:"upstream"`
	Standalone bool   `json:"standalone,omitempty"`
}

type projectReservation struct {
	Key string
	registry.Reservation
}

type portRow struct {
	Project    string `json:"project"`
	Service    string `json:"service"`
	Domain     string `json:"domain"`
	Port       int    `json:"port"`
	Route      string `json:"route"`
	Upstream   string `json:"upstream"`
	Standalone bool   `json:"standalone,omitempty"`
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
	if handled, code := parseFlags(fs, "ls", args, stdout, stderr); handled {
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
		})
	}

	if *jsonOut {
		return writeJSON(stdout, map[string]any{"services": rows})
	}
	if len(rows) == 0 {
		if richOut(stdout, false) {
			fmt.Fprintln(stdout, ui.Dim.Render("No reservations yet — run `gate up` in a project or `gate add <service> <domain> <port>`."))
		} else {
			fmt.Fprintln(stdout, "No reservations.")
		}
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
		if richOut(stdout, false) {
			fmt.Fprintln(stdout, ui.Dim.Render("No reserved ports yet — run `gate up` in a project or `gate add <service> <domain> <port>`."))
		} else {
			fmt.Fprintln(stdout, "No reserved ports.")
		}
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
	scopeFlags := defineDaemonScopeFlags(fs, false)
	if handled, code := parseFlags(fs, "add", args, stdout, stderr); handled {
		return code
	}
	sel, err := registryScopeFromFlags(scopeFlags, false)
	if err != nil {
		return fail(stderr, *jsonOut, ExitUsage, "bad_scope", err.Error())
	}
	rest := fs.Args()
	if len(rest) != 3 {
		return usageFail(stderr, *jsonOut, "add")
	}
	name := strings.TrimSpace(rest[0])
	if err := validateRegistryName(name, "service"); err != nil {
		return fail(stderr, *jsonOut, ExitUsage, "bad_service", err.Error())
	}
	domain := config.CanonicalDomain(rest[1])
	if err := config.ValidateDomain(domain); err != nil {
		return fail(stderr, *jsonOut, ExitUsage, "bad_domain", err.Error())
	}
	p, err := strconv.Atoi(rest[2])
	if err != nil || p < 1 || p > 65535 {
		return fail(stderr, *jsonOut, ExitUsage, "bad_port", "port must be 1-65535")
	}

	if sel.Scope.Kind == daemonScopeGlobal {
		res := registry.Reservation{Service: name, Domain: domain, Port: p, TLS: config.TLSInternal, Standalone: true}
		res.Active = true
		res.DNS = dns.ModeFor(domain, "")
		return addStandalone(res, stdout, stderr, *jsonOut)
	}

	project := sel.CurrentProject
	path := sel.CurrentProjectPath
	if project == nil {
		project, path, err = projectConfigForName(sel.Scope.Name)
		if err != nil {
			return fail(stderr, *jsonOut, ExitError, "project", err.Error())
		}
	}
	res := registry.Reservation{
		Project: project.Name, Service: name, Domain: domain, Port: p,
		TLS: config.TLSInternal, ConfigPath: path,
	}
	key := registry.Key(project.Name, name)
	reg, err := registryStore().Read()
	if err != nil {
		return fail(stderr, *jsonOut, ExitError, "registry_error", err.Error())
	}
	previous, hadPrevious := reg.Get(key)
	if hadPrevious {
		res.Active = previous.Active
		res.DNS = previous.DNS
		res.SetListenerPair(previous.ListenerPair())
		if res.Active && res.DNS == "" {
			res.DNS = dns.ModeFor(domain, "")
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
	if err := config.UpsertService(path, name, config.Service{Domain: domain, Port: p, TLS: config.TLSInternal}); err != nil {
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
			rollbackErr := restoreProjectAdd(path, originalConfig, originalInfo.Mode().Perm(), key, previous, hadPrevious, reloadRefs, stderr, *jsonOut)
			if rollbackErr != nil {
				return fail(stderr, *jsonOut, ExitError, "rollback_failed", "service add failed and rollback failed: "+rollbackErr.Error())
			}
			if os.IsPermission(err) || errors.Is(err, os.ErrPermission) {
				return fail(stderr, *jsonOut, ExitPerm, "permission", err.Error())
			}
			return fail(stderr, *jsonOut, ExitError, "dns_failed", err.Error())
		}
		regAfter, rerr := registryStore().Read()
		if rerr != nil {
			rollbackErr := restoreProjectAdd(path, originalConfig, originalInfo.Mode().Perm(), key, previous, hadPrevious, reloadRefs, stderr, *jsonOut)
			rollbackErr = errors.Join(rollbackErr, removeDomainDNS(res.Domain, res.DNS, stderr, *jsonOut))
			if rollbackErr != nil {
				return fail(stderr, *jsonOut, ExitError, "rollback_failed", "service add failed and rollback failed: "+rollbackErr.Error())
			}
			return fail(stderr, *jsonOut, ExitError, "registry_error", rerr.Error())
		}
		_ = regAfter
		if code := reloadListenerRoutes(reloadRefs, stderr, *jsonOut); code != ExitOK {
			rollbackErr := restoreProjectAdd(path, originalConfig, originalInfo.Mode().Perm(), key, previous, hadPrevious, reloadRefs, stderr, *jsonOut)
			rollbackErr = errors.Join(rollbackErr, removeDomainDNS(res.Domain, res.DNS, stderr, *jsonOut))
			if rollbackErr != nil {
				return fail(stderr, *jsonOut, ExitError, "rollback_failed", "service add failed and rollback failed: "+rollbackErr.Error())
			}
			return code
		}
		if hadPrevious && config.CanonicalDomain(previous.Domain) != config.CanonicalDomain(res.Domain) {
			if err := removeDomainDNS(previous.Domain, previous.DNS, stderr, *jsonOut); err != nil {
				rollbackErr := restoreProjectAdd(path, originalConfig, originalInfo.Mode().Perm(), key, previous, hadPrevious, reloadRefs, stderr, *jsonOut)
				rollbackErr = errors.Join(rollbackErr, removeDomainDNS(res.Domain, res.DNS, stderr, *jsonOut))
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
		return writeJSON(stdout, map[string]any{"project": project.Name, "service": res.Service, "domain": domain, "port": p, "reserved": true})
	}
	fmt.Fprintf(stdout, "reserved  %s/%s  %s -> :%d\n", project.Name, name, domain, p)
	return ExitOK
}

func addStandalone(res registry.Reservation, stdout, stderr io.Writer, jsonOut bool) int {
	key := registry.Key(res.Project, res.Service)
	if err := ensureDomainDNS(res.Domain, res.DNS, stderr, jsonOut); err != nil {
		if os.IsPermission(err) || errors.Is(err, os.ErrPermission) {
			return fail(stderr, jsonOut, ExitPerm, "permission", err.Error())
		}
		return fail(stderr, jsonOut, ExitError, "dns_failed", err.Error())
	}

	reg, err := registryStore().Read()
	if err != nil {
		return fail(stderr, jsonOut, ExitError, "registry_error", err.Error())
	}
	previous, hadPrevious := reg.Get(key)
	if hadPrevious {
		res.SetListenerPair(previous.ListenerPair())
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
		if !hadPrevious {
			_ = removeDomainDNS(res.Domain, res.DNS, stderr, jsonOut)
		}
		return addError(stderr, jsonOut, err)
	}
	if code := reloadListenerRoutes(reloadRefs, stderr, jsonOut); code != ExitOK {
		_ = rollbackStandaloneAdd(key, previous, hadPrevious, reloadRefs, stderr, jsonOut)
		if !hadPrevious {
			_ = removeDomainDNS(res.Domain, res.DNS, stderr, jsonOut)
		}
		return code
	}
	if hadPrevious && config.CanonicalDomain(previous.Domain) != config.CanonicalDomain(res.Domain) {
		if err := removeDomainDNS(previous.Domain, previous.DNS, stderr, jsonOut); err != nil {
			rollbackErr := rollbackStandaloneAdd(key, previous, true, reloadRefs, stderr, jsonOut)
			rollbackErr = errors.Join(rollbackErr, removeDomainDNS(res.Domain, res.DNS, stderr, jsonOut))
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
	fmt.Fprintf(stdout, "reserved  %s  %s -> :%d\n", res.Service, res.Domain, res.Port)
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
	sel, err := registryScopeFromFlags(scopeFlags, false)
	if err != nil {
		return fail(stderr, *jsonOut, ExitUsage, "bad_scope", err.Error())
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return usageFail(stderr, *jsonOut, "rm")
	}
	name := strings.TrimSpace(rest[0])
	if err := validateRegistryName(name, "service"); err != nil {
		return fail(stderr, *jsonOut, ExitUsage, "bad_service", err.Error())
	}

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
	fmt.Fprintf(stdout, "removed  %s/%s\n", project.Name, name)
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
		_ = restoreReservations([]projectReservation{{Key: key, Reservation: removedRes}}, scope, stderr, jsonOut)
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
	fmt.Fprintf(stdout, "removed  %s\n", displayReservationOwner(removedRes))
	return ExitOK
}

func removeReservationsDNS(removed []projectReservation, scope daemonScope, stderr io.Writer, jsonOut bool) int {
	for i, item := range removed {
		res := item.Reservation
		if err := removeDomainDNS(res.Domain, res.DNS, stderr, jsonOut); err != nil {
			rollbackErr := restoreProjectDNS(removed[:i], stderr, jsonOut)
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
	} else if err := setListenerRoutesForRefsWithActivity(refs, stderr, jsonOut, "restoring routes"); err != nil {
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
		if err := setListenerRoutesForRefsWithActivity(refs, stderr, jsonOut, "restoring routes"); err != nil {
			errs = append(errs, fmt.Errorf("restore daemon routes: %w", err))
		}
	}
	return errors.Join(errs...)
}

func restoreProjectConfig(path string, originalConfig []byte, mode os.FileMode) error {
	//nolint:gosec // G703: path is the already-discovered project gate.toml being restored after rollback.
	return os.WriteFile(path, originalConfig, mode)
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
	if err := setListenerRoutesForRefsWithActivity(refs, stderr, jsonOut, "restoring routes"); err != nil {
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
	if err := setListenerRoutesForRefsWithActivity(refs, stderr, jsonOut, "restoring routes"); err != nil {
		errs = append(errs, fmt.Errorf("restore daemon routes: %w", err))
	}
	return errors.Join(errs...)
}

func reloadListenerRoutes(refs []listenerDaemonRef, stderr io.Writer, jsonOut bool) int {
	if err := setListenerRoutesForRefsWithActivity(refs, stderr, jsonOut, "reloading routes"); err != nil {
		return fail(stderr, jsonOut, ExitError, "reload_failed", err.Error())
	}
	return ExitOK
}

func setListenerRoutesWithActivity(ref listenerDaemonRef, stderr io.Writer, jsonOut bool, label string) error {
	activity := startActivity(stderr, jsonOut, label)
	err := setListenerRoutesForRef(ref)
	activity.Stop()
	return err
}

func setListenerRoutesForRefsWithActivity(refs []listenerDaemonRef, stderr io.Writer, jsonOut bool, label string) error {
	for _, ref := range refs {
		if err := setListenerRoutesWithActivity(ref, stderr, jsonOut, label); err != nil {
			return err
		}
	}
	return nil
}

func setListenerRoutesForRef(ref listenerDaemonRef) error {
	reg, err := registryStore().Read()
	if err != nil {
		return err
	}
	routes := activeRoutesForListener(reg, ref.Pair)
	if err := applyExposureRecords(ref.String(), routes); err != nil {
		return err
	}
	return setListenerRoutesFunc(ref, routes)
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
	return runDomainDNS(provider, domain, stderr, jsonOut, "updating DNS", provider.Ensure)
}

func removeDomainDNS(domain, mode string, stderr io.Writer, jsonOut bool) error {
	provider := selectDNSProvider(domain, mode)
	return runDomainDNS(provider, domain, stderr, jsonOut, "updating DNS", provider.Remove)
}

func runDomainDNS(provider dns.Provider, domain string, stderr io.Writer, jsonOut bool, label string, fn func(string) error) error {
	if !dnsActivityAllowed(provider) {
		return fn(domain)
	}
	activity := startActivity(stderr, jsonOut, label)
	err := fn(domain)
	activity.Stop()
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
	if handled, code := parseFlags(fs, "clear", args, stdout, stderr); handled {
		return code
	}
	sel, err := registryScopeFromFlags(scopeFlags, false)
	if err != nil {
		return fail(stderr, *jsonOut, ExitUsage, "bad_scope", err.Error())
	}
	reg, err := registryStore().Read()
	if err != nil {
		return fail(stderr, *jsonOut, ExitError, "registry_error", err.Error())
	}
	removed := reservationsForScope(reg, sel)
	scope := sel.Scope
	reloadRefs := listenerRefsForReservations(removed)
	if !*yes {
		if code := confirmClear(sel, len(removed), stdout, stderr, *jsonOut); code != ExitOK {
			return code
		}
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
	fmt.Fprintf(stdout, "removed %s (%d reservations)\n", clearScopeLabel(scope), len(removed))
	return ExitOK
}

func confirmClear(sel registryScopeSelection, count int, stdout, stderr io.Writer, jsonOut bool) int {
	if jsonOut || !stdinIsTTYFunc() {
		return fail(stderr, jsonOut, ExitUsage, "confirmation_required", "pass -y to clear reservations")
	}
	fmt.Fprintf(stdout, "remove %s (%d reservations)? type y or yes: ", clearScopeLabel(sel.Scope), count)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return fail(stderr, false, ExitError, "confirm_failed", err.Error())
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	if answer != "y" && answer != "yes" {
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

func stdinIsTTY() bool {
	info, err := os.Stdin.Stat()
	return err == nil && (info.Mode()&os.ModeCharDevice) != 0
}

// Prune garbage-collects reservations whose owning gate.toml no longer exists.
func Prune(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("prune", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit JSON")
	if handled, code := parseFlags(fs, "prune", args, stdout, stderr); handled {
		return code
	}
	var removed []registry.Reservation
	err := registryStore().Update(func(r *registry.Registry) error {
		removed = r.Prune(fileExists)
		return nil
	})
	if err != nil {
		return fail(stderr, *jsonOut, ExitError, "registry_error", err.Error())
	}
	if *jsonOut {
		out := make([]map[string]any, 0, len(removed))
		for _, res := range removed {
			out = append(out, map[string]any{"project": res.Project, "service": res.Service, "port": res.Port})
		}
		return writeJSON(stdout, map[string]any{"pruned": out})
	}
	fmt.Fprintf(stdout, "pruned %d stale reservations\n", len(removed))
	return ExitOK
}

// Run executes `gate run <service> -- <cmd...>` with PORT injected.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		sp := specFor("run")
		WriteHelp(stdout, "run", sp.Args, sp.Summary, nil)
		return ExitOK
	}

	sep := indexOf(args, "--")
	if len(args) < 1 || sep < 1 || sep+1 >= len(args) {
		usageLine(stderr, "run")
		return ExitUsage
	}
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	scopeFlags := defineDaemonScopeFlags(fs, false)
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

	res, lerr := lookupScopedReservation(svc, sel)
	if lerr != nil {
		return fail(stderr, false, lerr.Exit, lerr.Code, lerr.Message)
	}
	return port.Exec(res.Port, cmd[0], cmd[1:], os.Stdin, stdout, stderr)
}

func indexOf(ss []string, want string) int {
	for i, s := range ss {
		if s == want {
			return i
		}
	}
	return -1
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.Mode().IsRegular()
}

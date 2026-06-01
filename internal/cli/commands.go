package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"prx/internal/config"
	"prx/internal/daemon"
	"prx/internal/dns"
	"prx/internal/paths"
	"prx/internal/port"
	"prx/internal/proxy"
	"prx/internal/registry"
	"prx/internal/ui"
)

// service is one row of `prx ls` output.
type service struct {
	Project string `json:"project"`
	Service string `json:"service"`
	Domain  string `json:"domain"`
	Port    int    `json:"port"`
	TLS     string `json:"tls"`
	DNS     string `json:"dns,omitempty"`
	Status  string `json:"status"`
}

type projectReservation struct {
	Key string
	registry.Reservation
}

var (
	selectDNSProvider   = dns.Select
	setDaemonRoutesFunc = setDaemonRoutes
)

func liveness(p int) string {
	if p != 0 && port.IsLive(p) {
		return "live"
	}
	return "down"
}

func reservationStatus(res registry.Reservation) string {
	if !res.Active {
		return "down"
	}
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
	all := fs.Bool("all", false, "show reservations from all projects")
	fs.BoolVar(all, "a", false, "show reservations from all projects")
	status := fs.String("status", "", "filter by status: live|down")
	if handled, code := parseFlags(fs, "ls", args, stdout, stderr); handled {
		return code
	}
	if *status != "" && *status != "live" && *status != "down" {
		return fail(stderr, *jsonOut, ExitUsage, "bad_status", "status must be live or down")
	}

	reg, err := registryStore().Read()
	if err != nil {
		return fail(stderr, *jsonOut, ExitError, "registry_error", err.Error())
	}

	projectName := ""
	if !*all {
		project, err := currentProject()
		if err != nil {
			return fail(stderr, *jsonOut, ExitError, "no_project", err.Error())
		}
		projectName = project.Name
	}

	rows := make([]service, 0, len(reg.Services))
	for _, k := range reg.Keys() {
		res := reg.Services[k]
		if !*all && (res.Project != projectName || !res.Active) {
			continue
		}
		rowStatus := reservationStatus(res)
		if *status != "" && rowStatus != *status {
			continue
		}
		rows = append(rows, service{
			Project: res.Project, Service: res.Service, Domain: res.Domain,
			Port: res.Port, TLS: res.TLS, DNS: res.DNS, Status: rowStatus,
		})
	}

	if *jsonOut {
		return writeJSON(stdout, map[string]any{"services": rows})
	}
	if len(rows) == 0 {
		if richOut(stdout, false) {
			fmt.Fprintln(stdout, ui.Dim.Render("No reservations yet — run `prx up` in a project or `prx add <domain> <port>`."))
		} else {
			fmt.Fprintln(stdout, "No reservations.")
		}
		return ExitOK
	}
	if richOut(stdout, false) {
		headers := []string{"PROJECT", "SERVICE", "DOMAIN", "PORT", "TLS", "STATUS"}
		data := make([][]string, 0, len(rows))
		for _, r := range rows {
			data = append(data, []string{
				r.Project, r.Service, displayDomainURL(r.Domain), strconv.Itoa(r.Port), r.TLS, statusDot(r.Status, true),
			})
		}
		fmt.Fprintln(stdout, ui.Render(headers, data))
		return ExitOK
	}
	color := isTTY(stdout)
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PROJECT\tSERVICE\tDOMAIN\tPORT\tTLS\tSTATUS")
	for _, r := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%s\n", r.Project, r.Service, displayDomainURL(r.Domain), r.Port, r.TLS, statusDot(r.Status, color))
	}
	_ = tw.Flush()
	return ExitOK
}

// Port prints the reserved port for a service (script injection).
func Port(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("port", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit JSON")
	if handled, code := parseFlags(fs, "port", args, stdout, stderr); handled {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return usageFail(stderr, *jsonOut, "port")
	}
	svc := rest[0]
	p, err := currentProject()
	if err != nil {
		return fail(stderr, *jsonOut, ExitError, "no_project", err.Error())
	}
	if _, ok := p.Services[svc]; !ok {
		return fail(stderr, *jsonOut, ExitError, "no_service", fmt.Sprintf("no service %q in project", svc))
	}
	reg, err := registryStore().Read()
	if err != nil {
		return fail(stderr, *jsonOut, ExitError, "registry_error", err.Error())
	}
	res, ok := reg.Get(registry.Key(p.Name, svc))
	if !ok || res.Port == 0 {
		return fail(stderr, *jsonOut, ExitError, "not_allocated", "no port allocated; run prx up")
	}
	if *jsonOut {
		return writeJSON(stdout, map[string]any{"service": svc, "port": res.Port})
	}
	fmt.Fprintln(stdout, res.Port)
	return ExitOK
}

// Add reserves a domain→port mapping. Inside a prx project, it also appends a
// service block to prx.toml; outside a project it creates an adhoc registry entry.
func Add(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit JSON")
	if handled, code := parseFlags(fs, "add", args, stdout, stderr); handled {
		return code
	}
	rest := fs.Args()
	if len(rest) != 2 {
		return usageFail(stderr, *jsonOut, "add")
	}
	domain := config.CanonicalDomain(rest[0])
	p, err := strconv.Atoi(rest[1])
	if err != nil || p < 1 || p > 65535 {
		return fail(stderr, *jsonOut, ExitUsage, "bad_port", "port must be 1-65535")
	}

	project, path, inProject, perr := optionalProject()
	if perr != nil {
		return fail(stderr, *jsonOut, ExitError, "project", perr.Error())
	}
	serviceName := serviceNameForDomain(domain)
	res := registry.Reservation{Service: domain, Domain: domain, Port: p, TLS: config.TLSInternal, Adhoc: true}
	if inProject {
		res = registry.Reservation{
			Project: project.Name, Service: serviceName, Domain: domain, Port: p,
			TLS: config.TLSInternal, ConfigPath: path,
		}
	}

	if err := registryStore().ReadReserve(res); err != nil {
		return addError(stderr, *jsonOut, err)
	}
	if inProject {
		if err := config.AddService(path, serviceName, config.Service{Domain: domain, Port: p, TLS: config.TLSInternal}); err != nil {
			return fail(stderr, *jsonOut, ExitError, "config", err.Error())
		}
	}
	err = registryStore().Update(func(r *registry.Registry) error { return r.Reserve(res) })
	var ce *registry.ConflictError
	if errors.As(err, &ce) {
		if inProject {
			_ = config.RemoveService(path, serviceName)
		}
		return fail(stderr, *jsonOut, ExitConflict, "port_conflict", ce.Error())
	}
	if err != nil {
		if inProject {
			_ = config.RemoveService(path, serviceName)
		}
		return fail(stderr, *jsonOut, ExitError, "registry_error", err.Error())
	}
	if *jsonOut {
		return writeJSON(stdout, map[string]any{"service": res.Service, "domain": domain, "port": p, "reserved": true})
	}
	fmt.Fprintf(stdout, "reserved  %s -> :%d\n", domain, p)
	return ExitOK
}

// Rm removes the reservation for a domain.
func Rm(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("rm", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit JSON")
	projectMode := fs.Bool("project", false, "remove all reservations for the current project, or the named project when NAME is passed")
	if handled, code := parseFlags(fs, "rm", args, stdout, stderr); handled {
		return code
	}
	rest := fs.Args()
	if *projectMode {
		return rmProject(rest, stdout, stderr, *jsonOut)
	}
	if len(rest) != 1 {
		return usageFail(stderr, *jsonOut, "rm")
	}
	domain := config.CanonicalDomain(rest[0])
	if project, path, ok, perr := optionalProject(); perr != nil {
		return fail(stderr, *jsonOut, ExitError, "project", perr.Error())
	} else if ok {
		for name, svc := range project.Services {
			if config.CanonicalDomain(svc.Domain) != domain {
				continue
			}
			if err := config.RemoveService(path, name); err != nil {
				return fail(stderr, *jsonOut, ExitError, "config", err.Error())
			}
			err := registryStore().Update(func(r *registry.Registry) error {
				r.Release(registry.Key(project.Name, name))
				return nil
			})
			if err != nil {
				return fail(stderr, *jsonOut, ExitError, "registry_error", err.Error())
			}
			if *jsonOut {
				return writeJSON(stdout, map[string]any{"domain": domain, "removed": true})
			}
			fmt.Fprintf(stdout, "removed  %s\n", domain)
			return ExitOK
		}
	}
	var removed bool
	err := registryStore().Update(func(r *registry.Registry) error {
		_, removed = r.ReleaseDomain(domain)
		return nil
	})
	if err != nil {
		return fail(stderr, *jsonOut, ExitError, "registry_error", err.Error())
	}
	if !removed {
		return fail(stderr, *jsonOut, ExitError, "not_found", fmt.Sprintf("no reservation for %q", domain))
	}
	if *jsonOut {
		return writeJSON(stdout, map[string]any{"domain": domain, "removed": true})
	}
	fmt.Fprintf(stdout, "removed  %s\n", domain)
	return ExitOK
}

func rmProject(args []string, stdout, stderr io.Writer, jsonOut bool) int {
	if len(args) > 1 {
		return usageFail(stderr, jsonOut, "rm")
	}
	projectName := ""
	if len(args) == 1 {
		projectName = strings.TrimSpace(args[0])
	} else {
		project, err := currentProject()
		if err != nil {
			return fail(stderr, jsonOut, ExitError, "no_project", err.Error())
		}
		projectName = project.Name
	}
	if projectName == "" {
		return fail(stderr, jsonOut, ExitUsage, "bad_project", "project name is required")
	}

	reg, err := registryStore().Read()
	if err != nil {
		return fail(stderr, jsonOut, ExitError, "registry_error", err.Error())
	}
	var removed []projectReservation
	for _, key := range reg.Keys() {
		res := reg.Services[key]
		if res.Project == projectName {
			removed = append(removed, projectReservation{Key: key, Reservation: res})
		}
	}
	if len(removed) == 0 {
		return fail(stderr, jsonOut, ExitError, "not_found", fmt.Sprintf("no reservations for project %q", projectName))
	}
	beforeRoutes := activeRoutes(reg)

	var routes []proxy.Route
	err = registryStore().Update(func(r *registry.Registry) error {
		for _, item := range removed {
			r.Release(item.Key)
		}
		routes = activeRoutes(r)
		return nil
	})
	if err != nil {
		return fail(stderr, jsonOut, ExitError, "registry_error", err.Error())
	}
	if code := reloadDaemonRoutes(routes, stderr, jsonOut); code != ExitOK {
		if err := restoreProjectReservations(removed, beforeRoutes); err != nil {
			return fail(stderr, jsonOut, ExitError, "rollback_failed", "project removal failed and rollback failed: "+err.Error())
		}
		return code
	}
	if code := removeProjectDNS(removed, beforeRoutes, stderr, jsonOut); code != ExitOK {
		return code
	}
	if jsonOut {
		return writeJSON(stdout, map[string]any{"project": projectName, "removed": len(removed)})
	}
	fmt.Fprintf(stdout, "removed %d reservations for %s\n", len(removed), projectName)
	return ExitOK
}

func removeProjectDNS(removed []projectReservation, beforeRoutes []proxy.Route, stderr io.Writer, jsonOut bool) int {
	for i, item := range removed {
		res := item.Reservation
		if err := selectDNSProvider(res.Domain, res.DNS).Remove(res.Domain); err != nil {
			rollbackErr := restoreProjectDNS(removed[:i])
			rollbackErr = errors.Join(rollbackErr, restoreProjectReservations(removed, beforeRoutes))
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

func restoreProjectDNS(removed []projectReservation) error {
	var errs []error
	for _, item := range removed {
		res := item.Reservation
		if err := selectDNSProvider(res.Domain, res.DNS).Ensure(res.Domain); err != nil {
			errs = append(errs, fmt.Errorf("restore DNS %s: %w", res.Domain, err))
		}
	}
	return errors.Join(errs...)
}

func restoreProjectReservations(removed []projectReservation, routes []proxy.Route) error {
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
	if err := setDaemonRoutesFunc(routes); err != nil {
		errs = append(errs, fmt.Errorf("restore daemon routes: %w", err))
	}
	return errors.Join(errs...)
}

func reloadDaemonRoutes(routes []proxy.Route, stderr io.Writer, jsonOut bool) int {
	if err := setDaemonRoutesFunc(routes); err != nil {
		return fail(stderr, jsonOut, ExitError, "reload_failed", err.Error())
	}
	return ExitOK
}

func setDaemonRoutes(routes []proxy.Route) error {
	client := daemon.NewClient(paths.SocketPath())
	if !client.IsRunning() {
		return nil
	}
	if err := client.SetRoutes(routes); err != nil {
		return err
	}
	return nil
}

func optionalProject() (*config.Project, string, bool, error) {
	project, path, err := currentProjectPath()
	if errors.Is(err, config.ErrNotFound) {
		return nil, "", false, nil
	}
	if err != nil {
		return nil, "", false, err
	}
	return project, path, true, nil
}

func serviceNameForDomain(domain string) string {
	label, _, _ := strings.Cut(domain, ".")
	label = strings.Trim(label, "-_")
	if label == "" {
		return "web"
	}
	return label
}

func addError(stderr io.Writer, jsonOut bool, err error) int {
	var ce *registry.ConflictError
	if errors.As(err, &ce) {
		return fail(stderr, jsonOut, ExitConflict, "port_conflict", ce.Error())
	}
	return fail(stderr, jsonOut, ExitError, "registry_error", err.Error())
}

// Prune garbage-collects reservations whose owning prx.toml no longer exists.
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

// Run executes `prx run <service> -- <cmd...>` with PORT injected.
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
	svc := args[0]
	cmd := args[sep+1:]

	p, err := currentProject()
	if err != nil {
		return fail(stderr, false, ExitError, "no_project", err.Error())
	}
	reg, err := registryStore().Read()
	if err != nil {
		return fail(stderr, false, ExitError, "registry_error", err.Error())
	}
	res, ok := reg.Get(registry.Key(p.Name, svc))
	if !ok || res.Port == 0 {
		return fail(stderr, false, ExitError, "not_allocated", fmt.Sprintf("no port for %q; run prx up", svc))
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

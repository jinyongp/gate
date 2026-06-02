package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"sort"
	"strings"

	"gate/internal/config"
	"gate/internal/dns"
	"gate/internal/port"
	"gate/internal/proxy"
	"gate/internal/registry"
)

type upResult struct {
	Service   string `json:"service"`
	Domain    string `json:"domain"`
	Port      int    `json:"port"`
	Allocated bool   `json:"allocated"`
}

// Up reserves/allocates ports for the project, reflects DNS, and pushes the
// route table to a running daemon (or prints it when none is running).
func Up(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("up", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit JSON")
	dnsMode := fs.String("dns", "", "force DNS mode: localhost|hosts")
	startDaemon := fs.Bool("daemon", false, "start the background daemon before reloading routes")
	fs.BoolVar(startDaemon, "d", false, "start the background daemon before reloading routes")
	httpsAddr := fs.String("https-addr", defaultDaemonHTTPSAddr, "daemon HTTPS listen address (with --daemon)")
	httpAddr := fs.String("http-addr", defaultDaemonHTTPAddr, "daemon HTTP listen address (with --daemon)")
	if handled, code := parseFlags(fs, "up", args, stdout, stderr); handled {
		return code
	}
	if *dnsMode != "" && *dnsMode != "localhost" && *dnsMode != "hosts" {
		return fail(stderr, *jsonOut, ExitUsage, "bad_dns", "dns must be localhost or hosts")
	}
	httpsAddrSet, httpAddrSet := flagSet(fs, "https-addr"), flagSet(fs, "http-addr")

	project, path, err := currentProjectPath()
	if err != nil {
		return fail(stderr, *jsonOut, ExitError, "no_project", err.Error())
	}
	scope := projectDaemonScope(project.Name)

	var results []upResult
	var routes []proxy.Route
	err = registryStore().Update(func(reg *registry.Registry) error {
		reg.Prune(configPathExists)
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
			if rerr := reg.Reserve(res); rerr != nil {
				return rerr
			}
			results = append(results, upResult{Service: name, Domain: svc.Domain, Port: p, Allocated: allocated})
		}
		routes = activeRoutesForScope(reg, scope)
		return nil
	})
	var ce *registry.ConflictError
	if errors.As(err, &ce) {
		return fail(stderr, *jsonOut, ExitConflict, "conflict", ce.Error())
	}
	if err != nil {
		if errors.Is(err, port.ErrPoolExhausted) {
			return fail(stderr, *jsonOut, ExitConflict, "pool_exhausted", err.Error())
		}
		return fail(stderr, *jsonOut, ExitError, "up_failed", err.Error())
	}

	if code := ensureDNS(project, *dnsMode, stderr, *jsonOut); code != ExitOK {
		return code
	}

	reloaded := false
	actualHTTPSAddr := ""
	startedPID := 0
	client := daemonClientFor(scope)
	if *startDaemon {
		if st, err := client.Status(); err == nil {
			if !daemonExplicitListenMatches(st, *httpsAddr, *httpAddr, httpsAddrSet, httpAddrSet) {
				msg := fmt.Sprintf("daemon already running on https %s · http %s; requested https %s · http %s; run `gate daemon stop` first",
					displayListenAddr(st.HTTPSAddr), displayListenAddr(st.HTTPAddr), *httpsAddr, *httpAddr)
				return fail(stderr, *jsonOut, ExitConflict, "daemon_start", msg)
			}
		} else {
			activity := startActivity(stderr, *jsonOut, "starting daemon")
			result := startDaemonCommand(newDaemonServeCommand(executablePath(), scope.socketPath(), *httpsAddr, *httpAddr), client, scope)
			activity.Stop()
			if result.Code != ExitOK {
				return fail(stderr, *jsonOut, result.Code, "daemon_start", result.Message)
			}
			startedPID = result.PID
		}
	}
	if client.IsRunning() {
		if code := reloadDaemonRoutes(scope, routes, stderr, *jsonOut); code != ExitOK {
			if startedPID != 0 {
				cleanupStartedDaemon(client, scope, startedPID)
			}
			return code
		}
		if st, err := client.Status(); err == nil {
			actualHTTPSAddr = st.HTTPSAddr
		}
		reloaded = true
	}

	if *jsonOut {
		return writeJSON(stdout, map[string]any{"project": project.Name, "reloaded": reloaded, "services": results})
	}
	for _, r := range results {
		domain := r.Domain
		if reloaded {
			domain = proxyURL(r.Domain, actualHTTPSAddr)
		}
		fmt.Fprintf(stdout, "%s/%s  %s -> :%d\n", project.Name, r.Service, domain, r.Port)
	}
	if reloaded {
		fmt.Fprintf(stdout, "proxy reloaded · %d routes active\n", len(routes))
	} else {
		fmt.Fprintln(stderr, "note: no daemon running; start it with `gate daemon start`")
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

func configPathExists(path string) bool {
	if path == "" {
		return true
	}
	if _, err := os.Stat(path); err != nil {
		return !os.IsNotExist(err)
	}
	return true
}

func proxyURL(domain, httpsAddr string) string {
	port := proxyPort(httpsAddr)
	if port == "" {
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

// Down deactivates the current project's routes (reservations are preserved)
// and removes its DNS entries.
func Down(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("down", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit JSON")
	if handled, code := parseFlags(fs, "down", args, stdout, stderr); handled {
		return code
	}
	project, _, err := currentProjectPath()
	if err != nil {
		return fail(stderr, *jsonOut, ExitError, "no_project", err.Error())
	}
	scope := projectDaemonScope(project.Name)

	var routes []proxy.Route
	err = registryStore().Update(func(reg *registry.Registry) error {
		for name := range project.Services {
			key := registry.Key(project.Name, name)
			if res, ok := reg.Get(key); ok {
				res.Active = false
				_ = reg.Reserve(res)
			}
		}
		routes = activeRoutesForScope(reg, scope)
		return nil
	})
	if err != nil {
		return fail(stderr, *jsonOut, ExitError, "down_failed", err.Error())
	}

	for _, name := range sortedServices(project) {
		svc := project.Services[name]
		_ = removeDomainDNS(svc.Domain, "", stderr, *jsonOut)
	}

	client := daemonClientFor(scope)
	if client.IsRunning() {
		_ = setDaemonRoutesWithActivity(scope, routes, stderr, *jsonOut, "reloading routes")
	}
	if *jsonOut {
		return writeJSON(stdout, map[string]any{"project": project.Name, "down": true})
	}
	fmt.Fprintf(stdout, "%s down (reservations preserved)\n", project.Name)
	return ExitOK
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

func ensureDNS(project *config.Project, mode string, stderr io.Writer, jsonOut bool) int {
	for _, name := range sortedServices(project) {
		domain := project.Services[name].Domain
		if err := ensureDomainDNS(domain, mode, stderr, jsonOut); err != nil {
			if os.IsPermission(err) || errors.Is(err, os.ErrPermission) {
				return fail(stderr, jsonOut, ExitPerm, "permission", err.Error())
			}
			return fail(stderr, jsonOut, ExitError, "dns_failed", err.Error())
		}
	}
	return ExitOK
}

func activeRoutesForScope(reg *registry.Registry, scope daemonScope) []proxy.Route {
	var rs []proxy.Route
	for _, k := range reg.Keys() {
		res := reg.Services[k]
		if !res.Active || res.Port == 0 {
			continue
		}
		if scope.Kind == daemonScopeProject && res.Project != scope.Name {
			continue
		}
		if scope.Kind == daemonScopeGlobal && (res.Project != "" || !res.Standalone) {
			continue
		}
		rs = append(rs, proxy.Route{Domain: res.Domain, Upstream: fmt.Sprintf("127.0.0.1:%d", res.Port)})
	}
	return rs
}

func sortedServices(p *config.Project) []string {
	names := make([]string, 0, len(p.Services))
	for n := range p.Services {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

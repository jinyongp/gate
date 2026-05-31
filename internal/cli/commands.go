package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"text/tabwriter"

	"prx/internal/config"
	"prx/internal/port"
	"prx/internal/registry"
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

func liveness(p int) string {
	if p != 0 && port.IsLive(p) {
		return "live"
	}
	return "down"
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
	fs.SetOutput(stderr)
	jsonOut := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}

	reg, err := registryStore().Read()
	if err != nil {
		return fail(stderr, *jsonOut, ExitError, "registry_error", err.Error())
	}
	rows := make([]service, 0, len(reg.Services))
	for _, k := range reg.Keys() {
		res := reg.Services[k]
		rows = append(rows, service{
			Project: res.Project, Service: res.Service, Domain: res.Domain,
			Port: res.Port, TLS: res.TLS, DNS: res.DNS, Status: liveness(res.Port),
		})
	}

	if *jsonOut {
		return writeJSON(stdout, map[string]any{"services": rows})
	}
	color := isTTY(stdout)
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PROJECT\tSERVICE\tDOMAIN\tPORT\tTLS\tSTATUS")
	for _, r := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%s\n", r.Project, r.Service, r.Domain, r.Port, r.TLS, statusDot(r.Status, color))
	}
	_ = tw.Flush()
	return ExitOK
}

// Port prints the reserved port for a service (script injection).
func Port(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("port", flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonOut := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return fail(stderr, *jsonOut, ExitUsage, "usage", "usage: prx port <service>")
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

// Add reserves a domain→port mapping (adhoc registry entry).
func Add(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonOut := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	rest := fs.Args()
	if len(rest) != 2 {
		return fail(stderr, *jsonOut, ExitUsage, "usage", "usage: prx add <domain> <port>")
	}
	domain := rest[0]
	p, err := strconv.Atoi(rest[1])
	if err != nil || p < 1 || p > 65535 {
		return fail(stderr, *jsonOut, ExitUsage, "usage", "port must be 1-65535")
	}

	res := registry.Reservation{Service: domain, Domain: domain, Port: p, TLS: config.TLSInternal, Adhoc: true}
	err = registryStore().Update(func(r *registry.Registry) error { return r.Reserve(res) })
	var ce *registry.ConflictError
	if errors.As(err, &ce) {
		return fail(stderr, *jsonOut, ExitConflict, "port_conflict", ce.Error())
	}
	if err != nil {
		return fail(stderr, *jsonOut, ExitError, "registry_error", err.Error())
	}
	if *jsonOut {
		return writeJSON(stdout, map[string]any{"domain": domain, "port": p, "reserved": true})
	}
	fmt.Fprintf(stdout, "reserved  %s -> :%d\n", domain, p)
	return ExitOK
}

// Rm removes the reservation for a domain.
func Rm(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("rm", flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonOut := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return fail(stderr, *jsonOut, ExitUsage, "usage", "usage: prx rm <domain>")
	}
	domain := rest[0]
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

// Prune garbage-collects reservations whose owning prx.toml no longer exists.
func Prune(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("prune", flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonOut := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
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
	sep := indexOf(args, "--")
	if len(args) < 1 || sep < 1 || sep+1 >= len(args) {
		return fail(stderr, false, ExitUsage, "usage", "usage: prx run <service> -- <cmd> [args]")
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

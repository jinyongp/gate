package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"prx/internal/ca"
	"prx/internal/daemon"
	"prx/internal/expose"
	"prx/internal/paths"
)

// Trust installs the root CA into the OS and browser trust stores.
func Trust(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("trust", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	authority, err := ca.Load(paths.DataDir())
	if err != nil {
		return fail(stderr, false, ExitError, "ca", err.Error())
	}
	if err := authority.Trust(); err != nil {
		if os.IsPermission(err) {
			return fail(stderr, false, ExitPerm, "permission", err.Error())
		}
		return fail(stderr, false, ExitError, "trust", err.Error())
	}
	fmt.Fprintf(stdout, "root CA trusted\nfingerprint %s\n", authority.Fingerprint())
	return ExitOK
}

// Ca dispatches `prx ca export`.
func Ca(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "export" {
		return fail(stderr, false, ExitUsage, "usage", "usage: prx ca export [--out <path>]")
	}
	fs := flag.NewFlagSet("ca export", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("out", "prx-root.crt", "output path")
	if err := fs.Parse(args[1:]); err != nil {
		return ExitUsage
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
	fs := flag.NewFlagSet("expose", flag.ContinueOnError)
	fs.SetOutput(stderr)
	via := fs.String("via", "local", "provider: local|lan|cloudflared|tailscale")
	auth := fs.String("auth", "", "require basic auth as user:pass")
	jsonOut := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return fail(stderr, *jsonOut, ExitUsage, "usage", "usage: prx expose <service> --via <provider>")
	}
	svc := rest[0]
	project, err := currentProject()
	if err != nil {
		return fail(stderr, *jsonOut, ExitError, "no_project", err.Error())
	}
	service, ok := project.Services[svc]
	if !ok {
		return fail(stderr, *jsonOut, ExitError, "no_service", fmt.Sprintf("no service %q", svc))
	}
	provider, err := expose.For(*via)
	if err != nil {
		return fail(stderr, *jsonOut, ExitUsage, "bad_provider", err.Error())
	}
	if *auth == "" && *via != "local" {
		fmt.Fprintln(stderr, "warning: exposing without --auth; anyone with the URL can reach your dev server")
	}
	url, err := provider.Expose(context.Background(), service.Domain, expose.Opts{Auth: *auth})
	if err != nil {
		return fail(stderr, *jsonOut, ExitError, "expose_failed", err.Error())
	}

	// Mark the route exposed (so non-loopback clients are allowed) and apply
	// optional auth, then hot-reload the daemon. Auth is session-scoped: it
	// lives in the in-memory route table, not the persisted registry.
	client := daemon.NewClient(paths.SocketPath())
	if client.IsRunning() {
		if reg, rerr := registryStore().Read(); rerr == nil {
			routes := activeRoutes(reg)
			for i := range routes {
				if routes[i].Domain == service.Domain {
					routes[i].Exposed = true
					routes[i].Auth = *auth
				}
			}
			if serr := client.SetRoutes(routes); serr != nil {
				return fail(stderr, *jsonOut, ExitError, "reload_failed", serr.Error())
			}
		}
	}

	if *jsonOut {
		return writeJSON(stdout, map[string]any{"service": svc, "provider": *via, "public_url": url, "target": service.Domain})
	}
	fmt.Fprintf(stdout, "%s exposed via %s\n  %s -> %s\n", svc, *via, url, service.Domain)
	return ExitOK
}

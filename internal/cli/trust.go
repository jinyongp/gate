package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"gate/internal/ca"
	"gate/internal/expose"
	"gate/internal/paths"
)

var (
	trustAuthorityFunc   = func(authority *ca.CA) error { return authority.Trust() }
	untrustAuthorityFunc = func(authority *ca.CA) error { return authority.Untrust() }
	exposeProviderFor    = expose.For
)

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
	fs := flag.NewFlagSet("expose", flag.ContinueOnError)
	via := fs.String("via", "local", "provider: local|lan|cloudflared|tailscale")
	auth := fs.String("auth", "", "require basic auth as user:pass")
	jsonOut := fs.Bool("json", false, "emit JSON")
	if handled, code := parseFlags(fs, "expose", args, stdout, stderr); handled {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return usageFail(stderr, *jsonOut, "expose")
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
	url, err := provider.Expose(context.Background(), service.Domain, expose.Opts{Auth: *auth})
	if activity != nil {
		activity.Stop()
	}
	if err != nil {
		return fail(stderr, *jsonOut, ExitError, "expose_failed", err.Error())
	}

	// Mark the route exposed (so non-loopback clients are allowed) and apply
	// optional auth, then hot-reload the daemon. Auth is session-scoped: it
	// lives in the in-memory route table, not the persisted registry.
	scope := projectDaemonScope(project.Name)
	client := daemonClientFor(scope)
	if client.IsRunning() {
		if reg, rerr := registryStore().Read(); rerr == nil {
			routes := activeRoutesForScope(reg, scope)
			for i := range routes {
				if routes[i].Domain == service.Domain {
					routes[i].Exposed = true
					routes[i].Auth = *auth
				}
			}
			if serr := setDaemonRoutesWithActivity(scope, routes, stderr, *jsonOut, "reloading routes"); serr != nil {
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

func exposeActivityAllowed(via string) bool {
	return via == expose.ProviderCloudflared || via == expose.ProviderTailscale
}

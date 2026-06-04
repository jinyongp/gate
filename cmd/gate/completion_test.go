package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gate/internal/cli"
	"gate/internal/config"
	"gate/internal/paths"
	"gate/internal/registry"
)

func isolateCompletion(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(base, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(base, "data"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(base, "state"))
	cwd := filepath.Join(base, "work")
	if err := os.MkdirAll(cwd, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)
	return cwd
}

func writeCompletionProject(t *testing.T, dir, name string, services ...string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("[project]\n")
	b.WriteString("name = \"" + name + "\"\n\n")
	for _, service := range services {
		b.WriteString("[services." + service + "]\n")
		b.WriteString("domain = \"" + service + "." + name + ".localhost\"\n\n")
	}
	path := filepath.Join(dir, config.Filename)
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func completionRegistryStore() *registry.Store {
	return registry.Open(filepath.Join(paths.ConfigDir(), "registry.json"))
}

func reserveCompletion(t *testing.T, reservations ...registry.Reservation) {
	t.Helper()
	if err := completionRegistryStore().Update(func(reg *registry.Registry) error {
		for _, res := range reservations {
			if err := reg.Reserve(res); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func completeGate(t *testing.T, args ...string) string {
	t.Helper()
	var out, errb bytes.Buffer
	full := append([]string{"__complete"}, args...)
	if code := run(full, &out, &errb); code != 0 {
		t.Fatalf("run %v exit=%d stderr=%s stdout=%s", full, code, errb.String(), out.String())
	}
	return out.String() + errb.String()
}

func assertCompletionContains(t *testing.T, output string, values ...string) {
	t.Helper()
	for _, value := range values {
		if !strings.Contains(output, value) {
			t.Fatalf("completion missing %q in:\n%s", value, output)
		}
	}
}

func assertCompletionExcludes(t *testing.T, output string, values ...string) {
	t.Helper()
	for _, value := range values {
		if strings.Contains(output, value) {
			t.Fatalf("completion unexpectedly contains %q in:\n%s", value, output)
		}
	}
}

func assertInOrder(t *testing.T, output string, values ...string) {
	t.Helper()
	offset := 0
	for _, value := range values {
		i := strings.Index(output[offset:], value)
		if i < 0 {
			t.Fatalf("%q missing after byte %d in:\n%s", value, offset, output)
		}
		offset += i + len(value)
	}
}

func TestCompletionProvidersListLocalState(t *testing.T) {
	cwd := isolateCompletion(t)
	path := writeCompletionProject(t, cwd, "demo", "api", "web")
	reserveCompletion(t,
		registry.Reservation{Service: "global-web", Domain: "global.localhost", Port: 4400, Standalone: true},
		registry.Reservation{Project: "demo", Service: "admin", Domain: "admin.demo.localhost", Port: 4401, ConfigPath: path},
		registry.Reservation{Project: "other", Service: "api", Domain: "api.other.localhost", Port: 4402},
	)

	if got := completeProjects(nil); strings.Join(got, ",") != "demo,other" {
		t.Fatalf("projects = %v", got)
	}
	if got := completeGlobalNames(); strings.Join(got, ",") != "global-web" {
		t.Fatalf("global-scope names = %v", got)
	}
	if got := completeScopedNames(newCompletionContext(nil, nil, "")); strings.Join(got, ",") != "api,web" {
		t.Fatalf("current project services = %v", got)
	}
	if got := completeNamedProjectServices("demo"); strings.Join(got, ",") != "admin,api,web" {
		t.Fatalf("named project services = %v", got)
	}
}

func TestCompletionProvidersAreQuietOnRegistryErrors(t *testing.T) {
	isolateCompletion(t)
	if err := os.MkdirAll(paths.ConfigDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.ConfigDir(), "registry.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := completeProjects(nil); len(got) != 0 {
		t.Fatalf("projects on corrupt registry = %v", got)
	}
	if got := completeGlobalNames(); len(got) != 0 {
		t.Fatalf("global-scope names on corrupt registry = %v", got)
	}
}

func TestCompletionFlagGroupExpansion(t *testing.T) {
	scope := expandedCompletionFlags(completionSpec{FlagGroups: []completionFlagGroup{flagsScope}})
	if len(scope) != 2 || scope[0].Name != "global" || scope[0].Short != "g" || scope[1].Name != "project" || scope[1].Short != "p" {
		t.Fatalf("scope flags = %+v", scope)
	}
	all := expandedCompletionFlags(completionSpec{FlagGroups: []completionFlagGroup{flagsScopeAll}})
	if len(all) != 3 || all[2].Name != "all" || all[2].Short != "a" {
		t.Fatalf("scope-all flags = %+v", all)
	}
	json := expandedCompletionFlags(completionSpec{FlagGroups: []completionFlagGroup{flagsJSON}})
	if len(json) != 1 || json[0].Name != "json" {
		t.Fatalf("json flags = %+v", json)
	}
}

func TestCompletionSpecsCoverCommandSurface(t *testing.T) {
	specs := map[string]bool{"completion": true}
	for _, spec := range completionSpecs() {
		specs[spec.Command] = true
	}
	for _, spec := range cli.Specs {
		if !specs[spec.Name] {
			t.Fatalf("missing completion spec for %s", spec.Name)
		}
	}
	for _, group := range commandGroups {
		for _, name := range group.names {
			if !specs[name] {
				t.Fatalf("command group references %s without completion spec", name)
			}
		}
	}
}

func TestCompletionRmScopes(t *testing.T) {
	cwd := isolateCompletion(t)
	path := writeCompletionProject(t, cwd, "demo", "api", "web")
	reserveCompletion(t,
		registry.Reservation{Service: "global-web", Domain: "global.localhost", Port: 4400, Standalone: true},
		registry.Reservation{Project: "demo", Service: "admin", Domain: "admin.demo.localhost", Port: 4401, ConfigPath: path},
		registry.Reservation{Project: "smoke", Service: "worker", Domain: "worker.smoke.localhost", Port: 4402},
	)

	assertCompletionContains(t, completeGate(t, "rm", ""), "api", "web")
	assertCompletionExcludes(t, completeGate(t, "rm", ""), "global-web", "worker")
	assertCompletionContains(t, completeGate(t, "rm", "-g", ""), "global-web")
	assertCompletionContains(t, completeGate(t, "rm", "-p", "smoke", ""), "worker")
}

func TestCompletionAddProjectValues(t *testing.T) {
	isolateCompletion(t)
	reserveCompletion(t, registry.Reservation{Project: "smoke", Service: "web", Domain: "web.smoke.localhost", Port: 4400})

	assertCompletionContains(t, completeGate(t, "add", "-p", ""), "smoke")
	assertCompletionContains(t, completeGate(t, "add", "--project", ""), "smoke")
	assertCompletionContains(t, completeGate(t, "add", "--project="), "smoke")
	assertCompletionContains(t, completeGate(t, "add", "--project=sm"), "smoke")
	assertCompletionContains(t, completeGate(t, "add", "-p", "smoke", ""), "web")
}

func TestCompletionPortAllDoesNotCompleteServiceArgs(t *testing.T) {
	cwd := isolateCompletion(t)
	writeCompletionProject(t, cwd, "demo", "web")
	reserveCompletion(t, registry.Reservation{Project: "demo", Service: "api", Domain: "api.demo.localhost", Port: 4400})

	out := completeGate(t, "port", "-a", "")

	assertCompletionExcludes(t, out, "api", "web")
	assertCompletionContains(t, out, ":4")
}

func TestCompletionDaemonStatusHasOnlyListenerFlags(t *testing.T) {
	isolateCompletion(t)

	out := completeGate(t, "daemon", "status", "--")
	assertCompletionContains(t, out, "--help", "--json", "--all")
	assertCompletionExcludes(t, out, "--global", "--project")
}

func TestCompletionEnumFlagValues(t *testing.T) {
	isolateCompletion(t)

	assertCompletionContains(t, completeGate(t, "ls", "--route", ""), "active", "inactive")
	assertCompletionContains(t, completeGate(t, "ls", "--upstream", ""), "live", "down")
	assertCompletionContains(t, completeGate(t, "up", "--dns", ""), "localhost", "hosts")
	assertCompletionContains(t, completeGate(t, "expose", "web", "--via", ""), "local", "lan", "cloudflared", "tailscale")
	assertCompletionContains(t, completeGate(t, "expose", "ls", "--via", ""), "local", "lan", "cloudflared", "tailscale")
}

func TestCompletionFlagPrefixes(t *testing.T) {
	isolateCompletion(t)

	longFlags := completeGate(t, "up", "--")
	assertCompletionContains(t, longFlags, "--help", "--global", "--project", "--json", "--dns", "--daemon")
	assertInOrder(t, longFlags, "--daemon", "--dns", "--global", "--project", "--json", "--help")
	assertCompletionExcludes(t, longFlags, "--http-addr", "--https-addr")
	shortFlags := completeGate(t, "up", "-")
	assertCompletionContains(t, shortFlags, "-h", "-g", "-p", "-d")
	assertCompletionContains(t, completeGate(t, "trust", "--"), "--help")
}

func TestHelpFlagsUseTaskOrder(t *testing.T) {
	isolateCompletion(t)
	var out, errb bytes.Buffer
	if code := run([]string{"up", "-h"}, &out, &errb); code != 0 {
		t.Fatalf("run up -h exit=%d stderr=%s", code, errb.String())
	}
	assertInOrder(t, out.String(), "-d, --daemon", "--dns", "-g, --global", "-p, --project", "--json")
}

func TestCompletionRunStopsAfterDashDash(t *testing.T) {
	cwd := isolateCompletion(t)
	writeCompletionProject(t, cwd, "demo", "web")

	out := completeGate(t, "run", "web", "--", "")

	assertCompletionExcludes(t, out, "web")
	assertCompletionContains(t, out, ":0")
}

func TestCompletionExposeScopes(t *testing.T) {
	cwd := isolateCompletion(t)
	writeCompletionProject(t, cwd, "demo", "web")
	reserveCompletion(t,
		registry.Reservation{Service: "global-web", Domain: "global.localhost", Port: 4400, Standalone: true},
		registry.Reservation{Project: "smoke", Service: "worker", Domain: "worker.smoke.localhost", Port: 4401},
	)

	assertCompletionContains(t, completeGate(t, "expose", ""), "web")
	assertCompletionContains(t, completeGate(t, "expose", "-g", ""), "global-web")
	assertCompletionContains(t, completeGate(t, "expose", "-p", "smoke", ""), "worker")
}

func TestCompletionStaticSubcommands(t *testing.T) {
	isolateCompletion(t)

	assertCompletionContains(t, completeGate(t, "daemon", ""), "start", "stop", "restart", "status", "logs")
	assertInOrder(t, completeGate(t, "daemon", ""), "status", "start", "stop", "restart", "logs")
	assertCompletionContains(t, completeGate(t, "ca", ""), "export")
	assertCompletionContains(t, completeGate(t, "skill", ""), "path", "print")
	assertCompletionContains(t, completeGate(t, "completion", ""), "bash", "zsh", "fish")
}

func TestCompletionRootHidesInternalCommands(t *testing.T) {
	isolateCompletion(t)

	out := completeGate(t, "")

	assertCompletionContains(t, out, "add", "up", "daemon")
	assertInOrder(t, out, "init", "up", "ls", "port", "run", "down", "expose", "daemon", "add", "rm", "clear", "prune")
	assertCompletionExcludes(t, out, "__serve")
}

func TestCompletionCaExportOutUsesFileCompletion(t *testing.T) {
	isolateCompletion(t)

	out := completeGate(t, "ca", "export", "--out", "")
	assertCompletionContains(t, out, ":0")
	assertCompletionExcludes(t, out, ":4")
}

func TestCompletionScriptsSmoke(t *testing.T) {
	isolateCompletion(t)
	for _, shell := range []string{"bash", "zsh", "fish"} {
		var out, errb bytes.Buffer
		if code := run([]string{"completion", shell}, &out, &errb); code != 0 {
			t.Fatalf("completion %s exit=%d stderr=%s", shell, code, errb.String())
		}
		if !strings.Contains(out.String(), "__complete") {
			t.Fatalf("completion %s output missing __complete", shell)
		}
	}
}

func TestCompletionZshNoFileCompDoesNotFallbackToFiles(t *testing.T) {
	isolateCompletion(t)

	var out, errb bytes.Buffer
	if code := run([]string{"completion", "zsh"}, &out, &errb); code != 0 {
		t.Fatalf("completion zsh exit=%d stderr=%s", code, errb.String())
	}
	script := out.String()
	assertCompletionContains(t, script, "No file completion means gate owns this argument position.", "return 0")
	assertCompletionExcludes(t, script, "For example zsh can match letters in the middle of words.")
}

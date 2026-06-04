package cli

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gate/internal/listener"
	"gate/internal/registry"
)

func TestDaemonScopeDefaultsFromGateToml(t *testing.T) {
	setupProject(t)
	scope, err := currentDaemonScope()
	if err != nil {
		t.Fatalf("currentDaemonScope: %v", err)
	}
	if got, want := scope.String(), "project:demo"; got != want {
		t.Fatalf("scope = %q, want %q", got, want)
	}
}

func TestDaemonScopeDefaultsToGlobalOutsideProject(t *testing.T) {
	isolate(t)
	scope, err := currentDaemonScope()
	if err != nil {
		t.Fatalf("currentDaemonScope: %v", err)
	}
	if got, want := scope.String(), "global"; got != want {
		t.Fatalf("scope = %q, want %q", got, want)
	}
}

func TestDaemonScopeFlags(t *testing.T) {
	isolate(t)
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{name: "global", args: []string{"--global"}, want: []string{"global"}},
		{name: "global shorthand", args: []string{"-g"}, want: []string{"global"}},
		{name: "project", args: []string{"--project", "demo"}, want: []string{"project:demo"}},
		{name: "project shorthand", args: []string{"-p", "demo"}, want: []string{"project:demo"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := flag.NewFlagSet("x", flag.ContinueOnError)
			flags := defineDaemonScopeFlags(fs, true)
			if err := fs.Parse(tc.args); err != nil {
				t.Fatal(err)
			}
			scopes, err := daemonScopesFromCurrentDirAndFlags(flags, true)
			if err != nil {
				t.Fatalf("daemonScopesFromCurrentDirAndFlags: %v", err)
			}
			if len(scopes) != len(tc.want) {
				t.Fatalf("scopes = %v, want %v", scopes, tc.want)
			}
			for i := range scopes {
				if got := scopes[i].String(); got != tc.want[i] {
					t.Fatalf("scope[%d] = %q, want %q", i, got, tc.want[i])
				}
			}
		})
	}
}

func TestDaemonScopeRejectsConflictingFlags(t *testing.T) {
	isolate(t)
	fs := flag.NewFlagSet("x", flag.ContinueOnError)
	flags := defineDaemonScopeFlags(fs, true)
	if err := fs.Parse([]string{"--global", "--project", "demo"}); err != nil {
		t.Fatal(err)
	}
	if _, err := daemonScopesFromCurrentDirAndFlags(flags, true); err == nil {
		t.Fatal("conflicting scope flags accepted")
	}
}

func TestDaemonScopeRejectsAllWhenUnsupported(t *testing.T) {
	isolate(t)
	fs := flag.NewFlagSet("x", flag.ContinueOnError)
	flags := defineDaemonScopeFlags(fs, true)
	if err := fs.Parse([]string{"--all"}); err != nil {
		t.Fatal(err)
	}
	if _, err := daemonScopesFromCurrentDirAndFlags(flags, false); err == nil {
		t.Fatal("--all accepted for unsupported command")
	}
}

func TestDaemonScopeRejectsEmptyProjectFlag(t *testing.T) {
	isolate(t)
	for _, args := range [][]string{{"--project", ""}, {"-p", "   "}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			fs := flag.NewFlagSet("x", flag.ContinueOnError)
			flags := defineDaemonScopeFlags(fs, true)
			if err := fs.Parse(args); err != nil {
				t.Fatal(err)
			}
			if _, err := daemonScopesFromCurrentDirAndFlags(flags, true); err == nil {
				t.Fatalf("empty project flag accepted: %v", args)
			}
		})
	}
}

func TestAllDaemonScopesIncludesRegistryAndStateFiles(t *testing.T) {
	isolate(t)
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	demoKey := projectDaemonScope("demo").fileKey()
	stateOnlyKey := projectDaemonScope("from state").fileKey()
	if err := registryStore().Update(func(r *registry.Registry) error {
		return r.Reserve(registry.Reservation{Project: "demo", Service: "web", Domain: "web.localhost", Port: 4300})
	}); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(configHome, "gate", "daemons")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, demoKey+".pid"), []byte("123"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, stateOnlyKey+".pid"), []byte("123"), 0o600); err != nil {
		t.Fatal(err)
	}

	scopes, err := allDaemonScopes()
	if err != nil {
		t.Fatalf("allDaemonScopes: %v", err)
	}
	got := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		got = append(got, scope.String())
	}
	for _, want := range []string{"global", "project:demo", "project:" + strings.TrimPrefix(stateOnlyKey, "project-")} {
		if !containsString(got, want) {
			t.Fatalf("scopes = %v, missing %q", got, want)
		}
	}
	for _, scope := range scopes {
		if scope.Key == stateOnlyKey && scope.fileKey() != stateOnlyKey {
			t.Fatalf("state-derived fileKey = %q, want %q", scope.fileKey(), stateOnlyKey)
		}
	}
}

func TestDaemonScopeFileKeySlug(t *testing.T) {
	if got := projectDaemonScope("Stamp.is API").fileKey(); !strings.HasPrefix(got, "project-stamp-is-api-") {
		t.Fatalf("fileKey = %q, want project-stamp-is-api-*", got)
	}
	if got := projectDaemonScope("한글").fileKey(); got == "project-" || got == "" {
		t.Fatalf("fileKey fallback empty: %q", got)
	}
	if a, b := projectDaemonScope("stamp.is").fileKey(), projectDaemonScope("stamp is").fileKey(); a == b {
		t.Fatalf("fileKey collision: %q", a)
	}
	if a, b := projectDaemonScope("Demo").fileKey(), projectDaemonScope("demo").fileKey(); a == b {
		t.Fatalf("case-sensitive project fileKey collision: %q", a)
	}
}

func TestListenerPairForKeyRestoresPortOnlyListener(t *testing.T) {
	pair := listenerPairForKey(listener.Key("https-443-http-80"))
	if pair.HTTPSAddr != ":443" || pair.HTTPAddr != ":80" {
		t.Fatalf("pair = %+v", pair)
	}
}

func TestListenerPairForKeyLeavesHashedListenerUnknown(t *testing.T) {
	pair := listenerPairForKey(listener.Key("https-hdeadbeef-443-http-80"))
	if pair.HTTPSAddr != "" || pair.HTTPAddr != "" {
		t.Fatalf("hashed listener should not be guessed: %+v", pair)
	}
}

func containsString(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gate/internal/paths"
)

func TestRunVersion(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"--version"}, &out, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if got := strings.TrimSpace(out.String()); got != version {
		t.Fatalf("stdout = %q, want %q", got, version)
	}
}

func TestRunNoArgsIsUsageError(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run(nil, &out, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if out.Len() == 0 {
		t.Fatal("expected usage on stdout")
	}
}

func TestRunUnknownCommand(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"bogus"}, &out, &errb); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "unknown command") {
		t.Fatalf("stderr = %q, want unknown command", errb.String())
	}
}

func TestRunDispatch(t *testing.T) {
	commands["ping"] = func(args []string, stdout, _ io.Writer) int {
		_, _ = io.WriteString(stdout, "pong:"+strings.Join(args, ","))
		return 0
	}
	t.Cleanup(func() { delete(commands, "ping") })

	var out, errb bytes.Buffer
	if code := run([]string{"ping", "a", "b"}, &out, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if got, want := out.String(), "pong:a,b"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestRunIsolatedRootSetsEnvForCommand(t *testing.T) {
	prev, hadPrev := os.LookupEnv(isolatedRootEnv)
	if err := os.Unsetenv(isolatedRootEnv); err != nil {
		t.Fatalf("unset %s: %v", isolatedRootEnv, err)
	}
	t.Cleanup(func() {
		if hadPrev {
			_ = os.Setenv(isolatedRootEnv, prev)
			return
		}
		_ = os.Unsetenv(isolatedRootEnv)
	})

	commands["ping"] = func(_ []string, stdout, _ io.Writer) int {
		_, _ = io.WriteString(stdout, os.Getenv(isolatedRootEnv))
		return 0
	}
	t.Cleanup(func() { delete(commands, "ping") })

	root := filepath.Join(t.TempDir(), "isolated")
	var out, errb bytes.Buffer
	if code := run([]string{"ping", "--isolated-root", root}, &out, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, errb.String())
	}
	want, err := paths.ValidateIsolatedRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if _, ok := os.LookupEnv(isolatedRootEnv); ok {
		t.Fatalf("%s leaked after run", isolatedRootEnv)
	}
}

func TestRunRejectsUnsafeFlagAndAmbientIsolatedRoots(t *testing.T) {
	commands["ping"] = func(_ []string, _, _ io.Writer) int {
		t.Fatal("command ran with unsafe isolated root")
		return 0
	}
	t.Cleanup(func() { delete(commands, "ping") })
	for _, tc := range []struct {
		name string
		args []string
		env  string
	}{
		{name: "flag", args: []string{"ping", "--isolated-root", string(filepath.Separator)}},
		{name: "ambient", args: []string{"ping"}, env: string(filepath.Separator)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(isolatedRootEnv, tc.env)
			var out, errb bytes.Buffer
			if code := run(tc.args, &out, &errb); code != 2 {
				t.Fatalf("exit = %d, stderr=%s", code, errb.String())
			}
			if !strings.Contains(errb.String(), "invalid isolated root") {
				t.Fatalf("stderr = %q", errb.String())
			}
		})
	}
}

func TestExtractIsolatedRootPreservesChildArgsAfterSeparator(t *testing.T) {
	args, root, err := extractIsolatedRoot([]string{"run", "--isolated-root=.gate", "web", "--", "cmd", "--isolated-root", "child"})
	if err != nil {
		t.Fatalf("extractIsolatedRoot() error = %v", err)
	}
	if root != ".gate" {
		t.Fatalf("root = %q, want .gate", root)
	}
	want := []string{"run", "web", "--", "cmd", "--isolated-root", "child"}
	if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("args = %#v, want %#v", args, want)
	}
}

func TestRunDoctorHelpIsReachable(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"doctor", "-h"}, &out, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "gate doctor") {
		t.Fatalf("stdout = %q, want doctor help", out.String())
	}
}

func TestRootUsageIncludesDoctor(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run(nil, &out, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "doctor") {
		t.Fatalf("stdout = %q, want doctor in usage", out.String())
	}
}

func TestRootUsageIncludesFlags(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run(nil, &out, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, errb.String())
	}
	for _, want := range []string{"flags:", "-h, --help", "-v, --version", "--isolated-root"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("stdout = %q, want %q", out.String(), want)
		}
	}
}

func TestDispatcherHelpIncludesCommands(t *testing.T) {
	cases := map[string][]string{
		"daemon":     {"COMMANDS", "status", "start", "stop", "restart", "logs"},
		"expose":     {"COMMANDS", "ls", "stop"},
		"ca":         {"COMMANDS", "export"},
		"skill":      {"COMMANDS", "path", "print"},
		"completion": {"COMMANDS", "bash", "zsh", "fish"},
	}
	for cmd, wants := range cases {
		var out, errb bytes.Buffer
		if code := run([]string{cmd, "-h"}, &out, &errb); code != 0 {
			t.Fatalf("run %s -h exit = %d; stderr=%s", cmd, code, errb.String())
		}
		for _, want := range wants {
			if !strings.Contains(out.String(), want) {
				t.Fatalf("%s help = %q, want %q", cmd, out.String(), want)
			}
		}
	}
}

package main

import (
	"bytes"
	"io"
	"strings"
	"testing"
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
	for _, want := range []string{"flags:", "-h, --help", "-v, --version"} {
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

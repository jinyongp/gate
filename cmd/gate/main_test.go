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

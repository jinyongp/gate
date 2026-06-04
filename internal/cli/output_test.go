package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"gate/internal/ui/uitest"
)

func TestStatusDotPlain(t *testing.T) {
	if got := statusDot("live", false); got != "* live" {
		t.Fatalf("live plain = %q", got)
	}
	if got := statusDot("down", false); got != "o down" {
		t.Fatalf("down plain = %q", got)
	}
}

func TestStatusDotColorGlyph(t *testing.T) {
	if g := statusDot("live", true); !strings.Contains(g, "●") || !strings.Contains(g, "live") {
		t.Fatalf("live color = %q", g)
	}
	if g := statusDot("down", true); !strings.Contains(g, "○") || !strings.Contains(g, "down") {
		t.Fatalf("down color = %q", g)
	}
}

func TestRichOutGate(t *testing.T) {
	uitest.ClearColorEnv(t)
	var buf bytes.Buffer
	if richOut(&buf, false) {
		t.Fatal("richOut must be false for a non-TTY buffer")
	}
	uitest.ForceColor(t)
	if !richOut(&buf, false) {
		t.Fatal("forced colour should enable richOut for a non-TTY buffer")
	}
	if richOut(&buf, true) {
		t.Fatal("richOut must be false when emitting JSON")
	}
	uitest.DisableColor(t)
	if richOut(&buf, false) {
		t.Fatal("disabled colour should disable richOut")
	}
}

func TestPrintCancelledUsesFailureMarkerAndLeadingBlankLine(t *testing.T) {
	uitest.ClearColorEnv(t)
	var out bytes.Buffer
	printCancelled(&out, "init")
	got := out.String()
	if !strings.HasPrefix(got, "\n") {
		t.Fatalf("cancel output should start on a separate line: %q", got)
	}
	if !strings.Contains(got, "✗ init cancelled") {
		t.Fatalf("cancel output = %q", got)
	}
	if strings.Contains(got, "✓") {
		t.Fatalf("cancel output used success marker: %q", got)
	}
}

type recordingActivity struct {
	label  string
	events *[]string
}

func (a recordingActivity) Stop() {
	*a.events = append(*a.events, "stop:"+a.label)
}

func recordActivities(t *testing.T) *[]string {
	t.Helper()
	oldStart := startActivityFunc
	events := []string{}
	startActivityFunc = func(_ io.Writer, _ bool, label string) activityHandle {
		events = append(events, "start:"+label)
		return recordingActivity{label: label, events: &events}
	}
	t.Cleanup(func() { startActivityFunc = oldStart })
	return &events
}

func lastEvent(events []string) string {
	if len(events) == 0 {
		return ""
	}
	return events[len(events)-1]
}

// TestLsEmptyPlain shows a message, not a bare header, when there are no rows.
func TestLsEmptyPlain(t *testing.T) {
	isolate(t)
	var out, errb bytes.Buffer
	if code := Ls([]string{"--all"}, &out, &errb); code != ExitOK {
		t.Fatalf("Ls exit = %d", code)
	}
	s := out.String()
	if strings.Contains(s, "\x1b") {
		t.Fatalf("empty plain Ls leaked an escape: %q", s)
	}
	if !strings.Contains(s, "No reservations") {
		t.Fatalf("empty Ls should say so, got: %q", s)
	}
	if strings.Contains(s, "PROJECT") {
		t.Fatalf("empty Ls should not print a bare header: %q", s)
	}
}

// TestLsEmptyJSON keeps the machine contract: empty list, not a message.
func TestLsEmptyJSON(t *testing.T) {
	isolate(t)
	var out, errb bytes.Buffer
	if code := Ls([]string{"--all", "--json"}, &out, &errb); code != ExitOK {
		t.Fatalf("Ls --json exit = %d", code)
	}
	var got struct {
		Services []any `json:"services"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out.String())
	}
	if len(got.Services) != 0 {
		t.Fatalf("want empty services, got %d", len(got.Services))
	}
}

// TestLsPlainNoEscapes locks the pipe-safe contract: a non-TTY Ls emits the
// plain tabwriter table with no ANSI escapes.
func TestLsPlainNoEscapes(t *testing.T) {
	isolate(t)
	var out, errb bytes.Buffer
	if code := Add([]string{"web", "web.localhost", "4312"}, &out, &errb); code != ExitOK {
		t.Fatalf("Add exit = %d, stderr=%s", code, errb.String())
	}
	out.Reset()
	if code := Ls([]string{"--all"}, &out, &errb); code != ExitOK {
		t.Fatalf("Ls exit = %d", code)
	}
	s := out.String()
	if strings.Contains(s, "\x1b") {
		t.Fatalf("plain Ls leaked an ANSI escape:\n%q", s)
	}
	for _, want := range []string{"SCOPE", "SERVICE", "DOMAIN", "PORT", "ROUTE", "UPSTREAM", "global", "web", "https://web.localhost", "4312"} {
		if !strings.Contains(s, want) {
			t.Fatalf("Ls output missing %q in:\n%s", want, s)
		}
	}
}

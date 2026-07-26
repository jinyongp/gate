package ui

import (
	"bytes"
	"strings"
	"testing"

	"gate/internal/ui/uitest"
)

func TestConsolePlainContract(t *testing.T) {
	uitest.ClearColorEnv(t)
	var out, errOut bytes.Buffer
	console := NewConsole(&out, &errOut)

	console.Success("created")
	console.OK("checked")
	console.Info("next")
	console.KV("tag", "v1.2.3")
	console.Section("Checks")
	console.Cancelled("release")
	console.Warning("careful")
	console.Error("failed")

	if got, want := out.String(), "created\nchecked\nnext\n  tag: v1.2.3\n\nChecks\n\n✗ release cancelled\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got, want := errOut.String(), "warning: careful\nerror: failed\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestConsoleForcedColorUsesSharedPalette(t *testing.T) {
	uitest.ForceColor(t)
	var out, errOut bytes.Buffer
	console := NewConsole(&out, &errOut)

	console.Success("created")
	console.Warning("careful")

	if got := out.String(); !strings.Contains(got, "✓") {
		t.Fatalf("styled success = %q", got)
	}
	if got := errOut.String(); !strings.Contains(got, "!") {
		t.Fatalf("styled warning = %q", got)
	}
}

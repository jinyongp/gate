package cli

import (
	"strings"
	"testing"
)

func TestRenderPromptLabelIsNotDimmed(t *testing.T) {
	t.Setenv("FORCE_COLOR", "1")
	t.Setenv("NO_COLOR", "")
	t.Setenv("CLICOLOR", "")
	t.Setenv("CLICOLOR_FORCE", "")

	got := renderPromptLabel("Upgrade now?")
	if got != "Upgrade now? " {
		t.Fatalf("prompt label = %q", got)
	}
	if strings.Contains(got, "\x1b[") {
		t.Fatalf("prompt label contains ANSI styling: %q", got)
	}
}

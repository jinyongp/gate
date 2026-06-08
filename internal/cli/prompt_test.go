package cli

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestRenderPromptLabelIncludesMarkerAndDoesNotStyleQuestion(t *testing.T) {
	t.Setenv("FORCE_COLOR", "1")
	t.Setenv("NO_COLOR", "")
	t.Setenv("CLICOLOR", "")
	t.Setenv("CLICOLOR_FORCE", "")

	got := renderPromptLabel(&bytes.Buffer{}, "Upgrade now?")
	if !strings.HasSuffix(got, " Upgrade now? ") {
		t.Fatalf("prompt label = %q", got)
	}
	if !strings.Contains(got, "›") {
		t.Fatalf("prompt label missing marker: %q", got)
	}
}

func TestRenderPromptHeadingIncludesMarkerAndDoesNotStyleQuestion(t *testing.T) {
	t.Setenv("FORCE_COLOR", "1")
	t.Setenv("NO_COLOR", "")
	t.Setenv("CLICOLOR", "")
	t.Setenv("CLICOLOR_FORCE", "")

	got := renderPromptHeading(&bytes.Buffer{}, "Upgrade now?")
	if !strings.HasSuffix(got, " Upgrade now?") {
		t.Fatalf("prompt heading = %q", got)
	}
	if !strings.Contains(got, "›") {
		t.Fatalf("prompt heading missing marker: %q", got)
	}
}

func TestPromptInputDoneSequenceUsesSingleNewline(t *testing.T) {
	if got := promptInputDoneSequence(); got != "\r\n" {
		t.Fatalf("done sequence = %q", got)
	}
}

func TestRenderPromptInputKeepsPlaceholderCursorOnPromptLine(t *testing.T) {
	var out bytes.Buffer
	frame := promptInputFrame{Prompt: renderPromptLabel(&out, "Upgrade now?")}
	err := renderPromptInput(&out, &frame, "", promptInputSpec{
		Default:     "yes",
		Placeholder: "yes",
	})
	if err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if strings.Contains(got, "\r\n") {
		t.Fatalf("prompt render moved to another line: %q", got)
	}
	if !strings.Contains(got, "yes\x1b[3D") {
		t.Fatalf("prompt render did not move cursor back over placeholder: %q", got)
	}
}

func TestRenderPromptInputReturnsCursorFromStatusLine(t *testing.T) {
	var out bytes.Buffer
	frame := promptInputFrame{Prompt: renderPromptLabel(&out, "Upgrade now?")}
	err := renderPromptInput(&out, &frame, "asdf", promptInputSpec{
		Default: "yes",
		Validate: func(string) error {
			return fmt.Errorf("type yes to upgrade, or no to cancel")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "type yes to upgrade, or no to cancel") {
		t.Fatalf("prompt render missing status line: %q", got)
	}
	if !strings.HasSuffix(got, "\x1b[1A\r\x1b[19C") {
		t.Fatalf("prompt render did not return cursor to input line: %q", got)
	}
}

func TestPromptVisibleWidthStripsANSICSI(t *testing.T) {
	if got := promptVisibleWidth("\x1b[2myes\x1b[0m"); got != 3 {
		t.Fatalf("visible width = %d", got)
	}
}

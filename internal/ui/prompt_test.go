package ui

import (
	"bufio"
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestPromptChoiceFallbackRetriesAndUsesDefault(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("bogus\n\n"))
	var output bytes.Buffer

	got, err := PromptChoice(reader, &output, "Release now?", "yes", []string{"yes", "no"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "yes" {
		t.Fatalf("choice = %q", got)
	}
	if !strings.Contains(output.String(), "Choose one of: yes, no") {
		t.Fatalf("retry output = %q", output.String())
	}
}

func TestPromptChoiceRejectsEmptyAllowedSet(t *testing.T) {
	_, err := PromptChoice(bufio.NewReader(strings.NewReader("")), &bytes.Buffer{}, "Pick", "", nil)
	if err == nil {
		t.Fatal("expected empty allowed set error")
	}
}

func TestPromptInterruptedSentinel(t *testing.T) {
	if !errors.Is(ErrPromptInterrupted, ErrPromptInterrupted) {
		t.Fatal("interrupted sentinel must support errors.Is")
	}
}

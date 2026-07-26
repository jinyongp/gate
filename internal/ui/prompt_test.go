package ui

import (
	"bufio"
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestPromptChoiceFallbackRetriesAndMatchesCaseInsensitively(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("bogus\nYES\n"))
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

func TestPromptChoiceFallbackUsesDefault(t *testing.T) {
	got, err := PromptChoice(
		bufio.NewReader(strings.NewReader("\n")),
		&bytes.Buffer{},
		"Release now?",
		"yes",
		[]string{"yes", "no"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got != "yes" {
		t.Fatalf("choice = %q", got)
	}
}

func TestPromptChoiceRejectsEmptyAllowedSet(t *testing.T) {
	_, err := PromptChoice(bufio.NewReader(strings.NewReader("")), &bytes.Buffer{}, "Pick", "", nil)
	if err == nil {
		t.Fatal("expected empty allowed set error")
	}
}

func TestPromptChoicesMapsCaseSensitiveAliases(t *testing.T) {
	choices := []Choice{
		{Value: "patch", Aliases: []string{"p", "1"}, CaseSensitive: true},
		{Value: "minor", Aliases: []string{"m", "2"}, CaseSensitive: true},
		{Value: "major", Aliases: []string{"M", "3"}, CaseSensitive: true},
	}
	for input, want := range map[string]string{"p\n": "patch", "m\n": "minor", "M\n": "major", "3\n": "major"} {
		t.Run(strings.TrimSpace(input), func(t *testing.T) {
			got, err := PromptChoices(
				bufio.NewReader(strings.NewReader(input)),
				&bytes.Buffer{},
				"Pick",
				"patch",
				choices,
			)
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("choice = %q, want %q", got, want)
			}
		})
	}
}

func TestPromptChoicesUsesDisplayLabelsButListsValuesOnRetry(t *testing.T) {
	var output bytes.Buffer
	got, err := PromptChoices(
		bufio.NewReader(strings.NewReader("bogus\n\n")),
		&output,
		"Pick",
		"minor",
		[]Choice{{Value: "patch", Label: "patch v1.0.1"}, {Value: "minor", Label: "minor v1.1.0"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got != "minor" {
		t.Fatalf("choice = %q", got)
	}
	if !strings.Contains(output.String(), "Choose one of: patch, minor") {
		t.Fatalf("retry output = %q", output.String())
	}
}

func TestPromptInterruptedSentinel(t *testing.T) {
	if !errors.Is(ErrPromptInterrupted, ErrPromptInterrupted) {
		t.Fatal("interrupted sentinel must support errors.Is")
	}
}

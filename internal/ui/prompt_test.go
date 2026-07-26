package ui

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/term"
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

func TestReadLineFileContextCancelsWithoutClosingInput(t *testing.T) {
	readerFile, writerFile, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = readerFile.Close()
		_ = writerFile.Close()
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = ReadLineFileContext(ctx, readerFile, bufio.NewReader(readerFile))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ReadLineFileContext error = %v", err)
	}
	if _, err := readerFile.Stat(); err != nil {
		t.Fatalf("input was closed: %v", err)
	}
}

func TestPromptChoicesFileContextCancelsWithBufferedPartialRetry(t *testing.T) {
	readerFile, writerFile, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = readerFile.Close()
		_ = writerFile.Close()
	})
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, promptErr := PromptChoicesFileContext(
			ctx,
			readerFile,
			bufio.NewReader(readerFile),
			&bytes.Buffer{},
			"Pick",
			"patch",
			[]Choice{{Value: "patch"}, {Value: "minor"}},
		)
		result <- promptErr
	}()
	if _, err := writerFile.WriteString("bogus\npartial"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("PromptChoicesFileContext error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("buffered partial retry did not stop after cancellation")
	}
}

func TestReadRuneContextCancelsDuringPartialUTF8Rune(t *testing.T) {
	readerFile, writerFile, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = readerFile.Close()
		_ = writerFile.Close()
	})
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, readErr := readRuneContext(ctx, readerFile, bufio.NewReader(readerFile))
		result <- readErr
	}()
	if _, err := writerFile.Write([]byte{0xe2}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("readRuneContext error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("partial UTF-8 rune did not stop after cancellation")
	}
}

func TestPromptChoicesFileContextRestoresTerminalAfterCancellation(t *testing.T) {
	primary, terminal, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = primary.Close()
		_ = terminal.Close()
	})
	before, err := term.GetState(int(terminal.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, promptErr := PromptChoicesFileContext(
			ctx,
			terminal,
			bufio.NewReader(terminal),
			terminal,
			"Pick",
			"patch",
			[]Choice{{Value: "patch"}, {Value: "minor"}},
		)
		result <- promptErr
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("PromptChoicesFileContext error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("terminal prompt did not stop after cancellation")
	}
	after, err := term.GetState(int(terminal.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	if *before != *after {
		t.Fatal("terminal state was not restored after cancellation")
	}
}

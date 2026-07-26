package ui

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

var ErrPromptInterrupted = errors.New("interrupted")

func PromptLabel(w io.Writer, label string) string {
	label = strings.TrimSpace(label)
	if strings.HasSuffix(label, "?") {
		label += " "
	} else {
		label += ": "
	}
	marker := "›"
	if Enabled(w) {
		marker = Tint(Brand, marker)
	}
	return marker + " " + label
}

func PromptHeading(w io.Writer, label string) string {
	return strings.TrimSpace(PromptLabel(w, label))
}

func PromptValue(w io.Writer, value string) string {
	if Enabled(w) {
		return Header.Render(value)
	}
	return value
}

func PromptEnabled(w io.Writer) bool {
	output, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(output.Fd()))
}

func PromptChoice(reader *bufio.Reader, output io.Writer, label, defaultValue string, allowed []string) (string, error) {
	if len(allowed) == 0 {
		return "", errors.New("prompt choice requires at least one allowed value")
	}
	if PromptEnabled(output) {
		return promptChoiceRadio(output, label, defaultValue, allowed)
	}
	for {
		fmt.Fprint(output, PromptLabel(output, label))
		if defaultValue != "" {
			fmt.Fprintf(output, "[%s] ", defaultValue)
		}
		line, err := reader.ReadString('\n')
		if err != nil && line == "" {
			return "", err
		}
		value := strings.ToLower(strings.TrimSpace(line))
		if value == "" {
			value = defaultValue
		}
		for _, item := range allowed {
			if value == item {
				return value, nil
			}
		}
		fmt.Fprintf(output, "Choose one of: %s\n", strings.Join(allowed, ", "))
	}
}

func promptChoiceRadio(output io.Writer, label, defaultValue string, allowed []string) (string, error) {
	selected := choiceIndex(defaultValue, allowed)
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return "", err
	}
	defer func() {
		showPromptCursor(output)
		_ = term.Restore(int(os.Stdin.Fd()), oldState)
	}()
	hidePromptCursor(output)

	if _, err := fmt.Fprintf(output, "%s\r\n", PromptHeading(output, label)); err != nil {
		return "", err
	}
	if err := renderChoiceMenu(output, allowed, selected); err != nil {
		return "", err
	}

	input := bufio.NewReader(os.Stdin)
	for {
		previous := selected
		r, _, err := input.ReadRune()
		if err != nil {
			return "", err
		}
		switch {
		case r == '\r' || r == '\n':
			fmt.Fprint(output, "\r\n")
			return allowed[selected], nil
		case r == 0x03:
			return "", ErrPromptInterrupted
		case r == 0x1b:
			next, _, err := input.ReadRune()
			if err != nil {
				return "", err
			}
			if next != '[' {
				continue
			}
			arrow, _, err := input.ReadRune()
			if err != nil {
				return "", err
			}
			switch arrow {
			case 'A':
				selected = (selected + len(allowed) - 1) % len(allowed)
			case 'B':
				selected = (selected + 1) % len(allowed)
			}
		case r == 'j':
			selected = (selected + 1) % len(allowed)
		case r == 'k':
			selected = (selected + len(allowed) - 1) % len(allowed)
		case r >= '1' && r <= '9':
			index := int(r - '1')
			if index >= len(allowed) {
				continue
			}
			selected = index
			if err := updateChoiceMenu(output, allowed, selected); err != nil {
				return "", err
			}
			fmt.Fprint(output, "\r\n")
			return allowed[selected], nil
		}
		if selected != previous {
			if err := updateChoiceMenu(output, allowed, selected); err != nil {
				return "", err
			}
		}
	}
}

func choiceIndex(defaultValue string, allowed []string) int {
	for index, item := range allowed {
		if item == defaultValue {
			return index
		}
	}
	return 0
}

func renderChoiceMenu(output io.Writer, allowed []string, selected int) error {
	for index, item := range allowed {
		if err := renderChoiceOption(output, item, index == selected); err != nil {
			return err
		}
	}
	return nil
}

func updateChoiceMenu(output io.Writer, allowed []string, selected int) error {
	if _, err := fmt.Fprintf(output, "\x1b[%dA", len(allowed)); err != nil {
		return err
	}
	return renderChoiceMenu(output, allowed, selected)
}

func renderChoiceOption(output io.Writer, label string, selected bool) error {
	marker := "○"
	if selected {
		marker = "●"
	}
	if Enabled(output) {
		if selected {
			marker = Tint(Brand, marker)
			label = Header.Render(label)
		} else {
			marker = Dim.Render(marker)
		}
	}
	_, err := fmt.Fprintf(output, "  %s  %s\x1b[K\r\n", marker, label)
	return err
}

func hidePromptCursor(output io.Writer) {
	_, _ = fmt.Fprint(output, "\x1b[?25l")
}

func showPromptCursor(output io.Writer) {
	_, _ = fmt.Fprint(output, "\x1b[?25h")
}

package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"gate/internal/ui"

	"golang.org/x/term"
)

var errPromptInterrupted = errors.New("interrupted")

func promptString(reader *bufio.Reader, stdout io.Writer, label, def string) (string, error) {
	return promptInput(reader, stdout, promptInputSpec{
		Label:       label,
		Default:     def,
		Placeholder: def,
	})
}

func promptLabel(label string) string {
	label = strings.TrimSpace(label)
	if strings.HasSuffix(label, "?") {
		return label + " "
	}
	return label + ": "
}

func renderPromptLabel(label string) string {
	return promptLabel(label)
}

func renderPromptHeading(stdout io.Writer, label string) string {
	heading := strings.TrimSpace(promptLabel(label))
	if ui.Enabled(stdout) {
		return ui.Dim.Render(heading)
	}
	return heading
}

func renderPromptValue(stdout io.Writer, value string) string {
	if ui.Enabled(stdout) {
		return ui.Header.Render(value)
	}
	return value
}

func placeholderPromptEnabled(stdout io.Writer) bool {
	stdoutFile, ok := stdout.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(stdoutFile.Fd()))
}

type promptInputSpec struct {
	Label            string
	Default          string
	Placeholder      string
	Suffix           string
	AcceptRune       func(rune) bool
	Normalize        func(string) string
	Validate         func(string) error
	LiveDisplay      func(raw, value string) string
	ConfirmedDisplay func(value string) string
}

type promptInputState struct {
	Confirmed bool
}

type promptInputFrame struct {
	Prompt string
	Status bool
}

func promptInput(reader *bufio.Reader, stdout io.Writer, spec promptInputSpec) (string, error) {
	if placeholderPromptEnabled(stdout) {
		return promptInputPlaceholder(stdout, spec)
	}
	for {
		value, err := promptInputFallback(reader, stdout, spec)
		if err != nil {
			return "", err
		}
		if err := validatePromptInput(spec, value); err != nil {
			fmt.Fprintln(stdout, renderErrorMessage(stdout, err.Error()))
			continue
		}
		return value, nil
	}
}

func promptInputFallback(reader *bufio.Reader, stdout io.Writer, spec promptInputSpec) (string, error) {
	prompt := renderPromptLabel(spec.Label)
	if spec.Default == "" {
		fmt.Fprintf(stdout, "%s", prompt)
	} else {
		fmt.Fprintf(stdout, "%s[%s] ", prompt, spec.Default)
	}
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return promptInputValue(spec, line), nil
}

func promptInputPlaceholder(stdout io.Writer, spec promptInputSpec) (string, error) {
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return "", err
	}
	defer func() {
		_ = term.Restore(int(os.Stdin.Fd()), oldState)
	}()

	prompt := renderPromptLabel(spec.Label)
	frame := promptInputFrame{Prompt: prompt}
	value := ""
	skipEscape := 0
	if err := renderPromptInput(stdout, &frame, value, spec); err != nil {
		return "", err
	}
	input := bufio.NewReader(os.Stdin)
	for {
		r, _, err := input.ReadRune()
		if err != nil {
			return "", err
		}
		if skipEscape > 0 {
			skipEscape--
			continue
		}
		switch {
		case r == '\r' || r == '\n':
			result := promptInputValue(spec, value)
			if err := validatePromptInput(spec, result); err == nil {
				if err := clearPromptStatus(stdout, &frame); err != nil {
					return "", err
				}
				if _, err := fmt.Fprintf(stdout, "\r\x1b[2K%s", prompt); err != nil {
					return "", err
				}
				if err := renderPromptInputValue(stdout, value, result, spec, promptInputState{Confirmed: true}); err != nil {
					return "", err
				}
				_, err = fmt.Fprint(stdout, "\r\n\r\n")
				return result, err
			}
		case r == 0x03:
			return "", errPromptInterrupted
		case r == 0x04:
			if value == "" {
				return "", io.EOF
			}
		case r == 0x15:
			value = ""
		case r == 0x1b:
			skipEscape = 2
		case r == 0x7f || r == 0x08:
			value = trimLastRune(value)
		case r >= 0x20:
			if spec.AcceptRune != nil && !spec.AcceptRune(r) {
				continue
			}
			value += string(r)
		default:
			continue
		}
		if err := renderPromptInput(stdout, &frame, value, spec); err != nil {
			return "", err
		}
	}
}

func promptInputValue(spec promptInputSpec, raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		value = spec.Default
	}
	if spec.Normalize != nil {
		return spec.Normalize(value)
	}
	return value
}

func validatePromptInput(spec promptInputSpec, value string) error {
	if spec.Validate == nil {
		return nil
	}
	return spec.Validate(value)
}

func renderPromptInput(stdout io.Writer, frame *promptInputFrame, raw string, spec promptInputSpec) error {
	if err := clearPromptStatus(stdout, frame); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(stdout, "\r\x1b[2K%s", frame.Prompt); err != nil {
		return err
	}
	value := promptInputValue(spec, raw)
	if err := renderPromptInputValue(stdout, raw, value, spec, promptInputState{}); err != nil {
		return err
	}
	return renderPromptStatus(stdout, frame, validatePromptInput(spec, value))
}

func renderPromptInputValue(stdout io.Writer, raw, value string, spec promptInputSpec, state promptInputState) error {
	if state.Confirmed {
		confirmed := value
		if spec.ConfirmedDisplay != nil {
			confirmed = spec.ConfirmedDisplay(value)
		}
		_, err := fmt.Fprint(stdout, renderPromptValue(stdout, confirmed))
		return err
	}

	if raw == "" && spec.Placeholder != "" {
		if _, err := fmt.Fprint(stdout, "\x1b7"); err != nil {
			return err
		}
		placeholder := spec.Placeholder
		if ui.Enabled(stdout) {
			placeholder = ui.Dim.Render(placeholder)
		}
		_, err := fmt.Fprint(stdout, placeholder)
		return err
	}

	display := raw
	if spec.LiveDisplay != nil {
		display = spec.LiveDisplay(raw, value)
	}
	if _, err := fmt.Fprint(stdout, display); err != nil {
		return err
	}
	if _, err := fmt.Fprint(stdout, "\x1b7"); err != nil {
		return err
	}
	if spec.Suffix == "" || raw == "" {
		return nil
	}
	suffix := spec.Suffix
	if ui.Enabled(stdout) {
		suffix = ui.Dim.Render(suffix)
	}
	_, err := fmt.Fprint(stdout, suffix)
	return err
}

func trimLastRune(s string) string {
	if s == "" {
		return ""
	}
	_, size := utf8.DecodeLastRuneInString(s)
	return s[:len(s)-size]
}

func promptChoice(reader *bufio.Reader, stdout io.Writer, label, def string, allowed []string) (string, error) {
	if placeholderPromptEnabled(stdout) {
		return promptChoiceRadio(stdout, label, def, allowed)
	}
	for {
		value, err := promptString(reader, stdout, label, def)
		if err != nil {
			return "", err
		}
		value = strings.ToLower(strings.TrimSpace(value))
		for _, item := range allowed {
			if value == item {
				return value, nil
			}
		}
		fmt.Fprintf(stdout, "Choose one of: %s\n", strings.Join(allowed, ", "))
	}
}

func promptChoiceRadio(stdout io.Writer, label, def string, allowed []string) (string, error) {
	selected := choiceIndex(def, allowed)
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return "", err
	}
	defer func() {
		showCursor(stdout)
		_ = term.Restore(int(os.Stdin.Fd()), oldState)
	}()
	hideCursor(stdout)

	if _, err := fmt.Fprintf(stdout, "%s\r\n", renderPromptHeading(stdout, label)); err != nil {
		return "", err
	}
	if err := renderChoiceMenu(stdout, allowed, selected); err != nil {
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
			fmt.Fprint(stdout, "\r\n")
			return allowed[selected], nil
		case r == 0x03:
			return "", errPromptInterrupted
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
			if err := updateChoiceMenu(stdout, allowed, selected); err != nil {
				return "", err
			}
			fmt.Fprint(stdout, "\r\n")
			return allowed[selected], nil
		}
		if selected != previous {
			if err := updateChoiceMenu(stdout, allowed, selected); err != nil {
				return "", err
			}
		}
	}
}

func choiceIndex(def string, allowed []string) int {
	for i, item := range allowed {
		if item == def {
			return i
		}
	}
	return 0
}

func renderChoiceMenu(stdout io.Writer, allowed []string, selected int) error {
	for i, item := range allowed {
		if err := renderChoiceOption(stdout, item, i == selected); err != nil {
			return err
		}
	}
	return nil
}

func updateChoiceMenu(stdout io.Writer, allowed []string, selected int) error {
	if _, err := fmt.Fprintf(stdout, "\x1b[%dA", len(allowed)); err != nil {
		return err
	}
	return renderChoiceMenu(stdout, allowed, selected)
}

func renderChoiceOption(stdout io.Writer, label string, selected bool) error {
	marker := "○"
	if selected {
		marker = "●"
	}
	if ui.Enabled(stdout) {
		if selected {
			marker = ui.Tint(ui.Brand, marker)
			label = ui.Header.Render(label)
		} else {
			marker = ui.Dim.Render(marker)
		}
	}
	_, err := fmt.Fprintf(stdout, "  %s  %s\x1b[K\r\n", marker, label)
	return err
}

func hideCursor(stdout io.Writer) {
	_, _ = fmt.Fprint(stdout, "\x1b[?25l")
}

func showCursor(stdout io.Writer) {
	_, _ = fmt.Fprint(stdout, "\x1b[?25h")
}

func clearPromptStatus(stdout io.Writer, frame *promptInputFrame) error {
	if !frame.Status {
		return nil
	}
	if _, err := fmt.Fprint(stdout, "\r\n\x1b[2K\x1b8"); err != nil {
		return err
	}
	frame.Status = false
	return nil
}

func renderPromptStatus(stdout io.Writer, frame *promptInputFrame, err error) error {
	frame.Status = true
	if _, writeErr := fmt.Fprint(stdout, "\r\n\x1b[2K"); writeErr != nil {
		return writeErr
	}
	if err != nil {
		message := renderErrorMessage(stdout, err.Error())
		if _, writeErr := fmt.Fprint(stdout, message); writeErr != nil {
			return writeErr
		}
	}
	_, writeErr := fmt.Fprint(stdout, "\x1b8")
	return writeErr
}

func renderErrorMessage(stdout io.Writer, message string) string {
	if ui.Enabled(stdout) {
		return ui.Tint(ui.Danger, message)
	}
	if stdoutFile, ok := stdout.(*os.File); ok && term.IsTerminal(int(stdoutFile.Fd())) {
		return "\x1b[31m" + message + "\x1b[0m"
	}
	return message
}

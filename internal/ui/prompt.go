package ui

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/term"
)

var ErrPromptInterrupted = errors.New("interrupted")

type Choice struct {
	Value         string
	Label         string
	Aliases       []string
	CaseSensitive bool
}

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
	return PromptEnabledFor(os.Stdin, w)
}

func PromptEnabledFor(input *os.File, w io.Writer) bool {
	output, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(input.Fd())) && term.IsTerminal(int(output.Fd()))
}

func PromptConfirm(reader *bufio.Reader, output io.Writer, label string) (bool, error) {
	return PromptConfirmContext(context.Background(), reader, output, label)
}

func PromptConfirmContext(
	ctx context.Context,
	reader *bufio.Reader,
	output io.Writer,
	label string,
) (bool, error) {
	if PromptEnabled(output) {
		return promptConfirmKey(ctx, os.Stdin, reader, output, label)
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	confirmed, err := promptConfirmLine(output, label, func() (string, error) {
		return reader.ReadString('\n')
	})
	if ctxErr := ctx.Err(); ctxErr != nil {
		return false, ctxErr
	}
	return confirmed, err
}

func PromptConfirmFileContext(
	ctx context.Context,
	input *os.File,
	reader *bufio.Reader,
	output io.Writer,
	label string,
) (bool, error) {
	if PromptEnabledFor(input, output) {
		return promptConfirmKey(ctx, input, reader, output, label)
	}
	return promptConfirmLine(output, label, func() (string, error) {
		return ReadLineFileContext(ctx, input, reader)
	})
}

func promptConfirmLine(
	output io.Writer,
	label string,
	readLine func() (string, error),
) (bool, error) {
	fmt.Fprint(output, promptConfirmLabel(output, label, false))
	line, err := readLine()
	if err != nil && line == "" {
		return false, err
	}
	return line == "\n" || line == "\r\n", nil
}

func promptConfirmKey(
	ctx context.Context,
	inputFile *os.File,
	reader *bufio.Reader,
	output io.Writer,
	label string,
) (bool, error) {
	oldState, err := term.MakeRaw(int(inputFile.Fd()))
	if err != nil {
		return false, err
	}
	defer func() {
		showCursor(output)
		_ = term.Restore(int(inputFile.Fd()), oldState)
	}()
	hideCursor(output)

	if _, err := fmt.Fprint(output, promptConfirmLabel(output, label, true)); err != nil {
		return false, err
	}
	for {
		r, err := readRuneContext(ctx, inputFile, reader)
		if err != nil {
			return false, err
		}
		switch r {
		case '\r', '\n':
			fmt.Fprint(output, "\r\n")
			return true, nil
		case 0x03:
			fmt.Fprint(output, "\r\n")
			return false, ErrPromptInterrupted
		case 0x04:
			fmt.Fprint(output, "\r\n")
			return false, nil
		case 0x1b:
			sequence, err := promptEscapeSequenceContinues(ctx, inputFile, reader)
			if err != nil {
				return false, err
			}
			if sequence {
				continue
			}
			fmt.Fprint(output, "\r\n")
			return false, nil
		}
	}
}

func promptEscapeSequenceContinues(
	ctx context.Context,
	inputFile *os.File,
	reader *bufio.Reader,
) (bool, error) {
	sequenceCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	_, err := readRuneContext(sequenceCtx, inputFile, reader)
	if errors.Is(err, context.DeadlineExceeded) {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return false, ctxErr
		}
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func promptConfirmLabel(output io.Writer, label string, compact bool) string {
	hint := "[Enter to continue; any other input cancels]"
	if compact {
		hint = "ENTER continue · ESC cancel"
		if Enabled(output) {
			hint = FaintTint(Success, "ENTER") + " " +
				Dim.Render("continue ·") + " " +
				FaintTint(Danger, "ESC") + " " +
				Dim.Render("cancel")
		}
	} else if Enabled(output) {
		hint = Dim.Render(hint)
	}
	if compact {
		return PromptHeading(output, label) + "  " + hint
	}
	return PromptLabel(output, label) + hint + ": "
}

func PromptChoice(reader *bufio.Reader, output io.Writer, label, defaultValue string, allowed []string) (string, error) {
	choices := make([]Choice, 0, len(allowed))
	for _, value := range allowed {
		choices = append(choices, Choice{Value: value, Label: value})
	}
	return PromptChoices(reader, output, label, defaultValue, choices)
}

func PromptChoices(reader *bufio.Reader, output io.Writer, label, defaultValue string, choices []Choice) (string, error) {
	if len(choices) == 0 {
		return "", errors.New("prompt choice requires at least one allowed value")
	}
	if err := validateChoices(choices); err != nil {
		return "", err
	}
	if PromptEnabled(output) {
		return promptChoiceRadio(context.Background(), os.Stdin, output, label, defaultValue, choices)
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
		value := strings.TrimSpace(line)
		if value == "" {
			value = defaultValue
		}
		for _, choice := range choices {
			if choiceMatches(value, choice) {
				return choice.Value, nil
			}
		}
		fmt.Fprintf(output, "Choose one of: %s\n", strings.Join(choiceValues(choices), ", "))
	}
}

func PromptChoicesFileContext(
	ctx context.Context,
	input *os.File,
	reader *bufio.Reader,
	output io.Writer,
	label, defaultValue string,
	choices []Choice,
) (string, error) {
	if len(choices) == 0 {
		return "", errors.New("prompt choice requires at least one allowed value")
	}
	if err := validateChoices(choices); err != nil {
		return "", err
	}
	if PromptEnabledFor(input, output) {
		return promptChoiceRadio(ctx, input, output, label, defaultValue, choices)
	}
	for {
		fmt.Fprint(output, PromptLabel(output, label))
		if defaultValue != "" {
			fmt.Fprintf(output, "[%s] ", defaultValue)
		}
		line, err := ReadLineFileContext(ctx, input, reader)
		if err != nil && line == "" {
			return "", err
		}
		value := strings.TrimSpace(line)
		if value == "" {
			value = defaultValue
		}
		for _, choice := range choices {
			if choiceMatches(value, choice) {
				return choice.Value, nil
			}
		}
		fmt.Fprintf(output, "Choose one of: %s\n", strings.Join(choiceValues(choices), ", "))
	}
}

func ReadLineFileContext(ctx context.Context, input *os.File, reader *bufio.Reader) (string, error) {
	var line strings.Builder
	for {
		value, err := readByteFileContext(ctx, input, reader)
		if err != nil {
			return line.String(), err
		}
		line.WriteByte(value)
		if value == '\n' {
			return line.String(), nil
		}
	}
}

func validateChoices(choices []Choice) error {
	for _, choice := range choices {
		if strings.TrimSpace(choice.Value) == "" {
			return errors.New("prompt choice value is required")
		}
	}
	return nil
}

func choiceMatches(value string, choice Choice) bool {
	equal := strings.EqualFold
	if choice.CaseSensitive {
		equal = func(left, right string) bool {
			return left == right
		}
	}
	if equal(value, choice.Value) {
		return true
	}
	for _, alias := range choice.Aliases {
		if equal(value, alias) {
			return true
		}
	}
	return false
}

func choiceValues(choices []Choice) []string {
	values := make([]string, 0, len(choices))
	for _, choice := range choices {
		values = append(values, choice.Value)
	}
	return values
}

func promptChoiceRadio(
	ctx context.Context,
	inputFile *os.File,
	output io.Writer,
	label, defaultValue string,
	choices []Choice,
) (string, error) {
	selected := choiceIndex(defaultValue, choices)
	oldState, err := term.MakeRaw(int(inputFile.Fd()))
	if err != nil {
		return "", err
	}
	defer func() {
		showCursor(output)
		_ = term.Restore(int(inputFile.Fd()), oldState)
	}()
	hideCursor(output)

	if _, err := fmt.Fprintf(output, "%s\r\n", PromptHeading(output, label)); err != nil {
		return "", err
	}
	if err := renderChoiceMenu(output, choices, selected); err != nil {
		return "", err
	}

	input := bufio.NewReader(inputFile)
	for {
		previous := selected
		r, err := readRuneContext(ctx, inputFile, input)
		if err != nil {
			return "", err
		}
		switch {
		case r == '\r' || r == '\n':
			fmt.Fprint(output, "\r\n")
			return choices[selected].Value, nil
		case r == 0x03:
			return "", ErrPromptInterrupted
		case r == 0x1b:
			next, err := readRuneContext(ctx, inputFile, input)
			if err != nil {
				return "", err
			}
			if next != '[' {
				continue
			}
			arrow, err := readRuneContext(ctx, inputFile, input)
			if err != nil {
				return "", err
			}
			switch arrow {
			case 'A':
				selected = (selected + len(choices) - 1) % len(choices)
			case 'B':
				selected = (selected + 1) % len(choices)
			}
		case r == 'j':
			selected = (selected + 1) % len(choices)
		case r == 'k':
			selected = (selected + len(choices) - 1) % len(choices)
		case r >= '1' && r <= '9':
			index := int(r - '1')
			if index >= len(choices) {
				continue
			}
			selected = index
			if err := updateChoiceMenu(output, choices, selected); err != nil {
				return "", err
			}
			fmt.Fprint(output, "\r\n")
			return choices[selected].Value, nil
		default:
			value := string(r)
			for index, choice := range choices {
				if choiceMatches(value, choice) {
					selected = index
					if err := updateChoiceMenu(output, choices, selected); err != nil {
						return "", err
					}
					fmt.Fprint(output, "\r\n")
					return choices[selected].Value, nil
				}
			}
		}
		if selected != previous {
			if err := updateChoiceMenu(output, choices, selected); err != nil {
				return "", err
			}
		}
	}
}

func readRuneContext(ctx context.Context, input *os.File, reader *bufio.Reader) (rune, error) {
	var encoded [utf8.UTFMax]byte
	for length := 0; length < len(encoded); length++ {
		value, err := readByteFileContext(ctx, input, reader)
		if err != nil {
			return 0, err
		}
		encoded[length] = value
		if utf8.FullRune(encoded[:length+1]) {
			decoded, _ := utf8.DecodeRune(encoded[:length+1])
			return decoded, nil
		}
	}
	return utf8.RuneError, nil
}

func readByteFileContext(ctx context.Context, input *os.File, reader *bufio.Reader) (byte, error) {
	if reader.Buffered() == 0 {
		if err := waitReadable(ctx, input); err != nil {
			return 0, err
		}
	}
	value, err := reader.ReadByte()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return 0, ctxErr
	}
	return value, err
}

func choiceIndex(defaultValue string, choices []Choice) int {
	for index, choice := range choices {
		if choice.Value == defaultValue {
			return index
		}
	}
	return 0
}

func renderChoiceMenu(output io.Writer, choices []Choice, selected int) error {
	for index, choice := range choices {
		label := choice.Label
		if label == "" {
			label = choice.Value
		}
		if err := renderChoiceOption(output, label, index == selected); err != nil {
			return err
		}
	}
	return nil
}

func updateChoiceMenu(output io.Writer, choices []Choice, selected int) error {
	if _, err := fmt.Fprintf(output, "\x1b[%dA", len(choices)); err != nil {
		return err
	}
	return renderChoiceMenu(output, choices, selected)
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

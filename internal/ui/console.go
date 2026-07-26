package ui

import (
	"fmt"
	"io"
	"strings"
)

// Console is the shared human-facing output surface for gate commands and
// repository developer tooling. Machine-readable output remains owned by its
// calling package.
type Console struct {
	Out io.Writer
	Err io.Writer
}

func NewConsole(out, errOut io.Writer) Console {
	return Console{Out: out, Err: errOut}
}

func (c Console) Success(message string) {
	if ColorEnabled(c.Out) {
		fmt.Fprintf(c.Out, "%s %s\n", Tint(Success, "✓"), message)
		return
	}
	fmt.Fprintln(c.Out, message)
}

func (c Console) OK(message string) {
	if ColorEnabled(c.Out) {
		fmt.Fprintf(c.Out, "%s %s\n", Tint(Success, "ok:"), message)
		return
	}
	fmt.Fprintln(c.Out, message)
}

func (c Console) StatusOK(message string) {
	if ColorEnabled(c.Out) {
		fmt.Fprintf(c.Out, "%s %s\n", Tint(Success, "ok:"), message)
		return
	}
	fmt.Fprintf(c.Out, "ok: %s\n", message)
}

func (c Console) Info(message string) {
	if ColorEnabled(c.Out) {
		fmt.Fprintln(c.Out, Dim.Render(message))
		return
	}
	fmt.Fprintln(c.Out, message)
}

func (c Console) Warning(message string) {
	if ColorEnabled(c.Err) {
		fmt.Fprintf(c.Err, "%s %s\n", Tint(Warn, "!"), message)
		return
	}
	fmt.Fprintf(c.Err, "warning: %s\n", message)
}

func (c Console) Error(message string) {
	if ColorEnabled(c.Err) {
		fmt.Fprintln(c.Err, Tint(Danger, "error:")+" "+message)
		return
	}
	fmt.Fprintf(c.Err, "error: %s\n", message)
}

func (c Console) KV(label, value string) {
	if ColorEnabled(c.Out) {
		fmt.Fprintf(c.Out, "  %s  %s\n", Dim.Render(label), value)
		return
	}
	fmt.Fprintf(c.Out, "  %s: %s\n", label, value)
}

func (c Console) Item(message string) {
	fmt.Fprintf(c.Out, "  - %s\n", message)
}

func (c Console) Section(label string) {
	fmt.Fprintln(c.Out)
	if ColorEnabled(c.Out) {
		fmt.Fprintln(c.Out, Section(label))
		return
	}
	fmt.Fprintln(c.Out, label)
}

func (c Console) Cancelled(action string) {
	message := strings.TrimSpace(action) + " cancelled"
	if ColorEnabled(c.Out) {
		fmt.Fprintf(c.Out, "\n%s %s\n", Tint(Danger, "✗"), message)
		return
	}
	fmt.Fprintf(c.Out, "\n✗ %s\n", message)
}

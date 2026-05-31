// Package ui holds prx's presentation-tier styling: the lipgloss palette and
// shared render helpers. It is TTY-only sugar — callers gate on Enabled so that
// piped, --json, and NO_COLOR output stays plain. ui imports lipgloss only (no
// prx packages) so it stays free of import cycles and the core stays TUI-free.
package ui

import (
	"io"
	"os"

	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

// Palette. AdaptiveColor adapts to light/dark terminal backgrounds. Brand is a
// terminal-native green accent; section headers use the muted grey.
var (
	Brand   = lipgloss.AdaptiveColor{Light: "#1A7F37", Dark: "#3FB950"}
	Success = lipgloss.AdaptiveColor{Light: "#1A7F37", Dark: "#3FB950"}
	Muted   = lipgloss.AdaptiveColor{Light: "#6E7781", Dark: "#8B949E"}
	Warn    = lipgloss.AdaptiveColor{Light: "#9A6700", Dark: "#D29922"}
	Danger  = lipgloss.AdaptiveColor{Light: "#CF222E", Dark: "#F85149"}

	Header = lipgloss.NewStyle().Bold(true).Foreground(Brand)
	Dim    = lipgloss.NewStyle().Foreground(Muted)
)

// Title renders a rounded-box banner: bold-accent app name + dim tagline.
func Title(app, tagline string) string {
	inner := lipgloss.NewStyle().Bold(true).Foreground(Brand).Render(app) + "  " + Dim.Render(tagline)
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(Brand).
		Padding(0, 1).
		Render(inner)
}

// Section renders a dim section label for grouped help output.
func Section(label string) string {
	return lipgloss.NewStyle().Foreground(Muted).Render(label)
}

// Tint renders s in the foreground color c (e.g. one of the palette colors).
func Tint(c lipgloss.TerminalColor, s string) string {
	return lipgloss.NewStyle().Foreground(c).Render(s)
}

// Enabled reports whether w should receive styled output: a real terminal with
// NO_COLOR unset. It is the single gate for all rich rendering.
func Enabled(w io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// Command renders a fixed-width, left-aligned command name in the brand color,
// used by `prx` usage output.
func Command(name string, width int) string {
	return lipgloss.NewStyle().Foreground(Brand).Width(width).Render(name)
}

// Package ui holds gate's presentation-tier styling: the lipgloss palette and
// shared render helpers. By default it is TTY-only sugar; explicit colour env
// vars may override that for callers that opt in. ui imports lipgloss only (no
// gate packages) so it stays free of import cycles and the core stays TUI-free.
package ui

import (
	"io"

	"gate/internal/ui/policy"

	"github.com/charmbracelet/lipgloss"
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

func ColorEnabled(w io.Writer) bool {
	return policy.ColorEnabled(w)
}

// Enabled is kept for existing presentation callers. Prefer ColorEnabled in new
// code when the colour policy itself is the point.
func Enabled(w io.Writer) bool { return ColorEnabled(w) }

// ColorDisabled reports whether styling is explicitly disabled by the
// environment.
func ColorDisabled() bool {
	return policy.ColorDisabled()
}

// Command renders a fixed-width, left-aligned command name in the brand color,
// used by `gate` usage output.
func Command(name string, width int) string {
	return lipgloss.NewStyle().Foreground(Brand).Width(width).Render(name)
}

package policy

import (
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

const (
	noColor         = "NO_COLOR"
	forceColor      = "FORCE_COLOR"
	clicolorForce   = "CLICOLOR_FORCE"
	clicolor        = "CLICOLOR"
	ci              = "CI"
	gateNoIndicator = "GATE_NO_INDICATOR"
	gateNoSpinner   = "GATE_NO_SPINNER"
)

var (
	getenv     = os.Getenv
	isTerminal = func(f *os.File) bool { return term.IsTerminal(int(f.Fd())) }
)

// ColorEnabled reports whether w should receive styled output.
//
// Precedence:
//   - NO_COLOR disables styling.
//   - FORCE_COLOR or CLICOLOR_FORCE enables styling even for non-TTY writers.
//   - CLICOLOR=0 disables default TTY styling unless a force variable is set.
//   - otherwise styling is enabled only for terminal writers.
func ColorEnabled(w io.Writer) bool {
	if ColorDisabled() {
		return false
	}
	if colorForced() {
		return true
	}
	if strings.TrimSpace(getenv(clicolor)) == "0" {
		return false
	}
	f, ok := w.(*os.File)
	return ok && isTerminal(f)
}

// ColorDisabled reports whether styling is explicitly disabled by the
// environment.
func ColorDisabled() bool {
	return envSet(getenv(noColor))
}

// ActivityEnabled reports whether w should receive a single-line activity
// indicator.
func ActivityEnabled(w io.Writer, jsonOut bool, getenv func(string) string, isTerminal func(*os.File) bool) bool {
	if jsonOut {
		return false
	}
	if envSet(getenv(noColor)) || envSet(getenv(gateNoIndicator)) || envSet(getenv(gateNoSpinner)) {
		return false
	}
	if ciValue := strings.ToLower(strings.TrimSpace(getenv(ci))); ciValue != "" && ciValue != "false" && ciValue != "0" {
		return false
	}
	f, ok := w.(*os.File)
	return ok && isTerminal(f)
}

// ClearColorEnv removes colour-related environment overrides.
func ClearColorEnv(setenv func(string, string)) {
	for _, key := range []string{noColor, forceColor, clicolorForce, clicolor} {
		setenv(key, "")
	}
}

// ForceColorEnv enables styled output for non-TTY writers.
func ForceColorEnv(setenv func(string, string)) {
	ClearColorEnv(setenv)
	setenv(forceColor, "1")
}

// DisableColorEnv disables styled output.
func DisableColorEnv(setenv func(string, string)) {
	ClearColorEnv(setenv)
	setenv(noColor, "1")
}

func colorForced() bool {
	return envEnabled(getenv(forceColor)) || envEnabled(getenv(clicolorForce))
}

func envSet(value string) bool {
	return strings.TrimSpace(value) != ""
}

func envEnabled(value string) bool {
	v := strings.ToLower(strings.TrimSpace(value))
	return v != "" && v != "0" && v != "false" && v != "no" && v != "off"
}

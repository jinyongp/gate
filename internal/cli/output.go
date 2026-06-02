// Package cli implements gate's command surface and its text/json output
// contract: data on stdout, diagnostics on stderr, a single JSON value under
// --json, and stable exit codes.
package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"gate/internal/paths"
	"gate/internal/registry"
	"gate/internal/ui"
)

// Exit codes (see README §13).
const (
	ExitOK       = 0
	ExitError    = 1
	ExitUsage    = 2
	ExitPerm     = 3
	ExitConflict = 4
)

func registryStore() *registry.Store {
	return registry.Open(filepath.Join(paths.ConfigDir(), "registry.json"))
}

// isTTY reports whether w is an interactive terminal and colour is permitted.
func isTTY(w io.Writer) bool { return ui.Enabled(w) }

// richOut is the single gate for styled (lipgloss) output: a real terminal,
// NO_COLOR unset, and not emitting JSON.
func richOut(w io.Writer, jsonOut bool) bool { return !jsonOut && ui.Enabled(w) }

type activityHandle interface {
	Stop()
}

var startActivityFunc = func(stderr io.Writer, jsonOut bool, label string) activityHandle {
	return ui.StartActivity(stderr, label, ui.ActivityOptions{
		Enabled: ui.ActivityEnabled(stderr, jsonOut),
	})
}

func startActivity(stderr io.Writer, jsonOut bool, label string) activityHandle {
	return startActivityFunc(stderr, jsonOut, label)
}

func writeJSON(w io.Writer, v any) int {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return ExitError
	}
	return ExitOK
}

type errBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type errEnvelope struct {
	Error errBody `json:"error"`
}

// fail reports an error: a JSON envelope on stderr under --json, else a plain
// line. It returns code so callers can `return fail(...)`.
func fail(stderr io.Writer, jsonOut bool, code int, errCode, msg string) int {
	if jsonOut {
		enc := json.NewEncoder(stderr)
		_ = enc.Encode(errEnvelope{Error: errBody{Code: errCode, Message: msg}})
	} else {
		fmt.Fprintf(stderr, "gate: %s\n", msg)
	}
	return code
}

func statusDot(status string, color bool) string {
	if !color {
		if status == "live" {
			return "* live"
		}
		return "o down"
	}
	if status == "live" {
		return ui.Tint(ui.Success, "●") + " live"
	}
	return ui.Tint(ui.Muted, "○") + " down"
}

func printCancelled(stdout io.Writer, action string) {
	msg := strings.TrimSpace(action) + " cancelled"
	if richOut(stdout, false) {
		fmt.Fprintf(stdout, "\n%s %s\n", ui.Tint(ui.Danger, "✗"), msg)
		return
	}
	fmt.Fprintf(stdout, "\n✗ %s\n", msg)
}

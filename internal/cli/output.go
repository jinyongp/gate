// Package cli implements prx's command surface and its human/JSON output
// contract: data on stdout, diagnostics on stderr, a single JSON value under
// --json, and stable exit codes.
package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"prx/internal/paths"
	"prx/internal/registry"
	"golang.org/x/term"
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
func isTTY(w io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
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
		fmt.Fprintf(stderr, "prx: %s\n", msg)
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
		return "\x1b[32m●\x1b[0m live"
	}
	return "\x1b[90m○\x1b[0m down"
}

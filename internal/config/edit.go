package config

import (
	"fmt"
	"os"
	"strings"

	"prx/internal/fsutil"
)

// ErrServiceExists is returned by AddService when the service is already present.
var ErrServiceExists = fmt.Errorf("service already exists")

// AddService appends a [services.<name>] block to the prx.toml at path while
// preserving every existing line and comment. The file is created (with a
// minimal header) if it does not exist. It never rewrites the whole document,
// so hand-written comments survive.
func AddService(path, name string, svc Service) error {
	var lines []string
	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		lines = splitLines(string(b))
		if header := headerIndex(lines, name); header >= 0 {
			return fmt.Errorf("%q: %w", name, ErrServiceExists)
		}
	case os.IsNotExist(err):
		lines = []string{"# managed by prx"}
	default:
		return err
	}

	block := renderBlock(name, svc)
	// Ensure exactly one blank line separates the new block from prior content.
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	lines = append(lines, "", block)
	return writeLines(path, lines)
}

// RemoveService removes the [services.<name>] table from prx.toml, leaving all
// other content untouched. It is a no-op (returns nil) if the service is absent.
func RemoveService(path, name string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := splitLines(string(b))
	start := headerIndex(lines, name)
	if start < 0 {
		return nil
	}
	end := nextHeaderIndex(lines, start+1)
	kept := append(append([]string{}, lines[:start]...), lines[end:]...)
	return writeLines(path, collapseBlankRuns(kept))
}

func renderBlock(name string, svc Service) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "[services.%s]\n", name)
	fmt.Fprintf(&sb, "domain = %q\n", svc.Domain)
	if svc.Port > 0 {
		fmt.Fprintf(&sb, "port = %d\n", svc.Port)
	}
	if svc.TLS != "" && svc.TLS != TLSInternal {
		fmt.Fprintf(&sb, "tls = %q\n", svc.TLS)
	}
	if svc.ACMEDNS != "" {
		fmt.Fprintf(&sb, "acme_dns = %q\n", svc.ACMEDNS)
	}
	return strings.TrimRight(sb.String(), "\n")
}

// headerIndex returns the index of the `[services.<name>]` header line, or -1.
func headerIndex(lines []string, name string) int {
	want := "[services." + name + "]"
	for i, ln := range lines {
		if strings.TrimSpace(ln) == want {
			return i
		}
	}
	return -1
}

// nextHeaderIndex returns the index of the next TOML table header at or after
// from, or len(lines) if none.
func nextHeaderIndex(lines []string, from int) int {
	for i := from; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if strings.HasPrefix(t, "[") {
			return i
		}
	}
	return len(lines)
}

func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func collapseBlankRuns(lines []string) []string {
	out := make([]string, 0, len(lines))
	prevBlank := false
	for _, ln := range lines {
		blank := strings.TrimSpace(ln) == ""
		if blank && prevBlank {
			continue
		}
		out = append(out, ln)
		prevBlank = blank
	}
	return out
}

func writeLines(path string, lines []string) error {
	content := strings.Join(lines, "\n") + "\n"
	return fsutil.WriteAtomic(path, []byte(content), 0o644)
}

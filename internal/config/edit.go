package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"gate/internal/fsutil"
)

// ErrServiceExists is returned by AddService when the service is already present.
var ErrServiceExists = fmt.Errorf("service already exists")

var serviceNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// AddService appends a [services.<name>] block to the gate.toml at path while
// preserving every existing line and comment. The file is created (with a
// minimal header) if it does not exist. It never rewrites the whole document,
// so hand-written comments survive.
func AddService(path, name string, svc Service) error {
	svc.Domain = CanonicalDomain(svc.Domain)
	svc.Host = CanonicalHost(svc.Host)
	if svc.TLS == "" {
		svc.TLS = TLSInternal
	}
	if err := validateEdit(path, name, svc); err != nil {
		return err
	}
	var lines []string
	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		lines = splitLines(string(b))
		if header := headerIndex(lines, name); header >= 0 {
			return fmt.Errorf("%q: %w", name, ErrServiceExists)
		}
	case os.IsNotExist(err):
		lines = []string{"# managed by gate"}
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

// UpsertService adds or replaces the [services.<name>] table in gate.toml.
func UpsertService(path, name string, svc Service) error {
	svc.Domain = CanonicalDomain(svc.Domain)
	svc.Host = CanonicalHost(svc.Host)
	if svc.TLS == "" {
		svc.TLS = TLSInternal
	}
	if err := ValidateServiceName(name); err != nil {
		return err
	}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return AddService(path, name, svc)
	}
	if err != nil {
		return err
	}
	lines := splitLines(string(b))
	start := headerIndex(lines, name)
	if start < 0 {
		return AddService(path, name, svc)
	}
	end := nextHeaderIndex(lines, start+1)
	block := upsertServiceBlock(lines[start:end], svc)
	lines = append(append(append([]string{}, lines[:start]...), block...), lines[end:]...)
	content := strings.Join(lines, "\n") + "\n"
	if _, err := parse(path, []byte(content)); err != nil {
		return err
	}
	return fsutil.WriteAtomic(path, []byte(content), 0o644)
}

// RemoveService removes the [services.<name>] table from gate.toml, leaving all
// other content untouched. It is a no-op (returns nil) if the service is absent.
func RemoveService(path, name string) error {
	if !serviceNameRe.MatchString(name) {
		return fmt.Errorf("invalid service name %q", name)
	}
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

func validateEdit(path, name string, svc Service) error {
	if err := ValidateServiceName(name); err != nil {
		return err
	}
	if b, err := os.ReadFile(path); err == nil {
		lines := splitLines(string(b))
		if header := headerIndex(lines, name); header >= 0 {
			return fmt.Errorf("%q: %w", name, ErrServiceExists)
		}
		for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
			lines = lines[:len(lines)-1]
		}
		lines = append(lines, "", renderBlock(name, svc))
		_, err = parse(path, []byte(strings.Join(lines, "\n")+"\n"))
		return err
	}
	if svc.Domain == "" {
		return fmt.Errorf("service %q: domain is required when creating a config without project base", name)
	}
	p := &Project{Name: "edit", Services: map[string]Service{name: svc}}
	return p.Validate()
}

func ValidateServiceName(name string) error {
	if !serviceNameRe.MatchString(name) {
		return fmt.Errorf("invalid service name %q", name)
	}
	if IsReservedServiceName(name) {
		return fmt.Errorf("reserved service name %q", name)
	}
	return nil
}

func IsReservedServiceName(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "ls", "stop":
		return true
	default:
		return false
	}
}

func upsertServiceBlock(block []string, svc Service) []string {
	out := append([]string{}, block...)
	switch {
	case svc.Domain != "":
		out = removeTomlKey(out, "host")
		out = upsertTomlScalar(out, "domain", fmt.Sprintf("%q", svc.Domain))
	case svc.Host != "":
		out = removeTomlKey(out, "domain")
		out = upsertTomlScalar(out, "host", fmt.Sprintf("%q", svc.Host))
	default:
		out = removeTomlKey(removeTomlKey(out, "domain"), "host")
	}
	if svc.Port > 0 {
		out = upsertTomlScalar(out, "port", fmt.Sprintf("%d", svc.Port))
	}
	if svc.TLS != "" && svc.TLS != TLSInternal {
		out = upsertTomlScalar(out, "tls", fmt.Sprintf("%q", svc.TLS))
	}
	return out
}

func removeTomlKey(lines []string, key string) []string {
	out := lines[:0]
	prefix := key + " "
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, prefix) {
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))
			if strings.HasPrefix(rest, "=") {
				continue
			}
		}
		out = append(out, line)
	}
	return out
}

func upsertTomlScalar(lines []string, key, value string) []string {
	prefix := key + " "
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, prefix) {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))
		if !strings.HasPrefix(rest, "=") {
			continue
		}
		lines[i] = key + " = " + value
		return lines
	}
	return append(lines, key+" = "+value)
}

func renderBlock(name string, svc Service) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "[services.%s]\n", name)
	if svc.Domain != "" {
		fmt.Fprintf(&sb, "domain = %q\n", svc.Domain)
	} else if svc.Host != "" {
		fmt.Fprintf(&sb, "host = %q\n", svc.Host)
	}
	if svc.Port > 0 {
		fmt.Fprintf(&sb, "port = %d\n", svc.Port)
	}
	if len(svc.Env) == 1 {
		fmt.Fprintf(&sb, "env = %q\n", svc.Env[0])
	} else if len(svc.Env) > 1 {
		fmt.Fprintf(&sb, "env = [%s]\n", quoteStringList(svc.Env))
	}
	if len(svc.RouteEnv) == 1 {
		fmt.Fprintf(&sb, "route_env = %q\n", svc.RouteEnv[0])
	} else if len(svc.RouteEnv) > 1 {
		fmt.Fprintf(&sb, "route_env = [%s]\n", quoteStringList(svc.RouteEnv))
	}
	if svc.TLS != "" && svc.TLS != TLSInternal {
		fmt.Fprintf(&sb, "tls = %q\n", svc.TLS)
	}
	return strings.TrimRight(sb.String(), "\n")
}

func quoteStringList(values []string) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, fmt.Sprintf("%q", value))
	}
	return strings.Join(parts, ", ")
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

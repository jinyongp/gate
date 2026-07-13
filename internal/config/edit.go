package config

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"gate/internal/fsutil"

	toml "github.com/pelletier/go-toml/v2"
)

// ErrServiceExists is returned by AddService when the service is already present.
var ErrServiceExists = fmt.Errorf("service already exists")

var serviceNameRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_-]*$`)

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
	doc, err := readEditDocument(path)
	switch {
	case err == nil:
		if header := headerIndex(doc.lines, name); header >= 0 {
			return fmt.Errorf("%q: %w", name, ErrServiceExists)
		}
	case os.IsNotExist(err):
		doc = editDocument{lines: []string{"# managed by gate"}, newline: "\n", finalNewline: true, perm: 0o644}
	default:
		return err
	}

	// Ensure exactly one blank line separates the new block from prior content.
	for len(doc.lines) > 0 && strings.TrimSpace(doc.lines[len(doc.lines)-1]) == "" {
		doc.lines = doc.lines[:len(doc.lines)-1]
	}
	doc.lines = append(doc.lines, "")
	doc.lines = append(doc.lines, strings.Split(renderBlock(name, svc), "\n")...)
	doc.finalNewline = true
	return doc.write(path)
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
	doc, err := readEditDocument(path)
	if os.IsNotExist(err) {
		return AddService(path, name, svc)
	}
	if err != nil {
		return err
	}
	lines := doc.lines
	start := headerIndex(lines, name)
	if start < 0 {
		return AddService(path, name, svc)
	}
	end := nextHeaderIndex(lines, start+1)
	block := upsertServiceBlock(lines[start:end], svc)
	lines = append(append(append([]string{}, lines[:start]...), block...), lines[end:]...)
	content := doc.render(lines)
	if _, err := parse(path, []byte(content)); err != nil {
		return err
	}
	doc.lines = lines
	return doc.write(path)
}

// RemoveService removes the [services.<name>] table from gate.toml, leaving all
// other content untouched. It is a no-op (returns nil) if the service is absent.
func RemoveService(path, name string) error {
	if !serviceNameRe.MatchString(name) {
		return fmt.Errorf("invalid service name %q", name)
	}
	doc, err := readEditDocument(path)
	if err != nil {
		return err
	}
	lines := doc.lines
	start := headerIndex(lines, name)
	if start < 0 {
		return nil
	}
	end := nextHeaderIndex(lines, start+1)
	removeEnd := serviceRemovalEnd(lines, start, end)
	kept := append(append([]string{}, lines[:start]...), lines[removeEnd:]...)
	doc.lines = kept
	return doc.write(path)
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
	for _, line := range lines {
		if tomlAssignmentIndex(line, key) >= 0 {
			continue
		}
		out = append(out, line)
	}
	return out
}

func upsertTomlScalar(lines []string, key, value string) []string {
	for i, line := range lines {
		if tomlAssignmentIndex(line, key) < 0 {
			continue
		}
		lines[i] = replaceTomlScalarValue(line, value)
		return lines
	}
	return append(lines, key+" = "+value)
}

func tomlAssignmentIndex(line, key string) int {
	start := len(line) - len(strings.TrimLeft(line, " \t"))
	if !strings.HasPrefix(line[start:], key) {
		return -1
	}
	index := start + len(key)
	if index < len(line) && line[index] != ' ' && line[index] != '\t' && line[index] != '=' {
		return -1
	}
	for index < len(line) && (line[index] == ' ' || line[index] == '\t') {
		index++
	}
	if index >= len(line) || line[index] != '=' {
		return -1
	}
	return index
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

// headerIndex returns the index of the service table header line, or -1.
func headerIndex(lines []string, name string) int {
	headers := tomlHeaderLines(lines)
	for i, ln := range lines {
		if !headers[i] {
			continue
		}
		if got, ok := serviceHeaderName(ln); ok && got == name {
			return i
		}
	}
	return -1
}

// nextHeaderIndex returns the index of the next TOML table header at or after
// from, or len(lines) if none.
func nextHeaderIndex(lines []string, from int) int {
	headers := tomlHeaderLines(lines)
	for i := from; i < len(lines); i++ {
		if headers[i] {
			return i
		}
	}
	return len(lines)
}

type tomlLexState uint8

const (
	tomlNormal tomlLexState = iota
	tomlBasicString
	tomlLiteralString
	tomlMultilineBasic
	tomlMultilineLiteral
)

func tomlHeaderLines(lines []string) map[int]bool {
	headers := make(map[int]bool)
	state := tomlNormal
	for index, line := range lines {
		if state == tomlNormal && strings.HasPrefix(strings.TrimSpace(line), "[") {
			headers[index] = true
		}
		state = scanTOMLLine(line, state)
		if state == tomlBasicString || state == tomlLiteralString {
			state = tomlNormal
		}
	}
	return headers
}

func scanTOMLLine(line string, state tomlLexState) tomlLexState {
	for i := 0; i < len(line); {
		switch state {
		case tomlNormal:
			switch line[i] {
			case '#':
				return state
			case '"':
				if strings.HasPrefix(line[i:], `"""`) {
					state = tomlMultilineBasic
					i += 3
				} else {
					state = tomlBasicString
					i++
				}
			case '\'':
				if strings.HasPrefix(line[i:], `'''`) {
					state = tomlMultilineLiteral
					i += 3
				} else {
					state = tomlLiteralString
					i++
				}
			default:
				i++
			}
		case tomlBasicString:
			switch {
			case line[i] == '\\' && i+1 < len(line):
				i += 2
			case line[i] == '"':
				state = tomlNormal
				i++
			default:
				i++
			}
		case tomlLiteralString:
			if line[i] == '\'' {
				state = tomlNormal
			}
			i++
		case tomlMultilineBasic:
			switch {
			case line[i] == '\\' && i+1 < len(line):
				i += 2
			case strings.HasPrefix(line[i:], `"""`):
				state = tomlNormal
				i += 3
			default:
				i++
			}
		case tomlMultilineLiteral:
			if strings.HasPrefix(line[i:], `'''`) {
				state = tomlNormal
				i += 3
			} else {
				i++
			}
		}
	}
	return state
}

func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func serviceHeaderName(line string) (string, bool) {
	var decoded map[string]any
	probe := line + "\n__gate_service_header_probe = true\n"
	if err := toml.Unmarshal([]byte(probe), &decoded); err != nil {
		return "", false
	}
	services, ok := decoded["services"].(map[string]any)
	if !ok {
		return "", false
	}
	for name, raw := range services {
		table, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if value, ok := table["__gate_service_header_probe"].(bool); ok && value {
			return name, true
		}
	}
	return "", false
}

func replaceTomlScalarValue(line, value string) string {
	eq := strings.IndexByte(line, '=')
	if eq < 0 {
		return line
	}
	rhs := line[eq+1:]
	leading := len(rhs) - len(strings.TrimLeft(rhs, " \t"))
	comment := tomlCommentIndex(rhs)
	valueEnd := len(rhs)
	if comment >= 0 {
		valueEnd = comment
	}
	valuePart := rhs[leading:valueEnd]
	trailing := len(valuePart) - len(strings.TrimRight(valuePart, " \t"))
	suffixStart := valueEnd - trailing
	return line[:eq+1] + rhs[:leading] + value + rhs[suffixStart:]
}

func tomlCommentIndex(value string) int {
	var quote rune
	escaped := false
	for i, r := range value {
		if escaped {
			escaped = false
			continue
		}
		if quote == '"' && r == '\\' {
			escaped = true
			continue
		}
		switch quote {
		case 0:
			if r == '\'' || r == '"' {
				quote = r
				continue
			}
		case r:
			quote = 0
			continue
		}
		if r == '#' && quote == 0 {
			return i
		}
	}
	return -1
}

func serviceRemovalEnd(lines []string, start, end int) int {
	removeEnd := end
	for removeEnd > start+1 {
		trimmed := strings.TrimSpace(lines[removeEnd-1])
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			break
		}
		removeEnd--
	}
	return removeEnd
}

type editDocument struct {
	lines        []string
	newline      string
	finalNewline bool
	perm         os.FileMode
	target       string
}

func readEditDocument(path string) (editDocument, error) {
	target, err := ResolveFileTarget(path)
	if err != nil {
		return editDocument{}, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return editDocument{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return editDocument{}, err
	}
	raw := string(b)
	hasCRLF := strings.Contains(raw, "\r\n")
	hasBareLF := strings.Contains(strings.ReplaceAll(raw, "\r\n", ""), "\n")
	if hasCRLF && hasBareLF {
		return editDocument{}, errors.New("mixed line endings are not supported")
	}
	newline := "\n"
	if hasCRLF {
		newline = "\r\n"
	}
	text := strings.ReplaceAll(raw, "\r\n", "\n")
	finalNewline := strings.HasSuffix(text, "\n")
	text = strings.TrimSuffix(text, "\n")
	var lines []string
	if text != "" {
		lines = strings.Split(text, "\n")
	}
	return editDocument{lines: lines, newline: newline, finalNewline: finalNewline, perm: info.Mode().Perm(), target: target}, nil
}

func (d editDocument) render(lines []string) string {
	content := strings.Join(lines, d.newline)
	if d.finalNewline {
		content += d.newline
	}
	return content
}

func (d editDocument) write(path string) error {
	if d.target != "" {
		path = d.target
	}
	return fsutil.WriteAtomic(path, []byte(d.render(d.lines)), d.perm)
}

// Package config loads and edits the per-project gate.toml. The TOML is the
// single source of truth for a project; it is parsed for reading and edited
// surgically (comment-preserving) for writing — see edit.go.
package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	toml "github.com/pelletier/go-toml/v2"
)

// Filename is the fixed project config name.
const Filename = "gate.toml"

// TLS modes.
const (
	TLSInternal = "internal"
)

// ErrNotFound is returned by Discover when no gate.toml is found within bounds.
var ErrNotFound = errors.New("gate.toml not found")

// Service is a single route → port mapping within a project. Domain is the
// resolved domain after loading; Host is used when editing config before a
// base-derived service has been resolved.
type Service struct {
	Domain   string   `toml:"domain"`
	Host     string   `toml:"host,omitempty"`
	Port     int      `toml:"port,omitempty"` // 0 = auto-allocate
	TLS      string   `toml:"tls,omitempty"`  // internal only; omitted defaults to internal
	Env      []string `toml:"env,omitempty"`
	RouteEnv []string `toml:"route_env,omitempty"`
}

// Project is the decoded gate.toml.
type Project struct {
	Name     string
	Base     string
	EnvFiles []string
	Services map[string]Service
}

// file mirrors the on-disk TOML structure for decoding.
type file struct {
	Project struct {
		Name     string   `toml:"name"`
		Base     string   `toml:"base"`
		EnvFiles []string `toml:"env_files"`
	} `toml:"project"`
	Services map[string]rawService `toml:"services"`
}

type rawService struct {
	Domain             *string `toml:"domain"`
	Host               *string `toml:"host"`
	Port               any     `toml:"port,omitempty"`
	TLS                string  `toml:"tls,omitempty"`
	Env                any     `toml:"env,omitempty"`
	RouteEnv           any     `toml:"route_env,omitempty"`
	UnsupportedACMEDNS *string `toml:"acme_dns,omitempty"`
}

var envKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Load reads and validates the gate.toml at path.
func Load(path string) (*Project, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parse(path, b)
}

func parse(path string, b []byte) (*Project, error) {
	var f file
	if err := toml.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	env, err := loadEnvFiles(filepath.Dir(path), f.Project.EnvFiles)
	if err != nil {
		return nil, err
	}
	base, err := expandEnvRefs(f.Project.Base, env, "project base")
	if err != nil {
		return nil, err
	}
	p := &Project{Name: f.Project.Name, Base: CanonicalDomain(base), EnvFiles: f.Project.EnvFiles, Services: map[string]Service{}}
	if p.Services == nil {
		p.Services = map[string]Service{}
	}
	for name, raw := range f.Services {
		domain := ""
		if raw.Domain != nil {
			domain, err = expandEnvRefs(*raw.Domain, env, fmt.Sprintf("service %q domain", name))
			if err != nil {
				return nil, err
			}
			domain = CanonicalDomain(domain)
		}
		host := ""
		if raw.Host != nil {
			host, err = expandEnvRefs(*raw.Host, env, fmt.Sprintf("service %q host", name))
			if err != nil {
				return nil, err
			}
			host = CanonicalHost(host)
		}
		port, err := parsePort(raw.Port, env, name)
		if err != nil {
			return nil, err
		}
		envNames, err := parseServiceEnv(raw.Env, name, "env")
		if err != nil {
			return nil, err
		}
		routeEnvNames, err := parseServiceEnv(raw.RouteEnv, name, "route_env")
		if err != nil {
			return nil, err
		}
		if raw.UnsupportedACMEDNS != nil {
			return nil, fmt.Errorf("service %q: acme_dns is not supported", name)
		}
		svc := Service{
			Domain:   domain,
			Host:     host,
			Port:     port,
			TLS:      raw.TLS,
			Env:      envNames,
			RouteEnv: routeEnvNames,
		}
		if svc.TLS == "" {
			svc.TLS = TLSInternal
		}
		p.Services[name] = svc
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	for name, svc := range p.Services {
		domain, err := p.ServiceDomain(name)
		if err != nil {
			return nil, err
		}
		svc.Domain = domain
		svc.Host = ""
		p.Services[name] = svc
	}
	return p, nil
}

func loadEnvFiles(baseDir string, files []string) (map[string]string, error) {
	env := map[string]string{}
	for _, pair := range os.Environ() {
		key, value, ok := strings.Cut(pair, "=")
		if ok {
			env[key] = value
		}
	}
	for _, name := range files {
		path := name
		if !filepath.IsAbs(path) {
			path = filepath.Join(baseDir, name)
		}
		values, err := readDotenv(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		for key, value := range values {
			if _, exists := env[key]; !exists {
				env[key] = value
			}
		}
	}
	return env, nil
}

func readDotenv(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read env file %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	values := map[string]string{}
	scanner := bufio.NewScanner(f)
	for lineNo := 1; scanner.Scan(); lineNo++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("%s:%d: expected KEY=VALUE", path, lineNo)
		}
		key = strings.TrimSpace(key)
		if !envKeyRe.MatchString(key) {
			return nil, fmt.Errorf("%s:%d: invalid env key %q", path, lineNo, key)
		}
		parsed, err := parseDotenvValue(strings.TrimSpace(value))
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, lineNo, err)
		}
		values[key] = parsed
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read env file %s: %w", path, err)
	}
	return values, nil
}

func parseDotenvValue(value string) (string, error) {
	switch {
	case strings.HasPrefix(value, "'"):
		if !strings.HasSuffix(value, "'") || len(value) == 1 {
			return "", errors.New("unterminated single-quoted value")
		}
		return strings.TrimSuffix(strings.TrimPrefix(value, "'"), "'"), nil
	case strings.HasPrefix(value, "\""):
		if !strings.HasSuffix(value, "\"") || len(value) == 1 {
			return "", errors.New("unterminated double-quoted value")
		}
		parsed, err := strconv.Unquote(value)
		if err != nil {
			return "", fmt.Errorf("invalid double-quoted value: %w", err)
		}
		return parsed, nil
	default:
		return stripDotenvComment(value), nil
	}
}

func stripDotenvComment(value string) string {
	for i, r := range value {
		if r == '#' && (i == 0 || unicode.IsSpace(rune(value[i-1]))) {
			return strings.TrimSpace(value[:i])
		}
	}
	return strings.TrimSpace(value)
}

func parsePort(raw any, env map[string]string, serviceName string) (int, error) {
	switch v := raw.(type) {
	case nil:
		return 0, nil
	case int:
		return v, nil
	case int64:
		return int(v), nil
	case string:
		expanded, err := expandEnvRefs(v, env, fmt.Sprintf("service %q port", serviceName))
		if err != nil {
			return 0, err
		}
		expanded = strings.TrimSpace(expanded)
		if expanded == "" {
			return 0, nil
		}
		port, err := strconv.Atoi(expanded)
		if err != nil {
			return 0, fmt.Errorf("service %q: invalid port %q", serviceName, expanded)
		}
		return port, nil
	default:
		return 0, fmt.Errorf("service %q: port must be an integer or env string", serviceName)
	}
}

func parseServiceEnv(raw any, serviceName, field string) ([]string, error) {
	switch v := raw.(type) {
	case nil:
		return nil, nil
	case string:
		return []string{strings.TrimSpace(v)}, nil
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("service %q: %s entries must be strings", serviceName, field)
			}
			out = append(out, strings.TrimSpace(s))
		}
		return out, nil
	case []string:
		out := make([]string, 0, len(v))
		for _, item := range v {
			out = append(out, strings.TrimSpace(item))
		}
		return out, nil
	default:
		return nil, fmt.Errorf("service %q: %s must be a string or list of strings", serviceName, field)
	}
}

func expandEnvRefs(value string, env map[string]string, context string) (string, error) {
	var out strings.Builder
	for {
		start := strings.Index(value, "${")
		if start == -1 {
			out.WriteString(value)
			return out.String(), nil
		}
		out.WriteString(value[:start])
		rest := value[start+2:]
		end := strings.IndexByte(rest, '}')
		if end == -1 {
			return "", fmt.Errorf("%s: unterminated env reference", context)
		}
		expanded, err := expandEnvRef(rest[:end], env, context)
		if err != nil {
			return "", err
		}
		out.WriteString(expanded)
		value = rest[end+1:]
	}
}

func expandEnvRef(expr string, env map[string]string, context string) (string, error) {
	key, fallback, hasFallback := strings.Cut(expr, ":-")
	if !envKeyRe.MatchString(key) {
		return "", fmt.Errorf("%s: invalid env reference %q", context, expr)
	}
	value, ok := env[key]
	if hasFallback && (!ok || value == "") {
		return fallback, nil
	}
	if !ok {
		return "", fmt.Errorf("%s: env %s is not set", context, key)
	}
	return value, nil
}

// CanonicalDomain returns the case-insensitive DNS identity gate uses for config,
// registry, proxy lookup and certificate cache keys.
func CanonicalDomain(domain string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")
}

// CanonicalHost returns the case-insensitive service host label used for
// base-derived domains.
func CanonicalHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "." {
		return host
	}
	return strings.TrimSuffix(host, ".")
}

// ValidateDomain checks whether domain is a syntactically valid gate hostname.
func ValidateDomain(domain string) error {
	domain = CanonicalDomain(domain)
	if domain == "" || len(domain) > 253 {
		return fmt.Errorf("invalid domain %q", domain)
	}
	for _, label := range strings.Split(domain, ".") {
		if !validDomainLabel(label) {
			return fmt.Errorf("invalid domain %q", domain)
		}
	}
	return nil
}

func validDomainLabel(label string) bool {
	if label == "" || len(label) > 63 {
		return false
	}
	for i, r := range label {
		valid := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-'
		if !valid {
			return false
		}
		if (i == 0 || i == len(label)-1) && r == '-' {
			return false
		}
	}
	return true
}

// ServiceDomain returns the resolved route domain for service name.
func (p *Project) ServiceDomain(name string) (string, error) {
	svc, ok := p.Services[name]
	if !ok {
		return "", fmt.Errorf("service %q: not found", name)
	}
	if svc.Domain != "" {
		domain := CanonicalDomain(svc.Domain)
		if err := ValidateDomain(domain); err != nil {
			return "", err
		}
		return domain, nil
	}
	base := CanonicalDomain(p.Base)
	if base == "" {
		return "", errors.New("domain is required when project base is not set")
	}
	host := svc.Host
	if host == "" {
		host = CanonicalHost(name)
	}
	if host == "." {
		if err := ValidateDomain(base); err != nil {
			return "", err
		}
		return base, nil
	}
	host = CanonicalHost(host)
	if strings.Contains(host, ".") || !validDomainLabel(host) {
		return "", fmt.Errorf("invalid host %q", host)
	}
	domain := host + "." + base
	if err := ValidateDomain(domain); err != nil {
		return "", err
	}
	return domain, nil
}

// EnvServiceKey returns the normalized service fragment used in gate-owned env
// names, e.g. "admin-web" -> "ADMIN_WEB".
func EnvServiceKey(name string) string {
	name = strings.ToUpper(strings.TrimSpace(name))
	var b strings.Builder
	for _, r := range name {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('_')
	}
	return b.String()
}

// Validate checks the project for structural and semantic errors.
func (p *Project) Validate() error {
	if strings.TrimSpace(p.Name) == "" {
		return errors.New("project name must not be empty")
	}
	if strings.Contains(p.Name, "/") {
		return errors.New("project name must not contain /")
	}
	if p.Base != "" {
		if err := ValidateDomain(p.Base); err != nil {
			return fmt.Errorf("project base: %w", err)
		}
	}
	envPublishers := map[string]envPublisher{}
	envServiceKeys := map[string]string{}
	for name, svc := range p.Services {
		if strings.TrimSpace(name) == "" {
			return errors.New("service name must not be empty")
		}
		if IsReservedServiceName(name) {
			return fmt.Errorf("service %q: reserved service name", name)
		}
		if strings.Contains(name, "/") {
			return fmt.Errorf("service %q: name must not contain /", name)
		}
		if svc.Domain != "" && svc.Host != "" {
			return fmt.Errorf("service %q: host and domain are mutually exclusive", name)
		}
		if _, err := p.ServiceDomain(name); err != nil {
			return fmt.Errorf("service %q: %w", name, err)
		}
		switch svc.TLS {
		case "acme":
			return fmt.Errorf("service %q: tls acme is not supported", name)
		case TLSInternal:
		default:
			return fmt.Errorf("service %q: invalid tls %q", name, svc.TLS)
		}
		if svc.Port < 0 || svc.Port > 65535 {
			return fmt.Errorf("service %q: port %d out of range", name, svc.Port)
		}
		envServiceKey := EnvServiceKey(name)
		if prev, ok := envServiceKeys[envServiceKey]; ok {
			return fmt.Errorf("services %q and %q derive the same gate env key %q", prev, name, envServiceKey)
		}
		envServiceKeys[envServiceKey] = name
		for _, envName := range svc.Env {
			if err := publishEnvName(envPublishers, name, "env", envName); err != nil {
				return err
			}
		}
		for _, envName := range svc.RouteEnv {
			if err := publishEnvName(envPublishers, name, "route_env", envName); err != nil {
				return err
			}
		}
	}
	return nil
}

type envPublisher struct {
	service string
	field   string
}

func publishEnvName(publishers map[string]envPublisher, serviceName, field, envName string) error {
	envName = strings.TrimSpace(envName)
	if !envKeyRe.MatchString(envName) {
		return fmt.Errorf("service %q: invalid %s name %q", serviceName, field, envName)
	}
	if strings.HasPrefix(envName, "GATE_") {
		return fmt.Errorf("service %q: %s name %q uses reserved GATE_ prefix", serviceName, field, envName)
	}
	if prev, ok := publishers[envName]; ok {
		if prev.service == serviceName {
			return fmt.Errorf("service %q publishes env %q from both %s and %s", serviceName, envName, prev.field, field)
		}
		return fmt.Errorf("services %q and %q both publish env %q", prev.service, serviceName, envName)
	}
	publishers[envName] = envPublisher{service: serviceName, field: field}
	return nil
}

// Discover walks upward from start looking for gate.toml. The search stops
// after the first git root (a directory containing .git) or $HOME or the
// filesystem root, whichever comes first. Sibling directories are not searched.
func Discover(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	home, _ := os.UserHomeDir()
	for {
		candidate := filepath.Join(dir, Filename)
		if isFile(candidate) {
			return candidate, nil
		}
		if isDir(filepath.Join(dir, ".git")) || dir == home {
			return "", ErrNotFound
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", ErrNotFound
		}
		dir = parent
	}
}

func isFile(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.Mode().IsRegular()
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

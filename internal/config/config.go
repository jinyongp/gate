// Package config loads and edits the per-project prx.toml. The TOML is the
// single source of truth for a project; it is parsed for reading and edited
// surgically (comment-preserving) for writing — see edit.go.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

// Filename is the fixed project config name.
const Filename = "prx.toml"

// TLS modes.
const (
	TLSInternal = "internal"
	TLSACME     = "acme"
)

// ErrNotFound is returned by Discover when no prx.toml is found within bounds.
var ErrNotFound = errors.New("prx.toml not found")

// Service is a single domain → port mapping within a project.
type Service struct {
	Domain  string `toml:"domain"`
	Port    int    `toml:"port,omitempty"` // 0 = auto-allocate
	TLS     string `toml:"tls,omitempty"`  // internal (default) | acme
	ACMEDNS string `toml:"acme_dns,omitempty"`
}

// Project is the decoded prx.toml.
type Project struct {
	Name     string
	Services map[string]Service
}

// file mirrors the on-disk TOML structure for decoding.
type file struct {
	Project struct {
		Name string `toml:"name"`
	} `toml:"project"`
	Services map[string]Service `toml:"services"`
}

var domainRe = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9._-]*[a-zA-Z0-9])?$`)

// Load reads and validates the prx.toml at path.
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
	p := &Project{Name: f.Project.Name, Services: f.Services}
	if p.Services == nil {
		p.Services = map[string]Service{}
	}
	for name, svc := range p.Services {
		if svc.TLS == "" {
			svc.TLS = TLSInternal
		}
		svc.Domain = CanonicalDomain(svc.Domain)
		p.Services[name] = svc
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return p, nil
}

// CanonicalDomain returns the case-insensitive DNS identity prx uses for config,
// registry, proxy lookup and certificate cache keys.
func CanonicalDomain(domain string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")
}

// Validate checks the project for structural and semantic errors.
func (p *Project) Validate() error {
	for name, svc := range p.Services {
		if name == "" {
			return errors.New("service name must not be empty")
		}
		if !domainRe.MatchString(svc.Domain) {
			return fmt.Errorf("service %q: invalid domain %q", name, svc.Domain)
		}
		switch svc.TLS {
		case TLSInternal:
		case TLSACME:
			if svc.ACMEDNS == "" {
				return fmt.Errorf("service %q: tls=acme requires acme_dns", name)
			}
		default:
			return fmt.Errorf("service %q: invalid tls %q", name, svc.TLS)
		}
		if svc.Port < 0 || svc.Port > 65535 {
			return fmt.Errorf("service %q: port %d out of range", name, svc.Port)
		}
	}
	return nil
}

// Discover walks upward from start looking for prx.toml. The search stops
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

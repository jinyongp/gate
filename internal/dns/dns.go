// Package dns makes a domain resolve to 127.0.0.1. Two providers: localhost
// (no-op, for *.localhost which the OS resolves itself) and hosts (edits a
// managed block in /etc/hosts, which needs sudo).
package dns

import "strings"

// Provider ensures or removes a domain's loopback resolution.
type Provider interface {
	Ensure(domain string) error
	Remove(domain string) error
}

// Mode names.
const (
	ModeLocalhost = "localhost"
	ModeHosts     = "hosts"
)

// Select returns the provider for a domain. override is "localhost", "hosts",
// or "" for automatic detection (".localhost" suffix → localhost, else hosts).
func Select(domain, override string) Provider {
	switch override {
	case ModeLocalhost:
		return Localhost{}
	case ModeHosts:
		return DefaultHosts()
	}
	if strings.HasSuffix(domain, ".localhost") {
		return Localhost{}
	}
	return DefaultHosts()
}

// ModeFor reports which mode Select would choose (for display/logging).
func ModeFor(domain, override string) string {
	if override != "" {
		return override
	}
	if strings.HasSuffix(domain, ".localhost") {
		return ModeLocalhost
	}
	return ModeHosts
}

// Localhost is a no-op provider: *.localhost resolves to 127.0.0.1 without any
// /etc/hosts change, so no privileges are required.
type Localhost struct{}

// Ensure does nothing.
func (Localhost) Ensure(string) error { return nil }

// Remove does nothing.
func (Localhost) Remove(string) error { return nil }

// Package registry persists the machine-wide domain↔port↔service reservations.
// The registry is tool-owned (users use the CLI, not the file). Concurrent
// access is serialised with an advisory file lock and written atomically; see
// store.go. This file holds the schema and the pure, IO-free operations.
package registry

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"gate/internal/listener"
)

// SchemaVersion is the current on-disk schema version.
const SchemaVersion = 2

// ListenerTarget records a non-default front listener for a reservation.
type ListenerTarget struct {
	HTTPSAddr string `json:"https_addr"`
	HTTPAddr  string `json:"http_addr"`
}

// Reservation is a persisted service↔port binding.
type Reservation struct {
	Project    string          `json:"project"`
	Service    string          `json:"service"`
	Domain     string          `json:"domain"`
	Port       int             `json:"port"`
	TLS        string          `json:"tls,omitempty"`
	DNS        string          `json:"dns,omitempty"`
	Standalone bool            `json:"standalone,omitempty"`
	Active     bool            `json:"active,omitempty"`      // true while routed; reservation persists when false
	ConfigPath string          `json:"config_path,omitempty"` // gate.toml that owns this reservation; enables GC
	Listener   *ListenerTarget `json:"listener,omitempty"`
}

// UnmarshalJSON accepts the pre-standalone development-build `adhoc` flag while
// keeping the current schema write-only as `standalone`.
func (r *Reservation) UnmarshalJSON(data []byte) error {
	type reservation Reservation
	var raw struct {
		reservation
		Adhoc bool `json:"adhoc,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*r = Reservation(raw.reservation)
	if raw.Adhoc {
		r.Standalone = true
	}
	return nil
}

// Registry is the whole on-disk document.
type Registry struct {
	Version  int                    `json:"version"`
	Services map[string]Reservation `json:"services"`
}

// UnsupportedSchemaError means the registry was written by a newer gate
// version. Older binaries must not rewrite it.
type UnsupportedSchemaError struct {
	Version   int
	Supported int
}

func (e *UnsupportedSchemaError) Error() string {
	return fmt.Sprintf("registry schema version %d is newer than supported version %d", e.Version, e.Supported)
}

// IntegrityIssue describes a malformed registry reservation discovered at the
// storage boundary.
type IntegrityIssue struct {
	Code    string
	Key     string
	Message string
}

// IntegrityError reports one or more registry invariant violations.
type IntegrityError struct {
	Issues []IntegrityIssue
}

func (e *IntegrityError) Error() string {
	if len(e.Issues) == 0 {
		return "registry integrity error"
	}
	return e.Issues[0].Message
}

func (e *IntegrityError) Is(target error) bool {
	_, ok := target.(*IntegrityError)
	return ok
}

// ErrIntegrity is a sentinel for errors.Is checks.
var ErrIntegrity = &IntegrityError{}

// Key is the registry map key for a (project, service) pair.
func Key(project, service string) string {
	return project + "/" + service
}

// New returns an empty registry stamped with the current schema version.
func New() *Registry {
	return &Registry{Version: SchemaVersion, Services: map[string]Reservation{}}
}

// ConflictError reports that a domain or port is already taken by another key.
type ConflictError struct {
	Domain   string
	Port     int
	OwnerKey string
}

func (e *ConflictError) Error() string {
	if e.Domain != "" {
		return fmt.Sprintf("domain %q already reserved by %s", e.Domain, e.OwnerKey)
	}
	return fmt.Sprintf("port %d already reserved by %s", e.Port, e.OwnerKey)
}

// Reserve inserts or updates res. The domain and port must be globally unique
// across all other keys; otherwise a *ConflictError naming the owner is returned.
func (r *Registry) Reserve(res Reservation) error {
	res.Domain = canonicalDomain(res.Domain)
	res.SetListenerPair(res.ListenerPair())
	if strings.TrimSpace(res.Service) == "" {
		return errors.New("service must not be empty")
	}
	if res.Domain == "" {
		return errors.New("domain must not be empty")
	}
	if res.Port < 0 || res.Port > 65535 {
		return fmt.Errorf("port %d is outside valid range", res.Port)
	}
	if res.Listener != nil {
		pair := listener.Pair{HTTPSAddr: res.Listener.HTTPSAddr, HTTPAddr: res.Listener.HTTPAddr}
		if err := listener.Validate(pair, true); err != nil {
			return err
		}
	}
	self := Key(res.Project, res.Service)
	for key, ex := range r.Services {
		if key == self {
			continue
		}
		if ex.Domain == res.Domain {
			return &ConflictError{Domain: res.Domain, OwnerKey: key}
		}
		if res.Port != 0 && ex.Port == res.Port {
			return &ConflictError{Port: res.Port, OwnerKey: key}
		}
	}
	if r.Services == nil {
		r.Services = map[string]Reservation{}
	}
	r.Services[self] = res
	return nil
}

// ListenerPair returns the target listener for res. Missing metadata means the
// default listener pair.
func (r Reservation) ListenerPair() listener.Pair {
	if r.Listener == nil {
		return listener.DefaultPair()
	}
	return listener.FromFlags(r.Listener.HTTPSAddr, r.Listener.HTTPAddr)
}

// SetListenerPair stores pair on res. The default listener is omitted on disk.
func (r *Reservation) SetListenerPair(pair listener.Pair) {
	pair = listener.Normalize(pair)
	if listener.Equivalent(pair, listener.DefaultPair()) {
		r.Listener = nil
		return
	}
	r.Listener = &ListenerTarget{HTTPSAddr: pair.HTTPSAddr, HTTPAddr: pair.HTTPAddr}
}

// Get returns the reservation for key.
func (r *Registry) Get(key string) (Reservation, bool) {
	res, ok := r.Services[key]
	return res, ok
}

// Release removes key. It is a no-op if absent.
func (r *Registry) Release(key string) {
	delete(r.Services, key)
}

// ReleaseDomain removes the reservation whose domain matches, returning it.
func (r *Registry) ReleaseDomain(domain string) (Reservation, bool) {
	domain = canonicalDomain(domain)
	for k, res := range r.Services {
		if canonicalDomain(res.Domain) == domain {
			delete(r.Services, k)
			return res, true
		}
	}
	return Reservation{}, false
}

func canonicalDomain(domain string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")
}

// Prune removes reservations whose owning gate.toml no longer exists (per the
// exists predicate) and returns the removed reservations sorted by key.
func (r *Registry) Prune(exists func(path string) (bool, error)) ([]Reservation, error) {
	var staleKeys []string
	for _, key := range r.Keys() {
		res := r.Services[key]
		if res.ConfigPath == "" {
			continue
		}
		present, err := exists(res.ConfigPath)
		if err != nil {
			return nil, err
		}
		if !present {
			staleKeys = append(staleKeys, key)
		}
	}
	removed := make([]Reservation, 0, len(staleKeys))
	for _, key := range staleKeys {
		removed = append(removed, r.Services[key])
		delete(r.Services, key)
	}
	sort.Slice(removed, func(i, j int) bool {
		return Key(removed[i].Project, removed[i].Service) < Key(removed[j].Project, removed[j].Service)
	})
	return removed, nil
}

// UsedPorts returns the set of currently reserved ports.
func (r *Registry) UsedPorts() map[int]bool {
	used := make(map[int]bool, len(r.Services))
	for _, res := range r.Services {
		if res.Port != 0 {
			used[res.Port] = true
		}
	}
	return used
}

// Keys returns the reservation keys in sorted order (stable output).
func (r *Registry) Keys() []string {
	keys := make([]string, 0, len(r.Services))
	for k := range r.Services {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Validate checks invariants that must hold for a persisted registry snapshot.
func (r *Registry) Validate() []IntegrityIssue {
	if r.Services == nil {
		return nil
	}
	var issues []IntegrityIssue
	domains := map[string]string{}
	ports := map[int]string{}
	for _, key := range r.Keys() {
		res := r.Services[key]
		expected := Key(res.Project, res.Service)
		if key != expected {
			issues = append(issues, IntegrityIssue{
				Code:    "registry_key_mismatch",
				Key:     key,
				Message: fmt.Sprintf("registry key %q does not match reservation owner %q", key, expected),
			})
		}
		if strings.TrimSpace(res.Service) == "" {
			issues = append(issues, IntegrityIssue{
				Code:    "registry_empty_service",
				Key:     key,
				Message: fmt.Sprintf("registry reservation %q has an empty service", key),
			})
		}
		domain := canonicalDomain(res.Domain)
		if domain == "" {
			issues = append(issues, IntegrityIssue{
				Code:    "registry_empty_domain",
				Key:     key,
				Message: fmt.Sprintf("registry reservation %q has an empty domain", key),
			})
		} else if owner, ok := domains[domain]; ok && owner != key {
			issues = append(issues, IntegrityIssue{
				Code:    "registry_duplicate_domain",
				Key:     key,
				Message: fmt.Sprintf("registry domain %q is duplicated by %s and %s", domain, owner, key),
			})
		} else {
			domains[domain] = key
		}
		if res.Port < 0 || res.Port > 65535 {
			issues = append(issues, IntegrityIssue{
				Code:    "registry_invalid_port",
				Key:     key,
				Message: fmt.Sprintf("registry reservation %q has invalid port %d", key, res.Port),
			})
		} else if res.Port != 0 {
			if owner, ok := ports[res.Port]; ok && owner != key {
				issues = append(issues, IntegrityIssue{
					Code:    "registry_duplicate_port",
					Key:     key,
					Message: fmt.Sprintf("registry port %d is duplicated by %s and %s", res.Port, owner, key),
				})
			} else {
				ports[res.Port] = key
			}
		}
		if res.Listener != nil {
			pair := listener.Pair{HTTPSAddr: res.Listener.HTTPSAddr, HTTPAddr: res.Listener.HTTPAddr}
			if err := listener.Validate(pair, true); err != nil {
				issues = append(issues, IntegrityIssue{
					Code:    "registry_invalid_listener",
					Key:     key,
					Message: fmt.Sprintf("registry reservation %q has invalid listener: %v", key, err),
				})
			}
		}
	}
	return issues
}

// migrate upgrades an older registry to the current schema in place.
func migrate(r *Registry) {
	if r.Services == nil {
		r.Services = map[string]Reservation{}
	}
	for key, res := range r.Services {
		res.Domain = canonicalDomain(res.Domain)
		res.SetListenerPair(res.ListenerPair())
		r.Services[key] = res
	}
	r.Version = SchemaVersion
}

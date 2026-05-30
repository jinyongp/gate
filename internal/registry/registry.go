// Package registry persists the machine-wide domain↔port↔service reservations.
// The registry is tool-owned (humans use the CLI, not the file). Concurrent
// access is serialised with an advisory file lock and written atomically; see
// store.go. This file holds the schema and the pure, IO-free operations.
package registry

import (
	"fmt"
	"sort"
)

// SchemaVersion is the current on-disk schema version.
const SchemaVersion = 1

// Reservation is a persisted service↔port binding.
type Reservation struct {
	Project    string `json:"project"`
	Service    string `json:"service"`
	Domain     string `json:"domain"`
	Port       int    `json:"port"`
	TLS        string `json:"tls,omitempty"`
	DNS        string `json:"dns,omitempty"`
	Adhoc      bool   `json:"adhoc,omitempty"`
	ConfigPath string `json:"config_path,omitempty"` // prx.toml that owns this reservation; enables GC
}

// Registry is the whole on-disk document.
type Registry struct {
	Version  int                    `json:"version"`
	Services map[string]Reservation `json:"services"`
}

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
	for k, res := range r.Services {
		if res.Domain == domain {
			delete(r.Services, k)
			return res, true
		}
	}
	return Reservation{}, false
}

// Prune removes reservations whose owning prx.toml no longer exists (per the
// exists predicate) and returns the removed reservations sorted by key.
func (r *Registry) Prune(exists func(path string) bool) []Reservation {
	var removed []Reservation
	for k, res := range r.Services {
		if res.ConfigPath != "" && !exists(res.ConfigPath) {
			removed = append(removed, res)
			delete(r.Services, k)
		}
	}
	sort.Slice(removed, func(i, j int) bool {
		return Key(removed[i].Project, removed[i].Service) < Key(removed[j].Project, removed[j].Service)
	})
	return removed
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

// migrate upgrades an older registry to the current schema in place.
func migrate(r *Registry) {
	if r.Services == nil {
		r.Services = map[string]Reservation{}
	}
	if r.Version < 1 {
		r.Version = 1
	}
	r.Version = SchemaVersion
}

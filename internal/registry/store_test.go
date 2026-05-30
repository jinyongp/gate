package registry

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestStoreReadMissingReturnsEmpty(t *testing.T) {
	s := Open(filepath.Join(t.TempDir(), "registry.json"))
	reg, err := s.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(reg.Services) != 0 || reg.Version != SchemaVersion {
		t.Fatalf("unexpected empty registry: %+v", reg)
	}
}

func TestStoreUpdateRoundTripAndPerm(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	s := Open(path)
	err := s.Update(func(r *Registry) error {
		return r.Reserve(Reservation{Project: "a", Service: "web", Domain: "x.localhost", Port: 4300})
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("perm = %o, want 600", perm)
	}
	reg, _ := s.Read()
	if got, ok := reg.Get("a/web"); !ok || got.Port != 4300 {
		t.Fatalf("roundtrip failed: %+v", reg)
	}
}

// TestStoreConcurrentReserve verifies the flock serialises read-modify-write:
// N goroutines each reserve a distinct key/port and none is lost.
func TestStoreConcurrentReserve(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	s := Open(path)
	const n = 50

	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			err := s.Update(func(r *Registry) error {
				return r.Reserve(Reservation{
					Project: "p",
					Service: fmt.Sprintf("s%d", i),
					Domain:  fmt.Sprintf("s%d.localhost", i),
					Port:    4300 + i,
				})
			})
			if err != nil {
				t.Errorf("Update %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	reg, _ := s.Read()
	if len(reg.Services) != n {
		t.Fatalf("got %d reservations, want %d (lost update)", len(reg.Services), n)
	}
}

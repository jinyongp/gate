package registry

import (
	"errors"
	"testing"
)

func TestReserveDomainConflict(t *testing.T) {
	r := New()
	if err := r.Reserve(Reservation{Project: "a", Service: "web", Domain: "x.localhost", Port: 4300}); err != nil {
		t.Fatal(err)
	}
	err := r.Reserve(Reservation{Project: "b", Service: "web", Domain: "x.localhost", Port: 4301})
	var ce *ConflictError
	if !errors.As(err, &ce) || ce.Domain != "x.localhost" || ce.OwnerKey != "a/web" {
		t.Fatalf("err = %v, want domain conflict owned by a/web", err)
	}
}

func TestReservePortConflict(t *testing.T) {
	r := New()
	_ = r.Reserve(Reservation{Project: "a", Service: "web", Domain: "x.localhost", Port: 4300})
	err := r.Reserve(Reservation{Project: "b", Service: "api", Domain: "y.localhost", Port: 4300})
	var ce *ConflictError
	if !errors.As(err, &ce) || ce.Port != 4300 {
		t.Fatalf("err = %v, want port conflict", err)
	}
}

func TestReserveSameKeyUpdates(t *testing.T) {
	r := New()
	_ = r.Reserve(Reservation{Project: "a", Service: "web", Domain: "x.localhost", Port: 4300})
	// Re-reserving the same key with the same domain/port must succeed (update).
	if err := r.Reserve(Reservation{Project: "a", Service: "web", Domain: "x.localhost", Port: 4300, TLS: "internal"}); err != nil {
		t.Fatalf("update failed: %v", err)
	}
	got, _ := r.Get("a/web")
	if got.TLS != "internal" {
		t.Fatalf("update not applied: %+v", got)
	}
}

func TestUsedPortsAndRelease(t *testing.T) {
	r := New()
	_ = r.Reserve(Reservation{Project: "a", Service: "web", Domain: "x", Port: 4300})
	_ = r.Reserve(Reservation{Project: "a", Service: "api", Domain: "y", Port: 4301})
	used := r.UsedPorts()
	if !used[4300] || !used[4301] {
		t.Fatalf("UsedPorts = %v", used)
	}
	r.Release("a/web")
	if _, ok := r.Get("a/web"); ok {
		t.Fatal("release failed")
	}
}

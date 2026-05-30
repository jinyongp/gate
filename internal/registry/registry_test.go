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

func TestReleaseDomain(t *testing.T) {
	r := New()
	_ = r.Reserve(Reservation{Project: "a", Service: "web", Domain: "x.localhost", Port: 4300})
	res, ok := r.ReleaseDomain("x.localhost")
	if !ok || res.Port != 4300 {
		t.Fatalf("ReleaseDomain = %+v, %v", res, ok)
	}
	if _, ok := r.ReleaseDomain("missing"); ok {
		t.Fatal("expected miss for unknown domain")
	}
}

func TestPrune(t *testing.T) {
	r := New()
	_ = r.Reserve(Reservation{Project: "live", Service: "web", Domain: "a", Port: 4300, ConfigPath: "/exists"})
	_ = r.Reserve(Reservation{Project: "dead", Service: "web", Domain: "b", Port: 4301, ConfigPath: "/gone"})
	_ = r.Reserve(Reservation{Project: "", Service: "c", Domain: "c", Port: 4302}) // adhoc, no ConfigPath

	removed := r.Prune(func(p string) bool { return p == "/exists" })
	if len(removed) != 1 || removed[0].Project != "dead" {
		t.Fatalf("Prune removed = %+v", removed)
	}
	if _, ok := r.Get("dead/web"); ok {
		t.Fatal("dead reservation not pruned")
	}
	if _, ok := r.Get("live/web"); !ok {
		t.Fatal("live reservation wrongly pruned")
	}
	if _, ok := r.Get("/c"); !ok {
		t.Fatal("adhoc reservation wrongly pruned")
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

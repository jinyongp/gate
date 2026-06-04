package registry

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gate/internal/listener"
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

func TestReserveDomainConflictCanonicalCase(t *testing.T) {
	r := New()
	if err := r.Reserve(Reservation{Project: "a", Service: "web", Domain: "App.localhost.", Port: 4300}); err != nil {
		t.Fatal(err)
	}
	err := r.Reserve(Reservation{Project: "b", Service: "web", Domain: "app.localhost", Port: 4301})
	var ce *ConflictError
	if !errors.As(err, &ce) || ce.Domain != "app.localhost" {
		t.Fatalf("err = %v, want canonical domain conflict", err)
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
	_ = r.Reserve(Reservation{Project: "", Service: "c", Domain: "c", Port: 4302}) // standalone, no ConfigPath

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
		t.Fatal("standalone reservation wrongly pruned")
	}
}

func TestStorePersistsStandaloneJSONKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	store := Open(path)
	if err := store.Update(func(r *Registry) error {
		return r.Reserve(Reservation{Service: "web.localhost", Domain: "web.localhost", Port: 4312, Standalone: true})
	}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, `"standalone": true`) {
		t.Fatalf("registry JSON missing standalone key:\n%s", s)
	}
	if strings.Contains(s, `"adhoc"`) {
		t.Fatalf("registry JSON should not contain adhoc key:\n%s", s)
	}
}

func TestStoreMigratesLegacyAdhocOnTypedUpdate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	if err := os.WriteFile(path, []byte(`{
  "version": 1,
  "services": {
    "/web.localhost": {
      "service": "web.localhost",
      "domain": "web.localhost",
      "port": 4312,
      "adhoc": true,
      "active": true
    }
  }
}
`), 0o600); err != nil {
		t.Fatal(err)
	}

	store := Open(path)
	if err := store.Update(func(r *Registry) error { return nil }); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, `"standalone": true`) {
		t.Fatalf("typed update did not preserve legacy adhoc as standalone:\n%s", s)
	}
	if strings.Contains(s, `"adhoc"`) {
		t.Fatalf("typed update should remove legacy adhoc key:\n%s", s)
	}
}

func TestReservationListenerPairDefaultOmitted(t *testing.T) {
	var res Reservation
	if got := res.ListenerPair(); got != listener.DefaultPair() {
		t.Fatalf("ListenerPair default = %+v, want %+v", got, listener.DefaultPair())
	}
	res.SetListenerPair(listener.DefaultPair())
	if res.Listener != nil {
		t.Fatalf("default listener should be omitted, got %+v", res.Listener)
	}
}

func TestReservationListenerPairStoresNonDefault(t *testing.T) {
	var res Reservation
	pair := listener.Pair{HTTPSAddr: "127.0.0.1:18443", HTTPAddr: "127.0.0.1:18080"}
	res.SetListenerPair(pair)
	if res.Listener == nil {
		t.Fatal("non-default listener was omitted")
	}
	if got, want := res.ListenerPair(), listener.Normalize(pair); got != want {
		t.Fatalf("ListenerPair = %+v, want %+v", got, want)
	}
}

func TestStoreMigratesListenerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	if err := os.WriteFile(path, []byte(`{
  "version": 1,
  "services": {
    "demo/web": {
      "project": "demo",
      "service": "web",
      "domain": "Web.localhost.",
      "port": 4312,
      "active": true
    }
  }
}
`), 0o600); err != nil {
		t.Fatal(err)
	}

	store := Open(path)
	if err := store.Update(func(r *Registry) error { return nil }); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, `"version": 2`) {
		t.Fatalf("registry JSON missing schema version 2:\n%s", s)
	}
	if strings.Contains(s, `"listener"`) {
		t.Fatalf("default listener should be omitted after migration:\n%s", s)
	}
	if !strings.Contains(s, `"domain": "web.localhost"`) {
		t.Fatalf("domain not canonicalized during migration:\n%s", s)
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

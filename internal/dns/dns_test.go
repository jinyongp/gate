package dns

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func tempHosts(t *testing.T, body string) Hosts {
	t.Helper()
	p := filepath.Join(t.TempDir(), "hosts")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return Hosts{Path: p}
}

func read(t *testing.T, h Hosts) string {
	t.Helper()
	b, err := os.ReadFile(h.Path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestHostsEnsurePreservesExternal(t *testing.T) {
	h := tempHosts(t, "127.0.0.1\tlocalhost\n255.255.255.255\tbroadcasthost\n")
	if err := h.Ensure("app.example.com"); err != nil {
		t.Fatal(err)
	}
	s := read(t, h)
	for _, want := range []string{"localhost", "broadcasthost", beginMarker, "app.example.com", endMarker} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in:\n%s", want, s)
		}
	}
}

func TestHostsEnsureIdempotent(t *testing.T) {
	h := tempHosts(t, "")
	_ = h.Ensure("app.example.com")
	_ = h.Ensure("app.example.com")
	if n := strings.Count(read(t, h), "app.example.com"); n != 1 {
		t.Fatalf("domain appears %d times, want 1", n)
	}
}

func TestHostsEnsureConcurrentKeepsAllEntries(t *testing.T) {
	h := tempHosts(t, "")
	var wg sync.WaitGroup
	for _, domain := range []string{"a.example.com", "b.example.com", "c.example.com", "d.example.com"} {
		wg.Add(1)
		go func(domain string) {
			defer wg.Done()
			if err := h.Ensure(domain); err != nil {
				t.Errorf("Ensure(%s): %v", domain, err)
			}
		}(domain)
	}
	wg.Wait()
	s := read(t, h)
	for _, domain := range []string{"a.example.com", "b.example.com", "c.example.com", "d.example.com"} {
		if !strings.Contains(s, domain) {
			t.Fatalf("missing %s after concurrent ensure:\n%s", domain, s)
		}
	}
}

func TestHostsRemoveDropsBlockWhenEmpty(t *testing.T) {
	h := tempHosts(t, "127.0.0.1\tlocalhost\n")
	_ = h.Ensure("a.example.com")
	_ = h.Ensure("b.example.com")
	if err := h.Remove("a.example.com"); err != nil {
		t.Fatal(err)
	}
	s := read(t, h)
	if strings.Contains(s, "a.example.com") || !strings.Contains(s, "b.example.com") {
		t.Fatalf("remove wrong entry:\n%s", s)
	}
	// Removing the last entry should drop the markers entirely.
	_ = h.Remove("b.example.com")
	s = read(t, h)
	if strings.Contains(s, beginMarker) || strings.Contains(s, endMarker) {
		t.Fatalf("markers left after emptying block:\n%s", s)
	}
	if !strings.Contains(s, "localhost") {
		t.Fatalf("external line lost:\n%s", s)
	}
}

func TestHostsRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real")
	if err := os.WriteFile(target, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	h := Hosts{Path: link}
	if err := h.Ensure("x.example.com"); err == nil {
		t.Fatal("expected symlink to be refused")
	}
}

func TestSystemHostsUsesTempLockPath(t *testing.T) {
	h := Hosts{Path: hostsPath}
	if got := h.lockPath(); got != filepath.Join(os.TempDir(), "prx-hosts.lock") {
		t.Fatalf("lockPath = %q", got)
	}
}

func TestWriteSystemHostsReportsPermission(t *testing.T) {
	oldRun := runPrivilegedHostsCommand
	t.Cleanup(func() { runPrivilegedHostsCommand = oldRun })
	runPrivilegedHostsCommand = func(string, ...string) error {
		return errors.New("denied")
	}
	if err := writeSystemHosts([]byte("127.0.0.1 example.test\n")); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("error = %v, want permission", err)
	}
}

func TestSelectAndModeFor(t *testing.T) {
	if _, ok := Select("x.localhost", "").(Localhost); !ok {
		t.Fatal(".localhost should select localhost provider")
	}
	if _, ok := Select("app.example.com", "").(Hosts); !ok {
		t.Fatal("custom domain should select hosts provider")
	}
	if _, ok := Select("app.example.com", "localhost").(Localhost); !ok {
		t.Fatal("override should win")
	}
	if ModeFor("x.localhost", "") != ModeLocalhost {
		t.Fatal("ModeFor localhost")
	}
	if ModeFor("app.example.com", "") != ModeHosts {
		t.Fatal("ModeFor hosts")
	}
}

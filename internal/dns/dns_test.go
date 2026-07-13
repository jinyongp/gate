package dns

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
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
	h := tempHosts(t, "127.0.0.1\tlocalhost\n255.255.255.255\tbroadcasthost\n::1\tlocalhost\n")
	if err := h.Ensure("app.example.com"); err != nil {
		t.Fatal(err)
	}
	s := read(t, h)
	for _, want := range []string{"localhost", "broadcasthost", beginMarker, "app.example.com", endMarker} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in:\n%s", want, s)
		}
	}
	if strings.Contains(s, "# gate") {
		t.Fatalf("managed entry should not include redundant comment:\n%s", s)
	}
	want := "255.255.255.255\tbroadcasthost\n::1\tlocalhost\n\n" +
		beginMarker + "\n" +
		"127.0.0.1\tapp.example.com\n" +
		endMarker + "\n"
	if !strings.Contains(s, want) {
		t.Fatalf("managed block should be separated by one blank line:\n%s", s)
	}
}

func TestHostsEnsureSeparatesExistingTrailingContent(t *testing.T) {
	h := tempHosts(t, "127.0.0.1\tlocalhost\n\n"+beginMarker+"\n127.0.0.1\told.example.com\n"+endMarker+"\n# after\n")
	if err := h.Ensure("app.example.com"); err != nil {
		t.Fatal(err)
	}
	want := "127.0.0.1\tlocalhost\n\n" +
		beginMarker + "\n" +
		"127.0.0.1\told.example.com\n" +
		"127.0.0.1\tapp.example.com\n" +
		endMarker + "\n\n" +
		"# after\n"
	if got := read(t, h); got != want {
		t.Fatalf("hosts content =\n%q\nwant\n%q", got, want)
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

func TestHostsRemoveManagedBlock(t *testing.T) {
	h := tempHosts(t, "127.0.0.1\tlocalhost\n\n"+beginMarker+"\n127.0.0.1\told.example.com\n"+endMarker+"\n# after\n")
	if err := h.RemoveManagedBlock(); err != nil {
		t.Fatal(err)
	}
	want := "127.0.0.1\tlocalhost\n\n# after\n"
	if got := read(t, h); got != want {
		t.Fatalf("hosts content =\n%q\nwant\n%q", got, want)
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

func TestHostsRejectsSymlinkedLockPath(t *testing.T) {
	dir := t.TempDir()
	h := Hosts{Path: filepath.Join(dir, "hosts")}
	if err := os.WriteFile(h.Path, []byte("127.0.0.1 localhost\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, h.lockPath()); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if err := h.Ensure("x.example.com"); err == nil {
		t.Fatal("expected symlinked lock path to be refused")
	}
	b, err := os.ReadFile(victim)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "keep\n" {
		t.Fatalf("lock symlink target changed: %q", b)
	}
}

func TestHostsLockNormalizesModeDespiteUmask(t *testing.T) {
	dir := t.TempDir()
	h := Hosts{Path: filepath.Join(dir, "hosts")}
	oldUmask := syscall.Umask(0o077)
	defer syscall.Umask(oldUmask)
	unlock, err := h.lock()
	if err != nil {
		t.Fatal(err)
	}
	unlock()
	info, err := os.Stat(h.lockPath())
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Fatalf("lock mode = %o, want 644", got)
	}
}

func TestSystemHostsUsesTempLockPath(t *testing.T) {
	h := Hosts{Path: hostsPath}
	if got := h.lockPath(); got != filepath.Join(os.TempDir(), "gate-hosts.lock") {
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

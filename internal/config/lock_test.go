package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestResolveFileTargetResolvesFileAndParentSymlinks(t *testing.T) {
	realDir := t.TempDir()
	target := filepath.Join(realDir, "gate.toml")
	if err := os.WriteFile(target, []byte("[project]\nname = \"demo\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	resolvedTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	aliasDir := filepath.Join(root, "repo")
	if err := os.Symlink(realDir, aliasDir); err != nil {
		t.Fatal(err)
	}
	fileAlias := filepath.Join(root, "config.toml")
	if err := os.Symlink(target, fileAlias); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{target, filepath.Join(aliasDir, "gate.toml"), fileAlias} {
		got, err := ResolveFileTarget(path)
		if err != nil {
			t.Fatalf("ResolveFileTarget(%q): %v", path, err)
		}
		if got != resolvedTarget {
			t.Fatalf("ResolveFileTarget(%q) = %q, want %q", path, got, resolvedTarget)
		}
	}
}

func TestResolveFileTargetRejectsBrokenSymlinkAndDirectory(t *testing.T) {
	dir := t.TempDir()
	broken := filepath.Join(dir, "broken.toml")
	if err := os.Symlink(filepath.Join(dir, "missing.toml"), broken); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{broken, dir} {
		if _, err := ResolveFileTarget(path); err == nil {
			t.Fatalf("ResolveFileTarget(%q) succeeded", path)
		}
	}
}

func TestLockFileSerializesParentSymlinkAliases(t *testing.T) {
	realDir := t.TempDir()
	target := filepath.Join(realDir, "gate.toml")
	if err := os.WriteFile(target, []byte("[project]\nname = \"demo\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	aliasDir := filepath.Join(root, "repo")
	if err := os.Symlink(realDir, aliasDir); err != nil {
		t.Fatal(err)
	}

	unlock, err := LockFile(target)
	if err != nil {
		t.Fatal(err)
	}
	acquired := make(chan func(), 1)
	errs := make(chan error, 1)
	go func() {
		nextUnlock, nextErr := LockFile(filepath.Join(aliasDir, "gate.toml"))
		if nextErr != nil {
			errs <- nextErr
			return
		}
		acquired <- nextUnlock
	}()

	select {
	case nextUnlock := <-acquired:
		nextUnlock()
		unlock()
		t.Fatal("alias lock acquired before original lock released")
	case err := <-errs:
		unlock()
		t.Fatal(err)
	case <-time.After(100 * time.Millisecond):
	}
	unlock()

	select {
	case nextUnlock := <-acquired:
		nextUnlock()
	case err := <-errs:
		t.Fatal(err)
	case <-time.After(2 * time.Second):
		t.Fatal("alias lock did not acquire after release")
	}
}

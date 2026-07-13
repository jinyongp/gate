package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCanonicalStateLockPathResolvesExistingParentSymlink(t *testing.T) {
	realRoot := t.TempDir()
	parent := t.TempDir()
	alias := filepath.Join(parent, "config")
	if err := os.Symlink(realRoot, alias); err != nil {
		t.Fatal(err)
	}
	realPath, err := canonicalStateLockPath(filepath.Join(realRoot, "gate"))
	if err != nil {
		t.Fatal(err)
	}
	aliasPath, err := canonicalStateLockPath(filepath.Join(alias, "gate"))
	if err != nil {
		t.Fatal(err)
	}
	if aliasPath != realPath {
		t.Fatalf("canonical alias = %q, want %q", aliasPath, realPath)
	}
}

func TestStateMutationLockSerializesConfigRootAliases(t *testing.T) {
	t.Setenv("GATE_ISOLATED_ROOT", "")
	realRoot := t.TempDir()
	parent := t.TempDir()
	alias := filepath.Join(parent, "config")
	if err := os.Symlink(realRoot, alias); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", realRoot)
	unlock, err := lockStateMutation()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", alias)
	acquired := make(chan func(), 1)
	errs := make(chan error, 1)
	go func() {
		nextUnlock, nextErr := lockStateMutation()
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
		t.Fatal("alias state lock acquired before original lock released")
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
		t.Fatal("alias state lock did not acquire after release")
	}
}

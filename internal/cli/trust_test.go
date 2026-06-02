package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gate/internal/ca"
	"gate/internal/paths"
)

func TestUntrustDoesNotGenerateMissingCA(t *testing.T) {
	isolate(t)
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	oldUntrust := untrustAuthorityFunc
	t.Cleanup(func() { untrustAuthorityFunc = oldUntrust })
	untrustAuthorityFunc = func(*ca.CA) error {
		t.Fatal("untrust should not be called without an existing CA")
		return nil
	}

	var out, errb bytes.Buffer
	if code := Untrust(nil, &out, &errb); code != ExitOK {
		t.Fatalf("Untrust exit = %d, stderr=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "nothing to untrust") {
		t.Fatalf("stdout = %q", out.String())
	}
	if _, err := os.Stat(filepath.Join(dataHome, "gate", "ca", "root.crt")); !os.IsNotExist(err) {
		t.Fatalf("Untrust generated CA or stat failed: %v", err)
	}
}

func TestTrustStopsActivityBeforeTrustStoreCall(t *testing.T) {
	isolate(t)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	events := recordActivities(t)
	oldTrust := trustAuthorityFunc
	t.Cleanup(func() { trustAuthorityFunc = oldTrust })
	trustAuthorityFunc = func(*ca.CA) error {
		if got := lastEvent(*events); got != "stop:preparing trust store" {
			t.Fatalf("trust store called before activity stopped; events=%v", *events)
		}
		return nil
	}

	var out, errb bytes.Buffer
	if code := Trust(nil, &out, &errb); code != ExitOK {
		t.Fatalf("Trust exit = %d, stderr=%s", code, errb.String())
	}
}

func TestUntrustStopsActivityBeforeTrustStoreCall(t *testing.T) {
	isolate(t)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if _, err := ca.Load(paths.DataDir()); err != nil {
		t.Fatal(err)
	}
	events := recordActivities(t)
	oldUntrust := untrustAuthorityFunc
	t.Cleanup(func() { untrustAuthorityFunc = oldUntrust })
	untrustAuthorityFunc = func(*ca.CA) error {
		if got := lastEvent(*events); got != "stop:preparing trust store" {
			t.Fatalf("trust store called before activity stopped; events=%v", *events)
		}
		return nil
	}

	var out, errb bytes.Buffer
	if code := Untrust(nil, &out, &errb); code != ExitOK {
		t.Fatalf("Untrust exit = %d, stderr=%s", code, errb.String())
	}
}

func TestUntrustRemovesExistingCA(t *testing.T) {
	isolate(t)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	authority, err := ca.Load(paths.DataDir())
	if err != nil {
		t.Fatal(err)
	}
	oldUntrust := untrustAuthorityFunc
	t.Cleanup(func() { untrustAuthorityFunc = oldUntrust })
	var fingerprint string
	untrustAuthorityFunc = func(next *ca.CA) error {
		fingerprint = next.Fingerprint()
		return nil
	}

	var out, errb bytes.Buffer
	if code := Untrust(nil, &out, &errb); code != ExitOK {
		t.Fatalf("Untrust exit = %d, stderr=%s", code, errb.String())
	}
	if fingerprint != authority.Fingerprint() {
		t.Fatalf("untrusted fingerprint = %q, want %q", fingerprint, authority.Fingerprint())
	}
	if !strings.Contains(out.String(), "root CA untrusted") || !strings.Contains(out.String(), authority.Fingerprint()) {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestUntrustWorksWithoutRootKey(t *testing.T) {
	isolate(t)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	authority, err := ca.Load(paths.DataDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(paths.DataDir(), "ca", "root.key")); err != nil {
		t.Fatal(err)
	}
	oldUntrust := untrustAuthorityFunc
	t.Cleanup(func() { untrustAuthorityFunc = oldUntrust })
	var fingerprint string
	untrustAuthorityFunc = func(next *ca.CA) error {
		fingerprint = next.Fingerprint()
		return nil
	}

	var out, errb bytes.Buffer
	if code := Untrust(nil, &out, &errb); code != ExitOK {
		t.Fatalf("Untrust exit = %d, stderr=%s", code, errb.String())
	}
	if fingerprint != authority.Fingerprint() {
		t.Fatalf("untrusted fingerprint = %q, want %q", fingerprint, authority.Fingerprint())
	}
}

func TestUntrustPermissionError(t *testing.T) {
	isolate(t)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if _, err := ca.Load(paths.DataDir()); err != nil {
		t.Fatal(err)
	}
	oldUntrust := untrustAuthorityFunc
	t.Cleanup(func() { untrustAuthorityFunc = oldUntrust })
	untrustAuthorityFunc = func(*ca.CA) error {
		return os.ErrPermission
	}

	var out, errb bytes.Buffer
	if code := Untrust(nil, &out, &errb); code != ExitPerm {
		t.Fatalf("Untrust exit = %d, want permission; stdout=%s stderr=%s", code, out.String(), errb.String())
	}
}

func TestUntrustGenericError(t *testing.T) {
	isolate(t)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if _, err := ca.Load(paths.DataDir()); err != nil {
		t.Fatal(err)
	}
	oldUntrust := untrustAuthorityFunc
	t.Cleanup(func() { untrustAuthorityFunc = oldUntrust })
	untrustAuthorityFunc = func(*ca.CA) error {
		return errors.New("trust store failed")
	}

	var out, errb bytes.Buffer
	if code := Untrust(nil, &out, &errb); code != ExitError {
		t.Fatalf("Untrust exit = %d, want error; stdout=%s stderr=%s", code, out.String(), errb.String())
	}
}

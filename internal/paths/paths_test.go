package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigDir(t *testing.T) {
	t.Run("xdg set", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", "/xdg/cfg")
		if got, want := ConfigDir(), "/xdg/cfg/gate"; got != want {
			t.Fatalf("ConfigDir() = %q, want %q", got, want)
		}
	})
	t.Run("xdg unset falls back to home", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", "")
		t.Setenv("HOME", "/home/u")
		if got, want := ConfigDir(), filepath.Join("/home/u", ".config", "gate"); got != want {
			t.Fatalf("ConfigDir() = %q, want %q", got, want)
		}
	})
	t.Run("isolated root overrides xdg", func(t *testing.T) {
		root := t.TempDir()
		t.Setenv("GATE_ISOLATED_ROOT", root)
		t.Setenv("XDG_CONFIG_HOME", "/xdg/cfg")
		if got, want := ConfigDir(), filepath.Join(validatedRoot(t, root), "xdg", "config", "gate"); got != want {
			t.Fatalf("ConfigDir() = %q, want %q", got, want)
		}
	})
}

func TestDataDir(t *testing.T) {
	t.Run("xdg set", func(t *testing.T) {
		t.Setenv("XDG_DATA_HOME", "/xdg/data")
		if got, want := DataDir(), "/xdg/data/gate"; got != want {
			t.Fatalf("DataDir() = %q, want %q", got, want)
		}
	})
	t.Run("xdg unset", func(t *testing.T) {
		t.Setenv("XDG_DATA_HOME", "")
		t.Setenv("HOME", "/home/u")
		if got, want := DataDir(), filepath.Join("/home/u", ".local", "share", "gate"); got != want {
			t.Fatalf("DataDir() = %q, want %q", got, want)
		}
	})
	t.Run("isolated root overrides xdg", func(t *testing.T) {
		root := t.TempDir()
		t.Setenv("GATE_ISOLATED_ROOT", root)
		t.Setenv("XDG_DATA_HOME", "/xdg/data")
		if got, want := DataDir(), filepath.Join(validatedRoot(t, root), "xdg", "data", "gate"); got != want {
			t.Fatalf("DataDir() = %q, want %q", got, want)
		}
	})
}

func TestStateDir(t *testing.T) {
	t.Run("xdg overrides per-os", func(t *testing.T) {
		t.Setenv("XDG_STATE_HOME", "/xdg/state")
		if got, want := stateDir("darwin"), "/xdg/state/gate"; got != want {
			t.Fatalf("stateDir(darwin) = %q, want %q", got, want)
		}
		if got, want := stateDir("linux"), "/xdg/state/gate"; got != want {
			t.Fatalf("stateDir(linux) = %q, want %q", got, want)
		}
	})
	t.Run("darwin uses Library/Logs", func(t *testing.T) {
		t.Setenv("XDG_STATE_HOME", "")
		t.Setenv("HOME", "/Users/u")
		if got, want := stateDir("darwin"), filepath.Join("/Users/u", "Library", "Logs", "gate"); got != want {
			t.Fatalf("stateDir(darwin) = %q, want %q", got, want)
		}
	})
	t.Run("linux uses .local/state", func(t *testing.T) {
		t.Setenv("XDG_STATE_HOME", "")
		t.Setenv("HOME", "/home/u")
		if got, want := stateDir("linux"), filepath.Join("/home/u", ".local", "state", "gate"); got != want {
			t.Fatalf("stateDir(linux) = %q, want %q", got, want)
		}
	})
	t.Run("isolated root overrides xdg", func(t *testing.T) {
		root := t.TempDir()
		t.Setenv("GATE_ISOLATED_ROOT", root)
		t.Setenv("XDG_STATE_HOME", "/xdg/state")
		if got, want := StateDir(), filepath.Join(validatedRoot(t, root), "xdg", "state", "gate"); got != want {
			t.Fatalf("StateDir() = %q, want %q", got, want)
		}
	})
}

func TestEnsureCreates0700(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "b")
	got, err := Ensure(dir)
	if err != nil {
		t.Fatalf("Ensure() error: %v", err)
	}
	if got != dir {
		t.Fatalf("Ensure() = %q, want %q", got, dir)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Fatalf("mode = %o, want 700", perm)
	}
}

func TestFallbackHomeCreatesPrivateOwnedDirectory(t *testing.T) {
	tempDir := t.TempDir()
	got := fallbackHome(tempDir, os.Getuid())
	info, err := os.Lstat(got)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("fallback home mode = %v, want private directory", info.Mode())
	}
	if again := fallbackHome(tempDir, os.Getuid()); again != got {
		t.Fatalf("fallback home changed: %q != %q", again, got)
	}
}

func TestFallbackHomeRejectsPredictableSymlink(t *testing.T) {
	tempDir := t.TempDir()
	victim := filepath.Join(tempDir, "victim")
	if err := os.Mkdir(victim, 0o700); err != nil {
		t.Fatal(err)
	}
	predictable := filepath.Join(tempDir, "gate-user-"+fmt.Sprint(os.Getuid()))
	if err := os.Symlink(victim, predictable); err != nil {
		t.Fatal(err)
	}
	got := fallbackHome(tempDir, os.Getuid())
	if got == predictable {
		t.Fatalf("trusted attacker-controlled symlink %q", got)
	}
	info, err := os.Lstat(got)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("fallback home mode = %v, want private directory", info.Mode())
	}
}

func TestValidateIsolatedRootRejectsBroadAndSymlinkedRoots(t *testing.T) {
	for _, root := range []string{string(filepath.Separator), os.TempDir()} {
		if _, err := ValidateIsolatedRoot(root); err == nil {
			t.Fatalf("ValidateIsolatedRoot(%q) succeeded", root)
		}
	}
	parent := t.TempDir()
	link := filepath.Join(parent, "root-link")
	if err := os.Symlink(string(filepath.Separator), link); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateIsolatedRoot(link); err == nil {
		t.Fatal("symlink to filesystem root accepted")
	}
}

func TestValidateIsolatedRootCanonicalizesDedicatedDirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "agent-state")
	got, err := ValidateIsolatedRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(got) || filepath.Base(got) != "agent-state" {
		t.Fatalf("validated root = %q", got)
	}
}

func TestScopedDaemonPaths(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/xdg/cfg")
	t.Setenv("XDG_STATE_HOME", "/xdg/state")
	scope := "project-demo"
	if got, want := DaemonSocketPath(scope), "/xdg/cfg/gate/daemons/project-demo.sock"; got != want {
		t.Fatalf("DaemonSocketPath() = %q, want %q", got, want)
	}
	if got, want := DaemonPIDPath(scope), "/xdg/cfg/gate/daemons/project-demo.pid"; got != want {
		t.Fatalf("DaemonPIDPath() = %q, want %q", got, want)
	}
	if got, want := DaemonLogPath(scope), "/xdg/state/gate/daemons/project-demo.log"; got != want {
		t.Fatalf("DaemonLogPath() = %q, want %q", got, want)
	}
}

func TestIsolatedRuntimeDirUsesShortPath(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace", ".gate-agent")
	t.Setenv("GATE_ISOLATED_ROOT", root)
	want := filepath.Join(validatedRoot(t, root), "run")
	if got := RuntimeDir(); got != want {
		t.Fatalf("RuntimeDir() = %q, want %q", got, want)
	}
	if got := DaemonSocketPath("listener-https-443-http-80"); !strings.HasPrefix(got, want+string(filepath.Separator)) {
		t.Fatalf("DaemonSocketPath() = %q, want below %q", got, want)
	}
}

func validatedRoot(t *testing.T, root string) string {
	t.Helper()
	validated, err := ValidateIsolatedRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	return validated
}

func TestListenerDaemonPaths(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/xdg/cfg")
	t.Setenv("XDG_STATE_HOME", "/xdg/state")
	key := "https-443-http-80"
	if got, want := ListenerDaemonSocketPath(key), "/xdg/cfg/gate/daemons/listener-https-443-http-80.sock"; got != want {
		t.Fatalf("ListenerDaemonSocketPath() = %q, want %q", got, want)
	}
	if got, want := ListenerDaemonPIDPath(key), "/xdg/cfg/gate/daemons/listener-https-443-http-80.pid"; got != want {
		t.Fatalf("ListenerDaemonPIDPath() = %q, want %q", got, want)
	}
	if got, want := ListenerDaemonLogPath(key), "/xdg/state/gate/daemons/listener-https-443-http-80.log"; got != want {
		t.Fatalf("ListenerDaemonLogPath() = %q, want %q", got, want)
	}
}

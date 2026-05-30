package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigDir(t *testing.T) {
	t.Run("xdg set", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", "/xdg/cfg")
		if got, want := ConfigDir(), "/xdg/cfg/prx"; got != want {
			t.Fatalf("ConfigDir() = %q, want %q", got, want)
		}
	})
	t.Run("xdg unset falls back to home", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", "")
		t.Setenv("HOME", "/home/u")
		if got, want := ConfigDir(), filepath.Join("/home/u", ".config", "prx"); got != want {
			t.Fatalf("ConfigDir() = %q, want %q", got, want)
		}
	})
}

func TestDataDir(t *testing.T) {
	t.Run("xdg set", func(t *testing.T) {
		t.Setenv("XDG_DATA_HOME", "/xdg/data")
		if got, want := DataDir(), "/xdg/data/prx"; got != want {
			t.Fatalf("DataDir() = %q, want %q", got, want)
		}
	})
	t.Run("xdg unset", func(t *testing.T) {
		t.Setenv("XDG_DATA_HOME", "")
		t.Setenv("HOME", "/home/u")
		if got, want := DataDir(), filepath.Join("/home/u", ".local", "share", "prx"); got != want {
			t.Fatalf("DataDir() = %q, want %q", got, want)
		}
	})
}

func TestStateDir(t *testing.T) {
	t.Run("xdg overrides per-os", func(t *testing.T) {
		t.Setenv("XDG_STATE_HOME", "/xdg/state")
		if got, want := stateDir("darwin"), "/xdg/state/prx"; got != want {
			t.Fatalf("stateDir(darwin) = %q, want %q", got, want)
		}
		if got, want := stateDir("linux"), "/xdg/state/prx"; got != want {
			t.Fatalf("stateDir(linux) = %q, want %q", got, want)
		}
	})
	t.Run("darwin uses Library/Logs", func(t *testing.T) {
		t.Setenv("XDG_STATE_HOME", "")
		t.Setenv("HOME", "/Users/u")
		if got, want := stateDir("darwin"), filepath.Join("/Users/u", "Library", "Logs", "prx"); got != want {
			t.Fatalf("stateDir(darwin) = %q, want %q", got, want)
		}
	})
	t.Run("linux uses .local/state", func(t *testing.T) {
		t.Setenv("XDG_STATE_HOME", "")
		t.Setenv("HOME", "/home/u")
		if got, want := stateDir("linux"), filepath.Join("/home/u", ".local", "state", "prx"); got != want {
			t.Fatalf("stateDir(linux) = %q, want %q", got, want)
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

func TestSocketPathUnderConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/xdg/cfg")
	if got, want := SocketPath(), "/xdg/cfg/prx/prx.sock"; got != want {
		t.Fatalf("SocketPath() = %q, want %q", got, want)
	}
}

//go:build darwin || linux

package integrationtest

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestStandaloneInstallerRejectsUnsafeInputs(t *testing.T) {
	root := repositoryRoot(t)
	home := t.TempDir()
	tests := []struct {
		name  string
		extra map[string]string
	}{
		{
			name:  "PATH-delimited destination",
			extra: map[string]string{"GATE_BIN_DIR": filepath.Join(home, "bin") + ":unsafe"},
		},
		{
			name:  "relative destination",
			extra: map[string]string{"GATE_BIN_DIR": "relative/bin"},
		},
		{
			name:  "invalid release version",
			extra: map[string]string{"GATE_VERSION": "../../main"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			environment := map[string]string{
				"HOME":  home,
				"PATH":  "/usr/bin:/bin:/usr/sbin:/sbin",
				"SHELL": "/bin/sh",
			}
			for key, value := range test.extra {
				environment[key] = value
			}
			result := runCombined(t, root, environment, "sh", "scripts/install.sh")
			if result.err == nil {
				t.Fatalf("installer accepted unsafe input:\n%s", result.output)
			}
		})
	}
}

func TestStandaloneUninstallerSafetyAndRecovery(t *testing.T) {
	repository := repositoryRoot(t)
	root := t.TempDir()
	home := filepath.Join(root, "home")
	if err := os.Mkdir(home, 0o750); err != nil {
		t.Fatal(err)
	}
	run := func(extra map[string]string) commandResult {
		t.Helper()
		environment := map[string]string{
			"HOME": home,
			"PATH": "/usr/bin:/bin:/usr/sbin:/sbin",
		}
		for key, value := range extra {
			environment[key] = value
		}
		return runCombined(
			t,
			repository,
			environment,
			"sh",
			"scripts/uninstall.sh",
			"--yes",
			"--keep-trust",
		)
	}

	t.Run("rejects control character in isolated root", func(t *testing.T) {
		victim := filepath.Join(root, "victim")
		if err := os.Mkdir(victim, 0o750); err != nil {
			t.Fatal(err)
		}
		attack := filepath.Join(root, "queued") + "\n" + victim
		if result := run(map[string]string{"GATE_ISOLATED_ROOT": attack}); result.err == nil {
			t.Fatal("uninstaller accepted a control character")
		}
		if _, err := os.Stat(victim); err != nil {
			t.Fatalf("victim was removed: %v", err)
		}
	})

	t.Run("rejects filesystem root", func(t *testing.T) {
		if result := run(map[string]string{"GATE_ISOLATED_ROOT": "/"}); result.err == nil {
			t.Fatal("uninstaller accepted filesystem root")
		}
	})

	t.Run("rejects HOME as isolated root", func(t *testing.T) {
		victim := filepath.Join(home, "run", "victim")
		if err := os.MkdirAll(filepath.Dir(victim), 0o750); err != nil {
			t.Fatal(err)
		}
		writeFile(t, victim, "keep\n", 0o600)
		if result := run(map[string]string{"GATE_ISOLATED_ROOT": home}); result.err == nil {
			t.Fatal("uninstaller accepted HOME")
		}
		if got := readFile(t, victim); got != "keep\n" {
			t.Fatalf("victim = %q", got)
		}
	})

	t.Run("empty isolated state", func(t *testing.T) {
		isolated := filepath.Join(root, "isolated-empty")
		config := filepath.Join(isolated, "xdg", "config", "gate")
		if err := os.MkdirAll(config, 0o750); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(config, "exposures.json"), `{"version":1,"records":[]}`+"\n", 0o600)
		result := run(map[string]string{"GATE_ISOLATED_ROOT": isolated})
		if result.err != nil {
			t.Fatalf("uninstall failed: %v\n%s", result.err, result.output)
		}
		requireNotExists(t, config)
	})

	t.Run("missing binary destination", func(t *testing.T) {
		isolated := filepath.Join(root, "isolated-missing-bin")
		config := filepath.Join(isolated, "xdg", "config", "gate")
		if err := os.MkdirAll(config, 0o750); err != nil {
			t.Fatal(err)
		}
		canonical, err := filepath.EvalSymlinks(isolated)
		if err != nil {
			t.Fatal(err)
		}
		isolated = canonical
		config = filepath.Join(isolated, "xdg", "config", "gate")
		result := run(map[string]string{
			"GATE_ISOLATED_ROOT": isolated,
			"GATE_BIN_DIR":       filepath.Join(isolated, "missing", "bin"),
		})
		if result.err != nil {
			t.Fatalf("uninstall failed: %v\n%s", result.err, result.output)
		}
		requireNotExists(t, config)
	})

	if runtime.GOOS == "darwin" {
		t.Run("relative cache does not escape isolated root", func(t *testing.T) {
			isolated := filepath.Join(root, "isolated-relative-cache")
			config := filepath.Join(isolated, "xdg", "config", "gate")
			if err := os.MkdirAll(config, 0o750); err != nil {
				t.Fatal(err)
			}
			result := run(map[string]string{
				"GATE_ISOLATED_ROOT": isolated,
				"XDG_CACHE_HOME":     "relative/cache",
			})
			if result.err != nil {
				t.Fatalf("uninstall failed: %v\n%s", result.err, result.output)
			}
			requireNotExists(t, config)
		})
	}

	t.Run("rejects bin symlink outside isolated root", func(t *testing.T) {
		isolated := filepath.Join(root, "isolated-bin-symlink")
		outside := filepath.Join(root, "outside-bin")
		if err := os.MkdirAll(filepath.Join(isolated, "xdg", "config", "gate"), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(outside, 0o750); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(outside, "gate"), "outside\n", 0o600)
		if err := os.Symlink(outside, filepath.Join(isolated, "bin")); err != nil {
			t.Fatal(err)
		}
		result := run(map[string]string{
			"GATE_ISOLATED_ROOT": isolated,
			"GATE_BIN_DIR":       filepath.Join(isolated, "bin"),
		})
		if result.err == nil {
			t.Fatal("uninstaller accepted bin symlink outside isolated root")
		}
		if got := readFile(t, filepath.Join(outside, "gate")); got != "outside\n" {
			t.Fatalf("outside binary = %q", got)
		}
	})

	t.Run("rejects state symlink outside isolated root", func(t *testing.T) {
		isolated := filepath.Join(root, "isolated-state-symlink")
		outside := filepath.Join(root, "outside-state")
		if err := os.MkdirAll(filepath.Join(outside, "config", "gate"), 0o750); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(outside, "config", "gate", "marker"), "outside\n", 0o600)
		if err := os.Mkdir(isolated, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(isolated, "xdg")); err != nil {
			t.Fatal(err)
		}
		result := run(map[string]string{"GATE_ISOLATED_ROOT": isolated})
		if result.err == nil {
			t.Fatal("uninstaller followed state symlink outside isolated root")
		}
		if got := readFile(t, filepath.Join(outside, "config", "gate", "marker")); got != "outside\n" {
			t.Fatalf("outside state = %q", got)
		}
	})

	t.Run("rejects relative binary destination", func(t *testing.T) {
		if result := run(map[string]string{"GATE_BIN_DIR": "relative/bin"}); result.err == nil {
			t.Fatal("uninstaller accepted relative destination")
		}
	})

	t.Run("active standalone install lock", func(t *testing.T) {
		if runtime.GOOS != "linux" {
			t.Skip("Linux flock contract")
		}
		isolated := filepath.Join(root, "standalone-locked")
		bin := filepath.Join(isolated, "bin")
		if err := os.MkdirAll(filepath.Join(isolated, "xdg", "config", "gate"), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(bin, 0o750); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(bin, "gate.install.lock"), "lock\n", 0o600)
		lockFile(t, filepath.Join(bin, "gate.install.lock"))
		result := run(map[string]string{
			"GATE_ISOLATED_ROOT": isolated,
			"GATE_BIN_DIR":       bin,
		})
		if result.err == nil {
			t.Fatal("uninstaller ignored active install lock")
		}
	})

	t.Run("rejects setup lock inside removable state", func(t *testing.T) {
		if runtime.GOOS != "linux" {
			t.Skip("Linux overlap contract")
		}
		overlap := filepath.Join(root, "cache-overlap")
		config := filepath.Join(overlap, "config", "gate")
		if err := os.MkdirAll(config, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(filepath.Join(overlap, "bin"), 0o750); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(config, "marker"), "keep\n", 0o600)
		if err := os.Symlink(config, filepath.Join(overlap, "cache-link")); err != nil {
			t.Fatal(err)
		}
		result := run(map[string]string{
			"HOME":            filepath.Join(overlap, "home"),
			"XDG_CONFIG_HOME": filepath.Join(overlap, "config"),
			"XDG_DATA_HOME":   filepath.Join(overlap, "data"),
			"XDG_STATE_HOME":  filepath.Join(overlap, "state"),
			"XDG_CACHE_HOME":  filepath.Join(overlap, "cache-link", "new-cache"),
			"GATE_BIN_DIR":    filepath.Join(overlap, "bin"),
		})
		if result.err == nil {
			t.Fatal("uninstaller accepted setup lock inside removable state")
		}
		if got := readFile(t, filepath.Join(config, "marker")); got != "keep\n" {
			t.Fatalf("marker = %q", got)
		}
	})

	t.Run("stale standalone transaction cleanup", func(t *testing.T) {
		isolated := filepath.Join(root, "standalone-transaction")
		bin := filepath.Join(isolated, "bin")
		if err := os.MkdirAll(filepath.Join(isolated, "xdg", "config", "gate"), 0o750); err != nil {
			t.Fatal(err)
		}
		transaction := filepath.Join(bin, "gate.install.transaction")
		if err := os.MkdirAll(transaction, 0o750); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(bin, "gate.install.lock"), "lock\n", 0o600)
		writeFile(t, filepath.Join(transaction, "state"), "state\n", 0o600)
		result := run(map[string]string{
			"GATE_ISOLATED_ROOT": isolated,
			"GATE_BIN_DIR":       bin,
		})
		if result.err != nil {
			t.Fatalf("uninstall failed: %v\n%s", result.err, result.output)
		}
		if _, err := os.Stat(filepath.Join(bin, "gate.install.lock")); err != nil {
			t.Fatalf("install lock was removed: %v", err)
		}
		_, err := os.Stat(transaction)
		if runtime.GOOS == "linux" {
			if !os.IsNotExist(err) {
				t.Fatalf("transaction remains on Linux: %v", err)
			}
		} else if err != nil {
			t.Fatalf("transaction unexpectedly removed on %s: %v", runtime.GOOS, err)
		}
	})

	if strings.Contains(root, "\n") {
		t.Fatal("test root unexpectedly contains a newline")
	}
}

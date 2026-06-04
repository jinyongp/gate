// Package paths resolves gate's on-disk locations. Configuration and data
// follow the XDG Base Directory spec on every platform; logs/state follow XDG
// on Linux and the Apple convention (~/Library/Logs) on macOS.
package paths

import (
	"os"
	"path/filepath"
	"runtime"
)

// appName is the per-tool subdirectory created under every base directory.
const appName = "gate"

// ConfigDir returns the directory for configuration and the global registry
// (default ~/.config/gate).
func ConfigDir() string {
	return base("XDG_CONFIG_HOME", ".config")
}

// DataDir returns the directory for the CA, certificates and ACME state
// (default ~/.local/share/gate).
func DataDir() string {
	return base("XDG_DATA_HOME", filepath.Join(".local", "share"))
}

// StateDir returns the directory for logs and other persistent state.
func StateDir() string {
	return stateDir(runtime.GOOS)
}

// RuntimeDir returns the directory holding the admin control socket.
func RuntimeDir() string {
	return ConfigDir()
}

// DaemonSocketPath returns the scoped daemon admin control socket path.
func DaemonSocketPath(scope string) string {
	return filepath.Join(RuntimeDir(), "daemons", scope+".sock")
}

// DaemonPIDPath returns the scoped daemon pid file path.
func DaemonPIDPath(scope string) string {
	return filepath.Join(ConfigDir(), "daemons", scope+".pid")
}

// DaemonLogPath returns the scoped daemon log file path.
func DaemonLogPath(scope string) string {
	return filepath.Join(StateDir(), "daemons", scope+".log")
}

// ListenerDaemonSocketPath returns the listener daemon admin control socket path.
func ListenerDaemonSocketPath(key string) string {
	return DaemonSocketPath("listener-" + key)
}

// ListenerDaemonPIDPath returns the listener daemon pid file path.
func ListenerDaemonPIDPath(key string) string {
	return DaemonPIDPath("listener-" + key)
}

// ListenerDaemonLogPath returns the listener daemon log file path.
func ListenerDaemonLogPath(key string) string {
	return DaemonLogPath("listener-" + key)
}

// Ensure creates dir (mode 0700) if it does not exist and returns it.
func Ensure(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

func stateDir(goos string) string {
	if v := os.Getenv("XDG_STATE_HOME"); v != "" {
		return filepath.Join(v, appName)
	}
	if goos == "darwin" {
		return filepath.Join(home(), "Library", "Logs", appName)
	}
	return filepath.Join(home(), ".local", "state", appName)
}

func base(env, def string) string {
	if v := os.Getenv(env); v != "" {
		return filepath.Join(v, appName)
	}
	return filepath.Join(home(), def, appName)
}

func home() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return os.TempDir()
	}
	return h
}

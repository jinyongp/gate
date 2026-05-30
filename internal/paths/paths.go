// Package paths resolves prx's on-disk locations. Configuration and data
// follow the XDG Base Directory spec on every platform; logs/state follow XDG
// on Linux and the Apple convention (~/Library/Logs) on macOS.
package paths

import (
	"os"
	"path/filepath"
	"runtime"
)

// appName is the per-tool subdirectory created under every base directory.
const appName = "prx"

// ConfigDir returns the directory for configuration and the global registry
// (default ~/.config/prx).
func ConfigDir() string {
	return base("XDG_CONFIG_HOME", ".config")
}

// DataDir returns the directory for the CA, certificates and ACME state
// (default ~/.local/share/prx).
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

// SocketPath returns the path of the daemon admin control socket.
func SocketPath() string {
	return filepath.Join(RuntimeDir(), "prx.sock")
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

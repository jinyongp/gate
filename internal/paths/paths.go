// Package paths resolves gate's on-disk locations. Configuration and data
// follow the XDG Base Directory spec on every platform; logs/state follow XDG
// on Linux and the Apple convention (~/Library/Logs) on macOS.
package paths

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
)

// appName is the per-tool subdirectory created under every base directory.
const appName = "gate"

// ConfigDir returns the directory for configuration and the global registry
// (default ~/.config/gate).
func ConfigDir() string {
	if root := isolatedRoot(); root != "" {
		return filepath.Join(root, "xdg", "config", appName)
	}
	return base("XDG_CONFIG_HOME", ".config")
}

// DataDir returns the directory for the CA and certificates
// (default ~/.local/share/gate).
func DataDir() string {
	if root := isolatedRoot(); root != "" {
		return filepath.Join(root, "xdg", "data", appName)
	}
	return base("XDG_DATA_HOME", filepath.Join(".local", "share"))
}

// StateDir returns the directory for logs and other persistent state.
func StateDir() string {
	return stateDir(runtime.GOOS)
}

// RuntimeDir returns the directory holding the admin control socket.
func RuntimeDir() string {
	if root := isolatedRoot(); root != "" {
		return filepath.Join(root, "run")
	}
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
	if root := isolatedRoot(); root != "" {
		return filepath.Join(root, "xdg", "state", appName)
	}
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
	if err == nil && h != "" {
		return h
	}
	return fallbackHome(os.TempDir(), os.Getuid())
}

var (
	fallbackHomeMu    sync.Mutex
	fallbackHomeCache = map[string]string{}
)

func fallbackHome(tempDir string, uid int) string {
	fallbackHomeMu.Lock()
	defer fallbackHomeMu.Unlock()
	cacheKey := fmt.Sprintf("%s:%d", tempDir, uid)
	if cached := fallbackHomeCache[cacheKey]; cached != "" {
		return cached
	}

	candidate := filepath.Join(tempDir, fmt.Sprintf("gate-user-%d", uid))
	if err := os.Mkdir(candidate, 0o700); err == nil || os.IsExist(err) {
		if secureFallbackDir(candidate, uid) == nil {
			fallbackHomeCache[cacheKey] = candidate
			return candidate
		}
	}

	if path, err := os.MkdirTemp(tempDir, fmt.Sprintf("gate-user-%d-", uid)); err == nil {
		//nolint:gosec // G302: private directories require the execute bit for traversal.
		_ = os.Chmod(path, 0o700)
		fallbackHomeCache[cacheKey] = path
		return path
	}

	// Last-resort path remains unguessable even if the temp directory cannot be
	// written yet; the first state write will surface the underlying I/O error.
	var nonce [16]byte
	_, _ = rand.Read(nonce[:])
	path := filepath.Join(tempDir, fmt.Sprintf("gate-user-%d-%s", uid, hex.EncodeToString(nonce[:])))
	fallbackHomeCache[cacheKey] = path
	return path
}

func secureFallbackDir(path string, uid int) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("fallback home is not a directory")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != uid {
		return fmt.Errorf("fallback home has unexpected owner")
	}
	if info.Mode().Perm() != 0o700 {
		//nolint:gosec // G302: private directories require the execute bit for traversal.
		if err := os.Chmod(path, 0o700); err != nil {
			return err
		}
	}
	return nil
}

func isolatedRoot() string {
	root := os.Getenv("GATE_ISOLATED_ROOT")
	if root == "" {
		return ""
	}
	validated, err := ValidateIsolatedRoot(root)
	if err == nil {
		return validated
	}
	// Command entry points report the validation error. Internal callers still
	// fail closed instead of deriving paths such as /run from an unsafe root.
	return filepath.Join(os.TempDir(), fmt.Sprintf("gate-invalid-isolated-root-%d", os.Getpid()))
}

// ValidateIsolatedRoot canonicalizes an isolated state root and rejects roots
// that could overlap broad system or user directories during cleanup.
func ValidateIsolatedRoot(root string) (string, error) {
	if root == "" {
		return "", fmt.Errorf("isolated root must not be empty")
	}
	if strings.IndexFunc(root, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return "", fmt.Errorf("isolated root must not contain control characters")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	canonical, err := canonicalPath(abs)
	if err != nil {
		return "", err
	}
	volumeRoot := filepath.Clean(filepath.VolumeName(canonical) + string(filepath.Separator))
	rel, err := filepath.Rel(volumeRoot, canonical)
	if err != nil || rel == "." || rel == "" || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("isolated root is too broad: %s", canonical)
	}
	components := strings.FieldsFunc(rel, func(r rune) bool { return r == filepath.Separator })
	if len(components) < 2 {
		return "", fmt.Errorf("isolated root must be a dedicated nested directory: %s", canonical)
	}
	for _, broad := range []string{os.TempDir(), userHomeDir()} {
		if broad == "" {
			continue
		}
		resolved, resolveErr := canonicalPath(broad)
		if resolveErr == nil && canonical == resolved {
			return "", fmt.Errorf("isolated root must not be a shared directory: %s", canonical)
		}
	}
	return canonical, nil
}

func canonicalPath(path string) (string, error) {
	current := filepath.Clean(path)
	var suffix []string
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return filepath.Clean(resolved), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return filepath.Clean(path), nil
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
}

func userHomeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

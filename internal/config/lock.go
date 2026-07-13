package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

// LockFile serializes gate-owned read/modify/write operations for one config
// path across processes and across isolated gate state roots.
func LockFile(path string) (func(), error) {
	target, err := ResolveFileTarget(path)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("gate-config-locks-%d", os.Getuid()))
	if err := ensurePrivateLockDir(dir); err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(target))
	lockPath := filepath.Join(dir, hex.EncodeToString(sum[:])+".lock")
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(lockPath, 0o600); err != nil {
		_ = file.Close()
		return nil, err
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX); err != nil {
		_ = file.Close()
		return nil, err
	}
	return func() {
		_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
		_ = file.Close()
	}, nil
}

func ensurePrivateLockDir(dir string) error {
	if err := os.Mkdir(dir, 0o700); err != nil && !os.IsExist(err) {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("config lock path is not a private directory: %s", dir)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Getuid() {
		return fmt.Errorf("config lock directory is not owned by the current user: %s", dir)
	}
	if info.Mode().Perm()&0o077 != 0 {
		//nolint:gosec // G302: private directories require the execute bit for traversal.
		if err := os.Chmod(dir, 0o700); err != nil {
			return err
		}
	}
	return nil
}

// ResolveFileTarget returns the regular file that an atomic editor should
// replace. Existing symlinks are resolved so the link itself remains intact.
func ResolveFileTarget(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	_, err = os.Lstat(abs)
	if os.IsNotExist(err) {
		return canonicalMissingPath(abs)
	}
	if err != nil {
		return "", err
	}
	target, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(target)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("config target is not a regular file: %s", target)
	}
	return target, nil
}

func canonicalMissingPath(path string) (string, error) {
	current := path
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

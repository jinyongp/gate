package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"gate/internal/paths"

	"golang.org/x/sys/unix"
)

func lockStateMutation() (func(), error) {
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("gate-state-locks-%d", os.Getuid()))
	if err := ensurePrivateStateLockDir(dir); err != nil {
		return nil, err
	}
	configRoot, err := canonicalStateLockPath(paths.ConfigDir())
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(configRoot))
	path := filepath.Join(dir, hex.EncodeToString(sum[:])+".lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
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

func canonicalStateLockPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	current := abs
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
			return filepath.Clean(abs), nil
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
}

func ensurePrivateStateLockDir(dir string) error {
	if err := os.Mkdir(dir, 0o700); err != nil && !os.IsExist(err) {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("state lock path is not a private directory: %s", dir)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Getuid() {
		return fmt.Errorf("state lock directory is not owned by the current user: %s", dir)
	}
	if info.Mode().Perm()&0o077 != 0 {
		//nolint:gosec // G302: private directories require the execute bit for traversal.
		if err := os.Chmod(dir, 0o700); err != nil {
			return err
		}
	}
	return nil
}

func acquireStateMutation(stderr io.Writer, jsonOut bool) (func(), int) {
	unlock, err := lockStateMutation()
	if err != nil {
		return func() {}, fail(stderr, jsonOut, ExitError, "state_lock", err.Error())
	}
	return unlock, ExitOK
}

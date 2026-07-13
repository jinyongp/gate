// Package fsutil holds small filesystem helpers shared across gate.
package fsutil

import (
	"os"
	"path/filepath"
)

// WriteAtomic writes data to path atomically: it writes a temp file in the
// same directory, fsyncs it, sets perm, then renames it over path. A partial
// or crashed write therefore never leaves path corrupted.
func WriteAtomic(path string, data []byte, perm os.FileMode) (err error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err = tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = os.Chmod(tmpName, perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	dirFile, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = dirFile.Close() }()
	if err = os.Rename(tmpName, path); err != nil {
		return err
	}
	// Rename is the commit point. A directory fsync failure cannot be rolled
	// back safely, so do not report the already-visible write as uncommitted.
	_ = dirFile.Sync()
	return nil
}

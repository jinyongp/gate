package logx

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Default rotation policy.
const (
	DefaultMaxBytes = 10 << 20 // 10 MiB
	DefaultKeep     = 3
)

// RotatingFile is an io.WriteCloser that rotates its file once it exceeds
// MaxBytes, keeping Keep numbered backups (path.1 .. path.N). It avoids any
// external logrotate dependency.
type RotatingFile struct {
	path     string
	maxBytes int64
	keep     int

	mu   sync.Mutex
	f    *os.File
	size int64
}

// NewRotatingFile opens (creating/appending) the log at path.
func NewRotatingFile(path string, maxBytes int64, keep int) (*RotatingFile, error) {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	if keep <= 0 {
		keep = DefaultKeep
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	r := &RotatingFile{path: path, maxBytes: maxBytes, keep: keep}
	if err := r.open(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *RotatingFile) open() error {
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	r.f, r.size = f, info.Size()
	return nil
}

// Write appends p, rotating first if it would exceed maxBytes.
func (r *RotatingFile) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.size+int64(len(p)) > r.maxBytes {
		if err := r.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := r.f.Write(p)
	r.size += int64(n)
	return n, err
}

// Close closes the underlying file.
func (r *RotatingFile) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.f.Close()
}

func (r *RotatingFile) rotate() error {
	if err := r.f.Close(); err != nil {
		return err
	}
	// Shift path.(N-1) -> path.N, ..., path -> path.1. Drop the oldest.
	oldest := fmt.Sprintf("%s.%d", r.path, r.keep)
	_ = os.Remove(oldest)
	for i := r.keep - 1; i >= 1; i-- {
		from := fmt.Sprintf("%s.%d", r.path, i)
		to := fmt.Sprintf("%s.%d", r.path, i+1)
		_ = os.Rename(from, to)
	}
	if err := os.Rename(r.path, r.path+".1"); err != nil && !os.IsNotExist(err) {
		return err
	}
	return r.open()
}

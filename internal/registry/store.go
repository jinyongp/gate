package registry

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"prx/internal/fsutil"

	"golang.org/x/sys/unix"
)

// Store is a file-backed registry guarded by an advisory file lock.
type Store struct {
	path string
}

// Open returns a Store for the registry file at path. The file and its parent
// directory are created lazily on first write.
func Open(path string) *Store {
	return &Store{path: path}
}

// Update runs fn under an exclusive lock with the current registry, then writes
// the result atomically. This is the only safe read-modify-write path.
func (s *Store) Update(fn func(*Registry) error) error {
	unlock, err := s.lock(unix.LOCK_EX)
	if err != nil {
		return err
	}
	defer unlock()

	reg, err := s.read()
	if err != nil {
		return err
	}
	if err := fn(reg); err != nil {
		return err
	}
	return s.write(reg)
}

// Read returns a snapshot of the registry under a shared lock.
func (s *Store) Read() (*Registry, error) {
	unlock, err := s.lock(unix.LOCK_SH)
	if err != nil {
		return nil, err
	}
	defer unlock()
	return s.read()
}

func (s *Store) lock(how int) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return nil, err
	}
	lf, err := os.OpenFile(s.path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(lf.Fd()), how); err != nil {
		_ = lf.Close()
		return nil, err
	}
	return func() {
		_ = unix.Flock(int(lf.Fd()), unix.LOCK_UN)
		_ = lf.Close()
	}, nil
}

func (s *Store) read() (*Registry, error) {
	b, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return New(), nil
	}
	if err != nil {
		return nil, err
	}
	reg := New()
	if err := json.Unmarshal(b, reg); err != nil {
		return nil, err
	}
	migrate(reg)
	return reg, nil
}

func (s *Store) write(reg *Registry) error {
	reg.Version = SchemaVersion
	b, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return fsutil.WriteAtomic(s.path, b, 0o600)
}

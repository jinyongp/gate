package expose

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gate/internal/fsutil"

	"golang.org/x/sys/unix"
)

const StoreVersion = 1

type Record struct {
	Scope       string `json:"scope"`
	Project     string `json:"project,omitempty"`
	Service     string `json:"service"`
	Provider    string `json:"provider"`
	PublicURL   string `json:"public_url"`
	Target      string `json:"target"`
	AuthEnabled bool   `json:"auth_enabled,omitempty"`
	PID         int    `json:"pid,omitempty"`
	Command     string `json:"command,omitempty"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

type fileData struct {
	Version int      `json:"version"`
	Records []Record `json:"records"`
}

type Store struct {
	Path string
}

func (s Store) Read() ([]Record, error) {
	unlock, err := s.lock(unix.LOCK_SH)
	if err != nil {
		return nil, err
	}
	defer unlock()
	return s.read()
}

func (s Store) Write(records []Record) error {
	unlock, err := s.lock(unix.LOCK_EX)
	if err != nil {
		return err
	}
	defer unlock()
	return s.write(records)
}

func (s Store) Upsert(record Record) error {
	return s.Update(func(records []Record) ([]Record, error) {
		now := time.Now().UTC().Format(time.RFC3339)
		record.UpdatedAt = now
		for i := range records {
			if SameKey(records[i], record) {
				if strings.TrimSpace(record.CreatedAt) == "" {
					record.CreatedAt = records[i].CreatedAt
				}
				records[i] = record
				return records, nil
			}
		}
		if strings.TrimSpace(record.CreatedAt) == "" {
			record.CreatedAt = now
		}
		return append(records, record), nil
	})
}

func (s Store) Delete(match Record) (bool, error) {
	removed := false
	err := s.Update(func(records []Record) ([]Record, error) {
		next := records[:0]
		for _, record := range records {
			if SameKey(record, match) {
				removed = true
				continue
			}
			next = append(next, record)
		}
		return next, nil
	})
	return removed, err
}

func (s Store) Update(fn func([]Record) ([]Record, error)) error {
	unlock, err := s.lock(unix.LOCK_EX)
	if err != nil {
		return err
	}
	defer unlock()
	records, err := s.read()
	if err != nil {
		return err
	}
	next, err := fn(records)
	if err != nil {
		return err
	}
	return s.write(next)
}

func (s Store) lock(how int) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return nil, err
	}
	lf, err := os.OpenFile(s.Path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
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

func (s Store) read() ([]Record, error) {
	b, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var data fileData
	if err := json.Unmarshal(b, &data); err != nil {
		return nil, err
	}
	sortRecords(data.Records)
	return data.Records, nil
}

func (s Store) write(records []Record) error {
	sortRecords(records)
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return err
	}
	data := fileData{Version: StoreVersion, Records: records}
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return fsutil.WriteAtomic(s.Path, b, 0o600)
}

func SameKey(a, b Record) bool {
	return a.Scope == b.Scope && a.Project == b.Project && a.Service == b.Service && a.Provider == b.Provider
}

func sortRecords(records []Record) {
	sort.Slice(records, func(i, j int) bool {
		for _, less := range []int{
			strings.Compare(records[i].Scope, records[j].Scope),
			strings.Compare(records[i].Project, records[j].Project),
			strings.Compare(records[i].Service, records[j].Service),
			strings.Compare(records[i].Provider, records[j].Provider),
		} {
			if less < 0 {
				return true
			}
			if less > 0 {
				return false
			}
		}
		return false
	})
}

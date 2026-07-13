package dns

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"gate/internal/fsutil"

	"golang.org/x/sys/unix"
)

const (
	beginMarker = "# >>> gate managed >>>"
	endMarker   = "# <<< gate managed <<<"
	hostsPath   = "/etc/hosts"
)

var runPrivilegedHostsCommand = func(name string, args ...string) error {
	//nolint:gosec // command and args are fixed by gate; no shell is involved.
	return exec.Command(name, args...).Run()
}

// Hosts edits a marked block in an /etc/hosts-style file. It only ever touches
// lines between its own markers; everything else is preserved.
type Hosts struct {
	Path string
}

// DefaultHosts returns a Hosts provider for the system hosts file.
func DefaultHosts() Hosts { return Hosts{Path: hostsPath} }

// Ensure adds a 127.0.0.1 entry for domain inside the managed block.
func (h Hosts) Ensure(domain string) error {
	domain = canonicalDomain(domain)
	return h.edit(func(entries []string) []string {
		if containsDomain(entries, domain) {
			return entries
		}
		return append(entries, fmt.Sprintf("127.0.0.1\t%s", domain))
	})
}

// Remove deletes the entry for domain. If the block becomes empty its markers
// are removed too.
func (h Hosts) Remove(domain string) error {
	domain = canonicalDomain(domain)
	return h.edit(func(entries []string) []string {
		out := entries[:0]
		for _, e := range entries {
			if entryDomain(e) != domain {
				out = append(out, e)
			}
		}
		return out
	})
}

// RemoveManagedBlock removes every gate-managed hosts entry and its markers.
func (h Hosts) RemoveManagedBlock() error {
	return h.edit(func([]string) []string {
		return nil
	})
}

func canonicalDomain(domain string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")
}

func (h Hosts) edit(mutate func(entries []string) []string) error {
	unlock, err := h.lock()
	if err != nil {
		return err
	}
	defer unlock()
	if err := verifyTarget(h.Path); err != nil {
		return err
	}
	b, err := os.ReadFile(h.Path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	lines := splitLines(string(b))
	before, entries, after := splitBlock(lines)

	entries = mutate(entries)

	var out []string
	out = append(out, before...)
	if len(entries) > 0 {
		out = appendBlankLine(out)
		out = append(out, beginMarker)
		out = append(out, entries...)
		out = append(out, endMarker)
		after = trimLeadingBlankLines(after)
		if len(after) > 0 {
			out = append(out, "")
		}
	}
	out = append(out, after...)

	content := strings.Join(out, "\n")
	if content != "" {
		content += "\n"
	}
	if err := verifyTarget(h.Path); err != nil {
		return err
	}
	return h.write([]byte(content))
}

func appendBlankLine(lines []string) []string {
	if len(lines) == 0 || strings.TrimSpace(lines[len(lines)-1]) == "" {
		return lines
	}
	return append(lines, "")
}

func trimLeadingBlankLines(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	return lines
}

func (h Hosts) lock() (func(), error) {
	// O_NOFOLLOW prevents a predictable lock path from being redirected to an
	// unrelated file. The lock carries no data, so read-only mode lets different
	// users coordinate on the machine-wide /tmp lock without sharing writes.
	fd, err := unix.Open(h.lockPath(), unix.O_CREAT|unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0o644)
	if err != nil {
		return nil, err
	}
	// Creation honors the caller's umask. The owner normalizes a new/data-less
	// lock; another user only verifies that the existing lock is world-readable.
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("dns: hosts lock path is not a regular file: %s", h.lockPath())
	}
	if int(stat.Uid) == os.Geteuid() {
		if err := unix.Fchmod(fd, 0o644); err != nil {
			_ = unix.Close(fd)
			return nil, err
		}
	} else if stat.Mode&0o004 == 0 {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("dns: hosts lock owned by another user is not world-readable: %s", h.lockPath())
	}
	lf := os.NewFile(uintptr(fd), h.lockPath())
	if lf == nil {
		_ = unix.Close(fd)
		return nil, errors.New("dns: create hosts lock file handle")
	}
	info, err := lf.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = lf.Close()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("dns: hosts lock path is not a regular file: %s", h.lockPath())
	}
	if err := unix.Flock(int(lf.Fd()), unix.LOCK_EX); err != nil {
		_ = lf.Close()
		return nil, err
	}
	return func() {
		_ = unix.Flock(int(lf.Fd()), unix.LOCK_UN)
		_ = lf.Close()
	}, nil
}

func (h Hosts) lockPath() string {
	if h.Path == hostsPath {
		return filepath.Join(os.TempDir(), "gate-hosts.lock")
	}
	return h.Path + ".gate.lock"
}

func (h Hosts) write(content []byte) error {
	if h.Path == hostsPath {
		return writeSystemHosts(content)
	}
	return fsutil.WriteAtomic(h.Path, content, 0o644)
}

func writeSystemHosts(content []byte) (err error) {
	info, err := os.Stat(hostsPath)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("dns: cannot determine /etc/hosts ownership")
	}
	tmp, err := os.CreateTemp("", "gate-hosts-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	dst := fmt.Sprintf("%s.gate.tmp.%d", hostsPath, os.Getpid())
	defer func() { _ = runSystemHostsCommand("rm", "-f", dst) }()
	mode := fmt.Sprintf("%04o", info.Mode().Perm())
	if err := runSystemHostsCommand("install", "-m", mode, "-o", strconv.FormatUint(uint64(stat.Uid), 10), "-g", strconv.FormatUint(uint64(stat.Gid), 10), tmpName, dst); err != nil {
		return fmt.Errorf("%w: sudo install %s: %w", os.ErrPermission, hostsPath, err)
	}
	if err := runSystemHostsCommand("mv", dst, hostsPath); err != nil {
		return fmt.Errorf("%w: sudo mv %s: %w", os.ErrPermission, hostsPath, err)
	}
	return nil
}

func runSystemHostsCommand(name string, args ...string) error {
	if os.Geteuid() == 0 {
		return runPrivilegedHostsCommand(name, args...)
	}
	return runPrivilegedHostsCommand("sudo", append([]string{"--", name}, args...)...)
}

// verifyTarget hardens against symlink attacks: gate refuses to edit a path that
// is a symlink (an attacker could point it at a sensitive file).
func verifyTarget(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("dns: refusing to edit %s: it is a symlink", path)
	}
	return nil
}

// splitBlock partitions lines into content before the managed block, the entry
// lines inside it, and content after. If no block exists, before = all lines.
func splitBlock(lines []string) (before, entries, after []string) {
	begin, end := -1, -1
	for i, ln := range lines {
		switch strings.TrimSpace(ln) {
		case beginMarker:
			begin = i
		case endMarker:
			end = i
		}
	}
	if begin < 0 || end < 0 || end < begin {
		return lines, nil, nil
	}
	before = append(before, lines[:begin]...)
	for _, ln := range lines[begin+1 : end] {
		if strings.TrimSpace(ln) != "" {
			entries = append(entries, ln)
		}
	}
	after = append(after, lines[end+1:]...)
	return before, entries, after
}

func containsDomain(entries []string, domain string) bool {
	for _, e := range entries {
		if entryDomain(e) == domain {
			return true
		}
	}
	return false
}

// entryDomain extracts the hostname from a "127.0.0.1<tab>domain ..." line.
func entryDomain(entry string) string {
	fields := strings.Fields(entry)
	if len(fields) >= 2 {
		return fields[1]
	}
	return ""
}

func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

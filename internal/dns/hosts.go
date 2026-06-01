package dns

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"prx/internal/fsutil"

	"golang.org/x/sys/unix"
)

const (
	beginMarker = "# >>> prx managed >>>"
	endMarker   = "# <<< prx managed <<<"
	hostsPath   = "/etc/hosts"
)

var runPrivilegedHostsCommand = func(name string, args ...string) error {
	//nolint:gosec // command and args are fixed by prx; no shell is involved.
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
		return append(entries, fmt.Sprintf("127.0.0.1\t%s\t# prx", domain))
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

func canonicalDomain(domain string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")
}

func (h Hosts) edit(mutate func(entries []string) []string) error {
	if err := verifyTarget(h.Path); err != nil {
		return err
	}
	unlock, err := h.lock()
	if err != nil {
		return err
	}
	defer unlock()
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
		out = append(out, beginMarker)
		out = append(out, entries...)
		out = append(out, endMarker)
	}
	out = append(out, after...)

	content := strings.Join(out, "\n")
	if content != "" {
		content += "\n"
	}
	return h.write([]byte(content))
}

func (h Hosts) lock() (func(), error) {
	lf, err := os.OpenFile(h.lockPath(), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
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
		return filepath.Join(os.TempDir(), "prx-hosts.lock")
	}
	return h.Path + ".prx.lock"
}

func (h Hosts) write(content []byte) error {
	if h.Path == hostsPath {
		return writeSystemHosts(content)
	}
	return fsutil.WriteAtomic(h.Path, content, 0o644)
}

func writeSystemHosts(content []byte) (err error) {
	tmp, err := os.CreateTemp("", "prx-hosts-*")
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

	dst := fmt.Sprintf("%s.prx.tmp.%d", hostsPath, os.Getpid())
	defer func() { _ = runPrivilegedHostsCommand("sudo", "rm", "-f", dst) }()
	if err := runPrivilegedHostsCommand("sudo", "install", "-m", "0644", tmpName, dst); err != nil {
		return fmt.Errorf("%w: sudo install %s: %w", os.ErrPermission, hostsPath, err)
	}
	if err := runPrivilegedHostsCommand("sudo", "mv", dst, hostsPath); err != nil {
		return fmt.Errorf("%w: sudo mv %s: %w", os.ErrPermission, hostsPath, err)
	}
	return nil
}

// verifyTarget hardens against symlink attacks: prx refuses to edit a path that
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

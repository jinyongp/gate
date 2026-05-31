package dns

import (
	"fmt"
	"os"
	"strings"

	"prx/internal/fsutil"
)

const (
	beginMarker = "# >>> prx managed >>>"
	endMarker   = "# <<< prx managed <<<"
	hostsPath   = "/etc/hosts"
)

// Hosts edits a marked block in an /etc/hosts-style file. It only ever touches
// lines between its own markers; everything else is preserved.
type Hosts struct {
	Path string
}

// DefaultHosts returns a Hosts provider for the system hosts file.
func DefaultHosts() Hosts { return Hosts{Path: hostsPath} }

// Ensure adds a 127.0.0.1 entry for domain inside the managed block.
func (h Hosts) Ensure(domain string) error {
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

func (h Hosts) edit(mutate func(entries []string) []string) error {
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
		out = append(out, beginMarker)
		out = append(out, entries...)
		out = append(out, endMarker)
	}
	out = append(out, after...)

	content := strings.Join(out, "\n")
	if content != "" {
		content += "\n"
	}
	return fsutil.WriteAtomic(h.Path, []byte(content), 0o644)
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

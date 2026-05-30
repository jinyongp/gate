package port

import (
	"os"
	"strings"

	"github.com/jinyongp/prx/internal/fsutil"
)

// UpsertEnv sets key=value in the dotenv file at path, preserving every other
// line and comment. The previous file is backed up to path+".bak" first. The
// file is created if absent. This is opt-in (prx run injects PORT without it).
func UpsertEnv(path, key, value string) error {
	var lines []string
	if b, err := os.ReadFile(path); err == nil {
		if err := fsutil.WriteAtomic(path+".bak", b, 0o600); err != nil {
			return err
		}
		lines = splitLines(string(b))
	} else if !os.IsNotExist(err) {
		return err
	}

	prefix := key + "="
	found := false
	for i, ln := range lines {
		if strings.HasPrefix(strings.TrimSpace(ln), prefix) {
			lines[i] = key + "=" + value
			found = true
			break
		}
	}
	if !found {
		lines = append(lines, key+"="+value)
	}
	content := strings.Join(lines, "\n") + "\n"
	return fsutil.WriteAtomic(path, []byte(content), 0o600)
}

func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

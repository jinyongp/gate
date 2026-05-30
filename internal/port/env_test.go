package port

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpsertEnvAppendsAndBacksUp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("# config\nFOO=bar\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := UpsertEnv(path, "PORT", "4310"); err != nil {
		t.Fatalf("UpsertEnv: %v", err)
	}
	out, _ := os.ReadFile(path)
	s := string(out)
	for _, want := range []string{"# config", "FOO=bar", "PORT=4310"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in:\n%s", want, s)
		}
	}
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Fatalf("backup not written: %v", err)
	}
}

func TestUpsertEnvReplacesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("PORT=1111\nFOO=bar\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := UpsertEnv(path, "PORT", "4310"); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(path)
	s := string(out)
	if strings.Count(s, "PORT=") != 1 || !strings.Contains(s, "PORT=4310") {
		t.Fatalf("PORT not replaced cleanly:\n%s", s)
	}
}

func TestUpsertEnvCreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := UpsertEnv(path, "PORT", "4310"); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(path)
	if strings.TrimSpace(string(out)) != "PORT=4310" {
		t.Fatalf("unexpected: %q", out)
	}
}

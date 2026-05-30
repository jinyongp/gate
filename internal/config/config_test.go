package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadValidAppliesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, Filename)
	writeFile(t, path, `
[project]
name = "myapp"

[services.web]
domain = "app.example.com"

[services.api]
domain = "api.example.com"
port = 3001
`)
	p, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if p.Name != "myapp" {
		t.Fatalf("name = %q", p.Name)
	}
	if p.Services["web"].TLS != TLSInternal {
		t.Fatalf("web tls = %q, want internal default", p.Services["web"].TLS)
	}
	if p.Services["api"].Port != 3001 {
		t.Fatalf("api port = %d", p.Services["api"].Port)
	}
}

func TestLoadInvalid(t *testing.T) {
	cases := map[string]string{
		"bad domain": `
[services.web]
domain = "no spaces allowed!!"
`,
		"acme without dns": `
[services.web]
domain = "app.example.com"
tls = "acme"
`,
		"bad tls": `
[services.web]
domain = "app.example.com"
tls = "bogus"
`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, Filename)
			writeFile(t, path, body)
			if _, err := Load(path); err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

func TestDiscoverWalksUp(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(nested, 0o750); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(root, "a", Filename)
	writeFile(t, cfg, "[project]\nname=\"x\"\n")

	got, err := Discover(nested)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if got != cfg {
		t.Fatalf("Discover = %q, want %q", got, cfg)
	}
}

func TestDiscoverStopsAtGitRoot(t *testing.T) {
	root := t.TempDir()
	// prx.toml above the git root must NOT be found.
	writeFile(t, filepath.Join(root, Filename), "[project]\n")
	gitRoot := filepath.Join(root, "repo")
	start := filepath.Join(gitRoot, "sub")
	if err := os.MkdirAll(filepath.Join(gitRoot, ".git"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(start, 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := Discover(start); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Discover err = %v, want ErrNotFound", err)
	}
}

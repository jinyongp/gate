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
		"missing project name": `
[services.web]
domain = "web.localhost"
`,
		"empty project name": `
[project]
name = " "

[services.web]
domain = "web.localhost"
`,
		"slash project name": `
[project]
name = "team/demo"

[services.web]
domain = "web.localhost"
`,
		"slash service name": `
[project]
name = "demo"

[services."api/web"]
domain = "api.localhost"
`,
		"reserved service name": `
[project]
name = "demo"

[services.stop]
domain = "stop.localhost"
`,
		"bad domain": `
[project]
name = "demo"

[services.web]
domain = "no spaces allowed!!"
`,
		"empty domain label": `
[project]
name = "demo"

[services.web]
domain = "web.gate....localhost"
`,
		"leading hyphen domain label": `
[project]
name = "demo"

[services.web]
domain = "-web.localhost"
`,
		"underscore domain label": `
[project]
name = "demo"

[services.web]
domain = "web_gate.localhost"
`,
		"acme without dns": `
[project]
name = "demo"

[services.web]
domain = "app.example.com"
tls = "acme"
`,
		"bad tls": `
[project]
name = "demo"

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

func TestLoadExpandsServiceEnvFromEnvFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, Filename)
	writeFile(t, filepath.Join(dir, ".env.local"), `
BASE_DOMAIN=local.stamp.is
WEB_PORT=4306
`)
	writeFile(t, filepath.Join(dir, ".env"), `
BASE_DOMAIN=wrong.example
WEB_PORT=4999
`)
	writeFile(t, path, `
[project]
name = "myapp"
env_files = [".env.local", ".env"]

[services.web]
domain = "web.${BASE_DOMAIN}"
port = "${WEB_PORT}"
`)

	p, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	web := p.Services["web"]
	if web.Domain != "web.local.stamp.is" {
		t.Fatalf("domain = %q", web.Domain)
	}
	if web.Port != 4306 {
		t.Fatalf("port = %d", web.Port)
	}
}

func TestLoadProcessEnvOverridesEnvFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, Filename)
	t.Setenv("WEB_PORT", "5555")
	writeFile(t, filepath.Join(dir, ".env"), "WEB_PORT=4306\n")
	writeFile(t, path, `
[project]
name = "myapp"
env_files = [".env"]

[services.web]
domain = "web.localhost"
port = "${WEB_PORT}"
`)

	p, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if p.Services["web"].Port != 5555 {
		t.Fatalf("port = %d", p.Services["web"].Port)
	}
}

func TestLoadEnvReferenceDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, Filename)
	writeFile(t, path, `
[project]
name = "myapp"

[services.web]
domain = "${WEB_DOMAIN:-web.localhost}"
port = "${WEB_PORT:-4306}"
`)

	p, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if p.Services["web"].Domain != "web.localhost" {
		t.Fatalf("domain = %q", p.Services["web"].Domain)
	}
	if p.Services["web"].Port != 4306 {
		t.Fatalf("port = %d", p.Services["web"].Port)
	}
}

func TestLoadMissingEnvReferenceFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, Filename)
	writeFile(t, path, `
[project]
name = "myapp"

[services.web]
domain = "${WEB_DOMAIN}"
`)

	if _, err := Load(path); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestLoadMissingEnvFileUsesProcessEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, Filename)
	t.Setenv("WEB_DOMAIN", "web.localhost")
	writeFile(t, path, `
[project]
name = "myapp"
env_files = [".env.missing"]

[services.web]
domain = "${WEB_DOMAIN}"
`)

	p, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if p.Services["web"].Domain != "web.localhost" {
		t.Fatalf("domain = %q", p.Services["web"].Domain)
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
	// gate.toml above the git root must NOT be found.
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

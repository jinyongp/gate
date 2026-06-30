package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const withComments = `# top comment — keep me
[project]
name = "myapp"
base = "myapp.localhost"

[services.web]
domain = "app.example.com" # inline note
`

func TestAddServicePreservesComments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, Filename)
	if err := os.WriteFile(path, []byte(withComments), 0o600); err != nil {
		t.Fatal(err)
	}
	err := AddService(path, "api", Service{
		Domain:   "api.example.com",
		Port:     3001,
		Env:      []string{"API_URL"},
		RouteEnv: []string{"PUBLIC_API_URL"},
	})
	if err != nil {
		t.Fatalf("AddService: %v", err)
	}
	out, _ := os.ReadFile(path)
	s := string(out)
	for _, want := range []string{
		"# top comment — keep me",
		"# inline note",
		"[services.api]",
		`domain = "api.example.com"`,
		"port = 3001",
		`env = "API_URL"`,
		`route_env = "PUBLIC_API_URL"`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("output missing %q:\n%s", want, s)
		}
	}
	// Result must still parse and validate.
	if _, err := Load(path); err != nil {
		t.Fatalf("reparse: %v\n%s", err, s)
	}
}

func TestAddServiceRejectsDuplicate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, Filename)
	if err := os.WriteFile(path, []byte(withComments), 0o600); err != nil {
		t.Fatal(err)
	}
	err := AddService(path, "web", Service{Domain: "x.example.com"})
	if !errors.Is(err, ErrServiceExists) {
		t.Fatalf("err = %v, want ErrServiceExists", err)
	}
}

func TestUpsertServicePreservesServiceCommentsAndExtraFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, Filename)
	body := `# top
[project]
name = "myapp"
base = "myapp.localhost"

[services.web]
# keep service comment
domain = "old.example.com" # old inline
port = "${WEB_PORT:-3000}"
custom = "keep"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := UpsertService(path, "web", Service{Domain: "new.example.com", Port: 4312}); err != nil {
		t.Fatalf("UpsertService: %v", err)
	}
	out, _ := os.ReadFile(path)
	s := string(out)
	for _, want := range []string{
		"# keep service comment",
		`domain = "new.example.com"`,
		"port = 4312",
		`custom = "keep"`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("output missing %q:\n%s", want, s)
		}
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("reparse: %v\n%s", err, s)
	}
}

func TestUpsertServiceWritesHostAndRemovesDomain(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, Filename)
	body := `# top
[project]
name = "myapp"
base = "myapp.localhost"

[services.web]
domain = "old.example.com"
port = 3000
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := UpsertService(path, "web", Service{Host: "app", Port: 4312}); err != nil {
		t.Fatalf("UpsertService: %v", err)
	}
	out, _ := os.ReadFile(path)
	s := string(out)
	if strings.Contains(s, `domain = "old.example.com"`) {
		t.Fatalf("domain not removed:\n%s", s)
	}
	for _, want := range []string{`host = "app"`, "port = 4312"} {
		if !strings.Contains(s, want) {
			t.Fatalf("output missing %q:\n%s", want, s)
		}
	}
	p, err := Load(path)
	if err != nil {
		t.Fatalf("reparse: %v\n%s", err, s)
	}
	if p.Services["web"].Domain != "app.myapp.localhost" {
		t.Fatalf("domain = %q", p.Services["web"].Domain)
	}
}

func TestRemoveServiceKeepsOthers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, Filename)
	body := withComments + `
[services.api]
domain = "api.example.com"
port = 3001
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RemoveService(path, "api"); err != nil {
		t.Fatalf("RemoveService: %v", err)
	}
	out, _ := os.ReadFile(path)
	s := string(out)
	if strings.Contains(s, "[services.api]") {
		t.Fatalf("api block not removed:\n%s", s)
	}
	for _, want := range []string{"# top comment — keep me", "[services.web]", "# inline note"} {
		if !strings.Contains(s, want) {
			t.Fatalf("removed unrelated content %q:\n%s", want, s)
		}
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("reparse: %v", err)
	}
}

func TestRemoveServiceAbsentIsNoop(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, Filename)
	if err := os.WriteFile(path, []byte(withComments), 0o600); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	if err := RemoveService(path, "ghost"); err != nil {
		t.Fatalf("RemoveService: %v", err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatalf("file changed on no-op remove")
	}
}

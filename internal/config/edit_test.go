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

func TestUpsertServicePreservesServiceCommentsAndUnchangedFields(t *testing.T) {
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
env = ["KEEP_ME"]
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
		`domain = "new.example.com" # old inline`,
		"port = 4312",
		`env = ["KEEP_ME"]`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("output missing %q:\n%s", want, s)
		}
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("reparse: %v\n%s", err, s)
	}
}

func TestUpsertServiceHandlesCompactAssignments(t *testing.T) {
	path := filepath.Join(t.TempDir(), Filename)
	body := "[project]\nname=\"myapp\"\nbase=\"myapp.localhost\"\n\n[services.web]\ndomain=\"old.example.com\" # keep\nport\t=3000\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := UpsertService(path, "web", Service{Domain: "new.example.com", Port: 4312}); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if strings.Count(s, "domain") != 1 || strings.Count(s, "port") != 1 {
		t.Fatalf("duplicate assignments added:\n%s", s)
	}
	if !strings.Contains(s, `domain="new.example.com" # keep`) || !strings.Contains(s, "port\t=4312") {
		t.Fatalf("compact formatting not preserved:\n%s", s)
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("reparse: %v\n%s", err, s)
	}
}

func TestUpsertServiceRemovesTabbedHostAssignment(t *testing.T) {
	path := filepath.Join(t.TempDir(), Filename)
	body := "[project]\nname = \"myapp\"\nbase = \"myapp.localhost\"\n\n[services.web]\nhost\t=\"old\"\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := UpsertService(path, "web", Service{Domain: "new.example.com"}); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(path)
	if strings.Contains(string(out), "\nhost") {
		t.Fatalf("host assignment not removed:\n%s", out)
	}
}

func TestUpsertServiceRejectsMixedLineEndingsWithoutChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), Filename)
	body := "[project]\r\nname = \"myapp\"\nbase = \"myapp.localhost\"\r\n\n[services.web]\r\nport = 3000\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := UpsertService(path, "web", Service{Port: 4312}); err == nil || !strings.Contains(err.Error(), "mixed line endings") {
		t.Fatalf("error = %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != body {
		t.Fatalf("mixed-EOL file changed:\n%q", after)
	}
}

func TestEditServicePreservesModeAndCRLF(t *testing.T) {
	path := filepath.Join(t.TempDir(), Filename)
	body := "[project]\r\nname = \"myapp\"\r\nbase = \"myapp.localhost\"\r\n\r\n[services.web]\r\n  port   = 3000  # keep\r\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o640); err != nil { //nolint:gosec // G302: fixture verifies preservation of an existing group-readable config mode.
		t.Fatal(err)
	}

	if err := UpsertService(path, "web", Service{Port: 4312}); err != nil {
		t.Fatalf("UpsertService: %v", err)
	}
	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ReplaceAll(string(out), "\r\n", ""), "\n") {
		t.Fatalf("line endings changed: %q", out)
	}
	if !strings.Contains(string(out), "  port   = 4312  # keep\r\n") {
		t.Fatalf("scalar formatting changed:\n%s", out)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o640 {
		t.Fatalf("mode = %o, want 640", got)
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

func TestRemoveServiceFindsQuotedHeaderAndPreservesMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), Filename)
	body := "[project]\r\nname = \"myapp\"\r\nbase = \"myapp.localhost\"\r\n\r\n[services.\"web\"]\r\nport = 3000\r\n\r\n\r\n# keep spacing\r\n[services.api]\r\nport = 3001\r\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := RemoveService(path, "web"); err != nil {
		t.Fatalf("RemoveService: %v", err)
	}
	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if strings.Contains(s, `[services."web"]`) || !strings.Contains(s, "\r\n\r\n# keep spacing\r\n") {
		t.Fatalf("quoted block removal changed unrelated layout:\n%s", s)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %o, want 600", got)
	}
}

func TestRemoveServiceIgnoresHeaderTextInsideMultilineString(t *testing.T) {
	path := filepath.Join(t.TempDir(), Filename)
	body := "[project]\nname = \"demo\"\nbase = \"\"\"not-a-domain\n[services.web]\n\"\"\"\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RemoveService(path, "web"); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != body {
		t.Fatalf("multiline string changed:\n%s", after)
	}
}

func TestUpsertServiceRejectsMultilineScalarWithoutChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), Filename)
	body := "[project]\nname = \"demo\"\n\n[services.web]\ndomain = \"\"\"web.\nlocalhost\"\"\"\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := UpsertService(path, "web", Service{Domain: "new.localhost"}); err == nil {
		t.Fatal("multiline scalar update succeeded")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != body {
		t.Fatalf("invalid multiline update changed file:\n%s", after)
	}
}

func TestUpsertServicePreservesConfigSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real.toml")
	link := filepath.Join(dir, Filename)
	if err := os.WriteFile(target, []byte(withComments), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := UpsertService(link, "web", Service{Domain: "new.localhost", Port: 4312}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("config symlink was replaced")
	}
	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `domain = "new.localhost"`) {
		t.Fatalf("target not updated:\n%s", body)
	}
}

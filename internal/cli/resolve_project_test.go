package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"gate/internal/paths"
	"gate/internal/registry"
)

func TestResolveProjectUsesCanonicalConfigLoader(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "gate.toml")
	body := `[project]
name = "demo"
base = "${GATE_TEST_BASE:-fallback.localhost}"

[services."web-app"]
host = "frontend"

[services.api]
domain = "api.demo.localhost"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GATE_TEST_BASE", "resolved.localhost")

	var out, errb bytes.Buffer
	if code := ResolveProject([]string{"--config", path}, &out, &errb); code != ExitOK {
		t.Fatalf("ResolveProject exit = %d, stderr=%s", code, errb.String())
	}
	var got struct {
		Scope    string                   `json:"scope"`
		Project  string                   `json:"project"`
		Services []resolvedProjectService `json:"services"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Scope != daemonScopeProject || got.Project != "demo" || len(got.Services) != 2 {
		t.Fatalf("result = %+v", got)
	}
	if got.Services[0] != (resolvedProjectService{Service: "api", Domain: "api.demo.localhost"}) ||
		got.Services[1] != (resolvedProjectService{Service: "web-app", Domain: "frontend.resolved.localhost"}) {
		t.Fatalf("services = %+v", got.Services)
	}
}

func TestResolveProjectNamedScopeReadsRegistryWithoutMutation(t *testing.T) {
	isolate(t)
	if err := registryStore().Update(func(reg *registry.Registry) error {
		if err := reg.Reserve(registry.Reservation{Project: "demo", Service: "web", Domain: "web.test", Port: 4400}); err != nil {
			return err
		}
		return reg.Reserve(registry.Reservation{Service: "global", Domain: "global.localhost", Port: 4401, Standalone: true})
	}); err != nil {
		t.Fatal(err)
	}
	registryPath := filepath.Join(paths.ConfigDir(), "registry.json")
	before, err := os.ReadFile(registryPath)
	if err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	if code := ResolveProject([]string{"--project", "demo"}, &out, &errb); code != ExitOK {
		t.Fatalf("ResolveProject exit = %d, stderr=%s", code, errb.String())
	}
	var got struct {
		Services []resolvedProjectService `json:"services"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Services) != 1 || got.Services[0].Domain != "web.test" {
		t.Fatalf("services = %+v", got.Services)
	}
	after, err := os.ReadFile(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("resolver mutated registry")
	}
}

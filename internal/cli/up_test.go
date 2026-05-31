package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"prx/internal/daemon"
	"prx/internal/paths"
	"prx/internal/port"
	"prx/internal/proxy"
	"prx/internal/registry"
)

func setupUpProject(t *testing.T) {
	t.Helper()
	isolate(t)
	dir := t.TempDir()
	toml := `[project]
name = "demo"

[services.web]
domain = "web.demo.localhost"

[services.api]
domain = "api.demo.localhost"
port = 4501
`
	if err := os.WriteFile(filepath.Join(dir, "prx.toml"), []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
}

func TestUpAllocatesAndReserves(t *testing.T) {
	setupUpProject(t)
	var out, errb bytes.Buffer
	if code := Up([]string{"--json"}, &out, &errb); code != ExitOK {
		t.Fatalf("Up exit = %d, stderr=%s", code, errb.String())
	}

	reg, err := registryStore().Read()
	if err != nil {
		t.Fatal(err)
	}
	web, ok := reg.Get(registry.Key("demo", "web"))
	if !ok || !web.Active {
		t.Fatalf("web reservation missing/inactive: %+v", web)
	}
	if web.Port < port.DefaultPool.Min || web.Port > port.DefaultPool.Max {
		t.Fatalf("web port %d outside pool", web.Port)
	}
	api, ok := reg.Get(registry.Key("demo", "api"))
	if !ok || api.Port != 4501 {
		t.Fatalf("api should keep fixed port 4501: %+v", api)
	}
}

func TestUpIsStablePort(t *testing.T) {
	setupUpProject(t)
	var out, errb bytes.Buffer
	_ = Up(nil, &out, &errb)
	reg, _ := registryStore().Read()
	first := reg.Services[registry.Key("demo", "web")].Port

	_ = Up(nil, &out, &errb)
	reg, _ = registryStore().Read()
	second := reg.Services[registry.Key("demo", "web")].Port
	if first != second {
		t.Fatalf("port not stable across up: %d != %d", first, second)
	}
}

func TestDownDeactivatesKeepsReservation(t *testing.T) {
	setupUpProject(t)
	var out, errb bytes.Buffer
	_ = Up(nil, &out, &errb)
	if code := Down(nil, &out, &errb); code != ExitOK {
		t.Fatalf("Down exit = %d", code)
	}
	reg, _ := registryStore().Read()
	web := reg.Services[registry.Key("demo", "web")]
	if web.Active {
		t.Fatal("web still active after down")
	}
	if web.Port == 0 {
		t.Fatal("reservation lost after down")
	}
}

func TestUpDownReloadRunningDaemon(t *testing.T) {
	setupUpProject(t)
	shortConfigDir, err := os.MkdirTemp("/tmp", "prx-cli-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(shortConfigDir) })
	t.Setenv("XDG_CONFIG_HOME", shortConfigDir)
	srv := proxy.New(nil, nil)
	stop, err := daemon.ServeAdmin(context.Background(), paths.SocketPath(), srv)
	if err != nil {
		t.Fatalf("ServeAdmin: %v", err)
	}
	defer stop()

	var out, errb bytes.Buffer
	if code := Up([]string{"--json"}, &out, &errb); code != ExitOK {
		t.Fatalf("Up exit = %d, stderr=%s", code, errb.String())
	}
	var up struct {
		Reloaded bool `json:"reloaded"`
	}
	if err := json.Unmarshal(out.Bytes(), &up); err != nil {
		t.Fatalf("up json: %v\n%s", err, out.String())
	}
	if !up.Reloaded || srv.RouteCount() != 2 {
		t.Fatalf("up reloaded=%v route count=%d", up.Reloaded, srv.RouteCount())
	}

	out.Reset()
	if code := Down([]string{"--json"}, &out, &errb); code != ExitOK {
		t.Fatalf("Down exit = %d, stderr=%s", code, errb.String())
	}
	if srv.RouteCount() != 0 {
		t.Fatalf("down route count = %d, want 0", srv.RouteCount())
	}
}

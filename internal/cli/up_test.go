package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gate/internal/daemon"
	"gate/internal/paths"
	"gate/internal/port"
	"gate/internal/proxy"
	"gate/internal/registry"
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
	if err := os.WriteFile(filepath.Join(dir, "gate.toml"), []byte(toml), 0o600); err != nil {
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

func TestUpRejectsInvalidDNSMode(t *testing.T) {
	setupUpProject(t)
	var out, errb bytes.Buffer
	code := Up([]string{"--json", "--dns", "bogus"}, &out, &errb)
	if code != ExitUsage {
		t.Fatalf("Up exit = %d, want %d; stderr=%s", code, ExitUsage, errb.String())
	}
	if !strings.Contains(errb.String(), "bad_dns") {
		t.Fatalf("missing bad_dns error:\n%s", errb.String())
	}
	reg, err := registryStore().Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Services) != 0 {
		t.Fatalf("registry changed after invalid dns: %+v", reg.Services)
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

func TestUpUpdatesExistingFixedPort(t *testing.T) {
	setupUpProject(t)
	if err := registryStore().Update(func(reg *registry.Registry) error {
		return reg.Reserve(registry.Reservation{
			Project: "demo",
			Service: "api",
			Domain:  "api.demo.localhost",
			Port:    4301,
			Active:  true,
		})
	}); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	if code := Up(nil, &out, &errb); code != ExitOK {
		t.Fatalf("Up exit = %d, stderr=%s", code, errb.String())
	}
	reg, err := registryStore().Read()
	if err != nil {
		t.Fatal(err)
	}
	api, ok := reg.Get(registry.Key("demo", "api"))
	if !ok || api.Port != 4501 {
		t.Fatalf("api should update to fixed port 4501: %+v", api)
	}
}

func TestUpPrunesMissingConfigReservationBeforeConflict(t *testing.T) {
	setupUpProject(t)
	missing := filepath.Join(t.TempDir(), "missing", "gate.toml")
	if err := registryStore().Update(func(reg *registry.Registry) error {
		return reg.Reserve(registry.Reservation{
			Project:    "old",
			Service:    "web",
			Domain:     "web.demo.localhost",
			Port:       4300,
			ConfigPath: missing,
		})
	}); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	if code := Up(nil, &out, &errb); code != ExitOK {
		t.Fatalf("Up exit = %d, stderr=%s", code, errb.String())
	}
	reg, err := registryStore().Read()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Get(registry.Key("old", "web")); ok {
		t.Fatal("stale reservation still present")
	}
	if _, ok := reg.Get(registry.Key("demo", "web")); !ok {
		t.Fatal("new reservation missing")
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
	shortConfigDir, err := os.MkdirTemp("/tmp", "gate-cli-")
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

func TestUpDaemonStartsAndReloads(t *testing.T) {
	setupUpProject(t)
	shortConfigDir, err := os.MkdirTemp("/tmp", "gate-cli-")
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
	if code := Up([]string{"--daemon", "--https-addr", "127.0.0.1:0", "--http-addr", "127.0.0.1:0", "--json"}, &out, &errb); code != ExitOK {
		t.Fatalf("Up exit = %d, stderr=%s", code, errb.String())
	}

	var up struct {
		Reloaded bool `json:"reloaded"`
	}
	if err := json.Unmarshal(out.Bytes(), &up); err != nil {
		t.Fatalf("up json: %v\n%s", err, out.String())
	}
	if !up.Reloaded {
		t.Fatal("up --daemon did not reload daemon")
	}
	if srv.RouteCount() != 2 {
		t.Fatalf("route count = %d, want 2", srv.RouteCount())
	}
}

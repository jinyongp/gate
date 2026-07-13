package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"gate/internal/daemon"
	"gate/internal/dns"
	"gate/internal/expose"
	"gate/internal/listener"
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

func TestUpEmptyProjectJSONUsesEmptyServicesArray(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gate.toml"), []byte("[project]\nname = \"empty\"\nbase = \"empty.localhost\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	var out, errb bytes.Buffer
	if code := Up([]string{"--json"}, &out, &errb); code != ExitOK {
		t.Fatalf("Up exit = %d, stderr=%s", code, errb.String())
	}
	var got struct {
		Services []any `json:"services"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Services == nil || len(got.Services) != 0 {
		t.Fatalf("services = %#v, want []", got.Services)
	}
}

func TestUpUsesExplicitConfigPath(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "dev.gate.toml")
	toml := `[project]
name = "demo"

[services.web]
domain = "web.demo.localhost"
port = 4400
`
	if err := os.WriteFile(configPath, []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	if code := Up([]string{"--config", configPath, "--json"}, &out, &errb); code != ExitOK {
		t.Fatalf("Up exit = %d, stderr=%s", code, errb.String())
	}
	reg, err := registryStore().Read()
	if err != nil {
		t.Fatal(err)
	}
	res, ok := reg.Get(registry.Key("demo", "web"))
	if !ok || !res.Active || res.Port != 4400 {
		t.Fatalf("reservation = %+v, ok=%v", res, ok)
	}
	if res.ConfigPath != configPath {
		t.Fatalf("ConfigPath = %q, want %q", res.ConfigPath, configPath)
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

func TestUpRejectsInvalidListenerBeforeRegistryMutation(t *testing.T) {
	setupUpProject(t)
	var out, errb bytes.Buffer
	if code := Up([]string{"--https-addr", "bad host:443"}, &out, &errb); code != ExitUsage {
		t.Fatalf("Up exit = %d, want usage; stderr=%s", code, errb.String())
	}
	reg, err := registryStore().Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Services) != 0 {
		t.Fatalf("registry mutated: %+v", reg.Services)
	}
}

func TestUpReconcilesRemovedServiceAndProjectRename(t *testing.T) {
	setupUpProject(t)
	oldSelect := selectDNSProvider
	t.Cleanup(func() { selectDNSProvider = oldSelect })
	var removed []string
	selectDNSProvider = func(_, _ string) dns.Provider {
		return fakeDNSProvider{remove: func(domain string) error { removed = append(removed, domain); return nil }}
	}
	var out, errb bytes.Buffer
	if code := Up(nil, &out, &errb); code != ExitOK {
		t.Fatalf("first Up exit = %d, stderr=%s", code, errb.String())
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	body := "[project]\nname = \"renamed\"\n\n[services.web]\ndomain = \"web.demo.localhost\"\n"
	if err := os.WriteFile(filepath.Join(cwd, "gate.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errb.Reset()
	if code := Up(nil, &out, &errb); code != ExitOK {
		t.Fatalf("second Up exit = %d, stderr=%s", code, errb.String())
	}
	reg, err := registryStore().Read()
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{registry.Key("demo", "web"), registry.Key("demo", "api")} {
		if _, ok := reg.Get(key); ok {
			t.Fatalf("stale reservation remains: %s", key)
		}
	}
	if _, ok := reg.Get(registry.Key("renamed", "web")); !ok {
		t.Fatal("renamed project reservation missing")
	}
	if !slices.Contains(removed, "api.demo.localhost") {
		t.Fatalf("removed DNS = %v", removed)
	}
}

func TestDownDeactivatesConfigOwnedRemovedService(t *testing.T) {
	setupUpProject(t)
	oldSelect := selectDNSProvider
	t.Cleanup(func() { selectDNSProvider = oldSelect })
	selectDNSProvider = func(_, _ string) dns.Provider { return fakeDNSProvider{} }
	var out, errb bytes.Buffer
	if code := Up(nil, &out, &errb); code != ExitOK {
		t.Fatalf("Up exit = %d, stderr=%s", code, errb.String())
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	body := "[project]\nname = \"demo\"\n\n[services.web]\ndomain = \"web.demo.localhost\"\n"
	if err := os.WriteFile(filepath.Join(cwd, "gate.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := Down(nil, &out, &errb); code != ExitOK {
		t.Fatalf("Down exit = %d, stderr=%s", code, errb.String())
	}
	reg, err := registryStore().Read()
	if err != nil {
		t.Fatal(err)
	}
	api, ok := reg.Get(registry.Key("demo", "api"))
	if !ok || api.Active {
		t.Fatalf("removed config service was not deactivated: %+v", api)
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

func TestUpPrunesActiveReservationReloadsOldListenerAndRemovesDNS(t *testing.T) {
	setupUpProject(t)
	oldPair := listener.FromFlags("127.0.0.1:19001", "127.0.0.1:19002")
	missing := filepath.Join(t.TempDir(), "missing", "gate.toml")
	stale := registry.Reservation{
		Project: "old", Service: "web", Domain: "old.test", Port: 4300,
		DNS: "hosts", Active: true, ConfigPath: missing,
	}
	stale.SetListenerPair(oldPair)
	if err := registryStore().Update(func(reg *registry.Registry) error {
		return reg.Reserve(stale)
	}); err != nil {
		t.Fatal(err)
	}

	oldSelect := selectDNSProvider
	oldSetRoutes := setListenerRoutesFunc
	t.Cleanup(func() {
		selectDNSProvider = oldSelect
		setListenerRoutesFunc = oldSetRoutes
	})
	var removed []string
	selectDNSProvider = func(_, _ string) dns.Provider {
		return fakeDNSProvider{remove: func(domain string) error {
			removed = append(removed, domain)
			return nil
		}}
	}
	var refs []listenerDaemonRef
	setListenerRoutesFunc = func(ref listenerDaemonRef, _ []proxy.Route) error {
		refs = append(refs, ref)
		return nil
	}

	var out, errb bytes.Buffer
	if code := Up(nil, &out, &errb); code != ExitOK {
		t.Fatalf("Up exit = %d, stderr=%s", code, errb.String())
	}
	if len(refs) != 1 || refs[0].fileKey() != listenerRefFor(oldPair).fileKey() {
		t.Fatalf("reloaded refs = %+v, want old listener", refs)
	}
	if !reflect.DeepEqual(removed, []string{"old.test"}) {
		t.Fatalf("removed DNS = %v", removed)
	}
}

func TestUpPreservesUninspectableUnrelatedReservation(t *testing.T) {
	setupUpProject(t)
	dir := t.TempDir()
	loop := filepath.Join(dir, "loop")
	if err := os.Symlink(loop, loop); err != nil {
		t.Fatal(err)
	}
	if err := registryStore().Update(func(reg *registry.Registry) error {
		return reg.Reserve(registry.Reservation{
			Project: "old", Service: "legacy", Domain: "legacy.old.localhost", Port: 4600, ConfigPath: loop,
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
	if _, ok := reg.Get(registry.Key("old", "legacy")); !ok {
		t.Fatal("uninspectable unrelated reservation was pruned")
	}
}

func TestUpPreservesUnrelatedStaleExposedReservation(t *testing.T) {
	setupUpProject(t)
	missing := filepath.Join(t.TempDir(), "missing", "gate.toml")
	if err := registryStore().Update(func(reg *registry.Registry) error {
		return reg.Reserve(registry.Reservation{
			Project: "old", Service: "legacy", Domain: "legacy.old.localhost", Port: 4600, ConfigPath: missing,
		})
	}); err != nil {
		t.Fatal(err)
	}
	if err := exposureStore().Upsert(expose.Record{
		Scope: daemonScopeProject, Project: "old", Service: "legacy", Provider: expose.ProviderLAN,
		Target: "legacy.old.localhost", PublicURL: "https://legacy.old.localhost.local",
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
	if _, ok := reg.Get(registry.Key("old", "legacy")); !ok {
		t.Fatal("stale exposed unrelated reservation was pruned")
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

func TestUpDownGlobalScopeFromRegistryState(t *testing.T) {
	isolate(t)
	if err := registryStore().Update(func(reg *registry.Registry) error {
		return reg.Reserve(registry.Reservation{Service: "web", Domain: "web.localhost", Port: 4400, Standalone: true})
	}); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	if code := Up([]string{"-g", "--json"}, &out, &errb); code != ExitOK {
		t.Fatalf("Up -g exit = %d, stderr=%s", code, errb.String())
	}
	reg, err := registryStore().Read()
	if err != nil {
		t.Fatal(err)
	}
	res := reg.Services[registry.Key("", "web")]
	if !res.Active {
		t.Fatalf("global reservation not active: %+v", res)
	}

	out.Reset()
	if code := Down([]string{"-g", "--json"}, &out, &errb); code != ExitOK {
		t.Fatalf("Down -g exit = %d, stderr=%s", code, errb.String())
	}
	reg, err = registryStore().Read()
	if err != nil {
		t.Fatal(err)
	}
	res = reg.Services[registry.Key("", "web")]
	if res.Active || res.Port != 4400 {
		t.Fatalf("global reservation not deactivated/preserved: %+v", res)
	}
}

func TestUpDownNamedProjectScopeFromRegistryState(t *testing.T) {
	isolate(t)
	if err := registryStore().Update(func(reg *registry.Registry) error {
		return reg.Reserve(registry.Reservation{Project: "demo", Service: "web", Domain: "web.localhost", Port: 4400})
	}); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	if code := Up([]string{"-p", "demo", "--json"}, &out, &errb); code != ExitOK {
		t.Fatalf("Up -p demo exit = %d, stderr=%s", code, errb.String())
	}
	reg, err := registryStore().Read()
	if err != nil {
		t.Fatal(err)
	}
	res := reg.Services[registry.Key("demo", "web")]
	if !res.Active {
		t.Fatalf("project reservation not active: %+v", res)
	}

	out.Reset()
	if code := Down([]string{"-p", "demo", "--json"}, &out, &errb); code != ExitOK {
		t.Fatalf("Down -p demo exit = %d, stderr=%s", code, errb.String())
	}
	reg, err = registryStore().Read()
	if err != nil {
		t.Fatal(err)
	}
	res = reg.Services[registry.Key("demo", "web")]
	if res.Active || res.Port != 4400 {
		t.Fatalf("project reservation not deactivated/preserved: %+v", res)
	}
}

func TestUpGlobalRestoresRegistryWhenDNSFails(t *testing.T) {
	isolate(t)
	if err := registryStore().Update(func(reg *registry.Registry) error {
		return reg.Reserve(registry.Reservation{Service: "web", Domain: "web.localhost", Port: 4400, Standalone: true})
	}); err != nil {
		t.Fatal(err)
	}
	oldSelect := selectDNSProvider
	t.Cleanup(func() { selectDNSProvider = oldSelect })
	selectDNSProvider = func(_, _ string) dns.Provider {
		return fakeDNSProvider{ensure: func(string) error { return errors.New("dns failed") }}
	}

	var out, errb bytes.Buffer
	if code := Up([]string{"-g"}, &out, &errb); code != ExitError {
		t.Fatalf("Up -g exit = %d, stderr=%s", code, errb.String())
	}
	reg, err := registryStore().Read()
	if err != nil {
		t.Fatal(err)
	}
	res := reg.Services[registry.Key("", "web")]
	if res.Active || res.DNS != "" {
		t.Fatalf("reservation not restored: %+v", res)
	}
}

func TestUpCurrentProjectRestoresRegistryAndDNSWhenDNSFails(t *testing.T) {
	setupUpProject(t)
	oldSelect := selectDNSProvider
	t.Cleanup(func() { selectDNSProvider = oldSelect })
	var ensured, removed []string
	selectDNSProvider = func(_, _ string) dns.Provider {
		return fakeDNSProvider{
			ensure: func(domain string) error {
				if domain == "web.demo.localhost" {
					return errors.New("dns failed")
				}
				ensured = append(ensured, domain)
				return nil
			},
			remove: func(domain string) error {
				removed = append(removed, domain)
				return nil
			},
		}
	}

	var out, errb bytes.Buffer
	if code := Up(nil, &out, &errb); code != ExitError {
		t.Fatalf("Up exit = %d, stderr=%s", code, errb.String())
	}
	reg, err := registryStore().Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Services) != 0 {
		t.Fatalf("registry not restored: %+v", reg.Services)
	}
	if strings.Join(ensured, ",") != "api.demo.localhost" || strings.Join(removed, ",") != "api.demo.localhost" {
		t.Fatalf("ensured=%v removed=%v", ensured, removed)
	}
}

func TestUpCurrentProjectRestoresRegistryAndDNSWhenReloadFails(t *testing.T) {
	setupUpProject(t)
	shortConfigDir, err := os.MkdirTemp("/tmp", "gate-cli-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(shortConfigDir) })
	t.Setenv("XDG_CONFIG_HOME", shortConfigDir)
	t.Setenv("XDG_STATE_HOME", shortConfigDir)
	srv := proxy.New(nil, nil)
	stop, err := daemon.ServeAdmin(context.Background(), defaultListenerRef().socketPath(), srv)
	if err != nil {
		t.Fatalf("ServeAdmin: %v", err)
	}
	defer stop()

	oldSelect := selectDNSProvider
	oldSetRoutes := setListenerRoutesFunc
	t.Cleanup(func() {
		selectDNSProvider = oldSelect
		setListenerRoutesFunc = oldSetRoutes
	})
	var removed []string
	selectDNSProvider = func(_, _ string) dns.Provider {
		return fakeDNSProvider{
			remove: func(domain string) error {
				removed = append(removed, domain)
				return nil
			},
		}
	}
	setListenerRoutesFunc = func(_ listenerDaemonRef, _ []proxy.Route) error { return errors.New("reload failed") }

	var out, errb bytes.Buffer
	if code := Up(nil, &out, &errb); code != ExitError {
		t.Fatalf("Up exit = %d, stderr=%s", code, errb.String())
	}
	reg, err := registryStore().Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Services) != 0 {
		t.Fatalf("registry not restored: %+v", reg.Services)
	}
	if strings.Join(removed, ",") != "web.demo.localhost,api.demo.localhost" {
		t.Fatalf("removed DNS = %v", removed)
	}
}

func TestUpCurrentProjectKeepsExistingActiveDNSWhenReloadFails(t *testing.T) {
	setupUpProject(t)
	shortConfigDir, err := os.MkdirTemp("/tmp", "gate-cli-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(shortConfigDir) })
	t.Setenv("XDG_CONFIG_HOME", shortConfigDir)
	t.Setenv("XDG_STATE_HOME", shortConfigDir)
	previous := registry.Reservation{
		Project: "demo", Service: "web", Domain: "web.demo.localhost", Port: 4400,
		DNS: "localhost", Active: true,
	}
	if err := registryStore().Update(func(reg *registry.Registry) error {
		return reg.Reserve(previous)
	}); err != nil {
		t.Fatal(err)
	}
	stop, err := daemon.ServeAdmin(context.Background(), defaultListenerRef().socketPath(), proxy.New(nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	oldSelect := selectDNSProvider
	oldSetRoutes := setListenerRoutesFunc
	t.Cleanup(func() {
		selectDNSProvider = oldSelect
		setListenerRoutesFunc = oldSetRoutes
	})
	var ensured, removed []string
	selectDNSProvider = func(_, _ string) dns.Provider {
		return fakeDNSProvider{
			ensure: func(domain string) error {
				ensured = append(ensured, domain)
				return nil
			},
			remove: func(domain string) error {
				removed = append(removed, domain)
				return nil
			},
		}
	}
	setListenerRoutesFunc = func(_ listenerDaemonRef, _ []proxy.Route) error {
		return errors.New("reload failed")
	}

	var out, errb bytes.Buffer
	if code := Up(nil, &out, &errb); code != ExitError {
		t.Fatalf("Up exit = %d, want reload failure; stderr=%s", code, errb.String())
	}
	reg, err := registryStore().Read()
	if err != nil {
		t.Fatal(err)
	}
	if got := reg.Services[registry.Key("demo", "web")]; !reflect.DeepEqual(got, previous) {
		t.Fatalf("reservation not restored: %+v", got)
	}
	if len(removed) != 1 || removed[0] != "api.demo.localhost" {
		t.Fatalf("removed DNS = %v; existing web DNS must remain", removed)
	}
	if len(ensured) < 3 || ensured[len(ensured)-1] != "web.demo.localhost" {
		t.Fatalf("restored DNS not ensured: %v", ensured)
	}
}

func TestUpExistingScopeMovesRoutesFromOldListener(t *testing.T) {
	isolate(t)
	oldPair := listener.FromFlags("127.0.0.1:19001", "127.0.0.1:19002")
	nextPair := listener.FromFlags("127.0.0.1:19003", "127.0.0.1:19004")
	shortConfigDir, err := os.MkdirTemp("/tmp", "gate-cli-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(shortConfigDir) })
	t.Setenv("XDG_CONFIG_HOME", shortConfigDir)
	t.Setenv("XDG_STATE_HOME", shortConfigDir)
	res := registry.Reservation{
		Service: "web", Domain: "web.localhost", Port: 4400,
		DNS: "localhost", Standalone: true, Active: true,
	}
	res.SetListenerPair(oldPair)
	if err := registryStore().Update(func(reg *registry.Registry) error { return reg.Reserve(res) }); err != nil {
		t.Fatal(err)
	}

	stop, err := daemon.ServeAdmin(context.Background(), listenerRefFor(nextPair).socketPath(), proxy.New(nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	oldSelect := selectDNSProvider
	oldSetRoutes := setListenerRoutesFunc
	t.Cleanup(func() {
		selectDNSProvider = oldSelect
		setListenerRoutesFunc = oldSetRoutes
	})
	selectDNSProvider = func(_, _ string) dns.Provider { return fakeDNSProvider{} }
	var refs []listenerDaemonRef
	var routesByRef = map[string][]proxy.Route{}
	setListenerRoutesFunc = func(ref listenerDaemonRef, routes []proxy.Route) error {
		refs = append(refs, ref)
		routesByRef[ref.fileKey()] = append([]proxy.Route(nil), routes...)
		return nil
	}

	args := []string{"-g", "--https-addr", nextPair.HTTPSAddr, "--http-addr", nextPair.HTTPAddr}
	var out, errb bytes.Buffer
	if code := Up(args, &out, &errb); code != ExitOK {
		t.Fatalf("Up exit = %d, stderr=%s", code, errb.String())
	}
	if len(refs) != 2 || refs[0].fileKey() != listenerRefFor(nextPair).fileKey() || refs[1].fileKey() != listenerRefFor(oldPair).fileKey() {
		t.Fatalf("reload refs = %+v", refs)
	}
	if got := routesByRef[listenerRefFor(nextPair).fileKey()]; len(got) != 1 || got[0].Domain != res.Domain {
		t.Fatalf("new listener routes = %+v", got)
	}
	if got := routesByRef[listenerRefFor(oldPair).fileKey()]; len(got) != 0 {
		t.Fatalf("old listener routes = %+v, want empty", got)
	}
}

func TestUpExistingScopesRestoreRegistryWhenReloadFails(t *testing.T) {
	oldPair := listener.FromFlags("127.0.0.1:19001", "127.0.0.1:19002")
	nextPair := listener.FromFlags("127.0.0.1:19003", "127.0.0.1:19004")
	cases := []struct {
		name string
		args []string
		key  string
		res  registry.Reservation
	}{
		{
			name: "global",
			args: []string{"-g", "--https-addr", nextPair.HTTPSAddr, "--http-addr", nextPair.HTTPAddr},
			key:  registry.Key("", "web"),
			res:  registry.Reservation{Service: "web", Domain: "web.localhost", Port: 4400, TLS: "internal", ConfigPath: "/tmp/gate-global.toml", Standalone: true, Active: true},
		},
		{
			name: "named project",
			args: []string{"-p", "demo", "--https-addr", nextPair.HTTPSAddr, "--http-addr", nextPair.HTTPAddr},
			key:  registry.Key("demo", "web"),
			res:  registry.Reservation{Project: "demo", Service: "web", Domain: "web.localhost", Port: 4400, TLS: "internal", ConfigPath: "/tmp/gate-demo.toml", Active: true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolate(t)
			shortConfigDir, err := os.MkdirTemp("/tmp", "gate-cli-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(shortConfigDir) })
			t.Setenv("XDG_CONFIG_HOME", shortConfigDir)
			t.Setenv("XDG_STATE_HOME", shortConfigDir)
			tc.res.SetListenerPair(oldPair)
			if err := registryStore().Update(func(reg *registry.Registry) error {
				return reg.Reserve(tc.res)
			}); err != nil {
				t.Fatal(err)
			}
			srv := proxy.New(nil, nil)
			stop, err := daemon.ServeAdmin(context.Background(), listenerRefFor(nextPair).socketPath(), srv)
			if err != nil {
				t.Fatalf("ServeAdmin: %v", err)
			}
			defer stop()
			oldSelect := selectDNSProvider
			oldSetRoutes := setListenerRoutesFunc
			t.Cleanup(func() {
				selectDNSProvider = oldSelect
				setListenerRoutesFunc = oldSetRoutes
			})
			selectDNSProvider = func(_, _ string) dns.Provider { return fakeDNSProvider{} }
			var refs []listenerDaemonRef
			applied := map[string][]proxy.Route{}
			nextAttempts := 0
			setListenerRoutesFunc = func(ref listenerDaemonRef, routes []proxy.Route) error {
				refs = append(refs, ref)
				applied[ref.fileKey()] = append([]proxy.Route(nil), routes...)
				if ref.fileKey() == listenerRefFor(nextPair).fileKey() {
					nextAttempts++
					if nextAttempts == 1 {
						return errors.New("reload failed")
					}
				}
				return nil
			}

			var out, errb bytes.Buffer
			if code := Up(tc.args, &out, &errb); code != ExitError {
				t.Fatalf("Up exit = %d, stderr=%s", code, errb.String())
			}
			reg, err := registryStore().Read()
			if err != nil {
				t.Fatal(err)
			}
			res := reg.Services[tc.key]
			if !reflect.DeepEqual(res, tc.res) {
				t.Fatalf("reservation not restored: %+v", res)
			}
			if len(refs) != 3 ||
				refs[0].fileKey() != listenerRefFor(nextPair).fileKey() ||
				refs[1].fileKey() != listenerRefFor(oldPair).fileKey() ||
				refs[2].fileKey() != listenerRefFor(nextPair).fileKey() {
				t.Fatalf("reload refs = %+v", refs)
			}
			if routes := applied[listenerRefFor(nextPair).fileKey()]; len(routes) != 0 {
				t.Fatalf("next listener routes not cleared: %+v", routes)
			}
			if routes := applied[listenerRefFor(oldPair).fileKey()]; len(routes) != 1 || routes[0].Domain != tc.res.Domain {
				t.Fatalf("old listener routes not restored: %+v", routes)
			}
		})
	}
}

func TestUpReportsRollbackFailureWhenRestoreRoutesFails(t *testing.T) {
	isolate(t)
	shortConfigDir, err := os.MkdirTemp("/tmp", "gate-cli-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(shortConfigDir) })
	t.Setenv("XDG_CONFIG_HOME", shortConfigDir)
	t.Setenv("XDG_STATE_HOME", shortConfigDir)
	if err := registryStore().Update(func(reg *registry.Registry) error {
		return reg.Reserve(registry.Reservation{Service: "web", Domain: "web.localhost", Port: 4400, Standalone: true})
	}); err != nil {
		t.Fatal(err)
	}
	srv := proxy.New(nil, nil)
	stop, err := daemon.ServeAdmin(context.Background(), defaultListenerRef().socketPath(), srv)
	if err != nil {
		t.Fatalf("ServeAdmin: %v", err)
	}
	defer stop()
	oldSelect := selectDNSProvider
	oldSetRoutes := setListenerRoutesFunc
	t.Cleanup(func() {
		selectDNSProvider = oldSelect
		setListenerRoutesFunc = oldSetRoutes
	})
	selectDNSProvider = func(_, _ string) dns.Provider { return fakeDNSProvider{} }
	setListenerRoutesFunc = func(_ listenerDaemonRef, _ []proxy.Route) error { return errors.New("reload failed") }

	var out, errb bytes.Buffer
	if code := Up([]string{"-g"}, &out, &errb); code != ExitError {
		t.Fatalf("Up exit = %d, want rollback failure", code)
	}
	if !strings.Contains(errb.String(), "rollback_failed") && !strings.Contains(errb.String(), "rollback failed") {
		t.Fatalf("stderr = %q, want rollback failure", errb.String())
	}
}

func TestDownGlobalRestoresRegistryWhenDNSFails(t *testing.T) {
	isolate(t)
	if err := registryStore().Update(func(reg *registry.Registry) error {
		return reg.Reserve(registry.Reservation{Service: "web", Domain: "web.localhost", Port: 4400, DNS: "localhost", Standalone: true, Active: true})
	}); err != nil {
		t.Fatal(err)
	}
	oldSelect := selectDNSProvider
	t.Cleanup(func() { selectDNSProvider = oldSelect })
	selectDNSProvider = func(_, _ string) dns.Provider {
		return fakeDNSProvider{remove: func(string) error { return errors.New("dns failed") }}
	}

	var out, errb bytes.Buffer
	if code := Down([]string{"-g"}, &out, &errb); code != ExitError {
		t.Fatalf("Down -g exit = %d, stderr=%s", code, errb.String())
	}
	reg, err := registryStore().Read()
	if err != nil {
		t.Fatal(err)
	}
	res := reg.Services[registry.Key("", "web")]
	if !res.Active {
		t.Fatalf("reservation not restored: %+v", res)
	}
}

func TestDownGlobalRestoresRegistryWhenReloadFails(t *testing.T) {
	isolate(t)
	shortConfigDir, err := os.MkdirTemp("/tmp", "gate-cli-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(shortConfigDir) })
	t.Setenv("XDG_CONFIG_HOME", shortConfigDir)
	t.Setenv("XDG_STATE_HOME", shortConfigDir)
	if err := registryStore().Update(func(reg *registry.Registry) error {
		return reg.Reserve(registry.Reservation{Service: "web", Domain: "web.localhost", Port: 4400, DNS: "localhost", Standalone: true, Active: true})
	}); err != nil {
		t.Fatal(err)
	}
	srv := proxy.New(nil, nil)
	stop, err := daemon.ServeAdmin(context.Background(), defaultListenerRef().socketPath(), srv)
	if err != nil {
		t.Fatalf("ServeAdmin: %v", err)
	}
	defer stop()
	oldSelect := selectDNSProvider
	oldSetRoutes := setListenerRoutesFunc
	t.Cleanup(func() {
		selectDNSProvider = oldSelect
		setListenerRoutesFunc = oldSetRoutes
	})
	selectDNSProvider = func(_, _ string) dns.Provider { return fakeDNSProvider{} }
	setListenerRoutesFunc = func(_ listenerDaemonRef, _ []proxy.Route) error { return errors.New("reload failed") }

	var out, errb bytes.Buffer
	if code := Down([]string{"-g"}, &out, &errb); code != ExitError {
		t.Fatalf("Down -g exit = %d, stderr=%s", code, errb.String())
	}
	reg, err := registryStore().Read()
	if err != nil {
		t.Fatal(err)
	}
	res := reg.Services[registry.Key("", "web")]
	if !res.Active {
		t.Fatalf("reservation not restored: %+v", res)
	}
}

func TestDownCurrentProjectRestoresRegistryWhenReloadFails(t *testing.T) {
	setupUpProject(t)
	shortConfigDir, err := os.MkdirTemp("/tmp", "gate-cli-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(shortConfigDir) })
	t.Setenv("XDG_CONFIG_HOME", shortConfigDir)
	t.Setenv("XDG_STATE_HOME", shortConfigDir)
	if err := registryStore().Update(func(reg *registry.Registry) error {
		return reg.Reserve(registry.Reservation{Project: "demo", Service: "web", Domain: "web.demo.localhost", Port: 4400, DNS: "localhost", Active: true})
	}); err != nil {
		t.Fatal(err)
	}
	srv := proxy.New(nil, nil)
	stop, err := daemon.ServeAdmin(context.Background(), defaultListenerRef().socketPath(), srv)
	if err != nil {
		t.Fatalf("ServeAdmin: %v", err)
	}
	defer stop()
	oldSelect := selectDNSProvider
	oldSetRoutes := setListenerRoutesFunc
	t.Cleanup(func() {
		selectDNSProvider = oldSelect
		setListenerRoutesFunc = oldSetRoutes
	})
	selectDNSProvider = func(_, _ string) dns.Provider { return fakeDNSProvider{} }
	setListenerRoutesFunc = func(_ listenerDaemonRef, _ []proxy.Route) error { return errors.New("reload failed") }

	var out, errb bytes.Buffer
	if code := Down(nil, &out, &errb); code != ExitError {
		t.Fatalf("Down exit = %d, stderr=%s", code, errb.String())
	}
	reg, err := registryStore().Read()
	if err != nil {
		t.Fatal(err)
	}
	res := reg.Services[registry.Key("demo", "web")]
	if !res.Active {
		t.Fatalf("reservation not restored: %+v", res)
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
	t.Setenv("XDG_STATE_HOME", shortConfigDir)
	srv := proxy.New(nil, nil)
	stop, err := daemon.ServeAdmin(context.Background(), defaultListenerRef().socketPath(), srv)
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

func TestUpAppliesExposureRecordsWhenReloadingDaemon(t *testing.T) {
	isolate(t)
	shortConfigDir, err := os.MkdirTemp("/tmp", "gate-cli-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(shortConfigDir) })
	t.Setenv("XDG_CONFIG_HOME", shortConfigDir)
	t.Setenv("XDG_STATE_HOME", shortConfigDir)
	if err := registryStore().Update(func(reg *registry.Registry) error {
		return reg.Reserve(registry.Reservation{
			Service: "web", Domain: "web.localhost", Port: 4400, DNS: "localhost", Standalone: true, Active: true,
		})
	}); err != nil {
		t.Fatal(err)
	}
	if err := exposureStore().Upsert(expose.Record{
		Scope: daemonScopeGlobal, Service: "web", Provider: expose.ProviderLAN,
		PublicURL: "https://web.local", Target: "web.localhost", AuthEnabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	ref := defaultListenerRef()
	applyExposeSession(ref.String(), []proxy.Route{{Domain: "web.localhost"}}, "web.localhost", "user:pass")
	srv := proxy.New(nil, nil)
	stop, err := daemon.ServeAdmin(context.Background(), ref.socketPath(), srv)
	if err != nil {
		t.Fatalf("ServeAdmin: %v", err)
	}
	defer stop()

	var out, errb bytes.Buffer
	if code := Up([]string{"-g", "--json"}, &out, &errb); code != ExitOK {
		t.Fatalf("Up exit = %d, stderr=%s", code, errb.String())
	}
	routes := srv.Routes()
	if !routeExposed(routes, "web.localhost", "user:pass") {
		t.Fatalf("base route not exposed with auth: %+v", routes)
	}
	if !routeExposed(routes, "web.local", "user:pass") {
		t.Fatalf("LAN alias not exposed with auth: %+v", routes)
	}
}

func TestUpRestoresRegistryWhenExposureStoreReadFailsBeforeReload(t *testing.T) {
	isolate(t)
	shortConfigDir, err := os.MkdirTemp("/tmp", "gate-cli-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(shortConfigDir) })
	t.Setenv("XDG_CONFIG_HOME", shortConfigDir)
	t.Setenv("XDG_STATE_HOME", shortConfigDir)
	if err := registryStore().Update(func(reg *registry.Registry) error {
		return reg.Reserve(registry.Reservation{
			Service: "web", Domain: "web.localhost", Port: 4400, DNS: "localhost", Standalone: true,
		})
	}); err != nil {
		t.Fatal(err)
	}
	exposurePath := exposureStore().Path
	if err := os.MkdirAll(filepath.Dir(exposurePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exposurePath, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := proxy.New(nil, nil)
	stop, err := daemon.ServeAdmin(context.Background(), defaultListenerRef().socketPath(), srv)
	if err != nil {
		t.Fatalf("ServeAdmin: %v", err)
	}
	defer stop()

	var out, errb bytes.Buffer
	if code := Up([]string{"-g", "--json"}, &out, &errb); code != ExitError {
		t.Fatalf("Up exit = %d, want exposure-store failure", code)
	}
	if !strings.Contains(errb.String(), "expose_store") || strings.Contains(errb.String(), "rollback_failed") {
		t.Fatalf("stderr = %q, want expose_store without rollback_failed", errb.String())
	}
	reg, err := registryStore().Read()
	if err != nil {
		t.Fatal(err)
	}
	if res := reg.Services[registry.Key("", "web")]; res.Active {
		t.Fatalf("reservation active after rollback: %+v", res)
	}
	if srv.RouteCount() != 0 {
		t.Fatalf("routes mutated before exposure-store failure: %+v", srv.Routes())
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
	t.Setenv("XDG_STATE_HOME", shortConfigDir)
	srv := proxy.New(nil, nil)
	ref := listenerRefFor(listener.FromFlags("127.0.0.1:0", "127.0.0.1:0"))
	stop, err := daemon.ServeAdmin(context.Background(), ref.socketPath(), srv)
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

func TestUpDaemonSpawnsScopedDaemonAndWritesState(t *testing.T) {
	setupUpProject(t)
	shortConfigDir, err := os.MkdirTemp("/tmp", "gate-cli-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(shortConfigDir) })
	t.Setenv("XDG_CONFIG_HOME", shortConfigDir)
	t.Setenv("XDG_STATE_HOME", shortConfigDir)
	oldNewDaemonServeCommand := newDaemonServeCommand
	t.Cleanup(func() { newDaemonServeCommand = oldNewDaemonServeCommand })
	newDaemonServeCommand = func(_, socketPath, _, _ string) *exec.Cmd {
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		//nolint:gosec // G204: test launches this same test binary as a helper process.
		cmd := exec.Command(exe, "-test.run=TestDaemonStartHelperProcess", "--", "__serve")
		cmd.Env = append(os.Environ(), "GATE_TEST_DAEMON_START_HELPER=serve-admin", "GATE_TEST_DAEMON_SOCKET="+socketPath)
		return cmd
	}

	var out, errb bytes.Buffer
	if code := Up([]string{"--daemon", "--json"}, &out, &errb); code != ExitOK {
		t.Fatalf("Up exit = %d, stderr=%s", code, errb.String())
	}
	ref := defaultListenerRef()
	if _, err := os.Stat(ref.pidPath()); err != nil {
		t.Fatalf("pid file missing: %v", err)
	}
	if _, err := os.Stat(ref.logPath()); err != nil {
		t.Fatalf("log file missing: %v", err)
	}
	st, err := daemonClientForRef(ref).Status()
	if err != nil {
		t.Fatalf("daemon status: %v", err)
	}
	if st.Routes != 2 {
		t.Fatalf("routes = %d, want 2", st.Routes)
	}
	_ = stopDaemonProcess(daemonClientForRef(ref), st.PID, 2*time.Second)
}

func TestUpDaemonStartConflictIncludesCrossStateGateDaemonHint(t *testing.T) {
	setupUpProject(t)
	shortConfigDir, err := os.MkdirTemp("/tmp", "gate-cli-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(shortConfigDir) })
	t.Setenv("XDG_CONFIG_HOME", shortConfigDir)
	t.Setenv("XDG_STATE_HOME", shortConfigDir)
	oldNewDaemonServeCommand := newDaemonServeCommand
	oldOwners := tcpListenOwnersForPort
	t.Cleanup(func() {
		newDaemonServeCommand = oldNewDaemonServeCommand
		tcpListenOwnersForPort = oldOwners
	})
	newDaemonServeCommand = func(_, _, _, _ string) *exec.Cmd {
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		//nolint:gosec // G204: test launches this same test binary as a helper process.
		cmd := exec.Command(exe, "-test.run=TestDaemonStartHelperProcess", "--", "__serve")
		cmd.Env = append(os.Environ(), "GATE_TEST_DAEMON_START_HELPER=addr-in-use")
		return cmd
	}
	tcpListenOwnersForPort = func(port string) []tcpListenOwner {
		if port != "443" {
			return nil
		}
		return []tcpListenOwner{{
			PID:  80029,
			Args: "gate __serve --socket /tmp/ssg/xdg/config/gate/daemons/listener-https-443-http-80.sock --https-addr :443 --http-addr :80",
		}}
	}

	var out, errb bytes.Buffer
	if code := Up([]string{"--daemon"}, &out, &errb); code != ExitConflict {
		t.Fatalf("Up exit = %d, want conflict; stdout=%s stderr=%s", code, out.String(), errb.String())
	}
	got := errb.String()
	if !strings.Contains(got, "another gate daemon is already listening on TCP :443") ||
		!strings.Contains(got, "owner socket: /tmp/ssg/xdg/config/gate/daemons/listener-https-443-http-80.sock") {
		t.Fatalf("up conflict missing cross-state hint:\n%s", got)
	}
}

func TestUpDaemonStartJSONConflictIncludesCrossStateGateDaemonHint(t *testing.T) {
	setupUpProject(t)
	shortConfigDir, err := os.MkdirTemp("/tmp", "gate-cli-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(shortConfigDir) })
	t.Setenv("XDG_CONFIG_HOME", shortConfigDir)
	t.Setenv("XDG_STATE_HOME", shortConfigDir)
	oldNewDaemonServeCommand := newDaemonServeCommand
	oldOwners := tcpListenOwnersForPort
	t.Cleanup(func() {
		newDaemonServeCommand = oldNewDaemonServeCommand
		tcpListenOwnersForPort = oldOwners
	})
	newDaemonServeCommand = func(_, _, _, _ string) *exec.Cmd {
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		//nolint:gosec // G204: test launches this same test binary as a helper process.
		cmd := exec.Command(exe, "-test.run=TestDaemonStartHelperProcess", "--", "__serve")
		cmd.Env = append(os.Environ(), "GATE_TEST_DAEMON_START_HELPER=addr-in-use")
		return cmd
	}
	tcpListenOwnersForPort = func(port string) []tcpListenOwner {
		if port != "443" {
			return nil
		}
		return []tcpListenOwner{{
			PID:  80029,
			Args: "gate __serve --socket /tmp/ssg/xdg/config/gate/daemons/listener-https-443-http-80.sock --https-addr :443 --http-addr :80",
		}}
	}

	var out, errb bytes.Buffer
	if code := Up([]string{"--daemon", "--json"}, &out, &errb); code != ExitConflict {
		t.Fatalf("Up exit = %d, want conflict; stdout=%s stderr=%s", code, out.String(), errb.String())
	}
	var env errEnvelope
	if err := json.Unmarshal(errb.Bytes(), &env); err != nil {
		t.Fatalf("error json: %v\n%s", err, errb.String())
	}
	if !strings.Contains(env.Error.Message, "another gate daemon is already listening on TCP :443") {
		t.Fatalf("json error missing cross-state hint:\n%+v", env.Error)
	}
}

func TestUpDaemonCleansUpSpawnedDaemonWhenReloadFails(t *testing.T) {
	setupUpProject(t)
	shortConfigDir, err := os.MkdirTemp("/tmp", "gate-cli-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(shortConfigDir) })
	t.Setenv("XDG_CONFIG_HOME", shortConfigDir)
	t.Setenv("XDG_STATE_HOME", shortConfigDir)
	oldNewDaemonServeCommand := newDaemonServeCommand
	oldSetRoutes := setListenerRoutesFunc
	t.Cleanup(func() {
		newDaemonServeCommand = oldNewDaemonServeCommand
		setListenerRoutesFunc = oldSetRoutes
	})
	newDaemonServeCommand = func(_, socketPath, _, _ string) *exec.Cmd {
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		//nolint:gosec // G204: test launches this same test binary as a helper process.
		cmd := exec.Command(exe, "-test.run=TestDaemonStartHelperProcess", "--", "__serve")
		cmd.Env = append(os.Environ(), "GATE_TEST_DAEMON_START_HELPER=serve-admin", "GATE_TEST_DAEMON_SOCKET="+socketPath)
		return cmd
	}
	setListenerRoutesFunc = func(_ listenerDaemonRef, _ []proxy.Route) error {
		return errors.New("reload failed")
	}

	var out, errb bytes.Buffer
	if code := Up([]string{"--daemon", "--json"}, &out, &errb); code != ExitError {
		t.Fatalf("Up exit = %d, want reload failure", code)
	}
	ref := defaultListenerRef()
	if _, err := os.Stat(ref.pidPath()); !os.IsNotExist(err) {
		t.Fatalf("pid file still exists or stat failed: %v", err)
	}
	client := daemonClientForRef(ref)
	for i := 0; i < 50 && client.IsRunning(); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if client.IsRunning() {
		t.Fatal("spawned daemon still running after reload failure")
	}
}

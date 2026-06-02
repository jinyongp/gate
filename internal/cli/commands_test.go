package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gate/internal/dns"
	"gate/internal/proxy"
	"gate/internal/registry"
)

// isolate points gate's config dir and cwd at temp dirs for the duration of the test.
func isolate(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Chdir(t.TempDir())
}

type fakeDNSProvider struct {
	ensure func(string) error
	remove func(string) error
}

func (f fakeDNSProvider) Ensure(domain string) error {
	if f.ensure == nil {
		return nil
	}
	return f.ensure(domain)
}

func (f fakeDNSProvider) Remove(domain string) error {
	if f.remove == nil {
		return nil
	}
	return f.remove(domain)
}

func TestAddThenLsJSON(t *testing.T) {
	isolate(t)
	var out, errb bytes.Buffer
	if code := Add([]string{"web.localhost", "4312"}, &out, &errb); code != ExitOK {
		t.Fatalf("Add exit = %d, stderr=%s", code, errb.String())
	}

	out.Reset()
	if code := Ls([]string{"--all", "--json"}, &out, &errb); code != ExitOK {
		t.Fatalf("Ls exit = %d", code)
	}
	var got struct {
		Services []struct {
			Domain string `json:"domain"`
			Port   int    `json:"port"`
		} `json:"services"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	if len(got.Services) != 1 || got.Services[0].Domain != "web.localhost" || got.Services[0].Port != 4312 {
		t.Fatalf("unexpected ls: %+v", got.Services)
	}
}

func TestAddPortConflictExit4(t *testing.T) {
	isolate(t)
	var out, errb bytes.Buffer
	if code := Add([]string{"a.localhost", "4312"}, &out, &errb); code != ExitOK {
		t.Fatalf("first Add exit = %d", code)
	}
	errb.Reset()
	code := Add([]string{"b.localhost", "4312"}, &out, &errb)
	if code != ExitConflict {
		t.Fatalf("conflict exit = %d, want %d", code, ExitConflict)
	}
}

func TestAddRejectsInvalidDomain(t *testing.T) {
	isolate(t)
	var out, errb bytes.Buffer
	code := Add([]string{"--json", "web..localhost", "4312"}, &out, &errb)
	if code != ExitUsage {
		t.Fatalf("Add exit = %d, want %d; stderr=%s", code, ExitUsage, errb.String())
	}
	if !strings.Contains(errb.String(), "bad_domain") {
		t.Fatalf("missing bad_domain error:\n%s", errb.String())
	}
}

func TestAddJSONErrorEnvelope(t *testing.T) {
	isolate(t)
	var out, errb bytes.Buffer
	_ = Add([]string{"a.localhost", "4312", "--json"}, &out, &errb) // parse note: flags before args
	// Force a conflict with JSON envelope.
	out.Reset()
	errb.Reset()
	_ = Add([]string{"--json", "a.localhost", "4312"}, &out, &errb)
	errb.Reset()
	code := Add([]string{"--json", "b.localhost", "4312"}, &out, &errb)
	if code != ExitConflict {
		t.Fatalf("exit = %d", code)
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(errb.Bytes(), &env); err != nil {
		t.Fatalf("stderr not JSON: %v\n%s", err, errb.String())
	}
	if env.Error.Code != "port_conflict" {
		t.Fatalf("error code = %q", env.Error.Code)
	}
}

func TestAddDomainConflictJSONErrorEnvelope(t *testing.T) {
	isolate(t)
	if err := registryStore().Update(func(r *registry.Registry) error {
		return r.Reserve(registry.Reservation{Project: "other", Service: "web", Domain: "a.localhost", Port: 4311})
	}); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	code := Add([]string{"--json", "a.localhost", "4313"}, &out, &errb)
	if code != ExitConflict {
		t.Fatalf("exit = %d, want %d", code, ExitConflict)
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(errb.Bytes(), &env); err != nil {
		t.Fatalf("stderr not JSON: %v\n%s", err, errb.String())
	}
	if env.Error.Code != "domain_conflict" {
		t.Fatalf("error code = %q", env.Error.Code)
	}
}

func TestTrailingJSONFlag(t *testing.T) {
	isolate(t)
	var out, errb bytes.Buffer
	if code := Add([]string{"a.localhost", "4312", "--json"}, &out, &errb); code != ExitOK {
		t.Fatalf("Add exit = %d, stderr=%s", code, errb.String())
	}
	var add struct {
		Domain string `json:"domain"`
		Port   int    `json:"port"`
	}
	if err := json.Unmarshal(out.Bytes(), &add); err != nil {
		t.Fatalf("add json: %v\n%s", err, out.String())
	}
	out.Reset()
	if code := Port([]string{"a.localhost", "--json"}, &out, &errb); code != ExitOK {
		t.Fatalf("Port outside project exit = %d, stderr=%s", code, errb.String())
	}
	var gotPort struct {
		Service    string `json:"service"`
		Port       int    `json:"port"`
		Standalone bool   `json:"standalone"`
	}
	if err := json.Unmarshal(out.Bytes(), &gotPort); err != nil {
		t.Fatalf("port json: %v\n%s", err, out.String())
	}
	if gotPort.Service != "a.localhost" || gotPort.Port != 4312 || !gotPort.Standalone {
		t.Fatalf("port json = %+v", gotPort)
	}
}

func TestAddStandaloneActivatesDNSAndRoutes(t *testing.T) {
	isolate(t)
	oldSelect := selectDNSProvider
	oldSetRoutes := setDaemonRoutesFunc
	t.Cleanup(func() {
		selectDNSProvider = oldSelect
		setDaemonRoutesFunc = oldSetRoutes
	})
	var ensured []string
	selectDNSProvider = func(_, _ string) dns.Provider {
		return fakeDNSProvider{
			ensure: func(domain string) error {
				ensured = append(ensured, domain)
				return nil
			},
		}
	}
	var routes []proxy.Route
	setDaemonRoutesFunc = func(next []proxy.Route) error {
		routes = append([]proxy.Route{}, next...)
		return nil
	}

	var out, errb bytes.Buffer
	if code := Add([]string{"web.localhost", "4312"}, &out, &errb); code != ExitOK {
		t.Fatalf("Add exit = %d, stderr=%s", code, errb.String())
	}
	reg, err := registryStore().Read()
	if err != nil {
		t.Fatal(err)
	}
	res, ok := reg.Get(registry.Key("", "web.localhost"))
	if !ok || !res.Adhoc || !res.Active || res.Port != 4312 {
		t.Fatalf("standalone reservation = %+v, ok=%v", res, ok)
	}
	if len(ensured) != 1 || ensured[0] != "web.localhost" {
		t.Fatalf("ensured = %v", ensured)
	}
	if len(routes) != 1 || routes[0].Domain != "web.localhost" || routes[0].Upstream != "127.0.0.1:4312" {
		t.Fatalf("routes = %+v", routes)
	}
}

func TestRmStandaloneRemovesDNSAndRoutes(t *testing.T) {
	isolate(t)
	if err := registryStore().Update(func(r *registry.Registry) error {
		return r.Reserve(registry.Reservation{
			Service: "web.localhost",
			Domain:  "web.localhost",
			Port:    4312,
			DNS:     "localhost",
			Adhoc:   true,
			Active:  true,
		})
	}); err != nil {
		t.Fatal(err)
	}
	oldSelect := selectDNSProvider
	oldSetRoutes := setDaemonRoutesFunc
	t.Cleanup(func() {
		selectDNSProvider = oldSelect
		setDaemonRoutesFunc = oldSetRoutes
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
	var routes []proxy.Route
	setDaemonRoutesFunc = func(next []proxy.Route) error {
		routes = append([]proxy.Route{}, next...)
		return nil
	}

	var out, errb bytes.Buffer
	if code := Rm([]string{"web.localhost"}, &out, &errb); code != ExitOK {
		t.Fatalf("Rm exit = %d, stderr=%s", code, errb.String())
	}
	reg, err := registryStore().Read()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Get(registry.Key("", "web.localhost")); ok {
		t.Fatal("standalone reservation not removed")
	}
	if len(removed) != 1 || removed[0] != "web.localhost" {
		t.Fatalf("removed DNS = %v", removed)
	}
	if len(routes) != 0 {
		t.Fatalf("routes = %+v, want empty", routes)
	}
}

func TestAddRmSyncProjectConfig(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	body := "# keep\n[project]\nname = \"demo\"\n"
	path := filepath.Join(dir, "gate.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	var out, errb bytes.Buffer
	if code := Add([]string{"api.demo.localhost", "4312"}, &out, &errb); code != ExitOK {
		t.Fatalf("Add exit = %d, stderr=%s", code, errb.String())
	}
	edited, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if s := string(edited); !strings.Contains(s, "# keep") || !strings.Contains(s, "[services.api]") {
		t.Fatalf("config not updated preserving comments:\n%s", s)
	}
	reg, err := registryStore().Read()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Get(registry.Key("demo", "api")); !ok {
		t.Fatalf("registry missing demo/api: %+v", reg.Services)
	}

	if code := Rm([]string{"api.demo.localhost"}, &out, &errb); code != ExitOK {
		t.Fatalf("Rm exit = %d, stderr=%s", code, errb.String())
	}
	edited, _ = os.ReadFile(path)
	if strings.Contains(string(edited), "[services.api]") {
		t.Fatalf("config service not removed:\n%s", edited)
	}
	reg, _ = registryStore().Read()
	if _, ok := reg.Get(registry.Key("demo", "api")); ok {
		t.Fatal("registry service not removed")
	}
}

func TestRmRemoves(t *testing.T) {
	isolate(t)
	var out, errb bytes.Buffer
	_ = Add([]string{"x.localhost", "4312"}, &out, &errb)
	out.Reset()
	if code := Rm([]string{"x.localhost"}, &out, &errb); code != ExitOK {
		t.Fatalf("Rm exit = %d", code)
	}
	if code := Rm([]string{"x.localhost"}, &out, &errb); code != ExitError {
		t.Fatalf("second Rm exit = %d, want error", code)
	}
}

func TestLsDefaultsToCurrentProjectActiveReservations(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	toml := "[project]\nname = \"demo\"\n\n[services.web]\ndomain = \"app.localhost\"\n"
	if err := os.WriteFile(filepath.Join(dir, "gate.toml"), []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	err := registryStore().Update(func(r *registry.Registry) error {
		if err := r.Reserve(registry.Reservation{Project: "demo", Service: "web", Domain: "app.localhost", Port: 4400, Active: true}); err != nil {
			return err
		}
		if err := r.Reserve(registry.Reservation{Project: "demo", Service: "api", Domain: "api.localhost", Port: 4401}); err != nil {
			return err
		}
		return r.Reserve(registry.Reservation{Project: "other", Service: "web", Domain: "other.localhost", Port: 4402, Active: true})
	})
	if err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	if code := Ls([]string{"--json"}, &out, &errb); code != ExitOK {
		t.Fatalf("Ls exit = %d, stderr=%s", code, errb.String())
	}
	var got struct {
		Services []struct {
			Project string `json:"project"`
			Service string `json:"service"`
		} `json:"services"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	if len(got.Services) != 1 || got.Services[0].Project != "demo" || got.Services[0].Service != "web" {
		t.Fatalf("services = %+v", got.Services)
	}
}

func TestLsAllAndStatusFilter(t *testing.T) {
	isolate(t)
	err := registryStore().Update(func(r *registry.Registry) error {
		if err := r.Reserve(registry.Reservation{Project: "demo", Service: "web", Domain: "app.localhost", Port: 4400}); err != nil {
			return err
		}
		return r.Reserve(registry.Reservation{Project: "other", Service: "web", Domain: "other.localhost", Port: 4401})
	})
	if err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	if code := Ls([]string{"--all", "--status=down", "--json"}, &out, &errb); code != ExitOK {
		t.Fatalf("Ls exit = %d, stderr=%s", code, errb.String())
	}
	var got struct {
		Services []struct {
			Domain string `json:"domain"`
			Status string `json:"status"`
		} `json:"services"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	if len(got.Services) != 2 {
		t.Fatalf("services = %+v", got.Services)
	}
	for _, svc := range got.Services {
		if svc.Status != "down" {
			t.Fatalf("status = %q", svc.Status)
		}
	}
}

func TestRmProjectRemovesCurrentProjectReservations(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	toml := "[project]\nname = \"demo\"\n\n[services.web]\ndomain = \"app.localhost\"\n"
	if err := os.WriteFile(filepath.Join(dir, "gate.toml"), []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	err := registryStore().Update(func(r *registry.Registry) error {
		if err := r.Reserve(registry.Reservation{Project: "demo", Service: "web", Domain: "app.localhost", Port: 4400, DNS: "localhost", Active: true}); err != nil {
			return err
		}
		if err := r.Reserve(registry.Reservation{Project: "demo", Service: "api", Domain: "api.localhost", Port: 4401, DNS: "localhost", Active: true}); err != nil {
			return err
		}
		return r.Reserve(registry.Reservation{Project: "other", Service: "web", Domain: "other.localhost", Port: 4402, DNS: "localhost", Active: true})
	})
	if err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	if code := Rm([]string{"--project"}, &out, &errb); code != ExitOK {
		t.Fatalf("Rm --project exit = %d, stderr=%s", code, errb.String())
	}
	reg, err := registryStore().Read()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Get(registry.Key("demo", "web")); ok {
		t.Fatal("demo/web not removed")
	}
	if _, ok := reg.Get(registry.Key("demo", "api")); ok {
		t.Fatal("demo/api not removed")
	}
	if _, ok := reg.Get(registry.Key("other", "web")); !ok {
		t.Fatal("other/web should remain")
	}
}

func TestRmProjectRemovesNamedProjectReservations(t *testing.T) {
	isolate(t)
	err := registryStore().Update(func(r *registry.Registry) error {
		if err := r.Reserve(registry.Reservation{Project: "demo", Service: "web", Domain: "app.localhost", Port: 4400, DNS: "localhost", Active: true}); err != nil {
			return err
		}
		return r.Reserve(registry.Reservation{Project: "other", Service: "web", Domain: "other.localhost", Port: 4401, DNS: "localhost", Active: true})
	})
	if err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	if code := Rm([]string{"--project", "demo"}, &out, &errb); code != ExitOK {
		t.Fatalf("Rm --project demo exit = %d, stderr=%s", code, errb.String())
	}
	reg, err := registryStore().Read()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Get(registry.Key("demo", "web")); ok {
		t.Fatal("demo/web not removed")
	}
	if _, ok := reg.Get(registry.Key("other", "web")); !ok {
		t.Fatal("other/web should remain")
	}
}

func TestRmProjectRestoresRegistryWhenReloadFails(t *testing.T) {
	isolate(t)
	oldSetRoutes := setDaemonRoutesFunc
	t.Cleanup(func() { setDaemonRoutesFunc = oldSetRoutes })
	calls := 0
	setDaemonRoutesFunc = func([]proxy.Route) error {
		calls++
		if calls == 1 {
			return errors.New("reload failed")
		}
		return nil
	}
	err := registryStore().Update(func(r *registry.Registry) error {
		if err := r.Reserve(registry.Reservation{Project: "demo", Service: "web", Domain: "app.localhost", Port: 4400, DNS: "localhost", Active: true}); err != nil {
			return err
		}
		return r.Reserve(registry.Reservation{Project: "other", Service: "web", Domain: "other.localhost", Port: 4401, DNS: "localhost", Active: true})
	})
	if err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	if code := Rm([]string{"--project", "demo"}, &out, &errb); code != ExitError {
		t.Fatalf("Rm --project demo exit = %d, want reload failure", code)
	}
	reg, err := registryStore().Read()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Get(registry.Key("demo", "web")); !ok {
		t.Fatal("demo/web should be restored after reload failure")
	}
	if _, ok := reg.Get(registry.Key("other", "web")); !ok {
		t.Fatal("other/web should remain")
	}
}

func TestRmProjectReportsRollbackFailureWhenRouteRestoreFails(t *testing.T) {
	isolate(t)
	oldSetRoutes := setDaemonRoutesFunc
	t.Cleanup(func() { setDaemonRoutesFunc = oldSetRoutes })
	setDaemonRoutesFunc = func([]proxy.Route) error {
		return errors.New("routes failed")
	}
	err := registryStore().Update(func(r *registry.Registry) error {
		if err := r.Reserve(registry.Reservation{Project: "demo", Service: "web", Domain: "app.localhost", Port: 4400, DNS: "localhost", Active: true}); err != nil {
			return err
		}
		return r.Reserve(registry.Reservation{Project: "other", Service: "web", Domain: "other.localhost", Port: 4401, DNS: "localhost", Active: true})
	})
	if err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	if code := Rm([]string{"--project", "demo"}, &out, &errb); code != ExitError {
		t.Fatalf("Rm --project demo exit = %d, want rollback failure", code)
	}
	if !strings.Contains(errb.String(), "rollback failed") {
		t.Fatalf("stderr = %q, want rollback failure", errb.String())
	}
	reg, err := registryStore().Read()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Get(registry.Key("demo", "web")); !ok {
		t.Fatal("demo/web should be restored after rollback failure")
	}
}

func TestRmProjectRestoresRegistryAndDNSWhenDNSFails(t *testing.T) {
	isolate(t)
	oldSetRoutes := setDaemonRoutesFunc
	oldSelect := selectDNSProvider
	t.Cleanup(func() {
		setDaemonRoutesFunc = oldSetRoutes
		selectDNSProvider = oldSelect
	})
	setDaemonRoutesFunc = func([]proxy.Route) error {
		return nil
	}
	var ensured []string
	selectDNSProvider = func(_, _ string) dns.Provider {
		return fakeDNSProvider{
			ensure: func(domain string) error {
				ensured = append(ensured, domain)
				return nil
			},
			remove: func(domain string) error {
				if domain == "b.localhost" {
					return errors.New("dns failed")
				}
				return nil
			},
		}
	}
	err := registryStore().Update(func(r *registry.Registry) error {
		if err := r.Reserve(registry.Reservation{Project: "demo", Service: "a", Domain: "a.localhost", Port: 4400, DNS: "localhost", Active: true}); err != nil {
			return err
		}
		return r.Reserve(registry.Reservation{Project: "demo", Service: "b", Domain: "b.localhost", Port: 4401, DNS: "localhost", Active: true})
	})
	if err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	if code := Rm([]string{"--project", "demo"}, &out, &errb); code != ExitError {
		t.Fatalf("Rm --project demo exit = %d, want DNS failure", code)
	}
	reg, err := registryStore().Read()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Get(registry.Key("demo", "a")); !ok {
		t.Fatal("demo/a should be restored after DNS failure")
	}
	if _, ok := reg.Get(registry.Key("demo", "b")); !ok {
		t.Fatal("demo/b should be restored after DNS failure")
	}
	if len(ensured) != 1 || ensured[0] != "a.localhost" {
		t.Fatalf("ensured rollback = %v", ensured)
	}
}

func TestRmProjectReportsRollbackFailureWhenDNSRestoreFails(t *testing.T) {
	isolate(t)
	oldSetRoutes := setDaemonRoutesFunc
	oldSelect := selectDNSProvider
	t.Cleanup(func() {
		setDaemonRoutesFunc = oldSetRoutes
		selectDNSProvider = oldSelect
	})
	setDaemonRoutesFunc = func([]proxy.Route) error {
		return nil
	}
	selectDNSProvider = func(_, _ string) dns.Provider {
		return fakeDNSProvider{
			ensure: func(domain string) error {
				if domain == "a.localhost" {
					return errors.New("restore failed")
				}
				return nil
			},
			remove: func(domain string) error {
				if domain == "b.localhost" {
					return errors.New("dns failed")
				}
				return nil
			},
		}
	}
	err := registryStore().Update(func(r *registry.Registry) error {
		if err := r.Reserve(registry.Reservation{Project: "demo", Service: "a", Domain: "a.localhost", Port: 4400, DNS: "localhost", Active: true}); err != nil {
			return err
		}
		return r.Reserve(registry.Reservation{Project: "demo", Service: "b", Domain: "b.localhost", Port: 4401, DNS: "localhost", Active: true})
	})
	if err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	if code := Rm([]string{"--project", "demo"}, &out, &errb); code != ExitError {
		t.Fatalf("Rm --project demo exit = %d, want rollback failure", code)
	}
	if !strings.Contains(errb.String(), "rollback failed") {
		t.Fatalf("stderr = %q, want rollback failure", errb.String())
	}
	reg, err := registryStore().Read()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Get(registry.Key("demo", "a")); !ok {
		t.Fatal("demo/a should be restored after rollback failure")
	}
	if _, ok := reg.Get(registry.Key("demo", "b")); !ok {
		t.Fatal("demo/b should be restored after rollback failure")
	}
}

func setupProject(t *testing.T) {
	t.Helper()
	isolate(t)
	dir := t.TempDir()
	toml := "[project]\nname = \"demo\"\n\n[services.web]\ndomain = \"app.localhost\"\n"
	if err := os.WriteFile(filepath.Join(dir, "gate.toml"), []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	// reserve a port for demo/web
	err := registryStore().Update(func(r *registry.Registry) error {
		return r.Reserve(registry.Reservation{Project: "demo", Service: "web", Domain: "app.localhost", Port: 4400})
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPortReadsReservation(t *testing.T) {
	setupProject(t)
	var out, errb bytes.Buffer
	if code := Port([]string{"web"}, &out, &errb); code != ExitOK {
		t.Fatalf("Port exit = %d, stderr=%s", code, errb.String())
	}
	if strings.TrimSpace(out.String()) != "4400" {
		t.Fatalf("port out = %q", out.String())
	}
}

func TestPortListsReservedPorts(t *testing.T) {
	setupProject(t)
	if err := registryStore().Update(func(r *registry.Registry) error {
		return r.Reserve(registry.Reservation{Project: "other", Service: "api", Domain: "api.localhost", Port: 5500})
	}); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := Port(nil, &out, &errb); code != ExitOK {
		t.Fatalf("Port exit = %d, stderr=%s", code, errb.String())
	}
	s := out.String()
	for _, want := range []string{"PORT", "OWNER", "TARGET", "STATUS", "4400", "demo/web", "https://app.localhost"} {
		if !strings.Contains(s, want) {
			t.Fatalf("port list missing %q in:\n%s", want, s)
		}
	}
	if strings.Contains(s, "5500") || strings.Contains(s, "api.localhost") {
		t.Fatalf("port list should default to current project only:\n%s", s)
	}
}

func TestPortListsAllReservedPorts(t *testing.T) {
	setupProject(t)
	if err := registryStore().Update(func(r *registry.Registry) error {
		return r.Reserve(registry.Reservation{Project: "other", Service: "api", Domain: "api.localhost", Port: 5500})
	}); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := Port([]string{"-a"}, &out, &errb); code != ExitOK {
		t.Fatalf("Port -a exit = %d, stderr=%s", code, errb.String())
	}
	s := out.String()
	for _, want := range []string{"4400", "demo/web", "5500", "other/api", "https://api.localhost"} {
		if !strings.Contains(s, want) {
			t.Fatalf("port -a missing %q in:\n%s", want, s)
		}
	}
}

func TestPortListsStandaloneReservations(t *testing.T) {
	isolate(t)
	var out, errb bytes.Buffer
	if code := Add([]string{"web.localhost", "4312"}, &out, &errb); code != ExitOK {
		t.Fatalf("Add exit = %d, stderr=%s", code, errb.String())
	}

	out.Reset()
	if code := Port([]string{"-a"}, &out, &errb); code != ExitOK {
		t.Fatalf("Port -a exit = %d, stderr=%s", code, errb.String())
	}
	s := out.String()
	if !strings.Contains(s, "standalone/web.localhost") {
		t.Fatalf("standalone owner missing in:\n%s", s)
	}
	if strings.Contains(s, "adhoc") {
		t.Fatalf("port list should not expose adhoc terminology:\n%s", s)
	}

	out.Reset()
	if code := Port([]string{"-a", "--json"}, &out, &errb); code != ExitOK {
		t.Fatalf("Port -a --json exit = %d, stderr=%s", code, errb.String())
	}
	var got struct {
		Ports []struct {
			Standalone bool `json:"standalone"`
		} `json:"ports"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	if len(got.Ports) != 1 || !got.Ports[0].Standalone {
		t.Fatalf("unexpected standalone json: %+v", got.Ports)
	}
}

func TestPortListsReservedPortsJSON(t *testing.T) {
	setupProject(t)
	var out, errb bytes.Buffer
	if code := Port([]string{"--json"}, &out, &errb); code != ExitOK {
		t.Fatalf("Port --json exit = %d, stderr=%s", code, errb.String())
	}
	var got struct {
		Ports []struct {
			Project string `json:"project"`
			Service string `json:"service"`
			Domain  string `json:"domain"`
			Port    int    `json:"port"`
		} `json:"ports"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	if len(got.Ports) != 1 || got.Ports[0].Project != "demo" || got.Ports[0].Service != "web" || got.Ports[0].Domain != "app.localhost" || got.Ports[0].Port != 4400 {
		t.Fatalf("unexpected ports: %+v", got.Ports)
	}
}

func TestLsHelpGroupsAllAlias(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Ls([]string{"-h"}, &out, &errb); code != ExitOK {
		t.Fatalf("Ls -h exit = %d, stderr=%s", code, errb.String())
	}
	s := out.String()
	if !strings.Contains(s, "-a, --all") {
		t.Fatalf("help should group shorthand with long flag:\n%s", s)
	}
}

func TestRunInjectsPort(t *testing.T) {
	setupProject(t)
	var out, errb bytes.Buffer
	code := Run([]string{"web", "--", "sh", "-c", `printf %s "$PORT"`}, &out, &errb)
	if code != ExitOK {
		t.Fatalf("Run exit = %d, stderr=%s", code, errb.String())
	}
	if out.String() != "4400" {
		t.Fatalf("PORT = %q", out.String())
	}
}

func TestRunUsesStandaloneReservationOutsideProject(t *testing.T) {
	isolate(t)
	if err := registryStore().Update(func(r *registry.Registry) error {
		return r.Reserve(registry.Reservation{
			Service: "web.localhost",
			Domain:  "web.localhost",
			Port:    4312,
			Adhoc:   true,
			Active:  true,
		})
	}); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	code := Run([]string{"web.localhost", "--", "sh", "-c", `printf %s "$PORT"`}, &out, &errb)
	if code != ExitOK {
		t.Fatalf("Run exit = %d, stderr=%s", code, errb.String())
	}
	if out.String() != "4312" {
		t.Fatalf("PORT = %q", out.String())
	}
}

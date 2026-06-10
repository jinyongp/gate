package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gate/internal/ca"
	"gate/internal/daemon"
	"gate/internal/expose"
	"gate/internal/paths"
	"gate/internal/proxy"
	"gate/internal/registry"
)

type fakeExposeProvider struct {
	called  *int
	stopped *int
	closed  *int
	auth    *string
	target  *string
	port    *int
	result  expose.Result
}

func (p fakeExposeProvider) Expose(_ context.Context, domain string, opts expose.Opts) (expose.Result, error) {
	if p.called != nil {
		*p.called++
	}
	if p.auth != nil {
		*p.auth = opts.Auth
	}
	if p.target != nil {
		*p.target = opts.TargetURL
	}
	if p.port != nil {
		*p.port = opts.ServePort
	}
	if p.result.URL != "" {
		return p.result, nil
	}
	return expose.Result{URL: "https://" + domain}, nil
}

func (fakeExposeProvider) Status(context.Context, expose.Record) (string, error) {
	return expose.StatusLive, nil
}

func (p fakeExposeProvider) Stop(context.Context, expose.Record, expose.StopOpts) error {
	if p.stopped != nil {
		*p.stopped++
	}
	return nil
}

func (p fakeExposeProvider) Close() error {
	if p.closed != nil {
		*p.closed++
	}
	return nil
}

func TestUntrustDoesNotGenerateMissingCA(t *testing.T) {
	isolate(t)
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	oldUntrust := untrustAuthorityFunc
	t.Cleanup(func() { untrustAuthorityFunc = oldUntrust })
	untrustAuthorityFunc = func(*ca.CA) error {
		t.Fatal("untrust should not be called without an existing CA")
		return nil
	}

	var out, errb bytes.Buffer
	if code := Untrust(nil, &out, &errb); code != ExitOK {
		t.Fatalf("Untrust exit = %d, stderr=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "nothing to untrust") {
		t.Fatalf("stdout = %q", out.String())
	}
	if _, err := os.Stat(filepath.Join(dataHome, "gate", "ca", "root.crt")); !os.IsNotExist(err) {
		t.Fatalf("Untrust generated CA or stat failed: %v", err)
	}
}

func TestTrustStopsActivityBeforeTrustStoreCall(t *testing.T) {
	isolate(t)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	events := recordActivities(t)
	oldTrust := trustAuthorityFunc
	t.Cleanup(func() { trustAuthorityFunc = oldTrust })
	trustAuthorityFunc = func(*ca.CA) error {
		if got := lastEvent(*events); got != "complete:preparing trust store" {
			t.Fatalf("trust store called before activity stopped; events=%v", *events)
		}
		return nil
	}

	var out, errb bytes.Buffer
	if code := Trust(nil, &out, &errb); code != ExitOK {
		t.Fatalf("Trust exit = %d, stderr=%s", code, errb.String())
	}
}

func TestUntrustStopsActivityBeforeTrustStoreCall(t *testing.T) {
	isolate(t)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if _, err := ca.Load(paths.DataDir()); err != nil {
		t.Fatal(err)
	}
	events := recordActivities(t)
	oldUntrust := untrustAuthorityFunc
	t.Cleanup(func() { untrustAuthorityFunc = oldUntrust })
	untrustAuthorityFunc = func(*ca.CA) error {
		if got := lastEvent(*events); got != "complete:preparing trust store" {
			t.Fatalf("trust store called before activity stopped; events=%v", *events)
		}
		return nil
	}

	var out, errb bytes.Buffer
	if code := Untrust(nil, &out, &errb); code != ExitOK {
		t.Fatalf("Untrust exit = %d, stderr=%s", code, errb.String())
	}
}

func TestUntrustRemovesExistingCA(t *testing.T) {
	isolate(t)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	authority, err := ca.Load(paths.DataDir())
	if err != nil {
		t.Fatal(err)
	}
	oldUntrust := untrustAuthorityFunc
	t.Cleanup(func() { untrustAuthorityFunc = oldUntrust })
	var fingerprint string
	untrustAuthorityFunc = func(next *ca.CA) error {
		fingerprint = next.Fingerprint()
		return nil
	}

	var out, errb bytes.Buffer
	if code := Untrust(nil, &out, &errb); code != ExitOK {
		t.Fatalf("Untrust exit = %d, stderr=%s", code, errb.String())
	}
	if fingerprint != authority.Fingerprint() {
		t.Fatalf("untrusted fingerprint = %q, want %q", fingerprint, authority.Fingerprint())
	}
	if !strings.Contains(out.String(), "root CA untrusted") || !strings.Contains(out.String(), authority.Fingerprint()) {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestUntrustWorksWithoutRootKey(t *testing.T) {
	isolate(t)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	authority, err := ca.Load(paths.DataDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(paths.DataDir(), "ca", "root.key")); err != nil {
		t.Fatal(err)
	}
	oldUntrust := untrustAuthorityFunc
	t.Cleanup(func() { untrustAuthorityFunc = oldUntrust })
	var fingerprint string
	untrustAuthorityFunc = func(next *ca.CA) error {
		fingerprint = next.Fingerprint()
		return nil
	}

	var out, errb bytes.Buffer
	if code := Untrust(nil, &out, &errb); code != ExitOK {
		t.Fatalf("Untrust exit = %d, stderr=%s", code, errb.String())
	}
	if fingerprint != authority.Fingerprint() {
		t.Fatalf("untrusted fingerprint = %q, want %q", fingerprint, authority.Fingerprint())
	}
}

func TestUntrustPermissionError(t *testing.T) {
	isolate(t)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if _, err := ca.Load(paths.DataDir()); err != nil {
		t.Fatal(err)
	}
	oldUntrust := untrustAuthorityFunc
	t.Cleanup(func() { untrustAuthorityFunc = oldUntrust })
	untrustAuthorityFunc = func(*ca.CA) error {
		return os.ErrPermission
	}

	var out, errb bytes.Buffer
	if code := Untrust(nil, &out, &errb); code != ExitPerm {
		t.Fatalf("Untrust exit = %d, want permission; stdout=%s stderr=%s", code, out.String(), errb.String())
	}
}

func TestUntrustGenericError(t *testing.T) {
	isolate(t)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if _, err := ca.Load(paths.DataDir()); err != nil {
		t.Fatal(err)
	}
	oldUntrust := untrustAuthorityFunc
	t.Cleanup(func() { untrustAuthorityFunc = oldUntrust })
	untrustAuthorityFunc = func(*ca.CA) error {
		return errors.New("trust store failed")
	}

	var out, errb bytes.Buffer
	if code := Untrust(nil, &out, &errb); code != ExitError {
		t.Fatalf("Untrust exit = %d, want error; stdout=%s stderr=%s", code, out.String(), errb.String())
	}
}

func TestExposeScopedGlobalAndNamedProjectReload(t *testing.T) {
	isolate(t)
	shortConfigDir, err := os.MkdirTemp("/tmp", "gate-cli-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(shortConfigDir) })
	t.Setenv("XDG_CONFIG_HOME", shortConfigDir)
	t.Setenv("XDG_STATE_HOME", shortConfigDir)
	if err := registryStore().Update(func(r *registry.Registry) error {
		if err := r.Reserve(registry.Reservation{Service: "web", Domain: "web.localhost", Port: 4400, Standalone: true, Active: true}); err != nil {
			return err
		}
		return r.Reserve(registry.Reservation{Project: "demo", Service: "api", Domain: "api.localhost", Port: 4401, Active: true})
	}); err != nil {
		t.Fatal(err)
	}

	srv := proxy.New(nil, nil)
	stopListener, err := daemon.ServeAdmin(context.Background(), defaultListenerRef().socketPath(), srv)
	if err != nil {
		t.Fatalf("ServeAdmin listener: %v", err)
	}
	defer stopListener()

	oldSetRoutes := setListenerRoutesFunc
	t.Cleanup(func() { setListenerRoutesFunc = oldSetRoutes })
	var calls []struct {
		scope  string
		routes []proxy.Route
	}
	setListenerRoutesFunc = func(scope listenerDaemonRef, routes []proxy.Route) error {
		calls = append(calls, struct {
			scope  string
			routes []proxy.Route
		}{scope: scope.String(), routes: append([]proxy.Route{}, routes...)})
		return oldSetRoutes(scope, routes)
	}

	var out, errb bytes.Buffer
	if code := Expose([]string{"-g", "web", "--via", "local"}, &out, &errb); code != ExitOK {
		t.Fatalf("Expose -g exit = %d, stderr=%s", code, errb.String())
	}
	oldProvider := exposeProviderFor
	t.Cleanup(func() { exposeProviderFor = oldProvider })
	exposeProviderFor = func(string) (expose.Provider, error) {
		return fakeExposeProvider{}, nil
	}
	if code := Expose([]string{"-p", "demo", "api", "--via", "lan", "--auth", "user:pass"}, &out, &errb); code != ExitOK {
		t.Fatalf("Expose -p exit = %d, stderr=%s", code, errb.String())
	}
	if len(calls) != 2 {
		t.Fatalf("reload calls = %+v", calls)
	}
	if calls[0].scope != defaultListenerRef().String() || len(calls[0].routes) != 2 || routeExposed(calls[0].routes, "web.localhost", "") {
		t.Fatalf("first reload = %+v", calls[0])
	}
	if calls[1].scope != defaultListenerRef().String() || len(calls[1].routes) != 3 || routeExposed(calls[1].routes, "web.localhost", "") || !routeExposed(calls[1].routes, "api.localhost", "user:pass") || !routeExposed(calls[1].routes, "api.local", "user:pass") {
		t.Fatalf("second reload = %+v", calls[1])
	}
}

func TestExposeRejectsInactiveReservationBeforeProviderCall(t *testing.T) {
	isolate(t)
	if err := registryStore().Update(func(r *registry.Registry) error {
		return r.Reserve(registry.Reservation{Service: "web", Domain: "web.localhost", Port: 4400, Standalone: true})
	}); err != nil {
		t.Fatal(err)
	}
	oldProvider := exposeProviderFor
	t.Cleanup(func() { exposeProviderFor = oldProvider })
	calls := 0
	exposeProviderFor = func(string) (expose.Provider, error) {
		return fakeExposeProvider{called: &calls}, nil
	}

	var out, errb bytes.Buffer
	if code := Expose([]string{"-g", "web", "--via", "local"}, &out, &errb); code != ExitError {
		t.Fatalf("Expose inactive exit = %d, stderr=%s", code, errb.String())
	}
	if calls != 0 {
		t.Fatalf("provider called %d times", calls)
	}
}

func TestExposeCleansUpRecordAndProviderWhenReloadFails(t *testing.T) {
	isolate(t)
	shortConfigDir, err := os.MkdirTemp("/tmp", "gate-cli-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(shortConfigDir) })
	t.Setenv("XDG_CONFIG_HOME", shortConfigDir)
	t.Setenv("XDG_STATE_HOME", shortConfigDir)
	if err := registryStore().Update(func(r *registry.Registry) error {
		return r.Reserve(registry.Reservation{Service: "web", Domain: "web.localhost", Port: 4400, Standalone: true, Active: true})
	}); err != nil {
		t.Fatal(err)
	}
	srv := proxy.New(nil, nil)
	stopListener, err := daemon.ServeAdmin(context.Background(), defaultListenerRef().socketPath(), srv)
	if err != nil {
		t.Fatal(err)
	}
	defer stopListener()

	oldProvider := exposeProviderFor
	oldSetRoutes := setListenerRoutesFunc
	t.Cleanup(func() {
		exposeProviderFor = oldProvider
		setListenerRoutesFunc = oldSetRoutes
	})
	var stopped, closed int
	exposeProviderFor = func(string) (expose.Provider, error) {
		return fakeExposeProvider{stopped: &stopped, closed: &closed}, nil
	}
	setListenerRoutesFunc = func(listenerDaemonRef, []proxy.Route) error {
		return errors.New("reload failed")
	}

	var out, errb bytes.Buffer
	if code := Expose([]string{"-g", "web", "--via", "local"}, &out, &errb); code != ExitError {
		t.Fatalf("Expose exit = %d, stderr=%s", code, errb.String())
	}
	records, err := exposureStore().Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Fatalf("records = %+v", records)
	}
	if stopped == 0 || closed == 0 {
		t.Fatalf("cleanup stopped=%d closed=%d", stopped, closed)
	}
}

func TestExposePreservesExistingSessionRoutesInScope(t *testing.T) {
	isolate(t)
	shortConfigDir, err := os.MkdirTemp("/tmp", "gate-cli-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(shortConfigDir) })
	t.Setenv("XDG_CONFIG_HOME", shortConfigDir)
	t.Setenv("XDG_STATE_HOME", shortConfigDir)
	if err := registryStore().Update(func(r *registry.Registry) error {
		if err := r.Reserve(registry.Reservation{Service: "web", Domain: "web.localhost", Port: 4400, Standalone: true, Active: true}); err != nil {
			return err
		}
		return r.Reserve(registry.Reservation{Service: "api", Domain: "api.localhost", Port: 4401, Standalone: true, Active: true})
	}); err != nil {
		t.Fatal(err)
	}
	srv := proxy.New(nil, nil)
	stopListener, err := daemon.ServeAdmin(context.Background(), defaultListenerRef().socketPath(), srv)
	if err != nil {
		t.Fatalf("ServeAdmin listener: %v", err)
	}
	defer stopListener()
	oldSetRoutes := setListenerRoutesFunc
	t.Cleanup(func() { setListenerRoutesFunc = oldSetRoutes })
	var calls [][]proxy.Route
	setListenerRoutesFunc = func(scope listenerDaemonRef, routes []proxy.Route) error {
		if scope.String() != defaultListenerRef().String() {
			t.Fatalf("scope = %s", scope.String())
		}
		calls = append(calls, append([]proxy.Route{}, routes...))
		return oldSetRoutes(scope, routes)
	}

	oldProvider := exposeProviderFor
	t.Cleanup(func() { exposeProviderFor = oldProvider })
	exposeProviderFor = func(string) (expose.Provider, error) {
		return fakeExposeProvider{}, nil
	}

	var out, errb bytes.Buffer
	if code := Expose([]string{"-g", "web", "--via", "lan", "--auth", "web:pass"}, &out, &errb); code != ExitOK {
		t.Fatalf("Expose web exit = %d, stderr=%s", code, errb.String())
	}
	if code := Expose([]string{"-g", "api", "--via", "lan", "--auth", "api:pass"}, &out, &errb); code != ExitOK {
		t.Fatalf("Expose api exit = %d, stderr=%s", code, errb.String())
	}
	if len(calls) != 2 {
		t.Fatalf("calls = %+v", calls)
	}
	final := calls[1]
	var sawWeb, sawAPI bool
	for _, route := range final {
		if route.Domain == "web.localhost" && route.Exposed && route.Auth == "web:pass" {
			sawWeb = true
		}
		if route.Domain == "api.localhost" && route.Exposed && route.Auth == "api:pass" {
			sawAPI = true
		}
	}
	if !sawWeb || !sawAPI {
		t.Fatalf("final routes = %+v", final)
	}
}

func TestExposeAddsPublicURLHostAlias(t *testing.T) {
	isolate(t)
	shortConfigDir, err := os.MkdirTemp("/tmp", "gate-cli-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(shortConfigDir) })
	t.Setenv("XDG_CONFIG_HOME", shortConfigDir)
	t.Setenv("XDG_STATE_HOME", shortConfigDir)
	if err := registryStore().Update(func(r *registry.Registry) error {
		return r.Reserve(registry.Reservation{Service: "web", Domain: "local.stamp.is", Port: 4400, Standalone: true, Active: true})
	}); err != nil {
		t.Fatal(err)
	}
	srv := proxy.New(nil, nil)
	stopListener, err := daemon.ServeAdmin(context.Background(), defaultListenerRef().socketPath(), srv)
	if err != nil {
		t.Fatalf("ServeAdmin listener: %v", err)
	}
	defer stopListener()

	oldSetRoutes := setListenerRoutesFunc
	t.Cleanup(func() { setListenerRoutesFunc = oldSetRoutes })
	var final []proxy.Route
	setListenerRoutesFunc = func(scope listenerDaemonRef, routes []proxy.Route) error {
		if scope.String() != defaultListenerRef().String() {
			t.Fatalf("scope = %s", scope.String())
		}
		final = append([]proxy.Route{}, routes...)
		return oldSetRoutes(scope, routes)
	}

	oldProvider := exposeProviderFor
	t.Cleanup(func() { exposeProviderFor = oldProvider })
	var target string
	var servePort int
	exposeProviderFor = func(string) (expose.Provider, error) {
		return fakeExposeProvider{
			target: &target,
			port:   &servePort,
			result: expose.Result{URL: "https://anubis.tail6c50d7.ts.net:10443"},
		}, nil
	}

	var out, errb bytes.Buffer
	if code := Expose([]string{"-g", "web", "--via", "tailscale", "--auth", "user:pass"}, &out, &errb); code != ExitOK {
		t.Fatalf("Expose exit = %d, stderr=%s", code, errb.String())
	}
	if target != "https+insecure://local.stamp.is" {
		t.Fatalf("target = %q", target)
	}
	if servePort != 10443 {
		t.Fatalf("serve port = %d", servePort)
	}
	if !routeExposed(final, "local.stamp.is", "user:pass") {
		t.Fatalf("base route not exposed: %+v", final)
	}
	if !routeExposed(final, "anubis.tail6c50d7.ts.net", "user:pass") {
		t.Fatalf("public host alias not exposed: %+v", final)
	}
	if !routeForwardHost(final, "anubis.tail6c50d7.ts.net", "local.stamp.is") {
		t.Fatalf("public host alias missing forward host: %+v", final)
	}
	if !strings.Contains(out.String(), "https://anubis.tail6c50d7.ts.net") || !strings.Contains(out.String(), "local.stamp.is") {
		t.Fatalf("stdout = %s", out.String())
	}
	records, err := exposureStore().Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].ServePort != 10443 || !records[0].AuthEnabled {
		t.Fatalf("records = %+v", records)
	}
}

func TestExposeTailscaleRejectsSecondService(t *testing.T) {
	isolate(t)
	shortConfigDir, err := os.MkdirTemp("/tmp", "gate-cli-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(shortConfigDir) })
	t.Setenv("XDG_CONFIG_HOME", shortConfigDir)
	t.Setenv("XDG_STATE_HOME", shortConfigDir)
	if err := registryStore().Update(func(r *registry.Registry) error {
		if err := r.Reserve(registry.Reservation{Service: "web", Domain: "web.localhost", Port: 4400, Standalone: true, Active: true}); err != nil {
			return err
		}
		return r.Reserve(registry.Reservation{Service: "api", Domain: "api.localhost", Port: 4401, Standalone: true, Active: true})
	}); err != nil {
		t.Fatal(err)
	}
	if err := exposureStore().Upsert(expose.Record{
		Scope: daemonScopeGlobal, Service: "api", Provider: expose.ProviderTailscale,
		PublicURL: "https://anubis.tail6c50d7.ts.net:10443", Target: "api.localhost", ServePort: 10443,
	}); err != nil {
		t.Fatal(err)
	}
	oldProvider := exposeProviderFor
	t.Cleanup(func() { exposeProviderFor = oldProvider })
	called := 0
	exposeProviderFor = func(string) (expose.Provider, error) {
		return fakeExposeProvider{called: &called}, nil
	}

	var out, errb bytes.Buffer
	if code := Expose([]string{"-g", "web", "--via", "tailscale"}, &out, &errb); code != ExitConflict {
		t.Fatalf("Expose exit = %d, stderr=%s", code, errb.String())
	}
	if called != 0 {
		t.Fatalf("provider called %d times", called)
	}
	if !strings.Contains(errb.String(), "tailscale exposure already exists") {
		t.Fatalf("stderr = %s", errb.String())
	}
}

func TestDeriveLANDomain(t *testing.T) {
	tests := map[string]string{
		"app.example.com":    "app.example.com.local",
		"web.demo.localhost": "web.demo.local",
		"myapp.local":        "myapp.local",
		"API.Example.COM.":   "api.example.com.local",
	}
	for input, want := range tests {
		if got := deriveLANDomain(input); got != want {
			t.Fatalf("deriveLANDomain(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestExposeLANDerivesAliasFromPrimaryDomain(t *testing.T) {
	isolate(t)
	shortConfigDir, err := os.MkdirTemp("/tmp", "gate-cli-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(shortConfigDir) })
	t.Setenv("XDG_CONFIG_HOME", shortConfigDir)
	t.Setenv("XDG_STATE_HOME", shortConfigDir)
	if err := registryStore().Update(func(r *registry.Registry) error {
		return r.Reserve(registry.Reservation{Service: "web", Domain: "app.example.com", Port: 4400, Standalone: true, Active: true})
	}); err != nil {
		t.Fatal(err)
	}
	srv := proxy.New(nil, nil)
	stopListener, err := daemon.ServeAdmin(context.Background(), defaultListenerRef().socketPath(), srv)
	if err != nil {
		t.Fatalf("ServeAdmin listener: %v", err)
	}
	defer stopListener()

	oldSetRoutes := setListenerRoutesFunc
	t.Cleanup(func() { setListenerRoutesFunc = oldSetRoutes })
	var final []proxy.Route
	setListenerRoutesFunc = func(scope listenerDaemonRef, routes []proxy.Route) error {
		final = append([]proxy.Route{}, routes...)
		return oldSetRoutes(scope, routes)
	}

	oldProvider := exposeProviderFor
	t.Cleanup(func() { exposeProviderFor = oldProvider })
	exposeProviderFor = func(string) (expose.Provider, error) {
		return fakeExposeProvider{}, nil
	}

	var out, errb bytes.Buffer
	if code := Expose([]string{"-g", "web", "--via", "lan"}, &out, &errb); code != ExitOK {
		t.Fatalf("Expose exit = %d, stderr=%s", code, errb.String())
	}
	if !routeExposed(final, "app.example.com", "") {
		t.Fatalf("base route not exposed: %+v", final)
	}
	if !routeExposed(final, "app.example.com.local", "") {
		t.Fatalf("LAN alias route not exposed: %+v", final)
	}
	if !routeForwardHost(final, "app.example.com.local", "app.example.com") {
		t.Fatalf("LAN alias missing forward host: %+v", final)
	}
	if !strings.Contains(out.String(), "https://app.example.com.local") || !strings.Contains(out.String(), "app.example.com") {
		t.Fatalf("stdout = %s", out.String())
	}
}

func TestExposeLANUsesDomainOverride(t *testing.T) {
	isolate(t)
	if err := registryStore().Update(func(r *registry.Registry) error {
		return r.Reserve(registry.Reservation{Service: "web", Domain: "app.example.com", Port: 4400, Standalone: true, Active: true})
	}); err != nil {
		t.Fatal(err)
	}
	oldProvider := exposeProviderFor
	t.Cleanup(func() { exposeProviderFor = oldProvider })
	exposeProviderFor = func(string) (expose.Provider, error) {
		return fakeExposeProvider{}, nil
	}

	var out, errb bytes.Buffer
	if code := Expose([]string{"-g", "web", "--via", "lan", "--domain", "phone.local"}, &out, &errb); code != ExitOK {
		t.Fatalf("Expose exit = %d, stderr=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "https://phone.local") || !strings.Contains(out.String(), "app.example.com") {
		t.Fatalf("stdout = %s", out.String())
	}
}

func TestExposeDomainFlagValidation(t *testing.T) {
	isolate(t)
	if err := registryStore().Update(func(r *registry.Registry) error {
		return r.Reserve(registry.Reservation{Service: "web", Domain: "app.example.com", Port: 4400, Standalone: true, Active: true})
	}); err != nil {
		t.Fatal(err)
	}
	oldProvider := exposeProviderFor
	t.Cleanup(func() { exposeProviderFor = oldProvider })
	exposeProviderFor = func(string) (expose.Provider, error) {
		t.Fatal("provider should not be called for invalid --domain")
		return fakeExposeProvider{}, nil
	}

	var out, errb bytes.Buffer
	if code := Expose([]string{"-g", "web", "--via", "lan", "--domain", "phone.example.com"}, &out, &errb); code != ExitUsage {
		t.Fatalf("Expose invalid LAN domain exit = %d, stderr=%s", code, errb.String())
	}
	errb.Reset()
	if code := Expose([]string{"-g", "web", "--via", "cloudflared", "--domain", "phone.local", "--auth", "user:pass"}, &out, &errb); code != ExitUsage {
		t.Fatalf("Expose cloudflared --domain exit = %d, stderr=%s", code, errb.String())
	}
}

func TestExposeLANRejectsInvalidDerivedDomain(t *testing.T) {
	isolate(t)
	longPrimary := strings.Join([]string{
		strings.Repeat("a", 63),
		strings.Repeat("b", 63),
		strings.Repeat("c", 63),
		strings.Repeat("d", 61),
	}, ".")
	if err := registryStore().Update(func(r *registry.Registry) error {
		return r.Reserve(registry.Reservation{Service: "web", Domain: longPrimary, Port: 4400, Standalone: true, Active: true})
	}); err != nil {
		t.Fatal(err)
	}
	oldProvider := exposeProviderFor
	t.Cleanup(func() { exposeProviderFor = oldProvider })
	exposeProviderFor = func(string) (expose.Provider, error) {
		t.Fatal("provider should not be called for invalid derived LAN domain")
		return fakeExposeProvider{}, nil
	}

	var out, errb bytes.Buffer
	if code := Expose([]string{"-g", "web", "--via", "lan"}, &out, &errb); code != ExitUsage {
		t.Fatalf("Expose invalid derived LAN domain exit = %d, stderr=%s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "invalid domain") {
		t.Fatalf("stderr = %s", errb.String())
	}
}

func TestExposeLANDerivedAliasConflict(t *testing.T) {
	isolate(t)
	if err := registryStore().Update(func(r *registry.Registry) error {
		if err := r.Reserve(registry.Reservation{Service: "web", Domain: "app.example.com", Port: 4400, Standalone: true, Active: true}); err != nil {
			return err
		}
		return r.Reserve(registry.Reservation{Service: "api", Domain: "app.example.com.local", Port: 4401, Standalone: true, Active: true})
	}); err != nil {
		t.Fatal(err)
	}
	oldProvider := exposeProviderFor
	t.Cleanup(func() { exposeProviderFor = oldProvider })
	exposeProviderFor = func(string) (expose.Provider, error) {
		t.Fatal("provider should not be called when LAN alias conflicts")
		return fakeExposeProvider{}, nil
	}

	var out, errb bytes.Buffer
	if code := Expose([]string{"-g", "web", "--via", "lan"}, &out, &errb); code != ExitConflict {
		t.Fatalf("Expose conflict exit = %d, stderr=%s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "app.example.com.local") {
		t.Fatalf("stderr = %s", errb.String())
	}
}

func TestExposeLsAndStop(t *testing.T) {
	isolate(t)
	shortConfigDir, err := os.MkdirTemp("/tmp", "gate-cli-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(shortConfigDir) })
	t.Setenv("XDG_CONFIG_HOME", shortConfigDir)
	t.Setenv("XDG_STATE_HOME", shortConfigDir)
	if err := registryStore().Update(func(r *registry.Registry) error {
		return r.Reserve(registry.Reservation{Service: "web", Domain: "web.localhost", Port: 4400, Standalone: true, Active: true})
	}); err != nil {
		t.Fatal(err)
	}
	srv := proxy.New(nil, nil)
	stopListener, err := daemon.ServeAdmin(context.Background(), defaultListenerRef().socketPath(), srv)
	if err != nil {
		t.Fatal(err)
	}
	defer stopListener()

	oldSetRoutes := setListenerRoutesFunc
	t.Cleanup(func() { setListenerRoutesFunc = oldSetRoutes })
	var calls [][]proxy.Route
	setListenerRoutesFunc = func(_ listenerDaemonRef, routes []proxy.Route) error {
		calls = append(calls, append([]proxy.Route{}, routes...))
		return oldSetRoutes(defaultListenerRef(), routes)
	}

	var out, errb bytes.Buffer
	if code := Expose([]string{"-g", "web", "--via", "local"}, &out, &errb); code != ExitOK {
		t.Fatalf("Expose exit = %d, stderr=%s", code, errb.String())
	}
	out.Reset()
	if code := Expose([]string{"ls", "-g", "--json"}, &out, &errb); code != ExitOK {
		t.Fatalf("Expose ls exit = %d, stderr=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), `"auth": false`) || !strings.Contains(out.String(), `"provider": "local"`) {
		t.Fatalf("ls json = %s", out.String())
	}
	out.Reset()
	if code := Expose([]string{"stop", "-g", "web", "--via", "local", "--json"}, &out, &errb); code != ExitOK {
		t.Fatalf("Expose stop exit = %d, stderr=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), `"removed": true`) {
		t.Fatalf("stop json = %s", out.String())
	}
	if len(calls) < 2 {
		t.Fatalf("reload calls = %+v", calls)
	}
	final := calls[len(calls)-1]
	if routeExposed(final, "web.localhost", "") {
		t.Fatalf("final routes should not be exposed: %+v", final)
	}
}

func TestExposeRejectsLocalAuthBeforeProviderCall(t *testing.T) {
	isolate(t)
	if err := registryStore().Update(func(r *registry.Registry) error {
		return r.Reserve(registry.Reservation{Service: "web", Domain: "web.localhost", Port: 4400, Standalone: true, Active: true})
	}); err != nil {
		t.Fatal(err)
	}
	oldProvider := exposeProviderFor
	t.Cleanup(func() { exposeProviderFor = oldProvider })
	calls := 0
	exposeProviderFor = func(string) (expose.Provider, error) {
		return fakeExposeProvider{called: &calls}, nil
	}
	var out, errb bytes.Buffer
	if code := Expose([]string{"-g", "web", "--via", "local", "--auth", "user:pass"}, &out, &errb); code != ExitUsage {
		t.Fatalf("Expose exit = %d, stderr=%s", code, errb.String())
	}
	if calls != 0 {
		t.Fatalf("provider called %d times", calls)
	}
}

func TestExposeRejectsEmptyViaAuthBeforeProviderCall(t *testing.T) {
	isolate(t)
	if err := registryStore().Update(func(r *registry.Registry) error {
		return r.Reserve(registry.Reservation{Service: "web", Domain: "web.localhost", Port: 4400, Standalone: true, Active: true})
	}); err != nil {
		t.Fatal(err)
	}
	oldProvider := exposeProviderFor
	t.Cleanup(func() { exposeProviderFor = oldProvider })
	calls := 0
	exposeProviderFor = func(string) (expose.Provider, error) {
		calls++
		return fakeExposeProvider{}, nil
	}
	var out, errb bytes.Buffer
	if code := Expose([]string{"-g", "web", "--via=", "--auth", "user:pass"}, &out, &errb); code != ExitUsage {
		t.Fatalf("Expose exit = %d, stderr=%s", code, errb.String())
	}
	if calls != 0 {
		t.Fatalf("provider called %d times", calls)
	}
}

func TestExposeStoresEmptyViaAsLocal(t *testing.T) {
	isolate(t)
	if err := registryStore().Update(func(r *registry.Registry) error {
		return r.Reserve(registry.Reservation{Service: "web", Domain: "web.localhost", Port: 4400, Standalone: true, Active: true})
	}); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := Expose([]string{"-g", "web", "--via="}, &out, &errb); code != ExitOK {
		t.Fatalf("Expose exit = %d, stderr=%s", code, errb.String())
	}
	records, err := exposureStore().Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Provider != expose.ProviderLocal || records[0].AuthEnabled {
		t.Fatalf("records = %+v", records)
	}
}

func TestExposeRejectsMalformedAuthBeforeProviderCall(t *testing.T) {
	isolate(t)
	if err := registryStore().Update(func(r *registry.Registry) error {
		return r.Reserve(registry.Reservation{Service: "web", Domain: "web.localhost", Port: 4400, Standalone: true, Active: true})
	}); err != nil {
		t.Fatal(err)
	}
	oldProvider := exposeProviderFor
	t.Cleanup(func() { exposeProviderFor = oldProvider })
	calls := 0
	exposeProviderFor = func(string) (expose.Provider, error) {
		return fakeExposeProvider{called: &calls}, nil
	}
	for _, auth := range []string{"admin", ":pass", "user:", "   :pass"} {
		t.Run(auth, func(t *testing.T) {
			var out, errb bytes.Buffer
			if code := Expose([]string{"-g", "web", "--via", "lan", "--auth", auth}, &out, &errb); code != ExitUsage {
				t.Fatalf("Expose exit = %d, stderr=%s", code, errb.String())
			}
		})
	}
	if calls != 0 {
		t.Fatalf("provider called %d times", calls)
	}
	records, err := exposureStore().Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Fatalf("records = %+v", records)
	}
}

func TestExposeAcceptsColonInPassword(t *testing.T) {
	isolate(t)
	if err := registryStore().Update(func(r *registry.Registry) error {
		return r.Reserve(registry.Reservation{Service: "web", Domain: "web.localhost", Port: 4400, Standalone: true, Active: true})
	}); err != nil {
		t.Fatal(err)
	}
	oldProvider := exposeProviderFor
	t.Cleanup(func() { exposeProviderFor = oldProvider })
	var gotAuth string
	exposeProviderFor = func(string) (expose.Provider, error) {
		return fakeExposeProvider{auth: &gotAuth}, nil
	}
	var out, errb bytes.Buffer
	if code := Expose([]string{"-g", "web", "--via", "lan", "--auth", "user:p:a:s:s"}, &out, &errb); code != ExitOK {
		t.Fatalf("Expose exit = %d, stderr=%s", code, errb.String())
	}
	if gotAuth != "user:p:a:s:s" {
		t.Fatalf("auth = %q", gotAuth)
	}
	b, err := os.ReadFile(filepath.Join(paths.ConfigDir(), "exposures.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "p:a:s:s") {
		t.Fatalf("exposures.json leaked auth secret:\n%s", string(b))
	}
	if !strings.Contains(string(b), `"auth_enabled": true`) {
		t.Fatalf("exposures.json missing auth flag:\n%s", string(b))
	}
}

func TestExposeCloudflaredRequiresAuthOrNoAuth(t *testing.T) {
	isolate(t)
	if err := registryStore().Update(func(r *registry.Registry) error {
		return r.Reserve(registry.Reservation{Service: "web", Domain: "web.localhost", Port: 4400, Standalone: true, Active: true})
	}); err != nil {
		t.Fatal(err)
	}
	oldProvider := exposeProviderFor
	t.Cleanup(func() { exposeProviderFor = oldProvider })
	calls := 0
	var gotAuth string
	exposeProviderFor = func(string) (expose.Provider, error) {
		return fakeExposeProvider{called: &calls, auth: &gotAuth}, nil
	}
	var out, errb bytes.Buffer
	if code := Expose([]string{"-g", "web", "--via", "cloudflared"}, &out, &errb); code != ExitUsage {
		t.Fatalf("Expose missing auth exit = %d, stderr=%s", code, errb.String())
	}
	if calls != 0 {
		t.Fatalf("provider called before auth requirement")
	}
	if code := Expose([]string{"-g", "web", "--via", "cloudflared", "--auth", "user:pass"}, &out, &errb); code != ExitOK {
		t.Fatalf("Expose auth exit = %d, stderr=%s", code, errb.String())
	}
	if gotAuth != "user:pass" {
		t.Fatalf("auth = %q", gotAuth)
	}
	gotAuth = "not reset"
	if code := Expose([]string{"-g", "web", "--via", "cloudflared", "--no-auth"}, &out, &errb); code != ExitOK {
		t.Fatalf("Expose --no-auth exit = %d, stderr=%s", code, errb.String())
	}
	if gotAuth != "" {
		t.Fatalf("auth with --no-auth = %q", gotAuth)
	}
}

func TestExposeLsReportsMissingAuthSecret(t *testing.T) {
	isolate(t)
	if err := registryStore().Update(func(r *registry.Registry) error {
		return r.Reserve(registry.Reservation{Service: "web", Domain: "web.localhost", Port: 4400, Standalone: true, Active: true})
	}); err != nil {
		t.Fatal(err)
	}
	if err := exposureStore().Upsert(expose.Record{
		Scope: daemonScopeGlobal, Service: "web", Provider: expose.ProviderLAN,
		PublicURL: "https://web.localhost", Target: "web.localhost", AuthEnabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	oldProvider := exposeProviderFor
	t.Cleanup(func() { exposeProviderFor = oldProvider })
	exposeProviderFor = func(string) (expose.Provider, error) {
		return fakeExposeProvider{}, nil
	}

	var out, errb bytes.Buffer
	if code := Expose([]string{"ls", "-g", "--json"}, &out, &errb); code != ExitOK {
		t.Fatalf("Expose ls json exit = %d, stderr=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), `"auth": true`) || !strings.Contains(out.String(), `"auth_status": "missing"`) {
		t.Fatalf("ls json = %s", out.String())
	}
	out.Reset()
	if code := Expose([]string{"ls", "-g"}, &out, &errb); code != ExitOK {
		t.Fatalf("Expose ls exit = %d, stderr=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "missing") {
		t.Fatalf("ls plain = %s", out.String())
	}
}

func TestExposeLsReportsActiveAuthSecret(t *testing.T) {
	isolate(t)
	shortConfigDir, err := os.MkdirTemp("/tmp", "gate-cli-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(shortConfigDir) })
	t.Setenv("XDG_CONFIG_HOME", shortConfigDir)
	t.Setenv("XDG_STATE_HOME", shortConfigDir)
	if err := registryStore().Update(func(r *registry.Registry) error {
		return r.Reserve(registry.Reservation{Service: "web", Domain: "web.localhost", Port: 4400, Standalone: true, Active: true})
	}); err != nil {
		t.Fatal(err)
	}
	srv := proxy.New(nil, nil)
	stopListener, err := daemon.ServeAdmin(context.Background(), defaultListenerRef().socketPath(), srv)
	if err != nil {
		t.Fatalf("ServeAdmin listener: %v", err)
	}
	defer stopListener()
	oldProvider := exposeProviderFor
	t.Cleanup(func() { exposeProviderFor = oldProvider })
	exposeProviderFor = func(string) (expose.Provider, error) {
		return fakeExposeProvider{}, nil
	}

	var out, errb bytes.Buffer
	if code := Expose([]string{"-g", "web", "--via", "lan", "--auth", "user:pass"}, &out, &errb); code != ExitOK {
		t.Fatalf("Expose exit = %d, stderr=%s", code, errb.String())
	}
	exposeSessionMu.Lock()
	exposeSessionRoutes = map[string]map[string]exposeSessionRoute{}
	exposeSessionMu.Unlock()
	out.Reset()
	if code := Expose([]string{"ls", "-g", "--json"}, &out, &errb); code != ExitOK {
		t.Fatalf("Expose ls exit = %d, stderr=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), `"auth_status": "active"`) {
		t.Fatalf("ls json = %s", out.String())
	}
}

func TestExposeStopPreservesRecordWhenReloadFails(t *testing.T) {
	isolate(t)
	shortConfigDir, err := os.MkdirTemp("/tmp", "gate-cli-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(shortConfigDir) })
	t.Setenv("XDG_CONFIG_HOME", shortConfigDir)
	t.Setenv("XDG_STATE_HOME", shortConfigDir)
	if err := registryStore().Update(func(r *registry.Registry) error {
		return r.Reserve(registry.Reservation{Service: "web", Domain: "web.localhost", Port: 4400, Standalone: true, Active: true})
	}); err != nil {
		t.Fatal(err)
	}
	if err := exposureStore().Upsert(expose.Record{
		Scope: daemonScopeGlobal, Service: "web", Provider: expose.ProviderLocal,
		PublicURL: "https://web.localhost", Target: "web.localhost",
	}); err != nil {
		t.Fatal(err)
	}
	srv := proxy.New(nil, nil)
	stopListener, err := daemon.ServeAdmin(context.Background(), defaultListenerRef().socketPath(), srv)
	if err != nil {
		t.Fatal(err)
	}
	defer stopListener()

	oldSetRoutes := setListenerRoutesFunc
	t.Cleanup(func() { setListenerRoutesFunc = oldSetRoutes })
	setListenerRoutesFunc = func(listenerDaemonRef, []proxy.Route) error {
		return errors.New("reload failed")
	}

	var out, errb bytes.Buffer
	if code := Expose([]string{"stop", "-g", "web", "--via", "local"}, &out, &errb); code != ExitError {
		t.Fatalf("Expose stop exit = %d, stderr=%s", code, errb.String())
	}
	records, err := exposureStore().Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Service != "web" {
		t.Fatalf("records = %+v", records)
	}
}

func TestExposeStopTailscaleRequiresForce(t *testing.T) {
	isolate(t)
	if err := exposureStore().Upsert(expose.Record{
		Scope: daemonScopeGlobal, Service: "web", Provider: expose.ProviderTailscale,
		PublicURL: "https://web.localhost", Target: "web.localhost",
	}); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := Expose([]string{"stop", "-g", "web", "--via", "tailscale"}, &out, &errb); code != ExitError {
		t.Fatalf("Expose stop exit = %d, want error", code)
	}
	records, err := exposureStore().Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Provider != expose.ProviderTailscale {
		t.Fatalf("records after failed stop = %+v", records)
	}
	oldProvider := exposeProviderFor
	t.Cleanup(func() { exposeProviderFor = oldProvider })
	exposeProviderFor = func(string) (expose.Provider, error) {
		return fakeExposeProvider{}, nil
	}
	if code := Expose([]string{"stop", "-g", "web", "--via", "tailscale", "--force"}, &out, &errb); code != ExitOK {
		t.Fatalf("Expose stop --force exit = %d, stderr=%s", code, errb.String())
	}
}

func TestExposeStopTailscaleRemovesAllTailscaleRecords(t *testing.T) {
	isolate(t)
	if err := exposureStore().Upsert(expose.Record{
		Scope: daemonScopeGlobal, Service: "web", Provider: expose.ProviderTailscale,
		PublicURL: "https://web.tail.ts.net:10443", Target: "web.localhost", ServePort: 10443,
	}); err != nil {
		t.Fatal(err)
	}
	if err := exposureStore().Upsert(expose.Record{
		Scope: daemonScopeGlobal, Service: "api", Provider: expose.ProviderTailscale,
		PublicURL: "https://api.tail.ts.net:10444", Target: "api.localhost", ServePort: 10444,
	}); err != nil {
		t.Fatal(err)
	}
	if err := exposureStore().Upsert(expose.Record{
		Scope: daemonScopeGlobal, Service: "local", Provider: expose.ProviderLocal,
		PublicURL: "https://local.localhost", Target: "local.localhost",
	}); err != nil {
		t.Fatal(err)
	}
	oldProvider := exposeProviderFor
	t.Cleanup(func() { exposeProviderFor = oldProvider })
	stopped := 0
	exposeProviderFor = func(string) (expose.Provider, error) {
		return fakeExposeProvider{stopped: &stopped}, nil
	}

	var out, errb bytes.Buffer
	if code := Expose([]string{"stop", "-g", "web", "--via", "tailscale"}, &out, &errb); code != ExitOK {
		t.Fatalf("Expose stop exit = %d, stderr=%s", code, errb.String())
	}
	if stopped != 1 {
		t.Fatalf("stopped = %d", stopped)
	}
	records, err := exposureStore().Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Provider != expose.ProviderLocal {
		t.Fatalf("records = %+v", records)
	}
}

func routeExposed(routes []proxy.Route, domain, auth string) bool {
	for _, route := range routes {
		if route.Domain == domain && route.Exposed && route.Auth == auth {
			return true
		}
	}
	return false
}

func routeForwardHost(routes []proxy.Route, domain, forwardHost string) bool {
	for _, route := range routes {
		if route.Domain == domain && route.ForwardHost == forwardHost {
			return true
		}
	}
	return false
}

package expose

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestForSelectsProvider(t *testing.T) {
	cases := map[string]any{
		"":            Local{},
		"local":       Local{},
		"lan":         LAN{},
		"cloudflared": &Cloudflared{},
		"tailscale":   Tailscale{},
	}
	for name := range cases {
		if _, err := For(name); err != nil {
			t.Errorf("For(%q): %v", name, err)
		}
	}
	if _, err := For("bogus"); err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestLocalExpose(t *testing.T) {
	result, err := Local{}.Expose(context.Background(), "app.localhost", Opts{})
	if err != nil || result.URL != "https://app.localhost" {
		t.Fatalf("Local.Expose = %+v, %v", result, err)
	}
}

func TestLANRequiresDotLocal(t *testing.T) {
	if _, err := (LAN{}).Expose(context.Background(), "app.example.com", Opts{}); err == nil {
		t.Fatal("lan should reject non-.local domain")
	}
	result, err := LAN{}.Expose(context.Background(), "app.local", Opts{})
	if err != nil || result.URL != "https://app.local" {
		t.Fatalf("LAN.Expose = %+v, %v", result, err)
	}
}

func TestCloudflaredStatusAndStopIgnoreStalePID(t *testing.T) {
	oldProcessArgs := processArgsForPID
	t.Cleanup(func() { processArgsForPID = oldProcessArgs })
	processArgsForPID = func(int) (string, error) { return "go test ./internal/expose", nil }
	record := Record{PID: os.Getpid(), Target: "web.localhost", Provider: ProviderCloudflared, Command: "cloudflared tunnel --url https://web.localhost"}
	status, err := (&Cloudflared{}).Status(context.Background(), record)
	if err != nil {
		t.Fatal(err)
	}
	if status != StatusDown {
		t.Fatalf("status = %q, want down", status)
	}
	if err := (&Cloudflared{}).Stop(context.Background(), record, StopOpts{}); err != nil {
		t.Fatal(err)
	}
	if !processExists(os.Getpid()) {
		t.Fatal("current process should still be alive")
	}
}

func TestCloudflaredProcessMatchesRequiresTarget(t *testing.T) {
	oldProcessArgs := processArgsForPID
	t.Cleanup(func() { processArgsForPID = oldProcessArgs })
	processArgsForPID = func(int) (string, error) {
		return "cloudflared tunnel --url https://other.localhost", nil
	}
	record := Record{PID: os.Getpid(), Target: "web.localhost", Provider: ProviderCloudflared}
	if cloudflaredProcessMatches(record) {
		t.Fatal("stale pid with different target matched")
	}
	processArgsForPID = func(int) (string, error) {
		return "cloudflared tunnel --url https://web.localhost", nil
	}
	record.Command = "cloudflared tunnel --url https://web.localhost"
	if !cloudflaredProcessMatches(record) {
		t.Fatal("matching cloudflared target did not match")
	}
	processArgsForPID = func(int) (string, error) {
		return "python tool.py cloudflared tunnel --url https://web.localhost", nil
	}
	if cloudflaredProcessMatches(record) {
		t.Fatal("non-cloudflared executable matched")
	}
}

func TestCloudflaredStatusPreservesUnverifiableLivePID(t *testing.T) {
	oldProcessArgs := processArgsForPID
	t.Cleanup(func() { processArgsForPID = oldProcessArgs })
	processArgsForPID = func(int) (string, error) { return "", errors.New("ps denied") }
	record := Record{PID: os.Getpid(), Provider: ProviderCloudflared, Command: "cloudflared tunnel --url https://web.localhost"}
	status, err := (&Cloudflared{}).Status(context.Background(), record)
	if err == nil || status != StatusUnverified {
		t.Fatalf("status = %q, err = %v", status, err)
	}
	if err := (&Cloudflared{}).Stop(context.Background(), record, StopOpts{}); err == nil {
		t.Fatal("Stop forgot an unverifiable live process")
	}
}

func TestCloudflaredExposeSucceedsBeforeTimeout(t *testing.T) {
	oldCommand := cloudflaredCommand
	oldTimeout := cloudflaredStartupTimeout
	t.Cleanup(func() {
		cloudflaredCommand = oldCommand
		cloudflaredStartupTimeout = oldTimeout
	})
	cloudflaredStartupTimeout = time.Second
	var targetURL string
	cloudflaredCommand = func(ctx context.Context, target string) *exec.Cmd {
		targetURL = target
		return exec.CommandContext(ctx, "sh", "-c", "echo https://quick.trycloudflare.com >&2; exec sleep 10")
	}

	provider := &Cloudflared{}
	result, err := provider.Expose(context.Background(), "web.localhost", Opts{TargetURL: "https://web.localhost:9443"})
	if err != nil {
		t.Fatalf("Expose: %v", err)
	}
	if result.URL != "https://quick.trycloudflare.com" || result.PID == 0 {
		t.Fatalf("result = %+v", result)
	}
	if targetURL != "https://web.localhost:9443" {
		t.Fatalf("cloudflared target = %q", targetURL)
	}
	if err := provider.Close(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		t.Fatalf("Close: %v", err)
	}
}

func TestCloudflaredExposeTimesOutAndKillsProcess(t *testing.T) {
	oldCommand := cloudflaredCommand
	oldTimeout := cloudflaredStartupTimeout
	t.Cleanup(func() {
		cloudflaredCommand = oldCommand
		cloudflaredStartupTimeout = oldTimeout
	})
	pidPath := filepath.Join(t.TempDir(), "cloudflared.pid")
	cloudflaredStartupTimeout = 500 * time.Millisecond
	cloudflaredCommand = func(ctx context.Context, _ string) *exec.Cmd {
		script := fmt.Sprintf("echo $$ > %q; echo waiting >&2; exec sleep 60", pidPath)
		//nolint:gosec // G204: test-controlled shell script used to simulate cloudflared.
		return exec.CommandContext(ctx, "sh", "-c", script)
	}

	provider := &Cloudflared{}
	_, err := provider.Expose(context.Background(), "web.localhost", Opts{})
	if err == nil || !strings.Contains(err.Error(), "public URL") || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("Expose error = %v", err)
	}
	assertCloudflaredCloseReturns(t, provider)
	b, readErr := os.ReadFile(pidPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	pid, convErr := strconv.Atoi(strings.TrimSpace(string(b)))
	if convErr != nil {
		t.Fatal(convErr)
	}
	if processExists(pid) {
		t.Fatalf("cloudflared process %d still exists after timeout", pid)
	}
}

func TestCloudflaredExposeReportsNoPublicURL(t *testing.T) {
	oldCommand := cloudflaredCommand
	oldTimeout := cloudflaredStartupTimeout
	t.Cleanup(func() {
		cloudflaredCommand = oldCommand
		cloudflaredStartupTimeout = oldTimeout
	})
	cloudflaredStartupTimeout = time.Second
	cloudflaredCommand = func(ctx context.Context, _ string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "echo no url >&2")
	}

	provider := &Cloudflared{}
	_, err := provider.Expose(context.Background(), "web.localhost", Opts{})
	if err == nil || !strings.Contains(err.Error(), "public URL") {
		t.Fatalf("Expose error = %v", err)
	}
	assertCloudflaredCloseReturns(t, provider)
}

func TestTailscaleExposeUsesNodeURLAndOriginTarget(t *testing.T) {
	oldStatusCommand := tailscaleStatusCommand
	oldServeCommand := tailscaleServeCommand
	t.Cleanup(func() {
		tailscaleStatusCommand = oldStatusCommand
		tailscaleServeCommand = oldServeCommand
	})
	tailscaleStatusCommand = func(ctx context.Context) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", `printf '{"Self":{"DNSName":"anubis.tail6c50d7.ts.net."}}'`)
	}
	var gotTarget string
	var gotPort int
	tailscaleServeCommand = func(ctx context.Context, target string, httpsPort int) *exec.Cmd {
		gotTarget = target
		gotPort = httpsPort
		return exec.CommandContext(ctx, "sh", "-c", "true")
	}

	result, err := Tailscale{}.Expose(context.Background(), "local.stamp.is", Opts{
		TargetURL: "https+insecure://local.stamp.is",
		ServePort: 10443,
	})
	if err != nil {
		t.Fatalf("Expose: %v", err)
	}
	if result.URL != "https://anubis.tail6c50d7.ts.net:10443" {
		t.Fatalf("URL = %q", result.URL)
	}
	if gotTarget != "https+insecure://local.stamp.is" {
		t.Fatalf("target = %q", gotTarget)
	}
	if gotPort != 10443 {
		t.Fatalf("port = %d", gotPort)
	}
}

func TestTailscaleExposeFailsBeforeServeWhenNodeURLMissing(t *testing.T) {
	oldStatusCommand := tailscaleStatusCommand
	oldServeCommand := tailscaleServeCommand
	t.Cleanup(func() {
		tailscaleStatusCommand = oldStatusCommand
		tailscaleServeCommand = oldServeCommand
	})
	tailscaleStatusCommand = func(ctx context.Context) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", `printf '{"Self":{}}'`)
	}
	serveCalled := false
	tailscaleServeCommand = func(ctx context.Context, target string, httpsPort int) *exec.Cmd {
		serveCalled = true
		return exec.CommandContext(ctx, "sh", "-c", "true")
	}

	_, err := Tailscale{}.Expose(context.Background(), "local.stamp.is", Opts{})
	if err == nil || !strings.Contains(err.Error(), "DNS name") {
		t.Fatalf("Expose error = %v", err)
	}
	if serveCalled {
		t.Fatal("serve command called before node URL was known")
	}
}

func TestTailscaleStopDisablesOwnedServePort(t *testing.T) {
	oldOffCommand := tailscaleServeOffCommand
	oldStatusCommand := tailscaleServeStatusCommand
	t.Cleanup(func() {
		tailscaleServeOffCommand = oldOffCommand
		tailscaleServeStatusCommand = oldStatusCommand
	})
	tailscaleServeStatusCommand = func(ctx context.Context) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", `printf '{"Web":{"node.ts.net:10443":{"Handlers":{"/":{"Proxy":"https+insecure://local.stamp.is"}}}}}'`)
	}
	offPort := 0
	tailscaleServeOffCommand = func(ctx context.Context, port int) *exec.Cmd {
		offPort = port
		return exec.CommandContext(ctx, "sh", "-c", "true")
	}
	record := Record{
		Provider:  ProviderTailscale,
		Target:    "local.stamp.is",
		ServePort: 10443,
		Command:   "tailscale serve --bg --https=10443 https+insecure://local.stamp.is",
	}

	if err := (Tailscale{}).Stop(context.Background(), record, StopOpts{}); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if offPort != 10443 {
		t.Fatalf("off port = %d, want 10443", offPort)
	}
}

func TestTailscaleStopRequiresOwnershipOrForce(t *testing.T) {
	oldOffCommand := tailscaleServeOffCommand
	oldStatusCommand := tailscaleServeStatusCommand
	t.Cleanup(func() {
		tailscaleServeOffCommand = oldOffCommand
		tailscaleServeStatusCommand = oldStatusCommand
	})
	tailscaleServeStatusCommand = func(ctx context.Context) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", `printf '{"Web":{}}'`)
	}
	offPort := 0
	tailscaleServeOffCommand = func(ctx context.Context, port int) *exec.Cmd {
		offPort = port
		return exec.CommandContext(ctx, "sh", "-c", "true")
	}
	record := Record{Provider: ProviderTailscale, Target: "local.stamp.is"}

	err := (Tailscale{}).Stop(context.Background(), record, StopOpts{})
	if err == nil || !strings.Contains(err.Error(), "not owned by gate") {
		t.Fatalf("Stop error = %v", err)
	}
	if offPort != 0 {
		t.Fatal("off command called without ownership or force")
	}
	if err := (Tailscale{}).Stop(context.Background(), record, StopOpts{Force: true}); err != nil {
		t.Fatalf("Stop --force: %v", err)
	}
	if offPort != 443 {
		t.Fatalf("off port = %d, want default 443", offPort)
	}
}

func TestTailscaleOwnershipIncludesCustomOriginPort(t *testing.T) {
	record := Record{
		Provider: ProviderTailscale, Target: "web.localhost", OriginURL: "https://web.localhost:9443",
		ServePort: 10443, Command: "tailscale serve --bg --https=10443 https+insecure://web.localhost:9443",
	}
	if !tailscaleRecordOwned(record) {
		t.Fatalf("custom-port record was not recognized: %+v", record)
	}
	oldStatusCommand := tailscaleServeStatusCommand
	t.Cleanup(func() { tailscaleServeStatusCommand = oldStatusCommand })
	tailscaleServeStatusCommand = func(ctx context.Context) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", `printf '{"Web":{"node.ts.net:10443":{"Handlers":{"/":{"Proxy":"https+insecure://web.localhost:9443"}}}}}'`)
	}
	status, err := (Tailscale{}).Status(context.Background(), record)
	if err != nil || status != StatusLive {
		t.Fatalf("status = %q, err = %v", status, err)
	}
}

func assertCloudflaredCloseReturns(t *testing.T, provider *Cloudflared) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- provider.Close() }()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, os.ErrProcessDone) {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close blocked after failed Expose")
	}
}

func TestBasicAuth(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := BasicAuth(ok, "user:secret")
	srv := httptest.NewServer(h)
	defer srv.Close()

	// No credentials -> 401.
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no-creds status = %d, want 401", resp.StatusCode)
	}

	// Correct credentials -> 200.
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	req.SetBasicAuth("user", "secret")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("good-creds status = %d, want 200", resp.StatusCode)
	}
}

func TestBasicAuthEmptyPassthrough(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	if got := BasicAuth(ok, ""); got == nil {
		t.Fatal("empty auth should return the handler unchanged")
	}
}

func TestStorePersistsAndRedactsAuth(t *testing.T) {
	store := Store{Path: filepath.Join(t.TempDir(), "exposures.json")}
	record := Record{
		Scope: "global", Service: "web", Provider: ProviderCloudflared,
		PublicURL: "https://public.example", Target: "web.localhost", AuthEnabled: true, PID: os.Getpid(),
	}
	if err := store.Upsert(record); err != nil {
		t.Fatal(err)
	}
	records, err := store.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || !records[0].AuthEnabled || records[0].PublicURL != record.PublicURL {
		t.Fatalf("records = %+v", records)
	}
	b, err := os.ReadFile(store.Path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "user:pass") || strings.Contains(string(b), "secret") {
		t.Fatalf("store leaked credentials:\n%s", b)
	}
}

func TestStoreDeleteByKey(t *testing.T) {
	store := Store{Path: filepath.Join(t.TempDir(), "exposures.json")}
	record := Record{Scope: "global", Service: "web", Provider: ProviderLocal, PublicURL: "https://web.localhost", Target: "web.localhost"}
	if err := store.Upsert(record); err != nil {
		t.Fatal(err)
	}
	removed, err := store.Delete(record)
	if err != nil || !removed {
		t.Fatalf("Delete = %v, %v", removed, err)
	}
	records, err := store.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Fatalf("records = %+v", records)
	}
}

func TestStoreConcurrentUpsert(t *testing.T) {
	store := Store{Path: filepath.Join(t.TempDir(), "exposures.json")}
	const n = 24
	var wg sync.WaitGroup
	errc := make(chan error, n)
	for i := 0; i < n; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			errc <- store.Upsert(Record{
				Scope: "global", Service: fmt.Sprintf("svc-%02d", i), Provider: ProviderLocal,
				PublicURL: fmt.Sprintf("https://svc-%02d.localhost", i),
				Target:    fmt.Sprintf("svc-%02d.localhost", i),
			})
		}()
	}
	wg.Wait()
	close(errc)
	for err := range errc {
		if err != nil {
			t.Fatal(err)
		}
	}
	records, err := store.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != n {
		t.Fatalf("records = %d, want %d: %+v", len(records), n, records)
	}
	info, err := os.Stat(store.Path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %s, want 0600", got)
	}
}

func TestStoreRejectsFutureSchemaWithoutOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "exposures.json")
	original := []byte(`{"version":999,"records":[]}` + "\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	store := Store{Path: path}
	if _, err := store.Read(); err == nil || !strings.Contains(err.Error(), "newer than supported") {
		t.Fatalf("Read error = %v", err)
	}
	if err := store.Write(nil); err == nil || !strings.Contains(err.Error(), "newer than supported") {
		t.Fatalf("Write error = %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(original) {
		t.Fatalf("future store changed: %s", after)
	}
}

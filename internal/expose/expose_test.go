package expose

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
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
	record := Record{PID: os.Getpid(), Target: "web.localhost", Provider: ProviderCloudflared}
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

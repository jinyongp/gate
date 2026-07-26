package cirelease

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gate/internal/devtool/runner"
)

func TestWaitReleaseAssetsRetriesUntilEveryAssetIsReady(t *testing.T) {
	var ready atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodHead {
			t.Fatalf("method = %s", request.Method)
		}
		if ready.Load() {
			response.WriteHeader(http.StatusFound)
			return
		}
		http.NotFound(response, request)
	}))
	t.Cleanup(server.Close)

	service, out, errOut := newTestService(t, &fakeRunner{})
	service.GitHubWeb = server.URL
	service.HTTPClient = server.Client()
	service.Getenv = environment(map[string]string{
		"GITHUB_REPOSITORY":                   "jinyongp/gate",
		"GATE_RELEASE_ASSET_WAIT_SECONDS":     "10",
		"GATE_TEST_SECRET_MUST_NOT_BE_LOGGED": "secret",
	})
	now := time.Unix(100, 0)
	service.Now = func() time.Time { return now }
	service.Sleep = func(_ context.Context, delay time.Duration) error {
		now = now.Add(delay)
		ready.Store(true)
		return nil
	}

	if code := service.Run(context.Background(), []string{"wait-release-assets", "v1.2.3"}); code != 0 {
		t.Fatalf("Run = %d", code)
	}
	requireContains(t, errOut.String(), "release asset not ready (404)", "retry 1 in 1s")
	requireContains(t, out.String(), "ok: release assets are ready")
}

func TestWaitReleaseAssetsFailsClosedOnTimeout(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(server.Close)
	service, _, errOut := newTestService(t, &fakeRunner{})
	service.GitHubWeb = server.URL
	service.HTTPClient = server.Client()
	service.Getenv = environment(map[string]string{
		"GATE_RELEASE_ASSET_WAIT_SECONDS": "2",
	})
	now := time.Unix(100, 0)
	service.Now = func() time.Time { return now }
	service.Sleep = func(_ context.Context, delay time.Duration) error {
		now = now.Add(delay)
		return nil
	}
	if code := service.Run(context.Background(), []string{"wait-release-assets", "v1.2.3"}); code != 1 {
		t.Fatalf("Run = %d", code)
	}
	requireContains(t, errOut.String(), "did not become ready within 2s")
}

func checksumManifest() string {
	return fmt.Sprintf(
		"%s  gate-darwin-amd64\n%s  gate-darwin-arm64\n%s  gate-linux-amd64\n%s  gate-linux-arm64\n",
		strings.Repeat("a", 64),
		strings.Repeat("b", 64),
		strings.Repeat("c", 64),
		strings.Repeat("d", 64),
	)
}

func TestGenerateHomebrewFormulaDownloadsChecksumsAndValidatesRuby(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if !strings.HasSuffix(request.URL.Path, "/checksums.txt") {
			http.NotFound(response, request)
			return
		}
		_, _ = response.Write([]byte(checksumManifest()))
	}))
	t.Cleanup(server.Close)
	fake := &fakeRunner{run: func(_ context.Context, command runner.Command) error {
		if command.Name != "ruby" || len(command.Args) != 2 || command.Args[0] != "-c" {
			t.Fatalf("unexpected command: %s", commandLine(command))
		}
		return nil
	}}
	service, out, _ := newTestService(t, fake)
	service.GitHubWeb = server.URL
	service.HTTPClient = server.Client()
	tap := filepath.Join(t.TempDir(), "tap")

	if code := service.Run(
		context.Background(),
		[]string{"generate-homebrew-formula", tap, "v1.2.3"},
	); code != 0 {
		t.Fatalf("Run = %d", code)
	}
	formulaPath := filepath.Join(tap, "Formula", "gate.rb")
	formula := readFile(t, formulaPath)
	requireContains(
		t,
		formula,
		`version "1.2.3"`,
		`sha256 "`+strings.Repeat("a", 64)+`"`,
		`sha256 "`+strings.Repeat("d", 64)+`"`,
		"`brew uninstall gate`",
	)
	if requests.Load() != 1 {
		t.Fatalf("requests = %d", requests.Load())
	}
	requireContains(t, out.String(), "ok: updated "+formulaPath)
}

func TestGenerateHomebrewFormulaRetriesAndRejectsConflictingManifest(t *testing.T) {
	t.Run("retry", func(t *testing.T) {
		var requests atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			if requests.Add(1) == 1 {
				http.Error(response, "temporary", http.StatusServiceUnavailable)
				return
			}
			_, _ = response.Write([]byte(checksumManifest()))
		}))
		t.Cleanup(server.Close)
		service, _, errOut := newTestService(t, &fakeRunner{})
		service.GitHubWeb = server.URL
		service.HTTPClient = server.Client()
		var sleeps []time.Duration
		service.Sleep = func(_ context.Context, delay time.Duration) error {
			sleeps = append(sleeps, delay)
			return nil
		}
		if code := service.Run(
			context.Background(),
			[]string{"generate-homebrew-formula", t.TempDir(), "v1.2.3"},
		); code != 0 {
			t.Fatalf("Run = %d, stderr=%s", code, errOut.String())
		}
		if len(sleeps) != 1 || sleeps[0] != time.Second {
			t.Fatalf("sleeps = %v", sleeps)
		}
		requireContains(t, errOut.String(), "retrying download in 1s")
	})

	t.Run("conflict", func(t *testing.T) {
		manifest := checksumManifest() +
			strings.Repeat("f", 64) + "  gate-darwin-amd64\n"
		server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			_, _ = response.Write([]byte(manifest))
		}))
		t.Cleanup(server.Close)
		service, _, errOut := newTestService(t, &fakeRunner{})
		service.GitHubWeb = server.URL
		service.HTTPClient = server.Client()
		tap := t.TempDir()
		if code := service.Run(
			context.Background(),
			[]string{"generate-homebrew-formula", tap, "v1.2.3"},
		); code != 1 {
			t.Fatalf("Run = %d", code)
		}
		requireContains(t, errOut.String(), "conflicting checksums")
		if _, err := os.Stat(filepath.Join(tap, "Formula", "gate.rb")); !os.IsNotExist(err) {
			t.Fatalf("formula should not exist: %v", err)
		}
	})
}

func TestGeneratedHomebrewFormulaPassesRealRubySyntaxCheck(t *testing.T) {
	if _, err := exec.LookPath("ruby"); err != nil {
		t.Skip("ruby is unavailable")
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte(checksumManifest()))
	}))
	t.Cleanup(server.Close)
	service, _, errOut := newTestService(t, &fakeRunner{})
	service.Runner = runner.OS{}
	service.GitHubWeb = server.URL
	service.HTTPClient = server.Client()
	if code := service.Run(
		context.Background(),
		[]string{"generate-homebrew-formula", t.TempDir(), "v1.2.3"},
	); code != 0 {
		t.Fatalf("Run = %d, stderr=%s", code, errOut.String())
	}
}

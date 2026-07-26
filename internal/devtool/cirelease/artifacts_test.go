package cirelease

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gate/internal/devtool/runner"
)

func TestBuildReleaseArtifactsUsesExplicitTargetMatrixAndOutput(t *testing.T) {
	fake := &fakeRunner{run: func(_ context.Context, command runner.Command) error {
		if command.Name != "go" {
			t.Fatalf("unexpected command: %s", commandLine(command))
		}
		return nil
	}}
	service, out, _ := newTestService(t, fake)
	outputPath := githubOutputFixture(t, service, map[string]string{})
	if code := service.Run(context.Background(), []string{"build-release-artifacts", "v2.11.0"}); code != 0 {
		t.Fatalf("Run = %d", code)
	}
	if got := readFile(t, outputPath); got != "version=v2.11.0\n" {
		t.Fatalf("GITHUB_OUTPUT = %q", got)
	}
	calls := fake.commandLines()
	if len(calls) != 4 {
		t.Fatalf("build calls = %v", calls)
	}
	for _, asset := range releaseAssets[:4] {
		found := false
		for _, call := range calls {
			if strings.Contains(call, "-o "+asset+" ") {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing build for %s: %v", asset, calls)
		}
	}
	requireContains(t, out.String(), "built gate-darwin-arm64", "built gate-linux-amd64")
}

func TestChecksumsAreDeterministicAndOrdered(t *testing.T) {
	service, _, _ := newTestService(t, &fakeRunner{})
	var expected strings.Builder
	for index, asset := range releaseAssets[:4] {
		data := []byte{byte(index + 1), byte(index + 10)}
		if err := os.WriteFile(filepath.Join(service.Dir, asset), data, 0o600); err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(data)
		expected.WriteString(hex.EncodeToString(digest[:]))
		expected.WriteString("  ")
		expected.WriteString(asset)
		expected.WriteByte('\n')
	}
	if code := service.Run(context.Background(), []string{"checksums"}); code != 0 {
		t.Fatalf("Run = %d", code)
	}
	if got := readFile(t, filepath.Join(service.Dir, "checksums.txt")); got != expected.String() {
		t.Fatalf("checksums = %q, want %q", got, expected.String())
	}
}

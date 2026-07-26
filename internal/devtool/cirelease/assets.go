package cirelease

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	localrelease "gate/internal/devtool/release"
	"gate/internal/devtool/runner"
)

var checksumPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func (service *Service) waitReleaseAssets(ctx context.Context, tag string) error {
	if _, err := localrelease.ParseVersion(tag); err != nil {
		return usage("version tag must look like vX.Y.Z: " + tag)
	}
	repository := service.Getenv("GITHUB_REPOSITORY")
	if repository == "" {
		repository = "jinyongp/gate"
	}
	if !repoSlugPattern.MatchString(repository) {
		return usage("GITHUB_REPOSITORY must be an owner/repository slug")
	}
	maxWait, err := positiveSeconds(
		service.Getenv("GATE_RELEASE_ASSET_WAIT_SECONDS"),
		"600",
		"GATE_RELEASE_ASSET_WAIT_SECONDS",
	)
	if err != nil {
		return err
	}
	baseURL := strings.TrimRight(service.GitHubWeb, "/") +
		"/" + repository + "/releases/download/" + tag
	start := service.Now()
	waitCtx, cancel := context.WithTimeout(ctx, maxWait)
	defer cancel()
	attempt := 1
	delay := time.Second
	for {
		ready := true
		for _, asset := range releaseAssets {
			remaining := maxWait - service.Now().Sub(start)
			if remaining <= 0 {
				return fmt.Errorf("release assets did not become ready within %s", durationSeconds(maxWait))
			}
			requestTimeout := min(service.AssetRequestTimeout, remaining)
			requestCtx, requestCancel := context.WithTimeout(waitCtx, requestTimeout)
			status, requestErr := service.releaseAssetStatus(requestCtx, baseURL+"/"+asset)
			requestCancel()
			if requestErr != nil {
				if waitCtx.Err() != nil {
					if ctx.Err() != nil {
						return ctx.Err()
					}
					return fmt.Errorf(
						"release assets did not become ready within %s: %v",
						durationSeconds(maxWait),
						waitCtx.Err().Error(),
					)
				}
				service.console().Warning(
					fmt.Sprintf("release asset request failed: %s: %v", asset, requestErr),
				)
				ready = false
				continue
			}
			if status < 200 || status >= 400 {
				service.console().Warning(fmt.Sprintf("release asset not ready (%03d): %s", status, asset))
				ready = false
			}
		}
		if ready {
			service.console().StatusOK("release assets are ready")
			return nil
		}
		elapsed := service.Now().Sub(start)
		if elapsed >= maxWait {
			return fmt.Errorf("release assets did not become ready within %s", durationSeconds(maxWait))
		}
		remaining := maxWait - elapsed
		sleepFor := min(delay, remaining)
		service.console().Warning(
			fmt.Sprintf("waiting for release assets; retry %d in %s", attempt, durationSeconds(sleepFor)),
		)
		if err := service.Sleep(ctx, sleepFor); err != nil {
			return err
		}
		attempt++
		delay = min(delay*2, 60*time.Second)
	}
}

func (service *Service) releaseAssetStatus(ctx context.Context, url string) (int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return 0, err
	}
	response, err := service.HTTPClient.Do(request)
	if err != nil {
		return 0, err
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	return response.StatusCode, nil
}

func (service *Service) generateHomebrewFormula(ctx context.Context, tapPath, tag string) error {
	if _, err := localrelease.ParseVersion(tag); err != nil {
		return usage("version tag must look like vX.Y.Z: " + tag)
	}
	if tapPath == "" || strings.ContainsAny(tapPath, "\x00\r\n") {
		return usage("tap path is required and cannot contain control characters")
	}
	version := strings.TrimPrefix(tag, "v")
	repository := "jinyongp/gate"
	releaseURL := strings.TrimRight(service.GitHubWeb, "/") +
		"/" + repository + "/releases/download/" + tag
	manifest, err := service.downloadWithRetry(ctx, releaseURL+"/checksums.txt")
	if err != nil {
		return err
	}
	checksums, err := parseChecksums(string(manifest))
	if err != nil {
		return err
	}
	required := []string{
		"gate-darwin-amd64",
		"gate-darwin-arm64",
		"gate-linux-amd64",
		"gate-linux-arm64",
	}
	for _, asset := range required {
		if !checksumPattern.MatchString(checksums[asset]) {
			return fmt.Errorf("invalid or missing checksum for %s", asset)
		}
	}

	formulaDir := filepath.Join(tapPath, "Formula")
	formulaPath := filepath.Join(formulaDir, "gate.rb")
	if err := service.MkdirAll(formulaDir, 0o750); err != nil {
		return fmt.Errorf("create formula directory: %w", err)
	}
	formula := renderFormula(version, releaseURL, checksums)
	if err := service.WriteFile(formulaPath, []byte(formula), 0o644); err != nil {
		return fmt.Errorf("write Homebrew formula: %w", err)
	}
	if err := service.Runner.Run(ctx, runner.Command{
		Name:   "ruby",
		Args:   []string{"-c", formulaPath},
		Dir:    service.Dir,
		Stdout: io.Discard,
		Stderr: service.Err,
	}); err != nil {
		return fmt.Errorf("validate Homebrew formula: %w", err)
	}
	service.console().StatusOK("updated " + formulaPath)
	return nil
}

func (service *Service) downloadWithRetry(ctx context.Context, url string) ([]byte, error) {
	delay := time.Second
	var lastErr error
	for attempt := 1; attempt <= 5; attempt++ {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("create download request: %w", err)
		}
		response, err := service.HTTPClient.Do(request) //nolint:bodyclose // readDownloadResponse closes every non-nil response.
		if err == nil {
			body, readErr := readDownloadResponse(response)
			if readErr == nil {
				return body, nil
			}
			lastErr = readErr
		} else {
			lastErr = err
		}
		if attempt == 5 {
			break
		}
		fmt.Fprintf(service.Err, "retrying download in %s: %s\n", durationSeconds(delay), url)
		if err := service.Sleep(ctx, delay); err != nil {
			return nil, err
		}
		delay *= 2
	}
	return nil, fmt.Errorf("download %s: %w", url, lastErr)
}

func readDownloadResponse(response *http.Response) ([]byte, error) {
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, response.Body)
		return nil, fmt.Errorf("unexpected HTTP status %s", response.Status)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	return data, nil
}

func parseChecksums(manifest string) (map[string]string, error) {
	checksums := make(map[string]string)
	for line := range strings.Lines(manifest) {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		name := strings.TrimPrefix(fields[1], "*")
		if !cleanAssetName(name) {
			continue
		}
		if existing, ok := checksums[name]; ok && existing != fields[0] {
			return nil, fmt.Errorf("conflicting checksums for %s", name)
		}
		checksums[name] = fields[0]
	}
	return checksums, nil
}

func renderFormula(version, releaseURL string, checksums map[string]string) string {
	return fmt.Sprintf(`class Gate < Formula
  desc "Local-dev global HTTPS reverse proxy and port registry"
  homepage "https://github.com/jinyongp/gate"
  version "%s"
  license "MIT"

  on_macos do
    if Hardware::CPU.arm?
      url "%s/gate-darwin-arm64", using: :nounzip
      sha256 "%s"
    else
      url "%s/gate-darwin-amd64", using: :nounzip
      sha256 "%s"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "%s/gate-linux-arm64", using: :nounzip
      sha256 "%s"
    else
      url "%s/gate-linux-amd64", using: :nounzip
      sha256 "%s"
    end
  end

  def install
    asset = if OS.mac?
      Hardware::CPU.arm? ? "gate-darwin-arm64" : "gate-darwin-amd64"
    elsif OS.linux?
      Hardware::CPU.arm? ? "gate-linux-arm64" : "gate-linux-amd64"
    else
      odie "unsupported platform"
    end

    chmod 0755, asset
    bin.install asset => "gate"
    generate_completions_from_executable(bin/"gate", "completion")
  end

  def caveats
    <<~EOS
      For full cleanup, run:
        gate uninstall

      `+"`brew uninstall gate`"+` removes only the Homebrew package. It does not remove
      gate's local state, trusted root CA, managed hosts block, or shell PATH block.
    EOS
  end

  test do
    assert_match "v#{version}", shell_output("#{bin}/gate --version")
  end
end
`,
		version,
		releaseURL,
		checksums["gate-darwin-arm64"],
		releaseURL,
		checksums["gate-darwin-amd64"],
		releaseURL,
		checksums["gate-linux-arm64"],
		releaseURL,
		checksums["gate-linux-amd64"],
	)
}

func durationSeconds(duration time.Duration) string {
	return fmt.Sprintf("%ds", int64(duration/time.Second))
}

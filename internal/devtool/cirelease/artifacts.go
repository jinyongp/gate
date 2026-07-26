package cirelease

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	devbuild "gate/internal/devtool/build"
	localrelease "gate/internal/devtool/release"
	"gate/internal/devtool/runner"
)

var buildVersionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]*$`)

var releaseAssets = []string{
	"gate-darwin-amd64",
	"gate-darwin-arm64",
	"gate-linux-amd64",
	"gate-linux-arm64",
	"checksums.txt",
}

func (service *Service) buildReleaseArtifacts(ctx context.Context, version string) error {
	if version == "" {
		described, err := service.output(ctx, "git", "describe", "--tags", "--always", "--dirty")
		if err != nil {
			return err
		}
		version = strings.TrimSpace(described)
	}
	if !buildVersionPattern.MatchString(version) {
		return usage(fmt.Sprintf("invalid build version %q", version))
	}
	if strings.HasPrefix(version, "v") {
		if _, err := localrelease.ParseVersion(version); err != nil {
			return usage(fmt.Sprintf("invalid release version %q", version))
		}
	}
	if err := service.appendGitHubOutput(outputValue{Name: "version", Value: version}); err != nil {
		return err
	}
	for _, target := range devbuild.ReleaseTargets() {
		output := target.BinaryName()
		err := service.Runner.Run(ctx, runner.Command{
			Name: "go",
			Args: []string{
				"build",
				"-trimpath",
				"-ldflags",
				"-s -w -X main.version=" + version,
				"-o",
				output,
				"./cmd/gate",
			},
			Dir:    service.Dir,
			Env:    target.Environment(),
			Stdout: service.Out,
			Stderr: service.Err,
		})
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			return fmt.Errorf("build %s: %w", output, err)
		}
		service.console().OK("built " + output)
	}
	return nil
}

func (service *Service) writeChecksums() error {
	assets := releaseAssets[:len(releaseAssets)-1]
	var output strings.Builder
	for _, asset := range assets {
		path := service.repositoryPath(asset)
		data, err := service.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read release asset %s: %w", asset, err)
		}
		digest := sha256.Sum256(data)
		fmt.Fprintf(&output, "%s  %s\n", hex.EncodeToString(digest[:]), asset)
	}
	if err := service.WriteFile(service.repositoryPath("checksums.txt"), []byte(output.String()), 0o644); err != nil {
		return fmt.Errorf("write checksums.txt: %w", err)
	}
	return nil
}

func existingRegularFile(path string) (os.FileInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file")
	}
	return info, nil
}

func cleanAssetName(name string) bool {
	return filepath.Base(name) == name && !strings.ContainsAny(name, "\x00\r\n")
}

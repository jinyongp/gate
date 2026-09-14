package cirelease

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	localrelease "gate/internal/devtool/release"
)

type githubReleaseState struct {
	Draft      bool `json:"isDraft"`
	Immutable  bool `json:"isImmutable"`
	Prerelease bool `json:"isPrerelease"`
}

func (service *Service) publishRelease(ctx context.Context, tag string) error {
	if _, err := localrelease.ParseVersion(tag); err != nil {
		return usage("release tag must be strict vX.Y.Z: " + tag)
	}
	expected := service.Getenv("GATE_RELEASE_TARGET_SHA")
	if expected == "" {
		expected = service.Getenv("GITHUB_SHA")
	}
	if expected == "" {
		return usage("GITHUB_SHA is required")
	}
	expectedObject := service.Getenv("GATE_RELEASE_TAG_OBJECT")
	if err := service.verifyReleaseTag(ctx, tag, expected, expectedObject); err != nil {
		return err
	}
	repository := service.Getenv("GITHUB_REPOSITORY")
	if !repoSlugPattern.MatchString(repository) {
		return usage("GITHUB_REPOSITORY must be an owner/repository slug")
	}
	for _, asset := range releaseAssets {
		if !cleanAssetName(asset) {
			return fmt.Errorf("unsafe release asset name %q", asset)
		}
		if _, err := existingRegularFile(service.repositoryPath(asset)); err != nil {
			return fmt.Errorf("release asset %s is unavailable: %w", asset, err)
		}
	}

	notes, err := service.releaseNotes(ctx, repository, tag)
	if err != nil {
		return err
	}
	state, stateErr := service.githubReleaseState(ctx, tag)
	switch {
	case stateErr == nil:
		if err := validateImmutableReleaseState(state, tag); err != nil {
			return err
		}
		return service.verifyReleaseAssets(ctx, tag)
	case !isGitHubNotFound(stateErr):
		return fmt.Errorf("inspect existing GitHub release: %w", stateErr)
	default:
		if err := service.requireImmutableReleases(ctx, repository); err != nil {
			return err
		}
		if err := service.verifyReleaseTag(ctx, tag, expected, expectedObject); err != nil {
			return err
		}
		args := []string{"release", "create", tag}
		args = append(args, releaseAssets...)
		args = append(args, "--title", tag, "--notes", notes, "--verify-tag")
		if err := service.stream(ctx, "gh", args...); err != nil {
			return fmt.Errorf("create GitHub release: %w", err)
		}
		return nil
	}
}

func validateImmutableReleaseState(state githubReleaseState, tag string) error {
	if state.Draft || state.Prerelease || !state.Immutable {
		return fmt.Errorf(
			"stable release must be published and immutable (draft=%t, prerelease=%t, immutable=%t): %s",
			state.Draft,
			state.Prerelease,
			state.Immutable,
			tag,
		)
	}
	return nil
}

func (service *Service) requireImmutableReleases(ctx context.Context, repository string) error {
	raw, err := service.output(
		ctx,
		"gh",
		"api",
		"repos/"+repository+"/immutable-releases",
		"--jq",
		".enabled",
	)
	if err != nil {
		return fmt.Errorf("inspect immutable release setting: %w", err)
	}
	if strings.TrimSpace(raw) != "true" {
		return fmt.Errorf("GitHub immutable releases must be enabled before publishing")
	}
	return nil
}

func (service *Service) verifyReleaseTag(
	ctx context.Context,
	tag,
	expectedTarget,
	expectedObject string,
) error {
	if expectedObject != "" {
		return service.verifyReleaseTagIdentity(ctx, tag, expectedTarget, expectedObject)
	}
	return service.verifyReleaseTagTarget(ctx, tag, expectedTarget)
}

func (service *Service) githubReleaseState(ctx context.Context, tag string) (githubReleaseState, error) {
	raw, err := service.output(
		ctx,
		"gh",
		"release",
		"view",
		tag,
		"--json",
		"isDraft,isImmutable,isPrerelease",
	)
	if err != nil {
		return githubReleaseState{}, err
	}
	var state githubReleaseState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return githubReleaseState{}, fmt.Errorf("decode GitHub release state: %w", err)
	}
	return state, nil
}

func (service *Service) releaseNotes(ctx context.Context, repository, tag string) (string, error) {
	if service.gitCommandSucceeds(ctx, "cat-file", "-e", tag+"^{tag}") {
		notes, err := service.output(
			ctx,
			"git",
			"tag",
			"-l",
			"--format=%(contents:subject)%0a%0a%(contents:body)",
			tag,
		)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(notes) != "" {
			return notes, nil
		}
	}

	base, err := service.latestPublishedTag(ctx, repository)
	if err != nil {
		return "", err
	}
	revision := tag
	if base != "" && service.gitCommandSucceeds(ctx, "rev-parse", "-q", "--verify", "refs/tags/"+base) {
		revision = base + ".." + tag
	}
	commits, err := service.output(ctx, "git", "log", "--oneline", "--no-decorate", revision)
	if err != nil {
		return "", err
	}
	var bullets strings.Builder
	for _, line := range lines(commits) {
		fmt.Fprintf(&bullets, "- %s\n", line)
	}
	return fmt.Sprintf("Release %s\n\n%s", tag, strings.TrimRight(bullets.String(), "\n")), nil
}

func (service *Service) latestPublishedTag(ctx context.Context, repository string) (string, error) {
	tag, err := service.output(ctx, "gh", "api", "repos/"+repository+"/releases/latest", "--jq", ".tag_name")
	if err != nil {
		if isGitHubNotFound(err) {
			return "", nil
		}
		return "", fmt.Errorf("read latest published GitHub release: %w", err)
	}
	return strings.TrimSpace(tag), nil
}

func (service *Service) verifyReleaseAssets(ctx context.Context, tag string) error {
	raw, err := service.output(ctx, "gh", "release", "view", tag, "--json", "assets", "--jq", ".assets[].name")
	if err != nil {
		return fmt.Errorf("list GitHub release assets: %w", err)
	}
	existing := make(map[string]bool)
	for _, asset := range lines(raw) {
		existing[asset] = true
	}
	for _, asset := range releaseAssets {
		if !existing[asset] {
			return fmt.Errorf("immutable release is missing required asset: %s", asset)
		}
	}
	if len(existing) != len(releaseAssets) {
		return fmt.Errorf("immutable release contains unexpected assets")
	}
	temp, err := service.MkdirTemp("", "gate-release-assets-*")
	if err != nil {
		return fmt.Errorf("create release comparison directory: %w", err)
	}
	defer func() { _ = service.RemoveAll(temp) }()

	for _, asset := range releaseAssets {
		if err := service.stream(ctx, "gh", "release", "download", tag, "--pattern", asset, "--dir", temp); err != nil {
			return fmt.Errorf("download existing release asset %s: %w", asset, err)
		}
		local, err := service.ReadFile(service.repositoryPath(asset))
		if err != nil {
			return fmt.Errorf("read local release asset %s: %w", asset, err)
		}
		remote, err := service.ReadFile(filepath.Join(temp, asset))
		if err != nil {
			return fmt.Errorf("read downloaded release asset %s: %w", asset, err)
		}
		if !bytes.Equal(local, remote) {
			return fmt.Errorf("release asset differs from local file; refusing to replace tagged artifact: %s", asset)
		}
	}
	return nil
}

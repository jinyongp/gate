package cirelease

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gate/internal/devtool/runner"
)

func writeReleaseAssetFixtures(t *testing.T, service *Service) map[string][]byte {
	t.Helper()
	data := make(map[string][]byte)
	for index, asset := range releaseAssets {
		content := []byte{byte(index + 1), byte(index + 20)}
		if err := os.WriteFile(filepath.Join(service.Dir, asset), content, 0o600); err != nil {
			t.Fatal(err)
		}
		data[asset] = content
	}
	return data
}

func publishEnvironment(service *Service) {
	service.Getenv = environment(map[string]string{
		"GITHUB_SHA":        testSHA,
		"GITHUB_REPOSITORY": "jinyongp/gate",
	})
}

func TestPublishReleaseCreatesMissingReleaseWithGeneratedNotes(t *testing.T) {
	var created runner.Command
	fake := &fakeRunner{}
	fake.run = func(_ context.Context, command runner.Command) error {
		switch commandLine(command) {
		case "git ls-remote origin refs/tags/v1.2.3^{}":
			writeCommandOutput(command, testSHA+"\trefs/tags/v1.2.3^{}\n")
		case "git cat-file -e v1.2.3^{tag}":
			return fakeExitError{code: 1}
		case "gh api repos/jinyongp/gate/releases/latest --jq .tag_name":
			return failCommand(command, "HTTP 404: Not Found")
		case "git log --oneline --no-decorate v1.2.3":
			writeCommandOutput(command, "abc123 first\n987def second\n")
		case "gh release view v1.2.3 --json isDraft,isPrerelease":
			return failCommand(command, "release not found")
		default:
			if len(command.Args) >= 2 && command.Args[0] == "release" && command.Args[1] == "create" {
				created = command
				return nil
			}
			t.Fatalf("unexpected command: %s", commandLine(command))
		}
		return nil
	}
	service, _, _ := newTestService(t, fake)
	publishEnvironment(service)
	writeReleaseAssetFixtures(t, service)

	if code := service.Run(context.Background(), []string{"publish-release", "v1.2.3"}); code != 0 {
		t.Fatalf("Run = %d", code)
	}
	if created.Name != "gh" {
		t.Fatalf("release create was not called: %v", fake.commandLines())
	}
	args := strings.Join(created.Args, "\x00")
	requireContains(t, args, "Release v1.2.3", "- abc123 first", "- 987def second", "--verify-tag")
	for _, asset := range releaseAssets {
		requireContains(t, args, asset)
	}
}

func TestPublishReleaseReconcilesMatchingAssetsAndUploadsOnlyMissing(t *testing.T) {
	fake := &fakeRunner{}
	service, _, _ := newTestService(t, fake)
	publishEnvironment(service)
	fixtures := writeReleaseAssetFixtures(t, service)
	existingAsset := releaseAssets[0]
	fake.run = func(_ context.Context, command runner.Command) error {
		line := commandLine(command)
		switch line {
		case "git ls-remote origin refs/tags/v1.2.3^{}":
			writeCommandOutput(command, testSHA+"\trefs/tags/v1.2.3^{}\n")
		case "git cat-file -e v1.2.3^{tag}":
			return nil
		case "git tag -l --format=%(contents:subject)%0a%0a%(contents:body) v1.2.3":
			writeCommandOutput(command, "Release notes")
		case "gh release view v1.2.3 --json isDraft,isPrerelease":
			writeCommandOutput(command, `{"isDraft":false,"isPrerelease":false}`)
		case "gh release view v1.2.3 --json assets --jq .assets[].name":
			writeCommandOutput(command, existingAsset+"\n")
		default:
			if strings.HasPrefix(line, "gh release download v1.2.3 --pattern "+existingAsset+" --dir ") {
				directory := command.Args[len(command.Args)-1]
				if err := os.WriteFile(filepath.Join(directory, existingAsset), fixtures[existingAsset], 0o600); err != nil {
					t.Fatal(err)
				}
				return nil
			}
			if strings.HasPrefix(line, "gh release upload v1.2.3 ") {
				return nil
			}
			t.Fatalf("unexpected command: %s", line)
		}
		return nil
	}

	if code := service.Run(context.Background(), []string{"publish-release", "v1.2.3"}); code != 0 {
		t.Fatalf("Run = %d", code)
	}
	var uploaded []string
	for _, line := range fake.commandLines() {
		if strings.HasPrefix(line, "gh release upload v1.2.3 ") {
			uploaded = append(uploaded, strings.TrimPrefix(line, "gh release upload v1.2.3 "))
		}
	}
	if !slices.Equal(uploaded, releaseAssets[1:]) {
		t.Fatalf("uploaded = %v, want %v", uploaded, releaseAssets[1:])
	}
	requireNoCallContaining(t, fake, "gh release create")
}

func TestPublishReleaseRefusesConflictingImmutableAsset(t *testing.T) {
	fake := &fakeRunner{}
	service, _, errOut := newTestService(t, fake)
	publishEnvironment(service)
	writeReleaseAssetFixtures(t, service)
	existingAsset := releaseAssets[0]
	fake.run = func(_ context.Context, command runner.Command) error {
		line := commandLine(command)
		switch line {
		case "git ls-remote origin refs/tags/v1.2.3^{}":
			writeCommandOutput(command, testSHA+"\trefs/tags/v1.2.3^{}\n")
		case "git cat-file -e v1.2.3^{tag}":
		case "git tag -l --format=%(contents:subject)%0a%0a%(contents:body) v1.2.3":
			writeCommandOutput(command, "Release notes")
		case "gh release view v1.2.3 --json isDraft,isPrerelease":
			writeCommandOutput(command, `{"isDraft":false,"isPrerelease":false}`)
		case "gh release view v1.2.3 --json assets --jq .assets[].name":
			writeCommandOutput(command, existingAsset)
		default:
			if strings.HasPrefix(line, "gh release download ") {
				directory := command.Args[len(command.Args)-1]
				if err := os.WriteFile(filepath.Join(directory, existingAsset), []byte("different"), 0o600); err != nil {
					t.Fatal(err)
				}
				return nil
			}
			t.Fatalf("unexpected command: %s", line)
		}
		return nil
	}

	if code := service.Run(context.Background(), []string{"publish-release", "v1.2.3"}); code != 1 {
		t.Fatalf("Run = %d", code)
	}
	requireContains(t, errOut.String(), "refusing to replace tagged artifact", existingAsset)
	requireNoCallContaining(t, fake, "gh release upload")
}

func TestPublishReleaseDoesNotTreatGitHubAPIFailureAsMissing(t *testing.T) {
	fake := &fakeRunner{}
	fake.run = func(_ context.Context, command runner.Command) error {
		switch commandLine(command) {
		case "git ls-remote origin refs/tags/v1.2.3^{}":
			writeCommandOutput(command, testSHA+"\trefs/tags/v1.2.3^{}\n")
		case "git cat-file -e v1.2.3^{tag}":
		case "git tag -l --format=%(contents:subject)%0a%0a%(contents:body) v1.2.3":
			writeCommandOutput(command, "Release notes")
		case "gh release view v1.2.3 --json isDraft,isPrerelease":
			return failCommand(command, "HTTP 500 upstream failure")
		default:
			t.Fatalf("unexpected command: %s", commandLine(command))
		}
		return nil
	}
	service, _, errOut := newTestService(t, fake)
	publishEnvironment(service)
	writeReleaseAssetFixtures(t, service)
	if code := service.Run(context.Background(), []string{"publish-release", "v1.2.3"}); code != 1 {
		t.Fatalf("Run = %d", code)
	}
	requireContains(t, errOut.String(), "inspect existing GitHub release", "HTTP 500")
	requireNoCallContaining(t, fake, "gh release create")
}

func TestPublishReleaseStopsWhenLatestReleaseLookupFails(t *testing.T) {
	fake := &fakeRunner{}
	fake.run = func(_ context.Context, command runner.Command) error {
		switch commandLine(command) {
		case "git ls-remote origin refs/tags/v1.2.3^{}":
			writeCommandOutput(command, testSHA+"\trefs/tags/v1.2.3^{}\n")
		case "git cat-file -e v1.2.3^{tag}":
			return fakeExitError{code: 1}
		case "gh api repos/jinyongp/gate/releases/latest --jq .tag_name":
			return failCommand(command, "HTTP 503 service unavailable")
		default:
			t.Fatalf("unexpected command: %s", commandLine(command))
		}
		return nil
	}
	service, _, errOut := newTestService(t, fake)
	publishEnvironment(service)
	writeReleaseAssetFixtures(t, service)
	if code := service.Run(context.Background(), []string{"publish-release", "v1.2.3"}); code != 1 {
		t.Fatalf("Run = %d", code)
	}
	requireContains(t, errOut.String(), "read latest published GitHub release", "HTTP 503")
	requireNoCallContaining(t, fake, "gh release view")
	requireNoCallContaining(t, fake, "gh release create")
}

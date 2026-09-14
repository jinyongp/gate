package cirelease

import (
	"context"
	"os"
	"path/filepath"
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
	viewCalls := 0
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
		case "gh release view v1.2.3 --json isDraft,isImmutable,isPrerelease":
			viewCalls++
			if viewCalls == 1 {
				return failCommand(command, "release not found")
			}
			writeCommandOutput(command, `{"isDraft":false,"isImmutable":true,"isPrerelease":false}`)
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

func TestPublishReleaseReverifiesTagImmediatelyBeforeCreate(t *testing.T) {
	lookups := 0
	fake := &fakeRunner{run: func(_ context.Context, command runner.Command) error {
		switch commandLine(command) {
		case "git ls-remote origin refs/tags/v1.2.3^{}":
			lookups++
			target := testSHA
			if lookups == 2 {
				target = differentSHA
			}
			writeCommandOutput(command, target+"\trefs/tags/v1.2.3^{}\n")
		case "git cat-file -e v1.2.3^{tag}":
			return fakeExitError{code: 1}
		case "gh api repos/jinyongp/gate/releases/latest --jq .tag_name":
			return failCommand(command, "HTTP 404: Not Found")
		case "git log --oneline --no-decorate v1.2.3":
			writeCommandOutput(command, "abc123 first\n")
		case "gh release view v1.2.3 --json isDraft,isImmutable,isPrerelease":
			return failCommand(command, "release not found")
		default:
			t.Fatalf("unexpected command: %s", commandLine(command))
		}
		return nil
	}}
	service, _, errOut := newTestService(t, fake)
	publishEnvironment(service)
	writeReleaseAssetFixtures(t, service)

	if code := service.Run(context.Background(), []string{"publish-release", "v1.2.3"}); code != 1 {
		t.Fatalf("Run = %d", code)
	}
	requireContains(t, errOut.String(), "release tag target moved")
	requireNoCallContaining(t, fake, "gh release create")
}

func TestPublishReleaseRefusesImmutableReleaseWithMissingAsset(t *testing.T) {
	fake := &fakeRunner{}
	service, _, errOut := newTestService(t, fake)
	publishEnvironment(service)
	writeReleaseAssetFixtures(t, service)
	existingAsset := releaseAssets[0]
	fake.run = func(_ context.Context, command runner.Command) error {
		switch commandLine(command) {
		case "git ls-remote origin refs/tags/v1.2.3^{}":
			writeCommandOutput(command, testSHA+"\trefs/tags/v1.2.3^{}\n")
		case "git cat-file -e v1.2.3^{tag}":
			return nil
		case "git tag -l --format=%(contents:subject)%0a%0a%(contents:body) v1.2.3":
			writeCommandOutput(command, "Release notes")
		case "gh release view v1.2.3 --json isDraft,isImmutable,isPrerelease":
			writeCommandOutput(command, `{"isDraft":false,"isImmutable":true,"isPrerelease":false}`)
		case "gh release view v1.2.3 --json assets --jq .assets[].name":
			writeCommandOutput(command, existingAsset+"\n")
		default:
			t.Fatalf("unexpected command: %s", commandLine(command))
		}
		return nil
	}

	if code := service.Run(context.Background(), []string{"publish-release", "v1.2.3"}); code != 1 {
		t.Fatalf("Run = %d", code)
	}
	requireContains(t, errOut.String(), "immutable release is missing required asset", releaseAssets[1])
	requireNoCallContaining(t, fake, "gh release download")
}

func TestPublishReleaseRefusesImmutableReleaseWithUnexpectedAsset(t *testing.T) {
	fake := &fakeRunner{}
	service, _, errOut := newTestService(t, fake)
	publishEnvironment(service)
	writeReleaseAssetFixtures(t, service)
	fake.run = func(_ context.Context, command runner.Command) error {
		switch commandLine(command) {
		case "git ls-remote origin refs/tags/v1.2.3^{}":
			writeCommandOutput(command, testSHA+"\trefs/tags/v1.2.3^{}\n")
		case "git cat-file -e v1.2.3^{tag}":
		case "git tag -l --format=%(contents:subject)%0a%0a%(contents:body) v1.2.3":
			writeCommandOutput(command, "Release notes")
		case "gh release view v1.2.3 --json isDraft,isImmutable,isPrerelease":
			writeCommandOutput(command, `{"isDraft":false,"isImmutable":true,"isPrerelease":false}`)
		case "gh release view v1.2.3 --json assets --jq .assets[].name":
			writeCommandOutput(command, strings.Join(append(releaseAssets, "unexpected.txt"), "\n"))
		default:
			t.Fatalf("unexpected command: %s", commandLine(command))
		}
		return nil
	}

	if code := service.Run(context.Background(), []string{"publish-release", "v1.2.3"}); code != 1 {
		t.Fatalf("Run = %d", code)
	}
	requireContains(t, errOut.String(), "immutable release contains unexpected assets")
	requireNoCallContaining(t, fake, "gh release download")
	requireNoCallContaining(t, fake, "gh release upload")
}

func TestPublishReleaseVerifiesCompleteImmutableRelease(t *testing.T) {
	fake := &fakeRunner{}
	service, _, _ := newTestService(t, fake)
	publishEnvironment(service)
	fixtures := writeReleaseAssetFixtures(t, service)
	fake.run = func(_ context.Context, command runner.Command) error {
		line := commandLine(command)
		switch line {
		case "git ls-remote origin refs/tags/v1.2.3^{}":
			writeCommandOutput(command, testSHA+"\trefs/tags/v1.2.3^{}\n")
		case "git cat-file -e v1.2.3^{tag}":
		case "git tag -l --format=%(contents:subject)%0a%0a%(contents:body) v1.2.3":
			writeCommandOutput(command, "Release notes")
		case "gh release view v1.2.3 --json isDraft,isImmutable,isPrerelease":
			writeCommandOutput(command, `{"isDraft":false,"isImmutable":true,"isPrerelease":false}`)
		case "gh release view v1.2.3 --json assets --jq .assets[].name":
			writeCommandOutput(command, strings.Join(releaseAssets, "\n"))
		default:
			if strings.HasPrefix(line, "gh release download ") {
				asset := command.Args[4]
				directory := command.Args[6]
				if err := os.WriteFile(filepath.Join(directory, asset), fixtures[asset], 0o600); err != nil {
					t.Fatal(err)
				}
				return nil
			}
			t.Fatalf("unexpected command: %s", line)
		}
		return nil
	}

	if code := service.Run(context.Background(), []string{"publish-release", "v1.2.3"}); code != 0 {
		t.Fatalf("Run = %d", code)
	}
	requireNoCallContaining(t, fake, "gh release upload")
}

func TestPublishReleaseRefusesMutableExistingRelease(t *testing.T) {
	fake := &fakeRunner{run: func(_ context.Context, command runner.Command) error {
		switch commandLine(command) {
		case "git ls-remote origin refs/tags/v1.2.3^{}":
			writeCommandOutput(command, testSHA+"\trefs/tags/v1.2.3^{}\n")
		case "git cat-file -e v1.2.3^{tag}":
		case "git tag -l --format=%(contents:subject)%0a%0a%(contents:body) v1.2.3":
			writeCommandOutput(command, "Release notes")
		case "gh release view v1.2.3 --json isDraft,isImmutable,isPrerelease":
			writeCommandOutput(command, `{"isDraft":false,"isImmutable":false,"isPrerelease":false}`)
		default:
			t.Fatalf("unexpected command: %s", commandLine(command))
		}
		return nil
	}}
	service, _, errOut := newTestService(t, fake)
	publishEnvironment(service)
	writeReleaseAssetFixtures(t, service)

	if code := service.Run(context.Background(), []string{"publish-release", "v1.2.3"}); code != 1 {
		t.Fatalf("Run = %d", code)
	}
	requireContains(t, errOut.String(), "stable release must be published and immutable", "immutable=false")
	requireNoCallContaining(t, fake, "gh release download")
}

func TestPublishReleaseRefusesMutableCreatedRelease(t *testing.T) {
	viewCalls := 0
	fake := &fakeRunner{}
	service, _, errOut := newTestService(t, fake)
	publishEnvironment(service)
	writeReleaseAssetFixtures(t, service)
	fake.run = func(_ context.Context, command runner.Command) error {
		switch commandLine(command) {
		case "git ls-remote origin refs/tags/v1.2.3^{}":
			writeCommandOutput(command, testSHA+"\trefs/tags/v1.2.3^{}\n")
		case "git cat-file -e v1.2.3^{tag}":
			return nil
		case "git tag -l --format=%(contents:subject)%0a%0a%(contents:body) v1.2.3":
			writeCommandOutput(command, "Release notes")
		case "gh release view v1.2.3 --json isDraft,isImmutable,isPrerelease":
			viewCalls++
			if viewCalls == 1 {
				return failCommand(command, "release not found")
			}
			writeCommandOutput(command, `{"isDraft":false,"isImmutable":false,"isPrerelease":false}`)
		default:
			if len(command.Args) >= 2 && command.Args[0] == "release" && command.Args[1] == "create" {
				return nil
			}
			t.Fatalf("unexpected command: %s", commandLine(command))
		}
		return nil
	}

	if code := service.Run(context.Background(), []string{"publish-release", "v1.2.3"}); code != 1 {
		t.Fatalf("Run = %d", code)
	}
	requireContains(t, errOut.String(), "stable release must be published and immutable", "immutable=false")
	requireContains(t, strings.Join(fake.commandLines(), "\n"), "gh release create")
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
		case "gh release view v1.2.3 --json isDraft,isImmutable,isPrerelease":
			writeCommandOutput(command, `{"isDraft":false,"isImmutable":true,"isPrerelease":false}`)
		case "gh release view v1.2.3 --json assets --jq .assets[].name":
			writeCommandOutput(command, strings.Join(releaseAssets, "\n"))
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
		case "gh release view v1.2.3 --json isDraft,isImmutable,isPrerelease":
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

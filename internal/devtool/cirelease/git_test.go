package cirelease

import (
	"context"
	"strings"
	"testing"

	"gate/internal/devtool/runner"
)

const (
	testSHA      = "1111111111111111111111111111111111111111"
	differentSHA = "2222222222222222222222222222222222222222"
)

func TestDetectReleaseTagWritesAnnotatedStableOutputs(t *testing.T) {
	fake := &fakeRunner{}
	fake.run = func(_ context.Context, command runner.Command) error {
		switch commandLine(command) {
		case "git cat-file -e v2.11.0^{tag}",
			"git fetch --force --quiet origin refs/heads/main:refs/remotes/origin/main",
			"git merge-base --is-ancestor " + testSHA + " refs/remotes/origin/main":
			return nil
		case "git rev-parse v2.11.0^{commit}", "git rev-parse HEAD":
			writeCommandOutput(command, testSHA+"\n")
			return nil
		case "git tag --list v*":
			writeCommandOutput(command, "v2.10.0\nv2.11.0\nnot-semver\n")
			return nil
		case "gh release view v2.11.0 --json isDraft,isPrerelease":
			writeCommandOutput(command, `{"isDraft":false,"isPrerelease":false}`)
			return nil
		default:
			t.Fatalf("unexpected command: %s", commandLine(command))
			return nil
		}
	}
	service, _, _ := newTestService(t, fake)
	outputPath := githubOutputFixture(t, service, map[string]string{
		"GITHUB_REF_TYPE": "tag",
		"GITHUB_REF_NAME": "v2.11.0",
	})

	if code := service.Run(context.Background(), []string{"detect-release-tag"}); code != 0 {
		t.Fatalf("Run = %d", code)
	}
	got := readFile(t, outputPath)
	requireContains(
		t,
		got,
		"tag=v2.11.0\n",
		"type=annotated\n",
		"target="+testSHA+"\n",
		"release=existing\n",
		"on_main=true\n",
	)
}

func TestDetectReleaseTagRejectsOlderStableTag(t *testing.T) {
	fake := &fakeRunner{}
	fake.run = func(_ context.Context, command runner.Command) error {
		switch commandLine(command) {
		case "git cat-file -e v2.10.0^{tag}":
			return fakeExitError{code: 1}
		case "git rev-parse v2.10.0", "git rev-parse HEAD":
			writeCommandOutput(command, testSHA)
			return nil
		case "git tag --list v*":
			writeCommandOutput(command, "v2.10.0\nv2.11.0\n")
			return nil
		default:
			t.Fatalf("unexpected command: %s", commandLine(command))
			return nil
		}
	}
	service, _, errOut := newTestService(t, fake)
	_ = githubOutputFixture(t, service, map[string]string{
		"GITHUB_REF_TYPE": "tag",
		"GITHUB_REF_NAME": "v2.10.0",
	})

	if code := service.Run(context.Background(), []string{"detect-release-tag"}); code != 1 {
		t.Fatalf("Run = %d", code)
	}
	requireContains(t, errOut.String(), "refusing to publish older tag", "v2.11.0")
	requireNoCallContaining(t, fake, "gh release")
}

func TestDetectReleaseTagKeepsGitHubAPIFailureDistinct(t *testing.T) {
	fake := &fakeRunner{}
	fake.run = func(_ context.Context, command runner.Command) error {
		switch commandLine(command) {
		case "git tag --points-at HEAD --list v* --sort=-v:refname":
			writeCommandOutput(command, "v1.2.3\n")
			return nil
		case "git cat-file -e v1.2.3^{tag}":
			return fakeExitError{code: 1}
		case "git rev-parse v1.2.3":
			writeCommandOutput(command, testSHA)
			return nil
		case "git tag --list v*":
			writeCommandOutput(command, "v1.2.3\n")
			return nil
		case "git fetch --force --quiet origin refs/heads/main:refs/remotes/origin/main":
			return nil
		case "git merge-base --is-ancestor " + testSHA + " refs/remotes/origin/main":
			return fakeExitError{code: 1}
		case "gh release view v1.2.3 --json isDraft,isPrerelease":
			return failCommand(command, "HTTP 500 service unavailable")
		default:
			t.Fatalf("unexpected command: %s", commandLine(command))
			return nil
		}
	}
	service, _, _ := newTestService(t, fake)
	outputPath := githubOutputFixture(t, service, map[string]string{})

	if code := service.Run(context.Background(), []string{"detect-release-tag"}); code != 0 {
		t.Fatalf("Run = %d", code)
	}
	requireContains(t, readFile(t, outputPath), "release=unknown\n", "on_main=false\n")
}

func TestDetectReleaseTagRejectsNonFinalExistingRelease(t *testing.T) {
	fake := &fakeRunner{}
	fake.run = func(_ context.Context, command runner.Command) error {
		switch {
		case commandLine(command) == "git tag --points-at HEAD --list v* --sort=-v:refname":
			writeCommandOutput(command, "v1.2.3")
		case strings.HasPrefix(commandLine(command), "git rev-parse "):
			writeCommandOutput(command, testSHA)
		case commandLine(command) == "git tag --list v*":
			writeCommandOutput(command, "v1.2.3")
		case strings.HasPrefix(commandLine(command), "gh release view "):
			writeCommandOutput(command, `{"isDraft":true,"isPrerelease":false}`)
		case commandLine(command) == "git cat-file -e v1.2.3^{tag}":
			return fakeExitError{code: 1}
		}
		return nil
	}
	service, _, errOut := newTestService(t, fake)
	_ = githubOutputFixture(t, service, map[string]string{})
	if code := service.Run(context.Background(), []string{"detect-release-tag"}); code != 1 {
		t.Fatalf("Run = %d", code)
	}
	requireContains(t, errOut.String(), "non-final GitHub release state", "draft=true")
}

func TestVerifyReleaseTagTargetHandlesAnnotatedAndMovedTags(t *testing.T) {
	t.Run("annotated", func(t *testing.T) {
		fake := &fakeRunner{}
		fake.run = func(_ context.Context, command runner.Command) error {
			writeCommandOutput(command, testSHA+"\trefs/tags/v1.2.3^{}\n")
			return nil
		}
		service, _, _ := newTestService(t, fake)
		if code := service.Run(
			context.Background(),
			[]string{"verify-release-tag-target", "v1.2.3", testSHA},
		); code != 0 {
			t.Fatalf("Run = %d", code)
		}
	})

	t.Run("lightweight fallback", func(t *testing.T) {
		fake := &fakeRunner{}
		fake.run = func(_ context.Context, command runner.Command) error {
			switch commandLine(command) {
			case "git ls-remote origin refs/tags/v1.2.3^{}":
				return nil
			case "git ls-remote origin refs/tags/v1.2.3":
				writeCommandOutput(command, testSHA+"\trefs/tags/v1.2.3\n")
				return nil
			default:
				t.Fatalf("unexpected command: %s", commandLine(command))
				return nil
			}
		}
		service, _, _ := newTestService(t, fake)
		if code := service.Run(
			context.Background(),
			[]string{"verify-release-tag-target", "v1.2.3", testSHA},
		); code != 0 {
			t.Fatalf("Run = %d", code)
		}
	})

	t.Run("moved", func(t *testing.T) {
		fake := &fakeRunner{}
		fake.run = func(_ context.Context, command runner.Command) error {
			writeCommandOutput(command, differentSHA+"\trefs/tags/v1.2.3^{}\n")
			return nil
		}
		service, _, errOut := newTestService(t, fake)
		if code := service.Run(
			context.Background(),
			[]string{"verify-release-tag-target", "v1.2.3", testSHA},
		); code != 1 {
			t.Fatalf("Run = %d", code)
		}
		requireContains(t, errOut.String(), "release tag target moved", differentSHA, testSHA)
	})
}

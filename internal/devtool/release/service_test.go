package release

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"gate/internal/devtool/runner"
)

const (
	testCommitSHA = "1111111111111111111111111111111111111111"
	testTagObject = "2222222222222222222222222222222222222222"
)

func TestServiceDryRunResolvesVersionWithoutMutation(t *testing.T) {
	fixture := newReleaseRepository(t, false, true)
	service, out, _ := testService(fixture.work, "")
	checkCalled := false
	service.Check = func(context.Context) error {
		checkCalled = true
		return nil
	}

	if code := service.Run(context.Background(), []string{"--dry-run", "--since", "v1.0.0", "patch"}); code != 0 {
		t.Fatalf("Run = %d", code)
	}
	if checkCalled {
		t.Fatal("dry run executed checks")
	}
	if hasRef(fixture.work, "refs/tags/v1.0.1") {
		t.Fatal("dry run created a tag")
	}
	if !strings.Contains(out.String(), "Tag: v1.0.1") || !strings.Contains(out.String(), "No tag or push was created.") {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestServiceInteractiveRecommendationAcceptsAlias(t *testing.T) {
	fixture := newReleaseRepository(t, false, true)
	service, out, _ := testService(fixture.work, "m\n")

	if code := service.Run(context.Background(), []string{"--dry-run", "--since=v1.0.0"}); code != 0 {
		t.Fatalf("Run = %d", code)
	}
	if !strings.Contains(out.String(), "Recommended: minor") || !strings.Contains(out.String(), "Tag: v1.1.0") {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestServicePushesBranchAndAnnotatedTagAtomically(t *testing.T) {
	fixture := newReleaseRepository(t, true, true)
	service, out, errOut := testService(fixture.work, "")

	if code := service.Run(context.Background(), []string{"--yes", "--since", "v1.0.0", "patch"}); code != 0 {
		t.Fatalf("Run = %d\nstdout:\n%s\nstderr:\n%s", code, out.String(), errOut.String())
	}
	if !hasRef(fixture.work, "refs/tags/v1.0.1") {
		t.Fatal("local tag missing")
	}
	if !remoteHasRef(t, fixture.work, "refs/tags/v1.0.1") {
		t.Fatal("remote tag missing")
	}
	tagObject := gitOutput(t, fixture.work, "cat-file", "-p", "refs/tags/v1.0.1")
	if !strings.Contains(tagObject, "Release v1.0.1") || !strings.Contains(tagObject, "feat: second") {
		t.Fatalf("tag object = %q", tagObject)
	}
	for _, want := range []string{
		"ok: checking GitHub release access",
		"ok: creating release tag v1.0.1",
		"ok: pushing main and v1.0.1",
		"ok: dispatching release workflow for v1.0.1",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("stdout = %q, want %q", out.String(), want)
		}
	}
	if !strings.Contains(
		out.String(),
		"Resolved version\n  Tag: v1.0.1 (from patch bump)\n\nok: checking GitHub release access",
	) {
		t.Fatalf("resolved version is not separated from activity output: %q", out.String())
	}
	for _, legacy := range []string{"Release dispatch", "\nChecks\n", "checks passed", "created and pushed tag"} {
		if strings.Contains(out.String(), legacy) {
			t.Fatalf("legacy release progress %q remains in %q", legacy, out.String())
		}
	}
}

func TestServicePushUsesCapturedCommitAndTagObjectIDs(t *testing.T) {
	fixture := newReleaseRepository(t, true, true)
	target := strings.TrimSpace(gitOutput(t, fixture.work, "rev-parse", "HEAD"))
	var out, errOut bytes.Buffer
	commandRunner := &capturePushRunner{delegate: runner.OS{}}
	service := New(strings.NewReader(""), &out, &errOut, commandRunner)
	service.Dir = fixture.work
	service.Getenv = func(string) string { return "" }
	service.Check = func(context.Context) error { return nil }
	disableReleaseDispatch(service)

	if code := service.Run(context.Background(), []string{"--yes", "--since", "v1.0.0", "patch"}); code != 0 {
		t.Fatalf("Run = %d\nstdout:\n%s\nstderr:\n%s", code, out.String(), errOut.String())
	}
	tagObject := strings.TrimSpace(gitOutput(t, fixture.work, "rev-parse", "refs/tags/v1.0.1"))
	if !reflect.DeepEqual(commandRunner.push.Args, []string{
		"push",
		"--atomic",
		"origin",
		target + ":refs/heads/main",
		tagObject + ":refs/tags/v1.0.1",
	}) {
		t.Fatalf("push args = %#v", commandRunner.push.Args)
	}
}

func TestServiceDispatchPreflightFailureCreatesNoTagOrPush(t *testing.T) {
	fixture := newReleaseRepository(t, true, true)
	service, _, errOut := testService(fixture.work, "")
	checkCalled := false
	service.Check = func(context.Context) error {
		checkCalled = true
		return nil
	}
	service.PrepareReleaseDispatch = func(context.Context) (string, error) {
		return "", errors.New("missing GitHub access")
	}

	if code := service.Run(context.Background(), []string{"--yes", "--since", "v1.0.0", "patch"}); code != 1 {
		t.Fatalf("Run = %d", code)
	}
	if checkCalled {
		t.Fatal("dispatch preflight failure executed release checks")
	}
	if hasRef(fixture.work, "refs/tags/v1.0.1") ||
		remoteHasRef(t, fixture.work, "refs/tags/v1.0.1") {
		t.Fatal("dispatch preflight failure created a release tag")
	}
	if !strings.Contains(errOut.String(), "prepare GitHub release dispatch") {
		t.Fatalf("stderr = %q", errOut.String())
	}
}

func TestServiceDispatchFailurePreservesPushedTagAndPrintsRecovery(t *testing.T) {
	fixture := newReleaseRepository(t, true, true)
	service, _, errOut := testService(fixture.work, "")
	service.DispatchRelease = func(context.Context, string, string, string, string) error {
		return errors.New("injected dispatch failure")
	}

	if code := service.Run(context.Background(), []string{"--yes", "--since", "v1.0.0", "patch"}); code != 1 {
		t.Fatalf("Run = %d", code)
	}
	if !hasRef(fixture.work, "refs/tags/v1.0.1") ||
		!remoteHasRef(t, fixture.work, "refs/tags/v1.0.1") {
		t.Fatal("dispatch failure removed the already-pushed release tag")
	}
	for _, want := range []string{
		"tag v1.0.1 was pushed, but release workflow dispatch failed",
		"gh api --method POST repos/acme/gate/dispatches",
		"client_payload[tag]=v1.0.1",
		"client_payload[target_sha]=",
		"client_payload[tag_object]=",
	} {
		if !strings.Contains(errOut.String(), want) {
			t.Fatalf("stderr = %q, want %q", errOut.String(), want)
		}
	}
}

func TestServiceCheckFailureCreatesNoTag(t *testing.T) {
	fixture := newReleaseRepository(t, true, true)
	service, _, errOut := testService(fixture.work, "")
	service.Check = func(context.Context) error {
		return errors.New("check failed")
	}

	if code := service.Run(context.Background(), []string{"--yes", "--since", "v1.0.0", "patch"}); code != 1 {
		t.Fatalf("Run = %d", code)
	}
	if hasRef(fixture.work, "refs/tags/v1.0.1") {
		t.Fatal("failed check left a tag")
	}
	if !strings.Contains(errOut.String(), "checks failed; aborting release") {
		t.Fatalf("stderr = %q", errOut.String())
	}
}

func TestServiceRejectsUnsignedTargetWhenCommitSigningIsRequired(t *testing.T) {
	fixture := newReleaseRepository(t, true, true)
	runGit(t, fixture.work, "config", "commit.gpgSign", "true")
	service, _, errOut := testService(fixture.work, "")
	checkCalled := false
	service.Check = func(context.Context) error {
		checkCalled = true
		return nil
	}

	if code := service.Run(context.Background(), []string{"--yes", "--since", "v1.0.0", "patch"}); code != 1 {
		t.Fatalf("Run = %d\nstderr:\n%s", code, errOut.String())
	}
	if checkCalled {
		t.Fatal("unsigned release target executed checks")
	}
	if hasRef(fixture.work, "refs/tags/v1.0.1") {
		t.Fatal("unsigned release target created a tag")
	}
	if !strings.Contains(errOut.String(), "is unsigned while commit.gpgSign=true") {
		t.Fatalf("stderr = %q", errOut.String())
	}
}

func TestServicePushFailureRemovesAttemptTag(t *testing.T) {
	fixture := newReleaseRepository(t, true, true)
	hook := filepath.Join(fixture.remote, "hooks", "pre-receive")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(hook, 0o700); err != nil { //nolint:gosec // executable fixture hook must be runnable
		t.Fatal(err)
	}
	service, _, errOut := testService(fixture.work, "")

	if code := service.Run(context.Background(), []string{"--yes", "--since", "v1.0.0", "patch"}); code != 1 {
		t.Fatalf("Run = %d\nstderr:\n%s", code, errOut.String())
	}
	if hasRef(fixture.work, "refs/tags/v1.0.1") {
		t.Fatal("rejected push left a local tag")
	}
	if remoteHasRef(t, fixture.work, "refs/tags/v1.0.1") {
		t.Fatal("rejected push created a remote tag")
	}
	if !strings.Contains(errOut.String(), "removed the local tag") {
		t.Fatalf("stderr = %q", errOut.String())
	}
}

func TestServiceInterruptedPushRemovesAttemptTag(t *testing.T) {
	fixture := newReleaseRepository(t, true, true)
	var out, errOut bytes.Buffer
	service := New(
		strings.NewReader(""),
		&out,
		&errOut,
		&cancelPushRunner{delegate: runner.OS{}},
	)
	service.Dir = fixture.work
	service.Getenv = func(string) string { return "" }
	service.Check = func(context.Context) error { return nil }
	disableReleaseDispatch(service)

	if code := service.Run(context.Background(), []string{"--yes", "--since", "v1.0.0", "patch"}); code != 130 {
		t.Fatalf("Run = %d\nstdout:\n%s\nstderr:\n%s", code, out.String(), errOut.String())
	}
	if hasRef(fixture.work, "refs/tags/v1.0.1") {
		t.Fatal("interrupted push left a local tag")
	}
	if !strings.Contains(out.String(), "Aborted.") {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestServicePushFailureDoesNotDeleteTagMovedByAnotherProcess(t *testing.T) {
	fixture := newReleaseRepository(t, true, true)
	var out, errOut bytes.Buffer
	service := New(
		strings.NewReader(""),
		&out,
		&errOut,
		&moveTagOnPushRunner{delegate: runner.OS{}},
	)
	service.Dir = fixture.work
	service.Getenv = func(string) string { return "" }
	service.Check = func(context.Context) error { return nil }
	disableReleaseDispatch(service)

	if code := service.Run(context.Background(), []string{"--yes", "--since", "v1.0.0", "patch"}); code != 1 {
		t.Fatalf("Run = %d\nstdout:\n%s\nstderr:\n%s", code, out.String(), errOut.String())
	}
	if !hasRef(fixture.work, "refs/tags/v1.0.1") {
		t.Fatal("compare-and-delete removed a tag moved by another process")
	}
	tagObject := gitOutput(t, fixture.work, "cat-file", "-p", "refs/tags/v1.0.1")
	if !strings.Contains(tagObject, "foreign tag") {
		t.Fatalf("tag object = %q", tagObject)
	}
	if !strings.Contains(errOut.String(), "local tag cleanup failed") {
		t.Fatalf("stderr = %q", errOut.String())
	}
}

func TestServiceInterruptedTagInstallationRemovesOwnedTag(t *testing.T) {
	fixture := newReleaseRepository(t, true, true)
	var out, errOut bytes.Buffer
	service := New(
		strings.NewReader(""),
		&out,
		&errOut,
		&cancelTagRunner{delegate: runner.OS{}},
	)
	service.Dir = fixture.work
	service.Getenv = func(string) string { return "" }
	service.Check = func(context.Context) error { return nil }
	disableReleaseDispatch(service)

	if code := service.Run(context.Background(), []string{"--yes", "--since", "v1.0.0", "patch"}); code != 130 {
		t.Fatalf("Run = %d\nstdout:\n%s\nstderr:\n%s", code, out.String(), errOut.String())
	}
	if hasRef(fixture.work, "refs/tags/v1.0.1") {
		t.Fatal("interrupted atomic tag installation left the owned tag")
	}
}

func TestServiceTagInstallationRacePreservesForeignTag(t *testing.T) {
	fixture := newReleaseRepository(t, true, true)
	var out, errOut bytes.Buffer
	service := New(
		strings.NewReader(""),
		&out,
		&errOut,
		&foreignTagBeforeInstallRunner{delegate: runner.OS{}},
	)
	service.Dir = fixture.work
	service.Getenv = func(string) string { return "" }
	service.Check = func(context.Context) error { return nil }
	disableReleaseDispatch(service)

	if code := service.Run(context.Background(), []string{"--yes", "--since", "v1.0.0", "patch"}); code != 1 {
		t.Fatalf("Run = %d\nstdout:\n%s\nstderr:\n%s", code, out.String(), errOut.String())
	}
	if !hasRef(fixture.work, "refs/tags/v1.0.1") {
		t.Fatal("atomic install race deleted a foreign tag")
	}
	tagObject := gitOutput(t, fixture.work, "cat-file", "-p", "refs/tags/v1.0.1")
	if !strings.Contains(tagObject, "foreign tag") {
		t.Fatalf("tag object = %q", tagObject)
	}
}

func TestServiceCreatesAndAtomicallyPushesSignedAnnotatedTag(t *testing.T) {
	for _, policy := range []string{"tag.gpgSign", "tag.forceSignAnnotated"} {
		t.Run(policy, func(t *testing.T) {
			fixture := newReleaseRepository(t, true, true)
			configureSSHSigning(t, fixture.work)
			runGit(t, fixture.work, "config", "commit.gpgSign", "true")
			runGit(t, fixture.work, "commit", "--amend", "--no-edit")
			runGit(t, fixture.work, "config", policy, "true")
			service, out, errOut := testService(fixture.work, "")

			if code := service.Run(context.Background(), []string{"--yes", "--since", "v1.0.0", "patch"}); code != 0 {
				t.Fatalf("Run = %d\nstdout:\n%s\nstderr:\n%s", code, out.String(), errOut.String())
			}
			if !hasRef(fixture.work, "refs/tags/v1.0.1") ||
				!remoteHasRef(t, fixture.work, "refs/tags/v1.0.1") {
				t.Fatal("signed tag was not created and pushed")
			}
			commitObject := gitOutput(t, fixture.work, "cat-file", "commit", "HEAD")
			if !strings.Contains(commitObject, "BEGIN SSH SIGNATURE") {
				t.Fatalf("release commit is not SSH-signed:\n%s", commitObject)
			}
			tagObject := gitOutput(t, fixture.work, "cat-file", "-p", "refs/tags/v1.0.1")
			if !strings.Contains(tagObject, "BEGIN SSH SIGNATURE") {
				t.Fatalf("tag is not SSH-signed:\n%s", tagObject)
			}
		})
	}
}

func TestServiceSignedTagPushFailureRemovesOwnedTag(t *testing.T) {
	fixture := newReleaseRepository(t, true, true)
	configureSSHSigning(t, fixture.work)
	runGit(t, fixture.work, "config", "tag.gpgSign", "true")
	hook := filepath.Join(fixture.remote, "hooks", "pre-receive")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(hook, 0o700); err != nil { //nolint:gosec // executable fixture hook must be runnable
		t.Fatal(err)
	}
	service, _, errOut := testService(fixture.work, "")

	if code := service.Run(context.Background(), []string{"--yes", "--since", "v1.0.0", "patch"}); code != 1 {
		t.Fatalf("Run = %d\nstderr:\n%s", code, errOut.String())
	}
	if hasRef(fixture.work, "refs/tags/v1.0.1") {
		t.Fatal("rejected signed-tag push left a local tag")
	}
	if remoteHasRef(t, fixture.work, "refs/tags/v1.0.1") {
		t.Fatal("rejected signed-tag push created a remote tag")
	}
}

func TestServiceCancellationAfterTagCreationRemovesOwnedTag(t *testing.T) {
	fixture := newReleaseRepository(t, true, true)
	ctx, cancel := context.WithCancel(context.Background())
	var out, errOut bytes.Buffer
	service := New(
		strings.NewReader(""),
		&out,
		&errOut,
		&cancelAfterTagRunner{delegate: runner.OS{}, cancel: cancel},
	)
	service.Dir = fixture.work
	service.Getenv = func(string) string { return "" }
	service.Check = func(context.Context) error { return nil }
	disableReleaseDispatch(service)

	if code := service.Run(ctx, []string{"--yes", "--since", "v1.0.0", "patch"}); code != 130 {
		t.Fatalf("Run = %d\nstdout:\n%s\nstderr:\n%s", code, out.String(), errOut.String())
	}
	if hasRef(fixture.work, "refs/tags/v1.0.1") {
		t.Fatal("cancellation after successful tag creation left the owned tag")
	}
}

func TestServiceCancellationUnblocksConfirmationWithoutMutation(t *testing.T) {
	fixture := newReleaseRepository(t, true, true)
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = reader.Close()
		_ = writer.Close()
	})
	var out, errOut bytes.Buffer
	service := New(reader, &out, &errOut, runner.OS{})
	service.Dir = fixture.work
	service.Getenv = func(string) string { return "" }
	disableReleaseDispatch(service)
	checkReached := make(chan struct{})
	service.Check = func(context.Context) error {
		close(checkReached)
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan int, 1)
	go func() {
		result <- service.Run(ctx, []string{"--since", "v1.0.0", "patch"})
	}()
	<-checkReached
	cancel()
	select {
	case code := <-result:
		if code != 130 {
			t.Fatalf("Run = %d\nstdout:\n%s\nstderr:\n%s", code, out.String(), errOut.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("confirmation did not unblock after context cancellation")
	}
	if hasRef(fixture.work, "refs/tags/v1.0.1") {
		t.Fatal("cancelled confirmation created a tag")
	}
	if _, err := reader.Stat(); err != nil {
		t.Fatalf("cancellation closed prompt input before terminal restoration: %v", err)
	}
	if _, err := writer.WriteString("n\n"); err != nil {
		t.Fatalf("prompt input is unusable after cancellation: %v", err)
	}
}

func TestServiceDeclinedConfirmationCreatesNoTag(t *testing.T) {
	fixture := newReleaseRepository(t, true, true)
	service, out, errOut := testService(fixture.work, "n\n")

	if code := service.Run(context.Background(), []string{"--since", "v1.0.0", "patch"}); code != 0 {
		t.Fatalf("Run = %d\nstdout:\n%s\nstderr:\n%s", code, out.String(), errOut.String())
	}
	if hasRef(fixture.work, "refs/tags/v1.0.1") {
		t.Fatal("declined release created a tag")
	}
	if !strings.Contains(out.String(), "Aborted. No tag created.") {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestServiceRejectsDirtyTreeBeforeChecks(t *testing.T) {
	fixture := newReleaseRepository(t, true, true)
	if err := os.WriteFile(filepath.Join(fixture.work, "dirty.txt"), []byte("dirty\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	service, _, errOut := testService(fixture.work, "")
	checkCalled := false
	service.Check = func(context.Context) error {
		checkCalled = true
		return nil
	}

	if code := service.Run(context.Background(), []string{"--yes", "--since", "v1.0.0", "patch"}); code != 1 {
		t.Fatalf("Run = %d", code)
	}
	if checkCalled {
		t.Fatal("dirty tree executed checks")
	}
	if !strings.Contains(errOut.String(), "clean working tree") {
		t.Fatalf("stderr = %q", errOut.String())
	}
}

func TestServiceDryRunDirtyTreeRequiresInteractiveConsent(t *testing.T) {
	fixture := newReleaseRepository(t, false, true)
	if err := os.WriteFile(filepath.Join(fixture.work, "dirty.txt"), []byte("dirty\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	service, _, _ := testService(fixture.work, "y\n")
	if code := service.Run(context.Background(), []string{"--dry-run", "--since", "v1.0.0", "patch"}); code != 0 {
		t.Fatalf("Run = %d", code)
	}
}

func TestServiceDryRunDirtyTreeRejectsAutomation(t *testing.T) {
	fixture := newReleaseRepository(t, false, true)
	if err := os.WriteFile(filepath.Join(fixture.work, "dirty.txt"), []byte("dirty\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	service, _, errOut := testService(fixture.work, "")
	if code := service.Run(context.Background(), []string{"--dry-run", "--yes", "--since", "v1.0.0", "patch"}); code != 1 {
		t.Fatalf("Run = %d", code)
	}
	if !strings.Contains(errOut.String(), "requires interactive confirmation") {
		t.Fatalf("stderr = %q", errOut.String())
	}
}

func TestServiceRejectsNonMainBranch(t *testing.T) {
	fixture := newReleaseRepository(t, false, true)
	runGit(t, fixture.work, "switch", "-c", "feature")
	service, _, errOut := testService(fixture.work, "")
	if code := service.Run(context.Background(), []string{"--dry-run", "--since", "v1.0.0", "patch"}); code != 1 {
		t.Fatalf("Run = %d", code)
	}
	if !strings.Contains(errOut.String(), "branch 'main'") {
		t.Fatalf("stderr = %q", errOut.String())
	}
}

func TestServiceRejectsExistingLocalTag(t *testing.T) {
	fixture := newReleaseRepository(t, false, true)
	service, _, errOut := testService(fixture.work, "")
	if code := service.Run(context.Background(), []string{"--dry-run", "--since", "v1.0.0", "v1.0.0"}); code != 1 {
		t.Fatalf("Run = %d", code)
	}
	if !strings.Contains(errOut.String(), "tag already exists") {
		t.Fatalf("stderr = %q", errOut.String())
	}
}

func TestServiceRejectsNonSemverPublishedReleaseTag(t *testing.T) {
	fixture := newReleaseRepository(t, false, true)
	runGit(t, fixture.work, "remote", "add", "origin", "https://github.com/acme/gate.git")
	var out, errOut bytes.Buffer
	service := New(strings.NewReader(""), &out, &errOut, &skipFetchRunner{delegate: runner.OS{}})
	service.Dir = fixture.work
	service.Getenv = func(string) string { return "" }
	service.latestTagFn = func(context.Context, string, string) (string, error) {
		return "--all", nil
	}

	if code := service.Run(context.Background(), []string{"--dry-run", "patch"}); code != 1 {
		t.Fatalf("Run = %d", code)
	}
	if !strings.Contains(errOut.String(), "not a strict semver tag") {
		t.Fatalf("stderr = %q", errOut.String())
	}
}

func TestServiceNoCommitsStopsBeforePrompt(t *testing.T) {
	fixture := newReleaseRepository(t, false, false)
	service, out, _ := testService(fixture.work, "")
	if code := service.Run(context.Background(), []string{"--dry-run", "--since", "v1.0.0"}); code != 0 {
		t.Fatalf("Run = %d", code)
	}
	if !strings.Contains(out.String(), "No commits to release.") {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestServiceCancelledContextReturns130(t *testing.T) {
	fixture := newReleaseRepository(t, false, true)
	service, out, _ := testService(fixture.work, "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if code := service.Run(ctx, []string{"--dry-run", "--since", "v1.0.0", "patch"}); code != 130 {
		t.Fatalf("Run = %d", code)
	}
	if !strings.Contains(out.String(), "Aborted.") {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestDefaultCheckStreamsJustCheckOutput(t *testing.T) {
	var out, errOut bytes.Buffer
	commandRunner := &recordingRunner{}
	service := New(strings.NewReader(""), &out, &errOut, commandRunner)
	service.Dir = "/tmp/repository"

	if err := service.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	command := commandRunner.command
	if command.Name != "just" || strings.Join(command.Args, " ") != "check" {
		t.Fatalf("command = %s %v", command.Name, command.Args)
	}
	if command.Dir != service.Dir || command.Stdout != &out || command.Stderr != &errOut {
		t.Fatalf("check command did not preserve repository directory and output streams: %#v", command)
	}
}

func TestGitHubReleaseDispatchUsesStructuredRepositoryDispatchAPI(t *testing.T) {
	var out, errOut bytes.Buffer
	commandRunner := &releaseDispatchRunner{}
	service := New(strings.NewReader(""), &out, &errOut, commandRunner)
	service.Dir = "/tmp/repository"
	commandRunner.run = func(command runner.Command) error {
		switch {
		case command.Name == "git":
			_, _ = io.WriteString(command.Stdout, "git@github.com:jinyongp/gate.git\n")
		case command.Name == "gh" && strings.Contains(strings.Join(command.Args, " "), "repos/jinyongp/gate --jq"):
			_, _ = io.WriteString(command.Stdout, "true\n")
		}
		return nil
	}

	repository, err := service.prepareReleaseDispatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if repository != "jinyongp/gate" {
		t.Fatalf("repository = %q", repository)
	}
	if err := service.dispatchRelease(
		context.Background(),
		repository,
		"v2.11.0",
		testCommitSHA,
		testTagObject,
	); err != nil {
		t.Fatal(err)
	}
	if len(commandRunner.commands) != 3 {
		t.Fatalf("commands = %#v", commandRunner.commands)
	}
	preflight := commandRunner.commands[1]
	if !reflect.DeepEqual(preflight.Args, []string{
		"api", "repos/jinyongp/gate", "--jq", ".permissions.push",
	}) {
		t.Fatalf("preflight args = %#v", preflight.Args)
	}
	dispatch := commandRunner.commands[2]
	if !reflect.DeepEqual(dispatch.Args, []string{
		"api",
		"--silent",
		"--method",
		"POST",
		"repos/jinyongp/gate/dispatches",
		"-f",
		"event_type=release",
		"-F",
		"client_payload[tag]=v2.11.0",
		"-F",
		"client_payload[target_sha]=" + testCommitSHA,
		"-F",
		"client_payload[tag_object]=" + testTagObject,
	}) {
		t.Fatalf("dispatch args = %#v", dispatch.Args)
	}
	for _, command := range []runner.Command{preflight, dispatch} {
		if !reflect.DeepEqual(command.Env, []string{"GH_PROMPT_DISABLED=1"}) {
			t.Fatalf("command env = %#v", command.Env)
		}
	}
}

type recordingRunner struct {
	command runner.Command
}

func (runner *recordingRunner) Run(_ context.Context, command runner.Command) error {
	runner.command = command
	return nil
}

type releaseDispatchRunner struct {
	commands []runner.Command
	run      func(runner.Command) error
}

func (commandRunner *releaseDispatchRunner) Run(_ context.Context, command runner.Command) error {
	commandRunner.commands = append(commandRunner.commands, command)
	if commandRunner.run != nil {
		return commandRunner.run(command)
	}
	return nil
}

type cancelPushRunner struct {
	delegate runner.Runner
}

type capturePushRunner struct {
	delegate runner.Runner
	push     runner.Command
}

func (commandRunner *capturePushRunner) Run(ctx context.Context, command runner.Command) error {
	if command.Name == "git" && len(command.Args) > 0 && command.Args[0] == "push" {
		commandRunner.push = command
	}
	return commandRunner.delegate.Run(ctx, command)
}

func (commandRunner *cancelPushRunner) Run(ctx context.Context, command runner.Command) error {
	if command.Name == "git" && len(command.Args) > 0 && command.Args[0] == "push" {
		return context.Canceled
	}
	return commandRunner.delegate.Run(ctx, command)
}

type cancelTagRunner struct {
	delegate runner.Runner
}

type moveTagOnPushRunner struct {
	delegate runner.Runner
}

type foreignTagBeforeInstallRunner struct {
	delegate runner.Runner
}

type cancelAfterTagRunner struct {
	delegate runner.Runner
	cancel   context.CancelFunc
}

func (commandRunner *cancelAfterTagRunner) Run(ctx context.Context, command runner.Command) error {
	err := commandRunner.delegate.Run(ctx, command)
	if err == nil &&
		command.Name == "git" &&
		len(command.Args) > 1 &&
		command.Args[0] == "update-ref" &&
		command.Args[1] == "refs/tags/v1.0.1" {
		commandRunner.cancel()
	}
	return err
}

func (commandRunner *foreignTagBeforeInstallRunner) Run(ctx context.Context, command runner.Command) error {
	if command.Name == "git" &&
		len(command.Args) > 1 &&
		command.Args[0] == "update-ref" &&
		command.Args[1] == "refs/tags/v1.0.1" {
		if err := commandRunner.delegate.Run(ctx, runner.Command{
			Name:   "git",
			Args:   []string{"tag", "-a", "v1.0.1", "-m", "foreign tag", "v1.0.0"},
			Dir:    command.Dir,
			Stdout: command.Stdout,
			Stderr: command.Stderr,
		}); err != nil {
			return err
		}
	}
	return commandRunner.delegate.Run(ctx, command)
}

func (commandRunner *moveTagOnPushRunner) Run(ctx context.Context, command runner.Command) error {
	if command.Name == "git" && len(command.Args) > 0 && command.Args[0] == "push" {
		if err := commandRunner.delegate.Run(ctx, runner.Command{
			Name:   "git",
			Args:   []string{"tag", "-f", "-a", "v1.0.1", "-m", "foreign tag", "v1.0.0"},
			Dir:    command.Dir,
			Stdout: command.Stdout,
			Stderr: command.Stderr,
		}); err != nil {
			return err
		}
		return errors.New("injected push failure")
	}
	return commandRunner.delegate.Run(ctx, command)
}

func (commandRunner *cancelTagRunner) Run(ctx context.Context, command runner.Command) error {
	if command.Name == "git" &&
		len(command.Args) > 1 &&
		command.Args[0] == "update-ref" &&
		command.Args[1] == "refs/tags/v1.0.1" {
		if err := commandRunner.delegate.Run(ctx, command); err != nil {
			return err
		}
		return context.Canceled
	}
	return commandRunner.delegate.Run(ctx, command)
}

type skipFetchRunner struct {
	delegate runner.Runner
}

func (commandRunner *skipFetchRunner) Run(ctx context.Context, command runner.Command) error {
	if command.Name == "git" && len(command.Args) > 0 && command.Args[0] == "fetch" {
		return nil
	}
	return commandRunner.delegate.Run(ctx, command)
}

type releaseFixture struct {
	work   string
	remote string
}

func newReleaseRepository(t *testing.T, withRemote, withSecondCommit bool) releaseFixture {
	t.Helper()
	root := t.TempDir()
	work := filepath.Join(root, "work")
	if err := os.Mkdir(work, 0o750); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "init", "-b", "main")
	runGit(t, work, "config", "user.name", "Gate Test")
	runGit(t, work, "config", "user.email", "gate@example.com")
	runGit(t, work, "config", "commit.gpgsign", "false")
	runGit(t, work, "config", "tag.gpgSign", "false")
	runGit(t, work, "config", "tag.forceSignAnnotated", "false")
	writeCommit(t, work, "README.md", "base\n", "chore: base")
	runGit(t, work, "tag", "v1.0.0")

	fixture := releaseFixture{work: work}
	if withRemote {
		fixture.remote = filepath.Join(root, "remote.git")
		runGit(t, root, "init", "--bare", fixture.remote)
		runGit(t, work, "remote", "add", "origin", fixture.remote)
		runGit(t, work, "push", "-u", "origin", "main", "--tags")
		runGit(t, fixture.remote, "config", "core.hooksPath", filepath.Join(fixture.remote, "hooks"))
	}
	if withSecondCommit {
		writeCommit(t, work, "feature.txt", "second\n", "feat: second")
	}
	return fixture
}

func writeCommit(t *testing.T, dir, name, content, message string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", name)
	runGit(t, dir, "commit", "-m", message)
}

func testService(dir, input string) (*Service, *bytes.Buffer, *bytes.Buffer) {
	var out, errOut bytes.Buffer
	service := New(strings.NewReader(input), &out, &errOut, runner.OS{})
	service.Dir = dir
	service.Getenv = func(string) string { return "" }
	service.Check = func(context.Context) error { return nil }
	disableReleaseDispatch(service)
	return service, &out, &errOut
}

func disableReleaseDispatch(service *Service) {
	service.PrepareReleaseDispatch = func(context.Context) (string, error) {
		return "acme/gate", nil
	}
	service.DispatchRelease = func(context.Context, string, string, string, string) error {
		return nil
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...) //nolint:gosec // test-owned structured Git arguments
	command.Dir = dir
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func configureSSHSigning(t *testing.T, dir string) {
	t.Helper()
	keyPath := filepath.Join(t.TempDir(), "release-signing-key")
	command := exec.Command( //nolint:gosec // test-owned key path and structured arguments
		"ssh-keygen",
		"-q",
		"-t",
		"ed25519",
		"-N",
		"",
		"-f",
		keyPath,
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("create SSH signing key: %v\n%s", err, output)
	}
	runGit(t, dir, "config", "gpg.format", "ssh")
	runGit(t, dir, "config", "user.signingkey", keyPath)
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...) //nolint:gosec // test-owned structured Git arguments
	command.Dir = dir
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return string(output)
}

func hasRef(dir, ref string) bool {
	command := exec.Command("git", "rev-parse", "--verify", ref) //nolint:gosec // test-owned ref
	command.Dir = dir
	return command.Run() == nil
}

func remoteHasRef(t *testing.T, dir, ref string) bool {
	t.Helper()
	command := exec.Command("git", "ls-remote", "--exit-code", "origin", ref) //nolint:gosec // test-owned ref
	command.Dir = dir
	err := command.Run()
	if err == nil {
		return true
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) && exitError.ExitCode() == 2 {
		return false
	}
	t.Fatalf("git ls-remote %s: %v", ref, err)
	return false
}

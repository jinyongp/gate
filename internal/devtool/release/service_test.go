package release

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gate/internal/devtool/runner"
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
	if !strings.Contains(out.String(), "created and pushed tag v1.0.1") {
		t.Fatalf("stdout = %q", out.String())
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

func TestServiceInterruptedTagCreationRemovesAttemptTag(t *testing.T) {
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

	if code := service.Run(context.Background(), []string{"--yes", "--since", "v1.0.0", "patch"}); code != 130 {
		t.Fatalf("Run = %d\nstdout:\n%s\nstderr:\n%s", code, out.String(), errOut.String())
	}
	if hasRef(fixture.work, "refs/tags/v1.0.1") {
		t.Fatal("interrupted tag creation left a local tag")
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

type recordingRunner struct {
	command runner.Command
}

func (runner *recordingRunner) Run(_ context.Context, command runner.Command) error {
	runner.command = command
	return nil
}

type cancelPushRunner struct {
	delegate runner.Runner
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

func (commandRunner *cancelTagRunner) Run(ctx context.Context, command runner.Command) error {
	if command.Name == "git" &&
		len(command.Args) > 1 &&
		command.Args[0] == "tag" &&
		command.Args[1] == "-a" {
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
	return service, &out, &errOut
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

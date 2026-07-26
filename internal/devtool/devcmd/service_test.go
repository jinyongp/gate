package devcmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"gate/internal/devtool/platform"
	"gate/internal/devtool/runner"
)

func TestHandlesDevelopmentCommands(t *testing.T) {
	for _, command := range []string{
		"build", "run", "test", "cover", "fmt-check", "vet", "lint",
		"lint-json", "vuln", "docs-check", "fmt", "check", "build-all", "scripts-check",
	} {
		if !Handles(command) {
			t.Fatalf("Handles(%q) = false", command)
		}
	}
	if Handles("release") || Handles("unknown") {
		t.Fatal("Handles accepted a non-development command")
	}
}

func TestBuildAllUsesExplicitReleaseTargetMatrix(t *testing.T) {
	fake := &fakeRunner{}
	service, out, _ := newTestService(fake, platform.Darwin{})
	var madeDirectory string
	service.MkdirAll = func(path string, _ os.FileMode) error {
		madeDirectory = path
		return nil
	}

	if code := service.Run(context.Background(), []string{"build-all", "v2.3.4", "dist"}); code != 0 {
		t.Fatalf("Run = %d", code)
	}
	if madeDirectory != "repo/dist" {
		t.Fatalf("created directory = %q", madeDirectory)
	}
	if len(fake.commands) != 4 {
		t.Fatalf("commands = %d", len(fake.commands))
	}
	wantTargets := [][]string{
		{"CGO_ENABLED=0", "GOOS=darwin", "GOARCH=arm64"},
		{"CGO_ENABLED=0", "GOOS=darwin", "GOARCH=amd64"},
		{"CGO_ENABLED=0", "GOOS=linux", "GOARCH=arm64"},
		{"CGO_ENABLED=0", "GOOS=linux", "GOARCH=amd64"},
	}
	for index, command := range fake.commands {
		if !reflect.DeepEqual(command.Env, wantTargets[index]) {
			t.Fatalf("command %d env = %v", index, command.Env)
		}
		if !containsArg(command.Args, "-s -w -X main.version=v2.3.4") {
			t.Fatalf("command %d args = %v", index, command.Args)
		}
	}
	if strings.Count(out.String(), "built dist/gate-") != 4 {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestBuildAllRejectsUnsafeVersionBeforeMutation(t *testing.T) {
	fake := &fakeRunner{}
	service, _, errOut := newTestService(fake, platform.Darwin{})
	mkdirCalled := false
	service.MkdirAll = func(string, os.FileMode) error {
		mkdirCalled = true
		return nil
	}

	if code := service.Run(context.Background(), []string{"build-all", "v1.0.0 -extldflags=bad"}); code != 2 {
		t.Fatalf("Run = %d", code)
	}
	if mkdirCalled || len(fake.commands) != 0 {
		t.Fatal("invalid version performed a mutation")
	}
	if !strings.Contains(errOut.String(), "invalid build version") {
		t.Fatalf("stderr = %q", errOut.String())
	}
}

func TestBuildFallsBackToDevWhenGitDescribeFails(t *testing.T) {
	fake := &fakeRunner{
		run: func(command runner.Command) error {
			if command.Name == "git" {
				return errors.New("no Git metadata")
			}
			return nil
		},
	}
	service, out, _ := newTestService(fake, platform.Darwin{})
	service.MkdirAll = func(string, os.FileMode) error { return nil }

	if code := service.Run(context.Background(), []string{"build"}); code != 0 {
		t.Fatalf("Run = %d", code)
	}
	if len(fake.commands) != 2 || !containsArg(fake.commands[1].Args, "-s -w -X main.version=dev") {
		t.Fatalf("commands = %#v", fake.commands)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestRunGateForwardsArgumentsWithoutShellParsing(t *testing.T) {
	fake := &fakeRunner{}
	service, _, _ := newTestService(fake, platform.Linux{})
	args := []string{"run", "up", "--domain", "value with spaces", "$(touch nope)"}
	if code := service.Run(context.Background(), args); code != 0 {
		t.Fatalf("Run = %d", code)
	}
	want := []string{"run", "./cmd/gate", "up", "--domain", "value with spaces", "$(touch nope)"}
	if !reflect.DeepEqual(fake.commands[0].Args, want) {
		t.Fatalf("args = %#v, want %#v", fake.commands[0].Args, want)
	}
}

func TestLintCoversAlternateHostFromDarwinAndLinux(t *testing.T) {
	tests := []struct {
		name      string
		host      platform.Host
		alternate string
	}{
		{name: "darwin", host: platform.Darwin{}, alternate: "GOOS=linux"},
		{name: "linux", host: platform.Linux{}, alternate: "GOOS=darwin"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := &fakeRunner{}
			service, _, _ := newTestService(fake, test.host)
			if code := service.Run(context.Background(), []string{"lint", "--fix"}); code != 0 {
				t.Fatalf("Run = %d", code)
			}
			if len(fake.commands) != 2 {
				t.Fatalf("commands = %d", len(fake.commands))
			}
			if containsArg(fake.commands[0].Args, "-exec") {
				t.Fatalf("current-host lint args = %v", fake.commands[0].Args)
			}
			alternate := strings.Join(fake.commands[1].Args, " ")
			if !strings.Contains(alternate, "/usr/bin/env "+test.alternate+" GOARCH=amd64") {
				t.Fatalf("alternate-host lint args = %v", fake.commands[1].Args)
			}
			if fake.commands[1].Args[len(fake.commands[1].Args)-1] != "--fix" {
				t.Fatalf("extra lint argument missing: %v", fake.commands[1].Args)
			}
		})
	}
}

func TestLintJSONUsesCurrentHostAndSplitOutputs(t *testing.T) {
	fake := &fakeRunner{}
	service, _, _ := newTestService(fake, platform.Linux{})
	if code := service.Run(context.Background(), []string{"lint-json"}); code != 0 {
		t.Fatalf("Run = %d", code)
	}
	if len(fake.commands) != 1 {
		t.Fatalf("commands = %d", len(fake.commands))
	}
	for _, argument := range []string{
		"--output.text.path=stderr",
		"--output.text.colors=false",
		"--output.json.path=stdout",
	} {
		if !containsArg(fake.commands[0].Args, argument) {
			t.Fatalf("lint-json args = %v", fake.commands[0].Args)
		}
	}
}

func TestFormatCheckExcludesTruststoreAndReportsFiles(t *testing.T) {
	fake := &fakeRunner{
		run: func(command runner.Command) error {
			switch command.Name {
			case "git":
				_, _ = io.WriteString(command.Stdout, "a.go\ninternal/truststore/generated.go\nb.go\n")
			case "gofmt":
				_, _ = io.WriteString(command.Stdout, "b.go\n")
			}
			return nil
		},
	}
	service, out, _ := newTestService(fake, platform.Darwin{})
	if code := service.Run(context.Background(), []string{"fmt-check"}); code != 1 {
		t.Fatalf("Run = %d", code)
	}
	if got := fake.commands[1].Args; !reflect.DeepEqual(got, []string{"-l", "a.go", "b.go"}) {
		t.Fatalf("gofmt args = %#v", got)
	}
	if !strings.Contains(out.String(), "unformatted: b.go") {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestFormatIncludesTrackedAndUntrackedFiles(t *testing.T) {
	fake := &fakeRunner{
		run: func(command runner.Command) error {
			if command.Name == "git" {
				_, _ = io.WriteString(command.Stdout, "a.go\nnew.go\n")
			}
			return nil
		},
	}
	service, _, _ := newTestService(fake, platform.Darwin{})
	if code := service.Run(context.Background(), []string{"fmt"}); code != 0 {
		t.Fatalf("Run = %d", code)
	}
	if !containsArg(fake.commands[0].Args, "--others") {
		t.Fatalf("git args = %v", fake.commands[0].Args)
	}
	if got := fake.commands[1].Args; !reflect.DeepEqual(got, []string{"-w", "a.go", "new.go"}) {
		t.Fatalf("gofmt args = %v", got)
	}
	if !containsArg(fake.commands[2].Args, "golang.org/x/tools/cmd/goimports@v0.47.0") {
		t.Fatalf("goimports args = %v", fake.commands[2].Args)
	}
}

func TestDocsCheckReportsEveryForbiddenMatchAndStillChecksHelp(t *testing.T) {
	fake := &fakeRunner{}
	service, _, errOut := newTestService(fake, platform.Darwin{})
	service.ReadFile = func(string) ([]byte, error) {
		return []byte("intro\n```bash\n`gate up`\n"), nil
	}
	if code := service.Run(context.Background(), []string{"docs-check"}); code != 1 {
		t.Fatalf("Run = %d", code)
	}
	if !strings.Contains(errOut.String(), "2:```bash") || !strings.Contains(errOut.String(), "3:`gate up`") {
		t.Fatalf("stderr = %q", errOut.String())
	}
	if len(fake.commands) != 1 || !containsArg(fake.commands[0].Args, "TestUsageQuickReferenceMatchesPublicHelp") {
		t.Fatalf("commands = %#v", fake.commands)
	}
}

func TestCheckProgressAndFailureDetail(t *testing.T) {
	fake := &fakeRunner{
		run: func(command runner.Command) error {
			if command.Name == "git" {
				return nil
			}
			if command.Name == "go" && len(command.Args) > 0 && command.Args[0] == "vet" {
				_, _ = io.WriteString(command.Stderr, "injected check failure\n")
				return exitError(23)
			}
			return nil
		},
	}
	service, out, errOut := newTestService(fake, platform.Darwin{})
	service.ReadFile = func(string) ([]byte, error) { return []byte("clean\n"), nil }
	service.Now = advancingClock(time.Second)

	if code := service.Run(context.Background(), []string{"check"}); code != 23 {
		t.Fatalf("Run = %d", code)
	}
	if !strings.Contains(out.String(), "[1/8] Documentation boundaries") ||
		!strings.Contains(out.String(), "ok: [2/8] Go formatting (1s)") ||
		strings.Contains(out.String(), "[4/8]") {
		t.Fatalf("stdout = %q", out.String())
	}
	if !strings.Contains(errOut.String(), "error: [3/8] Go vet (1s)") ||
		!strings.Contains(errOut.String(), "injected check failure") {
		t.Fatalf("stderr = %q", errOut.String())
	}
}

func TestCheckSuccessShowsAllEightStages(t *testing.T) {
	fake := &fakeRunner{run: func(command runner.Command) error {
		if command.Name == "git" && len(command.Args) > 0 && command.Args[0] == "ls-files" {
			_, _ = io.WriteString(command.Stdout, strings.Join(retainedShellFiles, "\x00")+"\x00")
		}
		return nil
	}}
	service, out, errOut := newTestService(fake, platform.Linux{})
	service.ReadFile = validRepositoryContractFixture
	service.LookPath = func(string) (string, error) { return "", os.ErrNotExist }
	service.Now = advancingClock(time.Second)
	if code := service.Run(context.Background(), []string{"check"}); code != 0 {
		t.Fatalf("Run = %d", code)
	}
	for index := 1; index <= 8; index++ {
		if !strings.Contains(out.String(), "["+string(rune('0'+index))+"/8]") {
			t.Fatalf("missing stage %d in %q", index, out.String())
		}
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q", errOut.String())
	}
}

func validRepositoryContractFixture(path string) ([]byte, error) {
	switch filepath.ToSlash(path) {
	case "repo/go.mod":
		return []byte("module gate\n\ngo 1.26\n\ntoolchain go1.26.5\n"), nil
	case "repo/justfile":
		return []byte(`set positional-arguments
go run ./cmd/gate-dev lint
go run ./cmd/gate-dev run "$@"
go run ./cmd/gate-dev scripts-check
go run ./cmd/gate-dev release
`), nil
	case "repo/.github/workflows/ci.yml":
		return []byte(`uses: actions/setup-go@sha
go-version: "1.26.x"
check-latest: true
uses: actions/setup-node@sha
node-version-file: .node-version
"$RUNNER_TEMP/gate-dev" lint
"$RUNNER_TEMP/gate-dev" scripts-check
GATE_RUN_LINUX_LOW_PORT_TEST
GATE_REQUIRE_INSTALL_PTY_TEST
`), nil
	case "repo/.github/workflows/release.yml":
		return []byte(`"$RUNNER_TEMP/gate-dev" ci detect-release-tag
"$RUNNER_TEMP/gate-dev" ci wait-for-ci
"$RUNNER_TEMP/gate-dev" ci build-release-artifacts
"$RUNNER_TEMP/gate-dev" ci checksums
"$RUNNER_TEMP/gate-dev" ci publish-release
"$RUNNER_TEMP/gate-dev" ci verify-release-tag-target
"$RUNNER_TEMP/gate-dev" ci wait-release-assets
"$RUNNER_TEMP/gate-dev" ci generate-homebrew-formula
node scripts/node/publish-packages.mjs "${VERSION_TAG}" bin
needs: [release_tag, ci_gate]
`), nil
	default:
		return []byte("clean\n"), nil
	}
}

func TestServiceReturns130OnCancellation(t *testing.T) {
	fake := &fakeRunner{run: func(runner.Command) error { return context.Canceled }}
	service, out, _ := newTestService(fake, platform.Linux{})
	if code := service.Run(context.Background(), []string{"vet"}); code != 130 {
		t.Fatalf("Run = %d", code)
	}
	if !strings.Contains(out.String(), "Aborted.") {
		t.Fatalf("stdout = %q", out.String())
	}
}

type fakeRunner struct {
	commands []runner.Command
	run      func(runner.Command) error
}

func (fake *fakeRunner) Run(_ context.Context, command runner.Command) error {
	copyCommand := command
	copyCommand.Args = append([]string(nil), command.Args...)
	copyCommand.Env = append([]string(nil), command.Env...)
	fake.commands = append(fake.commands, copyCommand)
	if fake.run != nil {
		return fake.run(command)
	}
	return nil
}

func newTestService(
	commandRunner runner.Runner,
	host platform.Host,
) (*Service, *bytes.Buffer, *bytes.Buffer) {
	var out, errOut bytes.Buffer
	service := New(strings.NewReader(""), &out, &errOut, commandRunner, host)
	service.Dir = "repo"
	service.Getenv = func(string) string { return "" }
	service.ReadFile = func(string) ([]byte, error) { return []byte{}, nil }
	service.MkdirAll = func(string, os.FileMode) error { return nil }
	service.PathExists = func(string) bool { return true }
	return service, &out, &errOut
}

func containsArg(args []string, wanted string) bool {
	for _, argument := range args {
		if argument == wanted {
			return true
		}
	}
	return false
}

func advancingClock(step time.Duration) func() time.Time {
	current := time.Unix(0, 0).Add(-step)
	return func() time.Time {
		current = current.Add(step)
		return current
	}
}

type exitError int

func (code exitError) Error() string { return "exit failure" }
func (code exitError) ExitCode() int { return int(code) }

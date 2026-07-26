package cirelease

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gate/internal/devtool/runner"
)

type fakeRunner struct {
	mu    sync.Mutex
	calls []runner.Command
	run   func(context.Context, runner.Command) error
}

func (fake *fakeRunner) Run(ctx context.Context, command runner.Command) error {
	fake.mu.Lock()
	fake.calls = append(fake.calls, runner.Command{
		Name: command.Name,
		Args: append([]string(nil), command.Args...),
		Dir:  command.Dir,
		Env:  append([]string(nil), command.Env...),
	})
	fake.mu.Unlock()
	if fake.run != nil {
		return fake.run(ctx, command)
	}
	return nil
}

func (fake *fakeRunner) commandLines() []string {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	lines := make([]string, len(fake.calls))
	for index, call := range fake.calls {
		lines[index] = strings.Join(append([]string{call.Name}, call.Args...), " ")
	}
	return lines
}

func commandLine(command runner.Command) string {
	return strings.Join(append([]string{command.Name}, command.Args...), " ")
}

func writeCommandOutput(command runner.Command, value string) {
	if command.Stdout != nil {
		_, _ = io.WriteString(command.Stdout, value)
	}
}

func writeCommandError(command runner.Command, value string) {
	if command.Stderr != nil {
		_, _ = io.WriteString(command.Stderr, value)
	}
}

type fakeExitError struct {
	code int
}

func (err fakeExitError) Error() string { return "exit" }
func (err fakeExitError) ExitCode() int { return err.code }

func failCommand(command runner.Command, message string) error {
	writeCommandError(command, message)
	return fakeExitError{code: 1}
}

func environment(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

func newTestService(t *testing.T, fake *fakeRunner) (*Service, *strings.Builder, *strings.Builder) {
	t.Helper()
	var out, errOut strings.Builder
	service := New(&out, &errOut, fake)
	service.Dir = t.TempDir()
	service.Getenv = environment(map[string]string{})
	service.Sleep = func(context.Context, time.Duration) error { return nil }
	return service, &out, &errOut
}

func githubOutputFixture(t *testing.T, service *Service, values map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "github-output")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	values["GITHUB_OUTPUT"] = path
	service.Getenv = environment(values)
	return path
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func requireContains(t *testing.T, value string, fragments ...string) {
	t.Helper()
	for _, fragment := range fragments {
		if !strings.Contains(value, fragment) {
			t.Fatalf("%q does not contain %q", value, fragment)
		}
	}
}

func requireNoCallContaining(t *testing.T, fake *fakeRunner, fragment string) {
	t.Helper()
	for _, line := range fake.commandLines() {
		if strings.Contains(line, fragment) {
			t.Fatalf("unexpected command containing %q: %v", fragment, fake.commandLines())
		}
	}
}

//go:build darwin || linux

package integrationtest

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

type commandResult struct {
	output string
	err    error
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve integration test source path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
}

func buildFakeTool(t *testing.T, root, output string) {
	t.Helper()
	command := exec.Command("go", "build", "-trimpath", "-o", output, "./internal/integrationtest/cmd/fake") //nolint:gosec // test-owned output path
	command.Dir = root
	if outputBytes, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build fake integration tool: %v\n%s", err, outputBytes)
	}
}

func linkTool(t *testing.T, source, destination string) {
	t.Helper()
	if err := os.Link(source, destination); err != nil {
		t.Fatalf("link %s: %v", filepath.Base(destination), err)
	}
}

func runCombined(
	t *testing.T,
	dir string,
	environment map[string]string,
	name string,
	args ...string,
) commandResult {
	t.Helper()
	command := exec.Command(name, args...) //nolint:gosec // test-owned structured command
	command.Dir = dir
	command.Env = mergedEnvironment(environment)
	output, err := command.CombinedOutput()
	return commandResult{output: string(output), err: err}
}

func mergedEnvironment(overrides map[string]string) []string {
	blocked := map[string]bool{
		"HOME": true, "PATH": true, "SHELL": true,
	}
	for key := range overrides {
		blocked[key] = true
	}
	environment := make([]string, 0, len(os.Environ())+len(overrides))
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if blocked[key] || strings.HasPrefix(key, "GATE_") || strings.HasPrefix(key, "XDG_") {
			continue
		}
		environment = append(environment, item)
	}
	for key, value := range overrides {
		environment = append(environment, key+"="+value)
	}
	return environment
}

func runPTY(
	dir string,
	environment map[string]string,
	input string,
	name string,
	args ...string,
) commandResult {
	command := exec.Command(name, args...) //nolint:gosec // test-owned structured command
	command.Dir = dir
	command.Env = mergedEnvironment(environment)
	terminal, err := pty.Start(command)
	if err != nil {
		return commandResult{err: fmt.Errorf("start PTY: %w", err)}
	}
	if _, err := io.WriteString(terminal, input); err != nil {
		_ = terminal.Close()
		_ = command.Process.Kill()
		_ = command.Wait()
		return commandResult{err: fmt.Errorf("write PTY input: %w", err)}
	}
	var output bytes.Buffer
	buffer := make([]byte, 4096)
	for {
		count, readErr := terminal.Read(buffer)
		if count > 0 {
			_, _ = output.Write(buffer[:count])
		}
		if readErr == nil {
			continue
		}
		if errors.Is(readErr, io.EOF) || errors.Is(readErr, syscall.EIO) {
			break
		}
		_ = terminal.Close()
		_ = command.Process.Kill()
		_ = command.Wait()
		return commandResult{output: output.String(), err: fmt.Errorf("read PTY: %w", readErr)}
	}
	_ = terminal.Close()
	return commandResult{output: output.String(), err: command.Wait()}
}

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(content)
}

func requireContains(t *testing.T, value, fragment string) {
	t.Helper()
	if !strings.Contains(value, fragment) {
		t.Fatalf("output does not contain %q:\n%s", fragment, value)
	}
}

func requireNotExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected %s to be absent, got %v", path, err)
	}
}

func lockFile(t *testing.T, path string) *os.File {
	t.Helper()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		t.Fatalf("lock %s: %v", path, err)
	}
	t.Cleanup(func() {
		_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
		_ = file.Close()
	})
	return file
}

func waitForPath(path string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

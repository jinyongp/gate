package runner

import (
	"bytes"
	"context"
	"errors"
	"os"
	"runtime"
	"testing"
	"time"
)

func TestOSRunnerPropagatesStreamsAndExitCode(t *testing.T) {
	var output, errorOutput bytes.Buffer
	err := (OS{}).Run(context.Background(), Command{
		Name:   "go",
		Args:   []string{"env", "GOOS"},
		Stdout: &output,
		Stderr: &errorOutput,
	})
	if err != nil {
		t.Fatalf("Run: %v; stderr=%s", err, errorOutput.String())
	}
	if got := string(bytes.TrimSpace(output.Bytes())); got != runtime.GOOS {
		t.Fatalf("GOOS = %q, want %q", got, runtime.GOOS)
	}

	err = (OS{}).Run(context.Background(), Command{Name: "go", Args: []string{"env", "-bad-flag"}})
	if ExitCode(err) == 0 {
		t.Fatalf("ExitCode(%v) = 0", err)
	}
}

func TestOSRunnerReturnsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := (OS{}).Run(ctx, Command{
		Name: os.Args[0],
		Args: []string{"-test.run=TestOSRunnerHelperProcess"},
		Env:  []string{"GATE_RUNNER_HELPER=1"},
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run returned %v, want deadline exceeded", err)
	}

	cancelled, stop := context.WithCancel(context.Background())
	stop()
	err = (OS{}).Run(cancelled, Command{Name: "go", Args: []string{"version"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v, want context.Canceled", err)
	}
}

func TestOSRunnerHelperProcess(t *testing.T) {
	if os.Getenv("GATE_RUNNER_HELPER") != "1" {
		return
	}
	time.Sleep(time.Hour)
}

func TestOSRunnerRejectsEmptyName(t *testing.T) {
	if err := (OS{}).Run(context.Background(), Command{}); err == nil {
		t.Fatal("expected empty command error")
	}
}

package devcmd

import (
	"bytes"
	"context"
	"io"
	"reflect"
	"strings"
	"testing"

	"gate/internal/devtool/platform"
	"gate/internal/devtool/runner"
	"gate/internal/ui/uitest"
)

func TestFormatGoTestOutputPreservesRowsAndAlignsPackages(t *testing.T) {
	uitest.ClearColorEnv(t)
	input := strings.Join([]string{
		"ok  \tgate/a\t1.23s\tcoverage: 50.0% of statements",
		"?   \tgate/longer/package\t[no test files]",
		"FAIL\tgate/c\t0.10s",
		"raw failure detail",
	}, "\n")
	var destination bytes.Buffer
	output := formatGoTestOutput(input, &destination)
	for _, fragment := range []string{
		"ok   gate/a",
		"coverage: 50.0% of statements",
		"?    gate/longer/package",
		"FAIL gate/c",
		"raw failure detail\n",
	} {
		if !strings.Contains(output, fragment) {
			t.Fatalf("formatted output missing %q:\n%s", fragment, output)
		}
	}
}

func TestGoTestPreservesFailureExitAndOutput(t *testing.T) {
	fake := &fakeRunner{
		run: func(command runner.Command) error {
			_, _ = io.WriteString(command.Stdout, "FAIL\tgate/example\t0.12s\nfailure detail\n")
			return exitError(7)
		},
	}
	service, out, errOut := newTestService(fake, platform.Darwin{})
	if code := service.Run(context.Background(), []string{"cover", "-count=1"}); code != 7 {
		t.Fatalf("Run = %d", code)
	}
	if !strings.Contains(out.String(), "FAIL gate/example") || !strings.Contains(out.String(), "failure detail") {
		t.Fatalf("stdout = %q", out.String())
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q", errOut.String())
	}
	wantArgs := []string{"test", "-race", "-count=1", "-cover", "./..."}
	if got := fake.commands[0].Args; !reflect.DeepEqual(got, wantArgs) {
		t.Fatalf("args = %v, want %v", got, wantArgs)
	}
}

func TestGoTestPreservesStderrStream(t *testing.T) {
	fake := &fakeRunner{
		run: func(command runner.Command) error {
			_, _ = io.WriteString(command.Stderr, "toolchain error\n")
			return exitError(2)
		},
	}
	service, out, errOut := newTestService(fake, platform.Darwin{})
	if code := service.Run(context.Background(), []string{"test"}); code != 2 {
		t.Fatalf("Run = %d", code)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q", out.String())
	}
	if got := errOut.String(); got != "toolchain error\n" {
		t.Fatalf("stderr = %q", got)
	}
}

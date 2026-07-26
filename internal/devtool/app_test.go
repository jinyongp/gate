package devtool

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestAppHelp(t *testing.T) {
	for _, args := range [][]string{nil, {"help"}, {"--help"}} {
		var out, errOut bytes.Buffer
		app := New(strings.NewReader(""), &out, &errOut)
		if code := app.Run(context.Background(), args); code != 0 {
			t.Fatalf("Run(%v) = %d", args, code)
		}
		if !strings.Contains(out.String(), "gate-dev") || !strings.Contains(out.String(), "commands:") {
			t.Fatalf("help output = %q", out.String())
		}
		if errOut.Len() != 0 {
			t.Fatalf("stderr = %q", errOut.String())
		}
	}
}

func TestAppRejectsUnknownCommand(t *testing.T) {
	var out, errOut bytes.Buffer
	app := New(strings.NewReader(""), &out, &errOut)
	if code := app.Run(context.Background(), []string{"unknown"}); code != 2 {
		t.Fatalf("Run = %d", code)
	}
	if !strings.Contains(errOut.String(), `unknown gate-dev command "unknown"`) {
		t.Fatalf("stderr = %q", errOut.String())
	}
}

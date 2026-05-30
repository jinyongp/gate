package port

import (
	"bytes"
	"testing"
)

func TestExecInjectsPort(t *testing.T) {
	var out, errb bytes.Buffer
	code := Exec(4321, "sh", []string{"-c", `printf %s "$PORT"`}, nil, &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d, stderr=%q", code, errb.String())
	}
	if out.String() != "4321" {
		t.Fatalf("PORT not injected: stdout=%q", out.String())
	}
}

func TestExecPropagatesExitCode(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Exec(1, "sh", []string{"-c", "exit 7"}, nil, &out, &errb); code != 7 {
		t.Fatalf("exit = %d, want 7", code)
	}
}

func TestExecMissingBinary(t *testing.T) {
	var out, errb bytes.Buffer
	code := Exec(1, "prx-no-such-binary-xyz", nil, nil, &out, &errb)
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if errb.Len() == 0 {
		t.Fatal("expected error on stderr")
	}
}

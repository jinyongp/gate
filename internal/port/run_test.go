package port

import (
	"bytes"
	"testing"
)

func TestExecInjectsPort(t *testing.T) {
	var out, errb bytes.Buffer
	code := Exec(4321, nil, "sh", []string{"-c", `printf %s "$PORT"`}, nil, &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d, stderr=%q", code, errb.String())
	}
	if out.String() != "4321" {
		t.Fatalf("PORT not injected: stdout=%q", out.String())
	}
}

func TestExecPropagatesExitCode(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Exec(1, nil, "sh", []string{"-c", "exit 7"}, nil, &out, &errb); code != 7 {
		t.Fatalf("exit = %d, want 7", code)
	}
}

func TestExecMapsSignalExitCode(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Exec(1, nil, "sh", []string{"-c", "kill -TERM $$"}, nil, &out, &errb); code != 143 {
		t.Fatalf("exit = %d, want 143", code)
	}
}

func TestExecMissingBinary(t *testing.T) {
	var out, errb bytes.Buffer
	code := Exec(1, nil, "gate-no-such-binary-xyz", nil, nil, &out, &errb)
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if errb.Len() == 0 {
		t.Fatal("expected error on stderr")
	}
}

func TestExecInjectsExtraEnvAndOverwritesProcessEnv(t *testing.T) {
	t.Setenv("API_URL", "wrong")
	var out, errb bytes.Buffer
	code := Exec(4321, []string{"API_URL=http://127.0.0.1:3001"}, "sh", []string{"-c", `printf %s "$API_URL"`}, nil, &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d, stderr=%q", code, errb.String())
	}
	if out.String() != "http://127.0.0.1:3001" {
		t.Fatalf("API_URL not injected: stdout=%q", out.String())
	}
}

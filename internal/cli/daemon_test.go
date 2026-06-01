package cli

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"prx/internal/daemon"
	"strings"
	"testing"
)

func TestDaemonStartReportsChildStderr(t *testing.T) {
	isolate(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	oldNewDaemonServeCommand := newDaemonServeCommand
	t.Cleanup(func() { newDaemonServeCommand = oldNewDaemonServeCommand })
	newDaemonServeCommand = func(_, _, _ string) *exec.Cmd {
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		//nolint:gosec // G204: test launches this same test binary as a helper process.
		cmd := exec.Command(exe, "-test.run=TestDaemonStartHelperProcess", "--", "__serve")
		cmd.Env = append(os.Environ(), "PRX_TEST_DAEMON_START_HELPER=1")
		return cmd
	}

	var out, errb bytes.Buffer
	code := daemonStart(nil, &out, &errb)
	if code != ExitPerm {
		t.Fatalf("daemonStart exit = %d, want %d; stderr=%s", code, ExitPerm, errb.String())
	}
	if got := errb.String(); !strings.Contains(got, "listen tcp :443: bind: permission denied") {
		t.Fatalf("stderr missing child failure: %q", got)
	}
	if strings.Contains(errb.String(), "exit status") {
		t.Fatalf("stderr should prefer child failure over wait status: %q", errb.String())
	}
}

func TestDaemonStartAddressInUseExitConflict(t *testing.T) {
	code := daemonStartExitCode("listen tcp :443: bind: address already in use")
	if code != ExitConflict {
		t.Fatalf("exit = %d, want %d", code, ExitConflict)
	}
}

func TestDaemonListenMatches(t *testing.T) {
	if !daemonListenMatches(daemon.Status{HTTPSAddr: ":443", HTTPAddr: ":80"}, ":443", ":80") {
		t.Fatal("matching listen addresses reported as mismatch")
	}
	if daemonListenMatches(daemon.Status{HTTPSAddr: ":18443", HTTPAddr: ":18080"}, ":443", ":80") {
		t.Fatal("mismatched listen addresses reported as match")
	}
	if !daemonListenMatches(daemon.Status{HTTPSAddr: "[::]:49152", HTTPAddr: "[::]:49153"}, ":0", ":0") {
		t.Fatal("requested :0 should match any running listen address")
	}
	if !daemonListenMatches(daemon.Status{HTTPSAddr: "[::]:443", HTTPAddr: "[::]:80"}, ":443", ":80") {
		t.Fatal("wildcard listen addresses should match port-only listen addresses")
	}
}

func TestDaemonStartHelperProcess(t *testing.T) {
	if os.Getenv("PRX_TEST_DAEMON_START_HELPER") != "1" {
		return
	}
	fmt.Fprintln(os.Stderr, "prx: listen tcp :443: bind: permission denied")
	os.Exit(1)
}

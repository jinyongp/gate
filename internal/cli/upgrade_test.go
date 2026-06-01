package cli

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"prx/internal/daemon"
)

func TestCompleteUpgradeRestartsRunningDaemon(t *testing.T) {
	oldRestart := restartDaemonAfterUpgradeFunc
	t.Cleanup(func() { restartDaemonAfterUpgradeFunc = oldRestart })

	var restarted daemon.Status
	restartDaemonAfterUpgradeFunc = func(st daemon.Status, _ io.Writer, _ io.Writer) int {
		restarted = st
		return ExitOK
	}

	var out, errb bytes.Buffer
	before := daemon.Status{PID: 123, HTTPSAddr: "[::]:443", HTTPAddr: "[::]:80"}
	code := completeUpgrade(&out, &errb, before, true)
	if code != ExitOK {
		t.Fatalf("completeUpgrade exit = %d, stderr=%s", code, errb.String())
	}
	if restarted != before {
		t.Fatalf("restart status = %+v, want %+v", restarted, before)
	}
	if !strings.Contains(out.String(), "upgrade complete") {
		t.Fatalf("stdout missing completion: %q", out.String())
	}
}

func TestCompleteUpgradeSkipsRestartWhenDaemonWasStopped(t *testing.T) {
	oldRestart := restartDaemonAfterUpgradeFunc
	t.Cleanup(func() { restartDaemonAfterUpgradeFunc = oldRestart })

	restartDaemonAfterUpgradeFunc = func(daemon.Status, io.Writer, io.Writer) int {
		t.Fatal("restart should not be called")
		return ExitError
	}

	var out, errb bytes.Buffer
	code := completeUpgrade(&out, &errb, daemon.Status{}, false)
	if code != ExitOK {
		t.Fatalf("completeUpgrade exit = %d, stderr=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "upgrade complete") {
		t.Fatalf("stdout missing completion: %q", out.String())
	}
}

func TestCompleteUpToDateRefreshesRunningDaemon(t *testing.T) {
	oldRestart := restartDaemonAfterUpgradeFunc
	t.Cleanup(func() { restartDaemonAfterUpgradeFunc = oldRestart })

	restarted := false
	restartDaemonAfterUpgradeFunc = func(st daemon.Status, _ io.Writer, _ io.Writer) int {
		restarted = true
		if st.PID != 456 {
			t.Fatalf("restart status = %+v", st)
		}
		return ExitOK
	}

	var out, errb bytes.Buffer
	code := completeUpToDate(&out, &errb, "v1.2.3", daemon.Status{PID: 456}, true)
	if code != ExitOK {
		t.Fatalf("completeUpToDate exit = %d, stderr=%s", code, errb.String())
	}
	if !restarted {
		t.Fatal("restart was not called")
	}
	if !strings.Contains(out.String(), "up to date (v1.2.3)") {
		t.Fatalf("stdout missing up-to-date status: %q", out.String())
	}
}

func TestRestartListenAddrFallsBackWhenStatusIsMissingAddr(t *testing.T) {
	if got := restartListenAddr("", ":443"); got != ":443" {
		t.Fatalf("empty addr = %q, want fallback", got)
	}
	if got := restartListenAddr("[::]:18443", ":443"); got != "[::]:18443" {
		t.Fatalf("addr = %q, want original", got)
	}
}

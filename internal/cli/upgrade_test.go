package cli

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"gate/internal/daemon"
	"gate/internal/proxy"
	"gate/internal/registry"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestCompleteUpgradeRestartsRunningDaemon(t *testing.T) {
	oldRestart := restartDaemonAfterUpgradeFunc
	t.Cleanup(func() { restartDaemonAfterUpgradeFunc = oldRestart })

	var restarted []daemon.Status
	restartDaemonAfterUpgradeFunc = func(st daemon.Status, _ io.Writer, _ io.Writer) int {
		restarted = append(restarted, st)
		return ExitOK
	}

	var out, errb bytes.Buffer
	before := []daemon.Status{
		{Scope: "global", PID: 123, HTTPSAddr: "[::]:443", HTTPAddr: "[::]:80"},
		{Scope: "project:demo", PID: 124, HTTPSAddr: "[::]:18443", HTTPAddr: "[::]:18080"},
	}
	code := completeUpgrade(&out, &errb, before)
	if code != ExitOK {
		t.Fatalf("completeUpgrade exit = %d, stderr=%s", code, errb.String())
	}
	if len(restarted) != len(before) || restarted[0] != before[0] || restarted[1] != before[1] {
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
	code := completeUpgrade(&out, &errb, nil)
	if code != ExitOK {
		t.Fatalf("completeUpgrade exit = %d, stderr=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "upgrade complete") {
		t.Fatalf("stdout missing completion: %q", out.String())
	}
}

func TestCompleteUpgradeAttemptsAllRestartsAfterFailure(t *testing.T) {
	oldRestart := restartDaemonAfterUpgradeFunc
	t.Cleanup(func() { restartDaemonAfterUpgradeFunc = oldRestart })

	var restarted []int
	restartDaemonAfterUpgradeFunc = func(st daemon.Status, _ io.Writer, _ io.Writer) int {
		restarted = append(restarted, st.PID)
		if st.PID == 123 {
			return ExitError
		}
		return ExitOK
	}

	var out, errb bytes.Buffer
	code := completeUpgrade(&out, &errb, []daemon.Status{{PID: 123}, {PID: 456}})
	if code != ExitError {
		t.Fatalf("completeUpgrade exit = %d, want first failure", code)
	}
	if len(restarted) != 2 || restarted[0] != 123 || restarted[1] != 456 {
		t.Fatalf("restarted = %v", restarted)
	}
	if strings.Contains(out.String(), "upgrade complete") {
		t.Fatalf("success printed after partial failure: %q", out.String())
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
	code := completeUpToDate(&out, &errb, "v1.2.3", []daemon.Status{{PID: 456}})
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

func TestScopeFromDaemonStatusPreservesScopeKey(t *testing.T) {
	st := daemon.Status{Scope: "project:state-only", ScopeKey: "project-state-only-deadbeef"}
	scope := scopeFromDaemonStatus(st)
	if got, want := scope.fileKey(), st.ScopeKey; got != want {
		t.Fatalf("fileKey = %q, want %q", got, want)
	}
	if got, want := scope.String(), st.Scope; got != want {
		t.Fatalf("scope = %q, want %q", got, want)
	}
}

func TestRestartDaemonAfterUpgradeReloadsScopedRoutes(t *testing.T) {
	isolate(t)
	shortConfigDir, err := os.MkdirTemp("/tmp", "gate-cli-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(shortConfigDir) })
	t.Setenv("XDG_CONFIG_HOME", shortConfigDir)
	t.Setenv("XDG_STATE_HOME", shortConfigDir)
	scope := projectDaemonScope("demo")
	if err := registryStore().Update(func(r *registry.Registry) error {
		return r.Reserve(registry.Reservation{Project: "demo", Service: "web", Domain: "web.localhost", Port: 4300, Active: true})
	}); err != nil {
		t.Fatal(err)
	}
	oldNewDaemonServeCommand := newDaemonServeCommand
	oldSetRoutes := setDaemonRoutesFunc
	t.Cleanup(func() {
		newDaemonServeCommand = oldNewDaemonServeCommand
		setDaemonRoutesFunc = oldSetRoutes
	})

	oldCmd := startHelperAdminDaemon(t, scope)
	newDaemonServeCommand = func(_, socketPath, _, _ string) *exec.Cmd {
		return helperAdminCommand(t, socketPath)
	}
	var reloadedScope daemonScope
	var reloadedRoutes []proxy.Route
	setDaemonRoutesFunc = func(scope daemonScope, routes []proxy.Route) error {
		reloadedScope = scope
		reloadedRoutes = append([]proxy.Route{}, routes...)
		return setDaemonRoutes(scope, routes)
	}

	var out, errb bytes.Buffer
	st := daemon.Status{Scope: scope.String(), ScopeKey: scope.fileKey(), PID: oldCmd.Process.Pid, HTTPSAddr: "[::]:18443", HTTPAddr: "[::]:18080"}
	if code := restartDaemonAfterUpgrade(st, &out, &errb); code != ExitOK {
		t.Fatalf("restartDaemonAfterUpgrade exit = %d, stderr=%s", code, errb.String())
	}
	if reloadedScope.fileKey() != scope.fileKey() {
		t.Fatalf("reload scope = %+v, want %+v", reloadedScope, scope)
	}
	if len(reloadedRoutes) != 1 || reloadedRoutes[0].Domain != "web.localhost" {
		t.Fatalf("reloaded routes = %+v", reloadedRoutes)
	}
	newStatus, err := daemonClientFor(scope).Status()
	if err != nil {
		t.Fatalf("new daemon status: %v", err)
	}
	if newStatus.PID == oldCmd.Process.Pid {
		t.Fatal("daemon pid did not change after restart")
	}
	_ = stopDaemonProcess(daemonClientFor(scope), newStatus.PID, 2*time.Second)
}

func TestPrepareUpgradeScriptStopsActivityBeforeInstallerHandoff(t *testing.T) {
	events := recordActivities(t)
	oldClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = oldClient })
	http.DefaultClient = &http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Body:       io.NopCloser(strings.NewReader("echo upgraded\n")),
			}, nil
		}),
	}

	var errb bytes.Buffer
	path, err := prepareUpgradeScript(context.Background(), &errb)
	if err != nil {
		t.Fatalf("prepareUpgradeScript: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	if got := lastEvent(*events); got != "stop:downloading installer" {
		t.Fatalf("installer handoff happened before activity stopped; events=%v", *events)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("script missing: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("script is not executable: %v", info.Mode().Perm())
	}
}

func TestConfirmUpgradeFallbackNormalizesAnswers(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{input: "\n", want: true},
		{input: "y\n", want: true},
		{input: "yes\n", want: true},
		{input: "n\n", want: false},
		{input: "no\n", want: false},
	}
	for _, tc := range cases {
		var out bytes.Buffer
		got, err := confirmUpgradePrompt(bufio.NewReader(strings.NewReader(tc.input)), &out, "dev", "v1.0.0")
		if err != nil {
			t.Fatalf("confirmUpgradePrompt(%q): %v", tc.input, err)
		}
		if got != tc.want {
			t.Fatalf("confirmUpgradePrompt(%q) = %v, want %v", tc.input, got, tc.want)
		}
		if strings.Contains(out.String(), "[Y/n]") {
			t.Fatalf("upgrade prompt used legacy y/n prompt: %q", out.String())
		}
		if !strings.Contains(out.String(), "A newer gate release is available.") {
			t.Fatalf("missing upgrade explanation: %q", out.String())
		}
		if !strings.Contains(out.String(), "Upgrade now?") {
			t.Fatalf("missing upgrade question: %q", out.String())
		}
	}
}

func TestConfirmUpgradeFallbackExplainsInvalidAnswer(t *testing.T) {
	var out bytes.Buffer
	got, err := confirmUpgradePrompt(bufio.NewReader(strings.NewReader("later\nno\n")), &out, "v1.1.3", "v1.2.2")
	if err != nil {
		t.Fatalf("confirmUpgradePrompt: %v", err)
	}
	if got {
		t.Fatal("invalid answer followed by no should decline upgrade")
	}
	if !strings.Contains(out.String(), "Current version: v1.1.3") {
		t.Fatalf("missing current version explanation: %q", out.String())
	}
	if !strings.Contains(out.String(), "Latest version: v1.2.2") {
		t.Fatalf("missing latest version explanation: %q", out.String())
	}
	if !strings.Contains(out.String(), "type yes to upgrade, or no to cancel") {
		t.Fatalf("missing invalid answer guidance: %q", out.String())
	}
}

func TestConfirmUpgradeRichPromptIsStyled(t *testing.T) {
	t.Setenv("FORCE_COLOR", "1")
	t.Setenv("NO_COLOR", "")
	t.Setenv("CLICOLOR", "")
	t.Setenv("CLICOLOR_FORCE", "")

	var out bytes.Buffer
	got, err := confirmUpgradePrompt(bufio.NewReader(strings.NewReader("yes\n")), &out, "v1.1.3", "v1.2.2")
	if err != nil {
		t.Fatalf("confirmUpgradePrompt: %v", err)
	}
	if !got {
		t.Fatal("yes should confirm upgrade")
	}
	if !strings.Contains(out.String(), "Upgrade available") {
		t.Fatalf("missing rich upgrade heading: %q", out.String())
	}
	if !strings.Contains(out.String(), "current  v1.1.3") {
		t.Fatalf("missing rich current version row: %q", out.String())
	}
	if !strings.Contains(out.String(), "latest   v1.2.2") {
		t.Fatalf("missing rich latest version row: %q", out.String())
	}
}

func TestConfirmUpgradeFallbackEOFDeclines(t *testing.T) {
	var out bytes.Buffer
	got, err := confirmUpgradePrompt(bufio.NewReader(strings.NewReader("")), &out, "dev", "v1.0.0")
	if err == nil {
		t.Fatal("confirmUpgradePrompt should report EOF")
	}
	if got {
		t.Fatal("EOF should decline upgrade")
	}
}

func TestPrintUpgradeCancelledUsesFailureMarker(t *testing.T) {
	var out bytes.Buffer
	printUpgradeCancelled(&out)
	got := out.String()
	if !strings.Contains(got, "✗ upgrade cancelled") {
		t.Fatalf("cancel output = %q", got)
	}
	if strings.Contains(got, "✓") {
		t.Fatalf("cancel output used success marker: %q", got)
	}
}

func startHelperAdminDaemon(t *testing.T, scope daemonScope) *exec.Cmd {
	t.Helper()
	var stderr bytes.Buffer
	cmd := helperAdminCommand(t, scope.socketPath())
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Signal(os.Interrupt)
			_, _ = cmd.Process.Wait()
		}
	})
	client := daemonClientFor(scope)
	for i := 0; i < 100; i++ {
		if _, err := client.Status(); err == nil {
			return cmd
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("helper admin daemon did not start; pid=%d stderr=%q", cmd.Process.Pid, stderr.String())
	return nil
}

func helperAdminCommand(t *testing.T, socketPath string) *exec.Cmd {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	//nolint:gosec // G204: test launches this same test binary as a helper process.
	cmd := exec.Command(exe, "-test.run=TestDaemonStartHelperProcess", "--", "__serve")
	cmd.Env = append(os.Environ(),
		"GATE_TEST_DAEMON_START_HELPER=serve-admin",
		"GATE_TEST_DAEMON_SOCKET="+socketPath,
		"GATE_TEST_DAEMON_READY_PID="+strconv.Itoa(os.Getpid()),
	)
	return cmd
}

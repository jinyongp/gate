package cli

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"gate/internal/daemon"
	"gate/internal/listener"
	"gate/internal/proxy"
	"gate/internal/registry"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func stubDoctorAfterUpgrade(t *testing.T, report doctorReport) {
	t.Helper()
	oldDoctor := doctorAfterUpgradeFunc
	t.Cleanup(func() { doctorAfterUpgradeFunc = oldDoctor })
	doctorAfterUpgradeFunc = func() doctorReport {
		return report
	}
}

func TestCompleteUpgradeRestartsRunningDaemon(t *testing.T) {
	stubDoctorAfterUpgrade(t, doctorReport{OK: true})
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
	if !strings.Contains(out.String(), "doctor") || !strings.Contains(out.String(), "no issues found") {
		t.Fatalf("stdout missing doctor report: %q", out.String())
	}
}

func TestCompleteUpgradeSkipsRestartWhenDaemonWasStopped(t *testing.T) {
	stubDoctorAfterUpgrade(t, doctorReport{OK: true})
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

func TestCompleteUpgradePrintsDoctorIssuesWithoutFailingUpgrade(t *testing.T) {
	stubDoctorAfterUpgrade(t, doctorReport{Issues: []doctorIssue{{
		Code:    "old_scoped_daemon_files",
		Message: "1 old scoped daemon file(s) found",
		Paths:   []string{"/tmp/gate/project-demo.pid"},
	}}})
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
	got := out.String()
	if !strings.Contains(got, "upgrade complete") || !strings.Contains(got, "doctor") {
		t.Fatalf("stdout missing upgrade or doctor status: %q", got)
	}
	if !strings.Contains(got, "issue old_scoped_daemon_files") {
		t.Fatalf("stdout missing doctor issue: %q", got)
	}
	if !strings.Contains(got, "fix: gate doctor --fix") {
		t.Fatalf("stdout missing doctor fix hint: %q", got)
	}
}

func TestCompleteUpgradeAttemptsAllRestartsAfterFailure(t *testing.T) {
	stubDoctorAfterUpgrade(t, doctorReport{OK: true})
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
	if strings.Contains(out.String(), "doctor") {
		t.Fatalf("doctor should not run after failed restart: %q", out.String())
	}
}

func TestRunUpgradeInstallUsesHomebrewForHomebrewInstall(t *testing.T) {
	oldExecutable := upgradeExecutablePathFunc
	oldHomebrewUpdate := upgradeHomebrewUpdateFunc
	oldHomebrewCommand := upgradeHomebrewCommandFunc
	oldVersionCommand := upgradeVersionCommandFunc
	t.Cleanup(func() {
		upgradeExecutablePathFunc = oldExecutable
		upgradeHomebrewUpdateFunc = oldHomebrewUpdate
		upgradeHomebrewCommandFunc = oldHomebrewCommand
		upgradeVersionCommandFunc = oldVersionCommand
	})

	upgradeExecutablePathFunc = func() string {
		return "/opt/homebrew/Cellar/gate/1.1.3/bin/gate"
	}
	var calls []string
	upgradeHomebrewUpdateFunc = func(context.Context) *exec.Cmd {
		calls = append(calls, "update")
		return helperUpgradeCommand(t, "brew update", 0)
	}
	upgradeHomebrewCommandFunc = func(context.Context) *exec.Cmd {
		calls = append(calls, "upgrade")
		return helperUpgradeCommand(t, "brew upgrade jinyongp/tap/gate", 0)
	}
	upgradeVersionCommandFunc = func(context.Context, string) *exec.Cmd {
		calls = append(calls, "version")
		return helperUpgradeCommand(t, "v1.1.4", 0)
	}

	var out, errb bytes.Buffer
	if err := runUpgradeInstall(context.Background(), &out, &errb, "v1.1.4"); err != nil {
		t.Fatalf("runUpgradeInstall: %v", err)
	}
	if strings.Contains(out.String(), "brew upgrade gate") || strings.Contains(errb.String(), "brew upgrade gate") {
		t.Fatalf("brew output leaked: stdout=%q stderr=%q", out.String(), errb.String())
	}
	if strings.Join(calls, ",") != "update,upgrade,version" {
		t.Fatalf("calls = %v, want update, upgrade, version", calls)
	}
}

func TestRunUpgradeInstallRejectsHomebrewNoop(t *testing.T) {
	oldExecutable := upgradeExecutablePathFunc
	oldHomebrewUpdate := upgradeHomebrewUpdateFunc
	oldHomebrewCommand := upgradeHomebrewCommandFunc
	oldVersionCommand := upgradeVersionCommandFunc
	t.Cleanup(func() {
		upgradeExecutablePathFunc = oldExecutable
		upgradeHomebrewUpdateFunc = oldHomebrewUpdate
		upgradeHomebrewCommandFunc = oldHomebrewCommand
		upgradeVersionCommandFunc = oldVersionCommand
	})

	upgradeExecutablePathFunc = func() string {
		return "/opt/homebrew/Cellar/gate/2.0.0/bin/gate"
	}
	upgradeHomebrewUpdateFunc = func(context.Context) *exec.Cmd {
		return helperUpgradeCommand(t, "brew update", 0)
	}
	upgradeHomebrewCommandFunc = func(context.Context) *exec.Cmd {
		return helperUpgradeCommand(t, "already installed", 0)
	}
	upgradeVersionCommandFunc = func(context.Context, string) *exec.Cmd {
		return helperUpgradeCommand(t, "v2.0.0", 0)
	}

	var out, errb bytes.Buffer
	err := runUpgradeInstall(context.Background(), &out, &errb, "v2.0.1")
	if err == nil {
		t.Fatal("runUpgradeInstall should reject a no-op Homebrew upgrade")
	}
	if !strings.Contains(err.Error(), "upgrade did not install v2.0.1") || !strings.Contains(err.Error(), "v2.0.0") {
		t.Fatalf("error did not explain version mismatch: %v", err)
	}
}

func TestRunUpgradeInstallStopsWhenHomebrewUpdateFails(t *testing.T) {
	oldExecutable := upgradeExecutablePathFunc
	oldHomebrewUpdate := upgradeHomebrewUpdateFunc
	oldHomebrewCommand := upgradeHomebrewCommandFunc
	oldVersionCommand := upgradeVersionCommandFunc
	t.Cleanup(func() {
		upgradeExecutablePathFunc = oldExecutable
		upgradeHomebrewUpdateFunc = oldHomebrewUpdate
		upgradeHomebrewCommandFunc = oldHomebrewCommand
		upgradeVersionCommandFunc = oldVersionCommand
	})

	upgradeExecutablePathFunc = func() string {
		return "/opt/homebrew/Cellar/gate/2.0.0/bin/gate"
	}
	upgradeHomebrewUpdateFunc = func(context.Context) *exec.Cmd {
		return helperUpgradeCommand(t, "tap fetch failed", 1)
	}
	upgradeHomebrewCommandFunc = func(context.Context) *exec.Cmd {
		t.Fatal("brew upgrade should not run after brew update fails")
		return nil
	}
	upgradeVersionCommandFunc = func(context.Context, string) *exec.Cmd {
		t.Fatal("version verification should not run after brew update fails")
		return nil
	}

	var out, errb bytes.Buffer
	err := runUpgradeInstall(context.Background(), &out, &errb, "v2.0.1")
	if err == nil {
		t.Fatal("runUpgradeInstall should fail when brew update fails")
	}
	if !strings.Contains(err.Error(), "brew update") || !strings.Contains(err.Error(), "tap fetch failed") {
		t.Fatalf("error did not include brew update failure: %v", err)
	}
}

func TestRunUpgradeInstallUsesInstallerForNonHomebrewInstall(t *testing.T) {
	oldExecutable := upgradeExecutablePathFunc
	oldHomebrewUpdate := upgradeHomebrewUpdateFunc
	oldHomebrewCommand := upgradeHomebrewCommandFunc
	oldVersionCommand := upgradeVersionCommandFunc
	oldClient := http.DefaultClient
	t.Cleanup(func() {
		upgradeExecutablePathFunc = oldExecutable
		upgradeHomebrewUpdateFunc = oldHomebrewUpdate
		upgradeHomebrewCommandFunc = oldHomebrewCommand
		upgradeVersionCommandFunc = oldVersionCommand
		http.DefaultClient = oldClient
	})

	upgradeExecutablePathFunc = func() string {
		return "/Users/me/.local/bin/gate"
	}
	upgradeHomebrewUpdateFunc = func(context.Context) *exec.Cmd {
		t.Fatal("brew update should not run for non-Homebrew install")
		return nil
	}
	upgradeHomebrewCommandFunc = func(context.Context) *exec.Cmd {
		t.Fatal("brew upgrade should not run for non-Homebrew install")
		return nil
	}
	upgradeVersionCommandFunc = func(context.Context, string) *exec.Cmd {
		return helperUpgradeCommand(t, "v1.1.4", 0)
	}
	http.DefaultClient = &http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Body:       io.NopCloser(strings.NewReader("echo installer upgrade\n")),
			}, nil
		}),
	}

	var out, errb bytes.Buffer
	if err := runUpgradeInstall(context.Background(), &out, &errb, "v1.1.4"); err != nil {
		t.Fatalf("runUpgradeInstall: %v", err)
	}
	if strings.Contains(out.String(), "installer upgrade") || strings.Contains(errb.String(), "installer upgrade") {
		t.Fatalf("installer output leaked: stdout=%q stderr=%q", out.String(), errb.String())
	}
}

func TestUpgradeInstallScriptTargetsCurrentExecutableDir(t *testing.T) {
	cmd := upgradeInstallScriptCommand(context.Background(), "/tmp/gate-upgrade.sh", "/opt/gate/bin/gate")
	got := ""
	for _, entry := range cmd.Env {
		if strings.HasPrefix(entry, "GATE_BIN_DIR=") {
			got = strings.TrimPrefix(entry, "GATE_BIN_DIR=")
			break
		}
	}
	if got != "/opt/gate/bin" {
		t.Fatalf("GATE_BIN_DIR = %q, want /opt/gate/bin", got)
	}
}

func TestRunUpgradeCommandReportsCapturedOutputOnFailure(t *testing.T) {
	var errb bytes.Buffer
	err := runUpgradeCommand(&errb, "upgrading test package", "test upgrade", helperUpgradeCommand(t, "hidden failure detail", 7))
	if err == nil {
		t.Fatal("runUpgradeCommand should fail")
	}
	if !strings.Contains(err.Error(), "test upgrade") || !strings.Contains(err.Error(), "hidden failure detail") {
		t.Fatalf("error missing action or output: %v", err)
	}
}

func TestCompleteUpToDateSkipsRestartAndDoctor(t *testing.T) {
	oldRestart := restartDaemonAfterUpgradeFunc
	oldDoctor := doctorAfterUpgradeFunc
	t.Cleanup(func() {
		restartDaemonAfterUpgradeFunc = oldRestart
		doctorAfterUpgradeFunc = oldDoctor
	})

	restartDaemonAfterUpgradeFunc = func(daemon.Status, io.Writer, io.Writer) int {
		t.Fatal("restart should not be called when already up to date")
		return ExitError
	}
	doctorAfterUpgradeFunc = func() doctorReport {
		t.Fatal("doctor should not run when already up to date")
		return doctorReport{}
	}

	var out, errb bytes.Buffer
	code := completeUpToDate(&out, "v1.2.3")
	if code != ExitOK {
		t.Fatalf("completeUpToDate exit = %d, stderr=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "up to date (v1.2.3)") {
		t.Fatalf("stdout missing up-to-date status: %q", out.String())
	}
	if strings.Contains(out.String(), "doctor") {
		t.Fatalf("stdout should not include doctor output: %q", out.String())
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
	st := daemon.Status{Scope: "listener:state-only-deadbeef", ScopeKey: "listener-state-only-deadbeef"}
	ref := listenerRefFromDaemonStatus(st)
	if got, want := ref.fileKey(), st.ScopeKey; got != want {
		t.Fatalf("fileKey = %q, want %q", got, want)
	}
	if got, want := ref.String(), st.Scope; got != want {
		t.Fatalf("ref = %q, want %q", got, want)
	}
}

func TestRestartDaemonAfterUpgradeReloadsListenerRoutes(t *testing.T) {
	isolate(t)
	shortConfigDir, err := os.MkdirTemp("/tmp", "gate-cli-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(shortConfigDir) })
	t.Setenv("XDG_CONFIG_HOME", shortConfigDir)
	t.Setenv("XDG_STATE_HOME", shortConfigDir)
	ref := listenerRefFor(listener.FromFlags("[::]:18443", "[::]:18080"))
	if err := registryStore().Update(func(r *registry.Registry) error {
		return r.Reserve(registry.Reservation{
			Project:  "demo",
			Service:  "web",
			Domain:   "web.localhost",
			Port:     4300,
			Active:   true,
			Listener: &registry.ListenerTarget{HTTPSAddr: "[::]:18443", HTTPAddr: "[::]:18080"},
		})
	}); err != nil {
		t.Fatal(err)
	}
	oldNewDaemonServeCommand := newDaemonServeCommand
	oldSetRoutes := setListenerRoutesFunc
	t.Cleanup(func() {
		newDaemonServeCommand = oldNewDaemonServeCommand
		setListenerRoutesFunc = oldSetRoutes
	})

	oldCmd := startHelperAdminDaemon(t, ref)
	newDaemonServeCommand = func(_, socketPath, _, _ string) *exec.Cmd {
		return helperAdminCommand(t, socketPath)
	}
	var reloadedRef listenerDaemonRef
	var reloadedRoutes []proxy.Route
	setListenerRoutesFunc = func(ref listenerDaemonRef, routes []proxy.Route) error {
		reloadedRef = ref
		reloadedRoutes = append([]proxy.Route{}, routes...)
		return setListenerRoutes(ref, routes)
	}

	var out, errb bytes.Buffer
	st := daemon.Status{Scope: ref.String(), ScopeKey: ref.fileKey(), PID: oldCmd.Process.Pid, HTTPSAddr: "[::]:18443", HTTPAddr: "[::]:18080"}
	if code := restartDaemonAfterUpgrade(st, &out, &errb); code != ExitOK {
		t.Fatalf("restartDaemonAfterUpgrade exit = %d, stderr=%s", code, errb.String())
	}
	if got := out.String(); strings.Contains(got, " · ") || !strings.Contains(got, "daemon restarted") || !strings.Contains(got, "https") || !strings.Contains(got, "http") {
		t.Fatalf("restart output should use daemon result fields, got:\n%s", got)
	}
	if reloadedRef.fileKey() != ref.fileKey() {
		t.Fatalf("reload ref = %+v, want %+v", reloadedRef, ref)
	}
	if len(reloadedRoutes) != 1 || reloadedRoutes[0].Domain != "web.localhost" {
		t.Fatalf("reloaded routes = %+v", reloadedRoutes)
	}
	newStatus, err := daemonClientForRef(ref).Status()
	if err != nil {
		t.Fatalf("new daemon status: %v", err)
	}
	if newStatus.PID == oldCmd.Process.Pid {
		t.Fatal("daemon pid did not change after restart")
	}
	_ = stopDaemonProcess(daemonClientForRef(ref), newStatus.PID, 2*time.Second)
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
	if got := lastEvent(*events); got != "complete:downloading installer" {
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

func TestConfirmUpgradeFallbackAcceptsChoices(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{input: "\n", want: true},
		{input: "yes\n", want: true},
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
	if !strings.Contains(out.String(), "Choose one of: yes, no") {
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
	if !strings.Contains(out.String(), "! upgrade available") {
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

func startHelperAdminDaemon(t *testing.T, ref daemonStateRef) *exec.Cmd {
	t.Helper()
	var stderr bytes.Buffer
	cmd := helperAdminCommand(t, ref.socketPath())
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
	client := daemonClientForRef(ref)
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

func helperUpgradeCommand(t *testing.T, output string, code int) *exec.Cmd {
	t.Helper()
	//nolint:gosec // G204: test launches this same test binary as a helper process.
	cmd := exec.Command(os.Args[0], "-test.run=TestUpgradeHelperProcess", "--", output, strconv.Itoa(code))
	cmd.Env = append(os.Environ(), "GATE_HELPER_UPGRADE=1")
	return cmd
}

func TestUpgradeHelperProcess(t *testing.T) {
	if os.Getenv("GATE_HELPER_UPGRADE") != "1" {
		return
	}
	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	if len(args) != 3 {
		fmt.Fprintln(os.Stderr, "bad helper args")
		os.Exit(2)
	}
	fmt.Fprintln(os.Stdout, args[1])
	code, err := strconv.Atoi(args[2])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(code)
}

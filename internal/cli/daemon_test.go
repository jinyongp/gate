package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"gate/internal/daemon"
	"gate/internal/listener"
	"gate/internal/proxy"
	"gate/internal/registry"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestDaemonStartReportsChildStderr(t *testing.T) {
	isolate(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	oldNewDaemonServeCommand := newDaemonServeCommand
	t.Cleanup(func() { newDaemonServeCommand = oldNewDaemonServeCommand })
	newDaemonServeCommand = func(_, _, _, _ string) *exec.Cmd {
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		//nolint:gosec // G204: test launches this same test binary as a helper process.
		cmd := exec.Command(exe, "-test.run=TestDaemonStartHelperProcess", "--", "__serve")
		cmd.Env = append(os.Environ(), "GATE_TEST_DAEMON_START_HELPER=1")
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
	assertNoIndicatorBytes(t, "daemon start failure stderr", errb.String())
	if strings.Contains(errb.String(), "exit status") {
		t.Fatalf("stderr should prefer child failure over wait status: %q", errb.String())
	}
}

func TestDaemonStartCleansUpStartedDaemonWhenRouteReloadFails(t *testing.T) {
	isolate(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	oldNewDaemonServeCommand := newDaemonServeCommand
	oldSetRoutes := setListenerRoutesFunc
	t.Cleanup(func() {
		newDaemonServeCommand = oldNewDaemonServeCommand
		setListenerRoutesFunc = oldSetRoutes
	})
	setListenerRoutesFunc = func(_ listenerDaemonRef, _ []proxy.Route) error {
		return errors.New("reload failed")
	}
	newDaemonServeCommand = func(_, socketPath, _, _ string) *exec.Cmd {
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		//nolint:gosec // G204: test launches this same test binary as a helper process.
		cmd := exec.Command(exe, "-test.run=TestDaemonStartHelperProcess", "--", "__serve")
		cmd.Env = append(os.Environ(), "GATE_TEST_DAEMON_START_HELPER=serve-admin", "GATE_TEST_DAEMON_SOCKET="+socketPath)
		return cmd
	}

	var out, errb bytes.Buffer
	code := daemonStart([]string{"--https-addr", "127.0.0.1:0", "--http-addr", "127.0.0.1:0"}, &out, &errb)
	if code != ExitError {
		t.Fatalf("daemonStart exit = %d, want reload failure; stderr=%s", code, errb.String())
	}
	ref := listenerRefFor(listener.FromFlags("127.0.0.1:0", "127.0.0.1:0"))
	if _, err := os.Stat(ref.pidPath()); !os.IsNotExist(err) {
		t.Fatalf("pid file still exists or stat failed: %v", err)
	}
	client := daemonClientForRef(ref)
	for i := 0; i < 50 && client.IsRunning(); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if client.IsRunning() {
		t.Fatal("started daemon still running after reload failure")
	}
}

func TestDaemonStartReplacesOldScopedDaemon(t *testing.T) {
	isolate(t)
	shortConfigDir, err := os.MkdirTemp("/tmp", "gate-cli-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(shortConfigDir) })
	t.Setenv("XDG_CONFIG_HOME", shortConfigDir)
	t.Setenv("XDG_STATE_HOME", shortConfigDir)

	oldSrv := proxy.New(nil, nil)
	stopOld, err := daemon.ServeAdmin(context.Background(), globalDaemonScope().socketPath(), oldSrv)
	if err != nil {
		t.Fatal(err)
	}
	defer stopOld()

	oldNewDaemonServeCommand := newDaemonServeCommand
	t.Cleanup(func() { newDaemonServeCommand = oldNewDaemonServeCommand })
	newDaemonServeCommand = func(_, socketPath, _, _ string) *exec.Cmd {
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		//nolint:gosec // G204: test launches this same test binary as a helper process.
		cmd := exec.Command(exe, "-test.run=TestDaemonStartHelperProcess", "--", "__serve")
		cmd.Env = append(os.Environ(), "GATE_TEST_DAEMON_START_HELPER=serve-admin", "GATE_TEST_DAEMON_SOCKET="+socketPath)
		return cmd
	}

	var out, errb bytes.Buffer
	if code := daemonStart([]string{"--https-addr", "127.0.0.1:0", "--http-addr", "127.0.0.1:0"}, &out, &errb); code != ExitOK {
		t.Fatalf("daemonStart exit = %d, stderr=%s", code, errb.String())
	}
	if daemonClientFor(globalDaemonScope()).IsRunning() {
		t.Fatal("old scoped daemon still running")
	}
	ref := listenerRefFor(listener.FromFlags("127.0.0.1:0", "127.0.0.1:0"))
	st, err := daemonClientForRef(ref).Status()
	if err != nil {
		t.Fatalf("listener status: %v", err)
	}
	_ = stopDaemonProcess(daemonClientForRef(ref), st.PID, 2*time.Second)
}

func TestDaemonStatusAllJSONIncludesKnownScopes(t *testing.T) {
	isolate(t)
	shortConfigDir, err := os.MkdirTemp("/tmp", "gate-cli-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(shortConfigDir) })
	t.Setenv("XDG_CONFIG_HOME", shortConfigDir)
	t.Setenv("XDG_STATE_HOME", shortConfigDir)
	if err := registryStore().Update(func(r *registry.Registry) error {
		return r.Reserve(registry.Reservation{Project: "demo", Service: "web", Domain: "web.localhost", Port: 4300})
	}); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	if code := daemonStatus([]string{"--all", "--json"}, &out, &errb); code != ExitOK {
		t.Fatalf("daemonStatus exit = %d, stderr=%s", code, errb.String())
	}
	var got []daemon.Status
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("status json: %v\n%s", err, out.String())
	}
	scopes := map[string]bool{}
	for _, st := range got {
		scopes[st.Scope] = true
	}
	for _, want := range []string{defaultListenerRef().String()} {
		if !scopes[want] {
			t.Fatalf("statuses = %+v, missing %q", got, want)
		}
	}
}

func TestDaemonStatusSingleJSONIsObject(t *testing.T) {
	isolate(t)
	var out, errb bytes.Buffer
	if code := daemonStatus([]string{"--json"}, &out, &errb); code != ExitOK {
		t.Fatalf("daemonStatus exit = %d, stderr=%s", code, errb.String())
	}
	var got daemon.Status
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("status json should be object: %v\n%s", err, out.String())
	}
	if got.Scope != defaultListenerRef().String() || got.Running {
		t.Fatalf("status = %+v", got)
	}
}

func TestDaemonSubcommandHelpShowsScopeFlags(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{name: "status", args: []string{"status", "-h"}, want: []string{"--json", "-a, --all"}},
		{name: "logs", args: []string{"logs", "-h"}, want: []string{"-a, --all"}},
		{name: "start", args: []string{"start", "-h"}, want: []string{}},
		{name: "restart", args: []string{"restart", "-h"}, want: []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			if code := Daemon(tc.args, &out, &errb); code != ExitOK {
				t.Fatalf("Daemon help exit = %d, stderr=%s", code, errb.String())
			}
			s := out.String()
			for _, want := range tc.want {
				if !strings.Contains(s, want) {
					t.Fatalf("help missing %q in:\n%s", want, s)
				}
			}
		})
	}
}

func TestDaemonLogsAllSkipsMissingScopeLogs(t *testing.T) {
	isolate(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if err := registryStore().Update(func(r *registry.Registry) error {
		return r.Reserve(registry.Reservation{Project: "demo", Service: "web", Domain: "web.localhost", Port: 4300})
	}); err != nil {
		t.Fatal(err)
	}
	ref := defaultListenerRef()
	if err := os.MkdirAll(filepath.Dir(ref.logPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ref.logPath(), []byte("listener log\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	if code := daemonLogs([]string{"--all"}, &out, &errb); code != ExitOK {
		t.Fatalf("daemonLogs exit = %d, stderr=%s", code, errb.String())
	}
	s := out.String()
	if s != "listener log\n" {
		t.Fatalf("logs output = %q", s)
	}
}

func TestDaemonLogsDefaultSelection(t *testing.T) {
	isolate(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	ref := defaultListenerRef()
	if err := os.MkdirAll(filepath.Dir(ref.logPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ref.logPath(), []byte("listener log\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	if code := daemonLogs(nil, &out, &errb); code != ExitOK {
		t.Fatalf("daemonLogs exit = %d, stderr=%s", code, errb.String())
	}
	if out.String() != "listener log\n" {
		t.Fatalf("logs = %q", out.String())
	}
}

func TestDaemonLogsAllFailsWhenNoLogsExist(t *testing.T) {
	isolate(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	var out, errb bytes.Buffer
	if code := daemonLogs([]string{"--all"}, &out, &errb); code != ExitError {
		t.Fatalf("daemonLogs exit = %d, want error", code)
	}
	if !strings.Contains(errb.String(), "no log files found") {
		t.Fatalf("stderr = %q", errb.String())
	}
}

func TestDaemonStopPidFallbackHandlesCorruptAndNonGatePID(t *testing.T) {
	isolate(t)
	scope := globalDaemonScope()
	if err := os.MkdirAll(filepath.Dir(scope.pidPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(scope.pidPath(), []byte("not-a-pid"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := daemonStopScope(scope, &out, &errb, false); code != ExitError {
		t.Fatalf("corrupt stop exit = %d, want error", code)
	}

	errb.Reset()
	out.Reset()
	if err := os.WriteFile(scope.pidPath(), []byte(fmt.Sprint(os.Getpid())), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := daemonStopScope(scope, &out, &errb, false); code != ExitOK {
		t.Fatalf("non-gate stop exit = %d, stderr=%s", code, errb.String())
	}
	if _, err := os.Stat(scope.pidPath()); !os.IsNotExist(err) {
		t.Fatalf("non-gate pid file not removed: %v", err)
	}
	if strings.TrimSpace(out.String()) != "not running" {
		t.Fatalf("stdout = %q", out.String())
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

func TestDaemonStatusMatchesListenerKeepsHostSpecificity(t *testing.T) {
	if !daemonStatusMatchesListener(
		daemon.Status{HTTPSAddr: "127.0.0.1:443", HTTPAddr: "127.0.0.1:80"},
		listener.Pair{HTTPSAddr: "127.0.0.1:443", HTTPAddr: "127.0.0.1:80"},
	) {
		t.Fatal("same loopback listener did not match")
	}
	if daemonStatusMatchesListener(
		daemon.Status{HTTPSAddr: "127.0.0.1:443", HTTPAddr: "127.0.0.1:80"},
		listener.Pair{HTTPSAddr: ":443", HTTPAddr: ":80"},
	) {
		t.Fatal("loopback listener matched wildcard listener")
	}
	if daemonStatusMatchesListener(
		daemon.Status{HTTPSAddr: ":443", HTTPAddr: ":80"},
		listener.Pair{HTTPSAddr: "127.0.0.1:443", HTTPAddr: "127.0.0.1:80"},
	) {
		t.Fatal("wildcard listener matched loopback listener")
	}
}

func TestDaemonExplicitListenMatchesOnlyChecksSetFlags(t *testing.T) {
	st := daemon.Status{HTTPSAddr: "[::]:58393", HTTPAddr: "[::]:58394"}
	if !daemonExplicitListenMatches(st, ":443", ":80", false, false) {
		t.Fatal("implicit default listen addresses should not conflict")
	}
	if daemonExplicitListenMatches(st, ":443", ":80", true, false) {
		t.Fatal("explicit mismatched HTTPS listen address should conflict")
	}
	if !daemonExplicitListenMatches(st, ":58393", ":80", true, false) {
		t.Fatal("explicit matching HTTPS port should pass")
	}
}

func TestIsGateDaemonArgsMatchesServeWithFlags(t *testing.T) {
	cases := []string{
		"gate __serve",
		"gate __serve --socket /tmp/gate.sock",
		"/usr/local/bin/gate __serve --socket /tmp/gate.sock --https-addr :443",
		"/tmp/build/gate __serve --http-addr :80",
	}
	for _, args := range cases {
		if !isGateDaemonArgs(args) {
			t.Fatalf("args not matched: %q", args)
		}
	}
	if isGateDaemonArgs("not-gate __serve --socket /tmp/gate.sock") {
		t.Fatal("non-gate args matched")
	}
	if isGateDaemonArgs("python /tmp/gate __serve --socket /tmp/gate.sock") {
		t.Fatal("non-gate executable mentioning gate __serve matched")
	}
}

func TestRestartListenAddrsPreservesRunningDaemonPorts(t *testing.T) {
	httpsAddr, httpAddr := restartListenAddrs(
		daemon.Status{HTTPSAddr: "[::]:18443", HTTPAddr: "[::]:18080"},
		defaultDaemonHTTPSAddr,
		defaultDaemonHTTPAddr,
		false,
		false,
	)
	if httpsAddr != "[::]:18443" || httpAddr != "[::]:18080" {
		t.Fatalf("restart addrs = %q %q", httpsAddr, httpAddr)
	}
}

func TestRestartListenAddrsAllowsExplicitOverrides(t *testing.T) {
	httpsAddr, httpAddr := restartListenAddrs(
		daemon.Status{HTTPSAddr: "[::]:18443", HTTPAddr: "[::]:18080"},
		":9443",
		":9080",
		true,
		true,
	)
	if httpsAddr != ":9443" || httpAddr != ":9080" {
		t.Fatalf("restart addrs = %q %q", httpsAddr, httpAddr)
	}
}

func TestDaemonStartHelperProcess(t *testing.T) {
	switch os.Getenv("GATE_TEST_DAEMON_START_HELPER") {
	case "1":
		fmt.Fprintln(os.Stderr, "gate: listen tcp :443: bind: permission denied")
		os.Exit(1)
	case "serve-admin":
		socketPath := os.Getenv("GATE_TEST_DAEMON_SOCKET")
		if socketPath == "" {
			fmt.Fprintln(os.Stderr, "missing GATE_TEST_DAEMON_SOCKET")
			os.Exit(1)
		}
		ctx, stopSignal := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
		stopAdmin, err := daemon.ServeAdmin(ctx, socketPath, proxy.New(nil, nil))
		if err != nil {
			stopSignal()
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		<-ctx.Done()
		stopAdmin()
		stopSignal()
		return
	default:
		return
	}
}

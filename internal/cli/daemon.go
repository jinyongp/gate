package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"gate/internal/ca"
	"gate/internal/daemon"
	"gate/internal/fsutil"
	"gate/internal/listener"
	"gate/internal/paths"
	"gate/internal/proxy"
	"gate/internal/ui"
)

var newDaemonServeCommand = func(exe, socketPath, httpsAddr, httpAddr string) *exec.Cmd {
	//nolint:gosec // G204: exe is our own binary path; listen addrs are passed as argv, not a shell.
	return exec.Command(exe, "__serve", "--socket", socketPath, "--https-addr", httpsAddr, "--http-addr", httpAddr)
}

var writeDaemonPID = func(path string, pid int) error {
	return fsutil.WriteAtomic(path, []byte(strconv.Itoa(pid)), 0o600)
}

var daemonReadyTimeout = 3 * time.Second

const (
	defaultDaemonHTTPSAddr = ":443"
	defaultDaemonHTTPAddr  = ":80"
	lowPortBindErrorCode   = "low_port_bind_permission"
	lowPortBindHint        = "hint: run `gate daemon setup`, then retry; do not run gate with sudo"
)

// Daemon dispatches `gate daemon status|start|stop|restart|logs`.
func Daemon(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		sp := specFor("daemon")
		WriteHelp(stdout, "daemon", sp.Args, sp.Summary, nil)
		return ExitOK
	}
	if len(args) == 0 {
		usageLine(stderr, "daemon")
		return ExitUsage
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "status":
		return daemonStatus(rest, stdout, stderr)
	case "setup":
		return daemonSetup(rest, stdout, stderr)
	case "start":
		return daemonStart(rest, stdout, stderr)
	case "stop":
		return daemonStop(rest, stdout, stderr)
	case "restart":
		return daemonRestart(rest, stdout, stderr)
	case "logs":
		return daemonLogs(rest, stdout, stderr)
	default:
		usageLine(stderr, "daemon")
		return ExitUsage
	}
}

func daemonStatus(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit JSON")
	all := fs.Bool("all", false, "target all known listener daemons")
	fs.BoolVar(all, "a", false, "target all known listener daemons")
	if handled, code := parseNoArgFlags(fs, "daemon status", args, stdout, stderr); handled {
		return code
	}
	refs := []listenerDaemonRef{defaultListenerRef()}
	if *all {
		var err error
		refs, err = allListenerRefs()
		if err != nil {
			return fail(stderr, *jsonOut, ExitError, "listener", err.Error())
		}
	}
	statuses := make([]daemon.Status, 0, len(refs))
	for _, ref := range refs {
		statuses = append(statuses, daemonStatusForRef(ref))
	}
	if *jsonOut {
		if *all {
			return writeJSON(stdout, statuses)
		}
		if len(statuses) == 1 {
			return writeJSON(stdout, statuses[0])
		}
		return writeJSON(stdout, statuses)
	}
	printDaemonStatuses(stdout, statuses)
	return ExitOK
}

func daemonStatusForRef(ref listenerDaemonRef) daemon.Status {
	pid, pidErr := readPIDFile(ref.pidPath())
	pidAlive := pidErr == nil && processExists(pid)
	st, err := daemonClientForRef(ref).Status()
	if err != nil {
		status := "stopped"
		if pidAlive {
			status = "stale"
		}
		return daemon.Status{
			Scope:     ref.String(),
			ScopeKey:  ref.fileKey(),
			Status:    status,
			Listener:  ref.String(),
			Socket:    ref.socketPath(),
			PIDPath:   ref.pidPath(),
			PIDAlive:  pidAlive,
			PID:       pid,
			Running:   false,
			HTTPSAddr: ref.Pair.HTTPSAddr,
			HTTPAddr:  ref.Pair.HTTPAddr,
		}
	}
	st.Scope = ref.String()
	st.ScopeKey = ref.fileKey()
	st.Status = "running"
	st.Listener = ref.String()
	st.Socket = ref.socketPath()
	st.PIDPath = ref.pidPath()
	if st.PID > 0 {
		st.PIDAlive = processExists(st.PID)
	} else {
		st.PIDAlive = pidAlive
	}
	if st.HTTPSAddr == "" {
		st.HTTPSAddr = ref.Pair.HTTPSAddr
	}
	if st.HTTPAddr == "" {
		st.HTTPAddr = ref.Pair.HTTPAddr
	}
	return st
}

func printDaemonStatus(stdout io.Writer, st daemon.Status) {
	printDaemonStatuses(stdout, []daemon.Status{st})
}

func printDaemonStatuses(stdout io.Writer, statuses []daemon.Status) {
	headers := []string{"STATUS", "HTTPS", "HTTP", "PID", "UPTIME", "ROUTES"}
	rows := make([][]string, 0, len(statuses))
	for _, st := range statuses {
		rows = append(rows, daemonStatusRow(st))
	}
	if richOut(stdout, false) {
		fmt.Fprintln(stdout, ui.Render(headers, rows))
		return
	}
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, strings.Join(headers, "\t"))
	for _, row := range rows {
		fmt.Fprintln(tw, strings.Join(row, "\t"))
	}
	_ = tw.Flush()
}

func daemonStatusRow(st daemon.Status) []string {
	if !st.Running {
		status := strings.TrimSpace(st.Status)
		if status == "" {
			status = "stopped"
		}
		return []string{status, daemonStatusAddr(st.HTTPSAddr), daemonStatusAddr(st.HTTPAddr), "-", "-", "-"}
	}
	return []string{
		"running",
		daemonStatusAddr(displayListenAddr(st.HTTPSAddr)),
		daemonStatusAddr(displayListenAddr(st.HTTPAddr)),
		strconv.Itoa(st.PID),
		formatDaemonUptime(st.UptimeSec),
		strconv.Itoa(st.Routes),
	}
}

func formatDaemonUptime(seconds int64) string {
	if seconds < 0 {
		seconds = 0
	}
	d := time.Duration(seconds) * time.Second
	hours := int(d / time.Hour)
	d -= time.Duration(hours) * time.Hour
	minutes := int(d / time.Minute)
	d -= time.Duration(minutes) * time.Minute
	secs := int(d / time.Second)
	parts := make([]string, 0, 3)
	if hours > 0 {
		parts = append(parts, strconv.Itoa(hours)+"h")
	}
	if minutes > 0 || hours > 0 {
		parts = append(parts, strconv.Itoa(minutes)+"m")
	}
	parts = append(parts, strconv.Itoa(secs)+"s")
	return strings.Join(parts, " ")
}

func daemonStatusAddr(addr string) string {
	if strings.TrimSpace(addr) == "" || addr == "unknown" {
		return "-"
	}
	return addr
}

func daemonStart(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("start", flag.ContinueOnError)
	httpsAddr := fs.String("https-addr", defaultDaemonHTTPSAddr, "HTTPS listen address")
	httpAddr := fs.String("http-addr", defaultDaemonHTTPAddr, "HTTP listen address")
	if handled, code := parseNoArgFlags(fs, "daemon start", args, stdout, stderr); handled {
		return code
	}
	httpsSet, httpSet := flagSet(fs, "https-addr"), flagSet(fs, "http-addr")
	pair := listener.FromFlags(*httpsAddr, *httpAddr)
	if err := listener.Validate(pair, true); err != nil {
		return fail(stderr, false, ExitUsage, "bad_listener", err.Error())
	}
	unlock, code := acquireStateMutation(stderr, false)
	if code != ExitOK {
		return code
	}
	defer unlock()
	ref := listenerRefFor(pair)

	client := daemonClientForRef(ref)
	if st, err := client.Status(); err == nil {
		if !daemonExplicitListenMatches(st, *httpsAddr, *httpAddr, httpsSet, httpSet) {
			msg := fmt.Sprintf("daemon already running on https %s and http %s; requested https %s and http %s; run `gate daemon stop` first",
				displayListenAddr(st.HTTPSAddr), displayListenAddr(st.HTTPAddr), *httpsAddr, *httpAddr)
			return fail(stderr, false, ExitConflict, "start", msg)
		}
		if err := setListenerRoutesWithActivity(ref, stderr, false, "reloading routes"); err != nil {
			return fail(stderr, false, ExitError, "reload_failed", err.Error())
		}
		printDaemonRunResult(stdout, "daemon already running", st.PID, displayListenAddr(st.HTTPSAddr), displayListenAddr(st.HTTPAddr))
		return ExitOK
	}
	if err := replaceScopedDaemonsForListener(pair); err != nil {
		return fail(stderr, false, ExitError, "migration", err.Error())
	}
	activity := startActivity(stderr, false, "starting daemon")
	result := startDaemonCommand(newDaemonServeCommand(executablePath(), ref.socketPath(), pair.HTTPSAddr, pair.HTTPAddr), client, ref)
	if result.Code == ExitOK {
		activity.Complete()
		if err := setListenerRoutesWithActivity(ref, stderr, false, "reloading routes"); err != nil {
			cleanupStartedDaemon(client, ref, result.PID)
			return fail(stderr, false, ExitError, "reload_failed", err.Error())
		}
		st, err := client.Status()
		if err != nil {
			printDaemonRunResult(stdout, "daemon started", result.PID, pair.HTTPSAddr, pair.HTTPAddr)
			return ExitOK
		}
		printDaemonRunResult(stdout, "daemon started", result.PID, displayListenAddr(st.HTTPSAddr), displayListenAddr(st.HTTPAddr))
		return ExitOK
	}
	activity.Stop()
	return failDaemonStart(stderr, false, result, pair, ref, "start")
}

func daemonRestart(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("restart", flag.ContinueOnError)
	httpsAddr := fs.String("https-addr", defaultDaemonHTTPSAddr, "HTTPS listen address")
	httpAddr := fs.String("http-addr", defaultDaemonHTTPAddr, "HTTP listen address")
	if handled, code := parseNoArgFlags(fs, "daemon restart", args, stdout, stderr); handled {
		return code
	}
	httpsSet, httpSet := flagSet(fs, "https-addr"), flagSet(fs, "http-addr")
	pair := listener.FromFlags(*httpsAddr, *httpAddr)
	if err := listener.Validate(pair, true); err != nil {
		return fail(stderr, false, ExitUsage, "bad_listener", err.Error())
	}
	unlock, code := acquireStateMutation(stderr, false)
	if code != ExitOK {
		return code
	}
	defer unlock()
	ref := listenerRefFor(pair)
	client := daemonClientForRef(ref)
	activity := startActivity(stderr, false, "restarting daemon")
	st, running := client.Status()
	if running == nil {
		*httpsAddr, *httpAddr = restartListenAddrs(st, *httpsAddr, *httpAddr, httpsSet, httpSet)
		pair = listener.FromFlags(*httpsAddr, *httpAddr)
		ref = listenerRefFor(pair)
		client = daemonClientForRef(ref)
		if err := stopDaemonProcess(client, st.PID, 5*time.Second); err != nil {
			activity.Stop()
			return fail(stderr, false, ExitError, "restart", err.Error())
		}
	}
	if running != nil {
		if err := replaceScopedDaemonsForListener(pair); err != nil {
			activity.Stop()
			return fail(stderr, false, ExitError, "migration", err.Error())
		}
	}

	result := startDaemonCommand(newDaemonServeCommand(executablePath(), ref.socketPath(), pair.HTTPSAddr, pair.HTTPAddr), client, ref)
	if result.Code != ExitOK {
		activity.Stop()
		return failDaemonStart(stderr, false, result, pair, ref, "restart")
	}
	activity.Complete()
	if err := setListenerRoutesWithActivity(ref, stderr, false, "reloading routes"); err != nil {
		cleanupStartedDaemon(client, ref, result.PID)
		return fail(stderr, false, ExitError, "reload_failed", err.Error())
	}
	if st, err := client.Status(); err == nil {
		printDaemonRunResult(stdout, "daemon restarted", st.PID, displayListenAddr(st.HTTPSAddr), displayListenAddr(st.HTTPAddr))
		return ExitOK
	}
	printDaemonRunResult(stdout, "daemon restarted", result.PID, pair.HTTPSAddr, pair.HTTPAddr)
	return ExitOK
}

func printDaemonRunResult(stdout io.Writer, msg string, pid int, httpsAddr, httpAddr string) {
	printSuccess(stdout, msg)
	printDaemonListenAddrs(stdout, httpsAddr, httpAddr)
	printKV(stdout, "pid", strconv.Itoa(pid))
}

func printDaemonListenAddrs(stdout io.Writer, httpsAddr, httpAddr string) {
	printKV(stdout, "https", httpsAddr)
	printKV(stdout, "http", httpAddr)
}

func replaceScopedDaemonsForListener(pair listener.Pair) error {
	pair = listener.Normalize(pair)
	scopes, err := allDaemonScopes()
	if err != nil {
		return err
	}
	for _, scope := range scopes {
		client := daemonClientFor(scope)
		st, err := client.Status()
		if err != nil {
			continue
		}
		if !daemonStatusMatchesListener(st, pair) {
			continue
		}
		if st.PID != os.Getpid() {
			if err := stopDaemonProcess(client, st.PID, 5*time.Second); err != nil {
				return fmt.Errorf("stop old scoped daemon %s: %w", scope.String(), err)
			}
		}
		_ = os.Remove(scope.pidPath())
		_ = os.Remove(scope.socketPath())
	}
	return nil
}

func daemonStatusMatchesListener(st daemon.Status, pair listener.Pair) bool {
	if st.HTTPSAddr == "" && st.HTTPAddr == "" {
		return true
	}
	return daemonListenMatches(st, pair.HTTPSAddr, pair.HTTPAddr)
}

func flagSet(fs *flag.FlagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			set = true
		}
	})
	return set
}

func restartListenAddrs(st daemon.Status, httpsAddr, httpAddr string, httpsSet, httpSet bool) (string, string) {
	if !httpsSet {
		httpsAddr = restartListenAddr(st.HTTPSAddr, defaultDaemonHTTPSAddr)
	}
	if !httpSet {
		httpAddr = restartListenAddr(st.HTTPAddr, defaultDaemonHTTPAddr)
	}
	return httpsAddr, httpAddr
}

func daemonListenMatches(st daemon.Status, httpsAddr, httpAddr string) bool {
	return listenAddrMatches(st.HTTPSAddr, httpsAddr) && listenAddrMatches(st.HTTPAddr, httpAddr)
}

func daemonExplicitListenMatches(st daemon.Status, httpsAddr, httpAddr string, httpsSet, httpSet bool) bool {
	return (!httpsSet || listenAddrMatches(st.HTTPSAddr, httpsAddr)) &&
		(!httpSet || listenAddrMatches(st.HTTPAddr, httpAddr))
}

func listenAddrMatches(actual, requested string) bool {
	if actual == "" || requested == ":0" {
		if actual == "" {
			return true
		}
	}
	actual = listener.Normalize(listener.Pair{HTTPSAddr: actual, HTTPAddr: actual}).HTTPSAddr
	requested = listener.Normalize(listener.Pair{HTTPSAddr: requested, HTTPAddr: requested}).HTTPSAddr
	actualHost, actualPort, actualErr := net.SplitHostPort(actual)
	requestedHost, requestedPort, requestedErr := net.SplitHostPort(requested)
	if actualErr != nil || requestedErr != nil {
		return actual == requested
	}
	if requestedPort == "0" {
		return listenHostsMatch(actualHost, requestedHost) && actualPort != ""
	}
	return actualPort == requestedPort && listenHostsMatch(actualHost, requestedHost)
}

var lookupListenerIPs = net.LookupIP

func listenHostsMatch(actual, requested string) bool {
	actual = strings.Trim(strings.TrimSpace(actual), "[]")
	requested = strings.Trim(strings.TrimSpace(requested), "[]")
	if actual == requested {
		return true
	}
	actualIP := net.ParseIP(strings.SplitN(actual, "%", 2)[0])
	requestedIP := net.ParseIP(strings.SplitN(requested, "%", 2)[0])
	if actualIP != nil && requestedIP != nil {
		return actualIP.Equal(requestedIP)
	}
	if actualIP == nil || requested == "" {
		return false
	}
	resolved, err := lookupListenerIPs(requested)
	if err != nil {
		return false
	}
	for _, ip := range resolved {
		if actualIP.Equal(ip) {
			return true
		}
	}
	return false
}

func listenPort(addr string) string {
	if addr == "" {
		return ""
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(port)
}

func displayListenAddr(addr string) string {
	if addr == "" {
		return "unknown"
	}
	return addr
}

type daemonStartResult struct {
	Code    int
	PID     int
	Message string
}

func startDaemonCommand(cmd *exec.Cmd, client *daemon.Client, ref daemonStateRef) (result daemonStartResult) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	logFile, logOffset, err := openDaemonLog(ref)
	if err != nil {
		return daemonStartResult{Code: ExitError, Message: err.Error()}
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return daemonStartResult{Code: ExitError, Message: err.Error()}
	}
	_ = logFile.Close()

	expectedPID := cmd.Process.Pid
	waitc := make(chan error, 1)
	go func() { waitc <- cmd.Wait() }()
	cleanup := true
	defer func() {
		if cleanup {
			terminateStartedCommand(cmd.Process, waitc)
			_ = os.Remove(ref.pidPath())
		}
	}()
	deadline := time.After(daemonReadyTimeout)
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case err := <-waitc:
			cleanup = false
			if err == nil {
				err = errors.New("daemon exited before becoming ready")
			}
			msg := daemonStartErrorMessage(err, daemonLogSince(ref, logOffset))
			return daemonStartResult{Code: daemonStartExitCode(msg), Message: msg}
		case <-deadline:
			return daemonStartResult{Code: ExitError, Message: "daemon did not become ready"}
		case <-tick.C:
			if st, err := client.Status(); err == nil && st.PID == expectedPID {
				if err := os.MkdirAll(filepath.Dir(ref.pidPath()), 0o700); err != nil {
					return daemonStartResult{Code: ExitError, Message: err.Error()}
				}
				if err := writeDaemonPID(ref.pidPath(), st.PID); err != nil {
					return daemonStartResult{Code: ExitError, Message: err.Error()}
				}
				cleanup = false
				return daemonStartResult{Code: ExitOK, PID: st.PID}
			}
		}
	}
}

func terminateStartedCommand(process *os.Process, waitc <-chan error) {
	if process == nil {
		return
	}
	_ = process.Signal(syscall.SIGTERM)
	select {
	case <-waitc:
		return
	case <-time.After(500 * time.Millisecond):
	}
	_ = process.Signal(syscall.SIGKILL)
	select {
	case <-waitc:
	case <-time.After(2 * time.Second):
	}
}

func openDaemonLog(ref daemonStateRef) (*os.File, int64, error) {
	logPath := ref.logPath()
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return nil, 0, err
	}
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, 0, err
	}
	offset, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		_ = f.Close()
		return nil, 0, err
	}
	return f, offset, nil
}

func daemonLogSince(ref daemonStateRef, offset int64) string {
	f, err := os.Open(ref.logPath())
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return ""
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return ""
	}
	return string(b)
}

func daemonStop(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("stop", flag.ContinueOnError)
	all := fs.Bool("all", false, "target all known listener daemons")
	fs.BoolVar(all, "a", false, "target all known listener daemons")
	httpsAddr := fs.String("https-addr", defaultDaemonHTTPSAddr, "HTTPS listen address")
	httpAddr := fs.String("http-addr", defaultDaemonHTTPAddr, "HTTP listen address")
	if handled, code := parseNoArgFlags(fs, "daemon stop", args, stdout, stderr); handled {
		return code
	}
	pair := listener.FromFlags(*httpsAddr, *httpAddr)
	if err := listener.Validate(pair, true); err != nil {
		return fail(stderr, false, ExitUsage, "bad_listener", err.Error())
	}
	unlock, code := acquireStateMutation(stderr, false)
	if code != ExitOK {
		return code
	}
	defer unlock()
	refs := []listenerDaemonRef{listenerRefFor(pair)}
	if *all {
		var err error
		refs, err = allListenerRefs()
		if err != nil {
			return fail(stderr, false, ExitError, "listener", err.Error())
		}
	}
	for _, ref := range refs {
		if code := daemonStopRef(ref, stdout, stderr, len(refs) > 1); code != ExitOK {
			return code
		}
	}
	return ExitOK
}

func daemonStopScope(scope daemonScope, stdout, stderr io.Writer, printScope bool) int {
	return daemonStopRef(scope, stdout, stderr, printScope)
}

func daemonStopRef(ref daemonStateRef, stdout, stderr io.Writer, printScope bool) int {
	client := daemonClientForRef(ref)
	if st, err := client.Status(); err == nil {
		if err := stopDaemonProcess(client, st.PID, 2*time.Second); err != nil {
			return fail(stderr, false, ExitError, "stop", err.Error())
		}
		_ = os.Remove(ref.pidPath())
		printDaemonStop(stdout, ref, "stopped", printScope)
		return ExitOK
	}
	b, err := os.ReadFile(ref.pidPath())
	if err != nil {
		printDaemonStop(stdout, ref, "not running", printScope)
		return ExitOK
	}
	pid, err := strconv.Atoi(string(b))
	if err != nil {
		return fail(stderr, false, ExitError, "pidfile", "corrupt pid file")
	}
	if !isGateDaemonPIDForSocket(pid, ref.socketPath()) {
		_ = os.Remove(ref.pidPath())
		printDaemonStop(stdout, ref, "not running", printScope)
		return ExitOK
	}
	if err := stopDaemonProcess(client, pid, 2*time.Second); err != nil {
		return fail(stderr, false, ExitError, "stop", err.Error())
	}
	_ = os.Remove(ref.pidPath())
	printDaemonStop(stdout, ref, "stopped", printScope)
	return ExitOK
}

func cleanupStartedDaemon(client *daemon.Client, ref daemonStateRef, pid int) {
	_ = stopDaemonProcess(client, pid, 2*time.Second)
	_ = os.Remove(ref.pidPath())
}

func printDaemonStop(stdout io.Writer, ref daemonStateRef, msg string, printScope bool) {
	line := msg
	if printScope {
		line = msg + " · " + ref.String()
	}
	if msg == "stopped" {
		printSuccess(stdout, line)
	} else {
		printInfo(stdout, line)
	}
}

func stopDaemonProcess(client *daemon.Client, pid int, timeout time.Duration) error {
	authorizedBySocket := false
	if client != nil {
		status, statusErr := client.Status()
		authorizedBySocket = statusErr == nil && status.PID == pid
		if !authorizedBySocket && !isGateDaemonPIDForSocket(pid, client.SocketPath()) {
			return fmt.Errorf("refusing to signal pid %d: it is not the gate daemon for %s", pid, client.SocketPath())
		}
	}
	if authorizedBySocket {
		if err := client.Shutdown(); err != nil {
			return fmt.Errorf("request daemon shutdown through %s: %w", client.SocketPath(), err)
		}
		deadline := time.NewTimer(timeout)
		defer deadline.Stop()
		tick := time.NewTicker(25 * time.Millisecond)
		defer tick.Stop()
		for {
			if !client.IsRunning() {
				return nil
			}
			select {
			case <-deadline.C:
				return daemonStopTimeoutError(pid)
			case <-tick.C:
			}
		}
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	expectedArgs, identityErr := processArgsForPID(pid)
	if identityErr != nil {
		if !processExists(pid) {
			return nil
		}
		if !authorizedBySocket {
			return fmt.Errorf("cannot verify daemon pid %d identity: %w", pid, identityErr)
		}
		expectedArgs = ""
	}
	if strings.TrimSpace(expectedArgs) == "" && !authorizedBySocket {
		return fmt.Errorf("cannot verify daemon pid %d identity: empty process arguments", pid)
	}
	if authorizedBySocket {
		status, statusErr := client.Status()
		if statusErr != nil || status.PID != pid {
			return nil
		}
	} else if !sameProcessIdentity(pid, expectedArgs) {
		return nil
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	if !waitForProcessExit(pid, expectedArgs, timeout) {
		if authorizedBySocket {
			status, statusErr := client.Status()
			if statusErr != nil || status.PID != pid {
				return nil
			}
		} else if !sameProcessIdentity(pid, expectedArgs) {
			return nil
		}
		if err := proc.Signal(syscall.SIGKILL); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return err
		}
		if !waitForProcessExit(pid, expectedArgs, 2*time.Second) {
			// A killed child may remain as a zombie until its parent reaps it.
			// The daemon is operationally stopped once its admin socket is gone.
			if client != nil && !client.IsRunning() {
				return nil
			}
			return daemonStopTimeoutError(pid)
		}
	}
	return nil
}

func waitForProcessExit(pid int, expectedArgs string, timeout time.Duration) bool {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		if processReaped(pid) {
			return true
		}
		if processIsZombie(pid) {
			return true
		}
		if !sameProcessIdentity(pid, expectedArgs) {
			return true
		}
		select {
		case <-deadline.C:
			return false
		case <-tick.C:
		}
	}
}

func processReaped(pid int) bool {
	var status syscall.WaitStatus
	waited, err := syscall.Wait4(pid, &status, syscall.WNOHANG, nil)
	return err == nil && waited == pid
}

func processIsZombie(pid int) bool {
	//nolint:gosec // G204: fixed command and numeric pid.
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "stat=").Output()
	return err == nil && strings.HasPrefix(strings.TrimSpace(string(out)), "Z")
}

func sameProcessIdentity(pid int, expectedArgs string) bool {
	if !processExists(pid) {
		return false
	}
	if expectedArgs == "" {
		return true
	}
	currentArgs, err := processArgsForPID(pid)
	return err == nil && currentArgs == expectedArgs
}

func daemonStopTimeoutError(pid int) error {
	return fmt.Errorf("daemon did not stop after SIGTERM and SIGKILL (pid %d). Run `gate daemon status --all`, then `kill -9 %d` if that process is still gate, and retry `gate daemon start` or `gate up -d`", pid, pid)
}

func daemonStartErrorMessage(waitErr error, childStderr string) string {
	msg := strings.TrimSpace(childStderr)
	msg = strings.TrimPrefix(msg, "gate: ")
	if msg != "" {
		return msg
	}
	return waitErr.Error()
}

func daemonStartExitCode(msg string) int {
	if strings.Contains(msg, "permission denied") {
		return ExitPerm
	}
	if strings.Contains(msg, "address already in use") {
		return ExitConflict
	}
	return ExitError
}

type tcpListenOwner struct {
	PID  int
	Args string
}

var tcpListenOwnersForPort = func(port string) []tcpListenOwner {
	if strings.TrimSpace(port) == "" {
		return nil
	}
	//nolint:gosec // G204: fixed executable and fixed flags; port is data used as lsof's filter value.
	out, err := exec.Command("lsof", "-nP", "-iTCP:"+port, "-sTCP:LISTEN").Output()
	if err != nil {
		return nil
	}
	lines := strings.Split(string(out), "\n")
	var owners []tcpListenOwner
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		args, err := processArgsForPID(pid)
		if err != nil {
			continue
		}
		owners = append(owners, tcpListenOwner{PID: pid, Args: args})
	}
	return owners
}

func daemonStartConflictMessage(msg string, pair listener.Pair, ref listenerDaemonRef) string {
	if !strings.Contains(msg, "address already in use") {
		return msg
	}
	ports := listenerPorts(pair)
	if failedPort := failedListenPortFromMessage(msg); failedPort != "" {
		ports = []string{failedPort}
	}
	hint := gateDaemonConflictHintForPorts(ports, ref)
	if hint == "" {
		return msg
	}
	return msg + "\n" + hint
}

func failDaemonStart(
	stderr io.Writer,
	jsonOut bool,
	result daemonStartResult,
	pair listener.Pair,
	ref listenerDaemonRef,
	fallbackCode string,
) int {
	message := daemonStartConflictMessage(result.Message, pair, ref)
	code := fallbackCode
	if isLinuxLowPortBindPermission(message) {
		code = lowPortBindErrorCode
		if !jsonOut {
			message += "\n" + lowPortBindHint
		}
	}
	return fail(stderr, jsonOut, result.Code, code, message)
}

func isLinuxLowPortBindPermission(msg string) bool {
	if runtimeGOOS() != "linux" {
		return false
	}
	lower := strings.ToLower(msg)
	if !strings.Contains(lower, "permission denied") {
		return false
	}
	port, err := strconv.Atoi(failedListenPortFromMessage(lower))
	return err == nil && port > 0 && port < 1024
}

func gateDaemonConflictHint(pair listener.Pair, currentRef listenerDaemonRef) string {
	return gateDaemonConflictHintForPorts(listenerPorts(pair), currentRef)
}

func gateDaemonConflictHintForPorts(ports []string, currentRef listenerDaemonRef) string {
	currentSocket := currentRef.socketPath()
	currentConfigHome := configHomeForDaemonSocket(currentSocket)
	for _, port := range ports {
		for _, owner := range tcpListenOwnersForPort(port) {
			if !isGateDaemonArgs(owner.Args) {
				continue
			}
			socket := gateDaemonSocketPath(owner.Args)
			if socket == "" || socket == currentSocket {
				continue
			}
			if currentConfigHome != "" && configHomeForDaemonSocket(socket) == currentConfigHome {
				continue
			}
			lines := []string{
				fmt.Sprintf("hint: another gate daemon is already listening on TCP :%s (pid %d)", port, owner.PID),
			}
			lines = append(lines,
				"hint: owner socket: "+socket,
				"hint: current socket: "+currentSocket,
			)
			lines = append(lines, "hint: "+gateDaemonStopHint(owner.PID, socket))
			return strings.Join(lines, "\n")
		}
	}
	return ""
}

func failedListenPortFromMessage(msg string) string {
	beforeBind, _, ok := strings.Cut(msg, ": bind")
	if !ok {
		return ""
	}
	index := strings.LastIndex(beforeBind, "listen tcp ")
	if index == -1 {
		return ""
	}
	addr := strings.TrimSpace(beforeBind[index+len("listen tcp "):])
	if addr == "" {
		return ""
	}
	if _, port, err := net.SplitHostPort(addr); err == nil {
		return strings.TrimSpace(port)
	}
	if strings.HasPrefix(addr, ":") {
		return strings.TrimSpace(strings.TrimPrefix(addr, ":"))
	}
	return ""
}

func listenerPorts(pair listener.Pair) []string {
	var ports []string
	for _, addr := range []string{pair.HTTPSAddr, pair.HTTPAddr} {
		port := listenPort(addr)
		if port == "" || stringInSlice(port, ports) {
			continue
		}
		ports = append(ports, port)
	}
	return ports
}

func stringInSlice(value string, values []string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func gateDaemonSocketPath(args string) string {
	for _, marker := range []string{" --socket=", " --socket "} {
		if socket := socketPathAfterMarker(" "+args, marker); socket != "" {
			return socket
		}
	}
	if strings.HasPrefix(args, "--socket=") {
		return socketPathBeforeNextFlag(strings.TrimPrefix(args, "--socket="))
	}
	if strings.HasPrefix(args, "--socket ") {
		return socketPathBeforeNextFlag(strings.TrimPrefix(args, "--socket "))
	}
	return ""
}

func socketPathAfterMarker(args, marker string) string {
	index := strings.Index(args, marker)
	if index == -1 {
		return ""
	}
	return socketPathBeforeNextFlag(args[index+len(marker):])
}

func socketPathBeforeNextFlag(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if index := strings.Index(value, " --"); index != -1 {
		value = value[:index]
	}
	return strings.TrimSpace(value)
}

func gateDaemonStopHint(pid int, socket string) string {
	if configHome := configHomeForDaemonSocket(socket); configHome != "" {
		return fmt.Sprintf("stop it with `XDG_CONFIG_HOME=%s gate daemon stop` or `kill %d`", shellQuote(configHome), pid)
	}
	return fmt.Sprintf("stop it with `kill %d`", pid)
}

func configHomeForDaemonSocket(socket string) string {
	if socket == "" {
		return ""
	}
	dir := filepath.Dir(socket)
	if filepath.Base(dir) != "daemons" {
		return ""
	}
	gateDir := filepath.Dir(dir)
	if filepath.Base(gateDir) != "gate" {
		return ""
	}
	return filepath.Dir(gateDir)
}

func isGateDaemonPID(pid int) bool {
	args, err := processArgsForPID(pid)
	if err != nil {
		return false
	}
	return isGateDaemonArgs(args)
}

func isGateDaemonPIDForSocket(pid int, socket string) bool {
	args, err := processArgsForPID(pid)
	if err != nil || !isGateDaemonArgs(args) {
		return false
	}
	return filepath.Clean(gateDaemonSocketPath(args)) == filepath.Clean(socket)
}

var processArgsForPID = func(pid int) (string, error) {
	//nolint:gosec // G204: fixed executable and fixed flags; pid is data, not a shell command.
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "args=").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func isGateDaemonArgs(args string) bool {
	if prefix, _, ok := strings.Cut(args, " __serve"); ok {
		exe := strings.TrimSpace(prefix)
		if exe == "gate" {
			return true
		}
		return filepath.IsAbs(exe) && filepath.Base(exe) == "gate"
	}
	fields := strings.Fields(args)
	if len(fields) < 2 {
		return false
	}
	return filepath.Base(fields[0]) == "gate" && fields[1] == "__serve"
}

func daemonLogs(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	all := fs.Bool("all", false, "target all known listener daemons")
	fs.BoolVar(all, "a", false, "target all known listener daemons")
	if handled, code := parseNoArgFlags(fs, "daemon logs", args, stdout, stderr); handled {
		return code
	}
	refs := []listenerDaemonRef{defaultListenerRef()}
	if *all {
		var err error
		refs, err = allListenerRefs()
		if err != nil {
			return fail(stderr, false, ExitError, "listener", err.Error())
		}
	}
	allRequested := *all
	printed := 0
	for _, ref := range refs {
		logPath := ref.logPath()
		b, err := os.ReadFile(logPath)
		if err != nil {
			if allRequested && os.IsNotExist(err) {
				continue
			}
			return fail(stderr, false, ExitError, "logs", "no log file at "+logPath)
		}
		if len(refs) > 1 {
			if printed > 0 {
				fmt.Fprintln(stdout)
			}
			printInfo(stdout, "== "+ref.String()+" ==")
		}
		_, _ = stdout.Write(b)
		printed++
	}
	if printed == 0 {
		return fail(stderr, false, ExitError, "logs", "no log files found")
	}
	return ExitOK
}

// Serve is the hidden `__serve` entrypoint: it runs the resident proxy and the
// control socket in the foreground until signalled. `gate daemon start` spawns it.
func Serve(args []string, _, stderr io.Writer) int {
	socketPath, httpsAddr, httpAddr, code := parseServeFlags(args, stderr)
	if code != ExitOK {
		return code
	}
	caObj, err := ca.Load(paths.DataDir())
	if err != nil {
		return fail(stderr, false, ExitError, "ca", err.Error())
	}
	srv := proxy.New(caObj.GetCertificate, nil)
	d := &daemon.Daemon{
		Proxy:     srv,
		Socket:    socketPath,
		HTTPSAddr: httpsAddr,
		HTTPAddr:  httpAddr,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := d.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return fail(stderr, false, ExitError, "serve", err.Error())
	}
	return ExitOK
}

func parseServeFlags(args []string, stderr io.Writer) (string, string, string, int) {
	fs := flag.NewFlagSet("__serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	socketPath := fs.String("socket", globalDaemonScope().socketPath(), "admin socket path")
	httpsAddr := fs.String("https-addr", defaultDaemonHTTPSAddr, "HTTPS listen address")
	httpAddr := fs.String("http-addr", defaultDaemonHTTPAddr, "HTTP listen address")
	if err := fs.Parse(args); err != nil {
		return "", "", "", ExitUsage
	}
	if fs.NArg() != 0 {
		usageLine(stderr, "__serve")
		return "", "", "", ExitUsage
	}
	return *socketPath, *httpsAddr, *httpAddr, ExitOK
}

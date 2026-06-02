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
	"time"

	"gate/internal/ca"
	"gate/internal/daemon"
	"gate/internal/paths"
	"gate/internal/proxy"
)

var newDaemonServeCommand = func(exe, socketPath, httpsAddr, httpAddr string) *exec.Cmd {
	//nolint:gosec // G204: exe is our own binary path; listen addrs are passed as argv, not a shell.
	return exec.Command(exe, "__serve", "--socket", socketPath, "--https-addr", httpsAddr, "--http-addr", httpAddr)
}

const (
	defaultDaemonHTTPSAddr = ":443"
	defaultDaemonHTTPAddr  = ":80"
)

// Daemon dispatches `gate daemon start|stop|restart|status|logs`.
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
	scopeFlags := defineDaemonScopeFlags(fs, true)
	if handled, code := parseFlags(fs, "daemon status", args, stdout, stderr); handled {
		return code
	}
	scopes, err := daemonScopesFromCurrentDirAndFlags(scopeFlags, true)
	if err != nil {
		return fail(stderr, *jsonOut, ExitUsage, "scope", err.Error())
	}
	statuses := make([]daemon.Status, 0, len(scopes))
	for _, scope := range scopes {
		statuses = append(statuses, daemonStatusForScope(scope))
	}
	if *jsonOut {
		if len(statuses) == 1 {
			return writeJSON(stdout, statuses[0])
		}
		return writeJSON(stdout, statuses)
	}
	for _, st := range statuses {
		printDaemonStatus(stdout, st)
	}
	return ExitOK
}

func daemonStatusForScope(scope daemonScope) daemon.Status {
	st, err := daemonClientFor(scope).Status()
	if err != nil {
		return daemon.Status{Scope: scope.String(), ScopeKey: scope.fileKey(), Running: false}
	}
	st.Scope = scope.String()
	st.ScopeKey = scope.fileKey()
	return st
}

func printDaemonStatus(stdout io.Writer, st daemon.Status) {
	if !st.Running {
		fmt.Fprintf(stdout, "stopped · scope %s\n", st.Scope)
		return
	}
	if st.HTTPSAddr != "" || st.HTTPAddr != "" {
		fmt.Fprintf(stdout, "running · scope %s · pid %d · uptime %ds · %d routes · https %s · http %s\n", st.Scope, st.PID, st.UptimeSec, st.Routes, st.HTTPSAddr, st.HTTPAddr)
		return
	}
	fmt.Fprintf(stdout, "running · scope %s · pid %d · uptime %ds · %d routes\n", st.Scope, st.PID, st.UptimeSec, st.Routes)
}

func daemonStart(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("start", flag.ContinueOnError)
	httpsAddr := fs.String("https-addr", defaultDaemonHTTPSAddr, "HTTPS listen address")
	httpAddr := fs.String("http-addr", defaultDaemonHTTPAddr, "HTTP listen address")
	scopeFlags := defineDaemonScopeFlags(fs, false)
	if handled, code := parseFlags(fs, "daemon start", args, stdout, stderr); handled {
		return code
	}
	scope, err := singleDaemonScopeFromFlags(scopeFlags)
	if err != nil {
		return fail(stderr, false, ExitUsage, "scope", err.Error())
	}
	httpsSet, httpSet := flagSet(fs, "https-addr"), flagSet(fs, "http-addr")

	client := daemonClientFor(scope)
	if st, err := client.Status(); err == nil {
		if !daemonExplicitListenMatches(st, *httpsAddr, *httpAddr, httpsSet, httpSet) {
			msg := fmt.Sprintf("daemon already running on https %s · http %s; requested https %s · http %s; run `gate daemon stop` first",
				displayListenAddr(st.HTTPSAddr), displayListenAddr(st.HTTPAddr), *httpsAddr, *httpAddr)
			return fail(stderr, false, ExitConflict, "start", msg)
		}
		fmt.Fprintf(stdout, "already running · scope %s · https %s · http %s\n", scope.String(), displayListenAddr(st.HTTPSAddr), displayListenAddr(st.HTTPAddr))
		return ExitOK
	}
	result := startDaemonCommand(newDaemonServeCommand(executablePath(), scope.socketPath(), *httpsAddr, *httpAddr), client, scope)
	if result.Code == ExitOK {
		if err := setDaemonRoutesForScope(scope); err != nil {
			cleanupStartedDaemon(client, scope, result.PID)
			return fail(stderr, false, ExitError, "reload_failed", err.Error())
		}
		st, err := client.Status()
		if err != nil {
			fmt.Fprintf(stdout, "started · scope %s · pid %d · https %s · http %s\n", scope.String(), result.PID, *httpsAddr, *httpAddr)
			return ExitOK
		}
		fmt.Fprintf(stdout, "started · scope %s · pid %d · https %s · http %s\n", scope.String(), result.PID, displayListenAddr(st.HTTPSAddr), displayListenAddr(st.HTTPAddr))
		return ExitOK
	}
	return fail(stderr, false, result.Code, "start", result.Message)
}

func daemonRestart(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("restart", flag.ContinueOnError)
	httpsAddr := fs.String("https-addr", defaultDaemonHTTPSAddr, "HTTPS listen address")
	httpAddr := fs.String("http-addr", defaultDaemonHTTPAddr, "HTTP listen address")
	scopeFlags := defineDaemonScopeFlags(fs, false)
	if handled, code := parseFlags(fs, "daemon restart", args, stdout, stderr); handled {
		return code
	}
	scope, err := singleDaemonScopeFromFlags(scopeFlags)
	if err != nil {
		return fail(stderr, false, ExitUsage, "scope", err.Error())
	}

	httpsSet, httpSet := flagSet(fs, "https-addr"), flagSet(fs, "http-addr")
	client := daemonClientFor(scope)
	st, running := client.Status()
	if running == nil {
		*httpsAddr, *httpAddr = restartListenAddrs(st, *httpsAddr, *httpAddr, httpsSet, httpSet)
		if err := stopDaemonProcess(client, st.PID, 5*time.Second); err != nil {
			return fail(stderr, false, ExitError, "restart", err.Error())
		}
	}

	result := startDaemonCommand(newDaemonServeCommand(executablePath(), scope.socketPath(), *httpsAddr, *httpAddr), client, scope)
	if result.Code != ExitOK {
		return fail(stderr, false, result.Code, "restart", result.Message)
	}
	if err := setDaemonRoutesForScope(scope); err != nil {
		cleanupStartedDaemon(client, scope, result.PID)
		return fail(stderr, false, ExitError, "reload_failed", err.Error())
	}
	if st, err := client.Status(); err == nil {
		fmt.Fprintf(stdout, "restarted · scope %s · pid %d · https %s · http %s\n", scope.String(), st.PID, displayListenAddr(st.HTTPSAddr), displayListenAddr(st.HTTPAddr))
		return ExitOK
	}
	fmt.Fprintf(stdout, "restarted · scope %s · pid %d · https %s · http %s\n", scope.String(), result.PID, *httpsAddr, *httpAddr)
	return ExitOK
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
		return true
	}
	if actual == requested {
		return true
	}
	return listenPort(actual) == listenPort(requested)
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

func startDaemonCommand(cmd *exec.Cmd, client *daemon.Client, scope daemonScope) daemonStartResult {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	logFile, logOffset, err := openDaemonLog(scope)
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
	deadline := time.After(3 * time.Second)
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case err := <-waitc:
			if err == nil {
				err = errors.New("daemon exited before becoming ready")
			}
			msg := daemonStartErrorMessage(err, daemonLogSince(scope, logOffset))
			return daemonStartResult{Code: daemonStartExitCode(msg), Message: msg}
		case <-deadline:
			return daemonStartResult{Code: ExitError, Message: "daemon did not become ready"}
		case <-tick.C:
			if st, err := client.Status(); err == nil && (expectedPID < 0 || st.PID == expectedPID) {
				if err := os.MkdirAll(filepath.Dir(scope.pidPath()), 0o700); err != nil {
					return daemonStartResult{Code: ExitError, Message: err.Error()}
				}
				if err := os.WriteFile(scope.pidPath(), []byte(strconv.Itoa(st.PID)), 0o600); err != nil {
					return daemonStartResult{Code: ExitError, Message: err.Error()}
				}
				return daemonStartResult{Code: ExitOK, PID: st.PID}
			}
		}
	}
}

func openDaemonLog(scope daemonScope) (*os.File, int64, error) {
	logPath := scope.logPath()
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

func daemonLogSince(scope daemonScope, offset int64) string {
	f, err := os.Open(scope.logPath())
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
	scopeFlags := defineDaemonScopeFlags(fs, true)
	if handled, code := parseFlags(fs, "daemon stop", args, stdout, stderr); handled {
		return code
	}
	scopes, err := daemonScopesFromCurrentDirAndFlags(scopeFlags, true)
	if err != nil {
		return fail(stderr, false, ExitUsage, "scope", err.Error())
	}
	for _, scope := range scopes {
		if code := daemonStopScope(scope, stdout, stderr, len(scopes) > 1); code != ExitOK {
			return code
		}
	}
	return ExitOK
}

func daemonStopScope(scope daemonScope, stdout, stderr io.Writer, printScope bool) int {
	client := daemonClientFor(scope)
	if st, err := client.Status(); err == nil {
		if err := stopDaemonProcess(client, st.PID, 2*time.Second); err != nil {
			return fail(stderr, false, ExitError, "stop", err.Error())
		}
		_ = os.Remove(scope.pidPath())
		printDaemonStop(stdout, scope, "stopped", printScope)
		return ExitOK
	}
	b, err := os.ReadFile(scope.pidPath())
	if err != nil {
		printDaemonStop(stdout, scope, "not running", printScope)
		return ExitOK
	}
	pid, err := strconv.Atoi(string(b))
	if err != nil {
		return fail(stderr, false, ExitError, "pidfile", "corrupt pid file")
	}
	if !isGateDaemonPID(pid) {
		_ = os.Remove(scope.pidPath())
		printDaemonStop(stdout, scope, "not running", printScope)
		return ExitOK
	}
	proc, err := os.FindProcess(pid)
	if err == nil {
		_ = proc.Signal(syscall.SIGTERM)
	}
	_ = os.Remove(scope.pidPath())
	printDaemonStop(stdout, scope, "stopped", printScope)
	return ExitOK
}

func cleanupStartedDaemon(client *daemon.Client, scope daemonScope, pid int) {
	_ = stopDaemonProcess(client, pid, 2*time.Second)
	_ = os.Remove(scope.pidPath())
}

func printDaemonStop(stdout io.Writer, scope daemonScope, msg string, printScope bool) {
	if printScope {
		fmt.Fprintf(stdout, "%s · scope %s\n", msg, scope.String())
		return
	}
	fmt.Fprintln(stdout, msg)
}

func stopDaemonProcess(client *daemon.Client, pid int, timeout time.Duration) error {
	proc, perr := os.FindProcess(pid)
	if perr == nil {
		_ = proc.Signal(syscall.SIGTERM)
	}
	if !waitForDaemonStop(client, timeout) {
		return errors.New("daemon did not stop")
	}
	return nil
}

func waitForDaemonStop(client *daemon.Client, timeout time.Duration) bool {
	deadline := time.After(timeout)
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		if !client.IsRunning() {
			return true
		}
		select {
		case <-deadline:
			return false
		case <-tick.C:
		}
	}
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

func isGateDaemonPID(pid int) bool {
	args, err := processArgsForPID(pid)
	if err != nil {
		return false
	}
	return isGateDaemonArgs(args)
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
	args = strings.TrimSpace(args)
	return args == "gate __serve" ||
		strings.HasPrefix(args, "gate __serve ") ||
		strings.HasSuffix(args, "/gate __serve") ||
		strings.Contains(args, "/gate __serve ") ||
		strings.Contains(args, " gate __serve ")
}

func daemonLogs(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	scopeFlags := defineDaemonScopeFlags(fs, true)
	if handled, code := parseFlags(fs, "daemon logs", args, stdout, stderr); handled {
		return code
	}
	scopes, err := daemonScopesFromCurrentDirAndFlags(scopeFlags, true)
	if err != nil {
		return fail(stderr, false, ExitUsage, "scope", err.Error())
	}
	allRequested := scopeFlags.all != nil && *scopeFlags.all
	printed := 0
	for _, scope := range scopes {
		logPath := scope.logPath()
		b, err := os.ReadFile(logPath)
		if err != nil {
			if allRequested && os.IsNotExist(err) {
				continue
			}
			return fail(stderr, false, ExitError, "logs", "no log file at "+logPath)
		}
		if len(scopes) > 1 {
			if printed > 0 {
				fmt.Fprintln(stdout)
			}
			fmt.Fprintf(stdout, "== %s ==\n", scope.String())
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
	return *socketPath, *httpsAddr, *httpAddr, ExitOK
}

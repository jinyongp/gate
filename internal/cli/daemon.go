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

	"prx/internal/ca"
	"prx/internal/daemon"
	"prx/internal/paths"
	"prx/internal/proxy"
)

func pidPath() string { return filepath.Join(paths.ConfigDir(), "prx.pid") }

var newDaemonServeCommand = func(exe, httpsAddr, httpAddr string) *exec.Cmd {
	//nolint:gosec // G204: exe is our own binary path; listen addrs are passed as argv, not a shell.
	return exec.Command(exe, "__serve", "--https-addr", httpsAddr, "--http-addr", httpAddr)
}

const (
	defaultDaemonHTTPSAddr = ":443"
	defaultDaemonHTTPAddr  = ":80"
)

// Daemon dispatches `prx daemon start|stop|restart|status|logs`.
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
		return daemonStop(stdout, stderr)
	case "restart":
		return daemonRestart(rest, stdout, stderr)
	case "logs":
		return daemonLogs(stdout, stderr)
	default:
		usageLine(stderr, "daemon")
		return ExitUsage
	}
}

func daemonStatus(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit JSON")
	if handled, code := parseFlags(fs, "daemon status", args, stdout, stderr); handled {
		return code
	}
	client := daemon.NewClient(paths.SocketPath())
	st, err := client.Status()
	if err != nil {
		if *jsonOut {
			return writeJSON(stdout, map[string]any{"running": false})
		}
		fmt.Fprintln(stdout, "stopped")
		return ExitOK
	}
	if *jsonOut {
		return writeJSON(stdout, st)
	}
	if st.HTTPSAddr != "" || st.HTTPAddr != "" {
		fmt.Fprintf(stdout, "running · pid %d · uptime %ds · %d routes · https %s · http %s\n", st.PID, st.UptimeSec, st.Routes, st.HTTPSAddr, st.HTTPAddr)
		return ExitOK
	}
	fmt.Fprintf(stdout, "running · pid %d · uptime %ds · %d routes\n", st.PID, st.UptimeSec, st.Routes)
	return ExitOK
}

func daemonStart(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("start", flag.ContinueOnError)
	httpsAddr := fs.String("https-addr", defaultDaemonHTTPSAddr, "HTTPS listen address")
	httpAddr := fs.String("http-addr", defaultDaemonHTTPAddr, "HTTP listen address")
	if handled, code := parseFlags(fs, "daemon start", args, stdout, stderr); handled {
		return code
	}

	client := daemon.NewClient(paths.SocketPath())
	if st, err := client.Status(); err == nil {
		if !daemonListenMatches(st, *httpsAddr, *httpAddr) {
			msg := fmt.Sprintf("daemon already running on https %s · http %s; requested https %s · http %s; run `prx daemon stop` first",
				displayListenAddr(st.HTTPSAddr), displayListenAddr(st.HTTPAddr), *httpsAddr, *httpAddr)
			return fail(stderr, false, ExitConflict, "start", msg)
		}
		fmt.Fprintf(stdout, "already running · https %s · http %s\n", displayListenAddr(st.HTTPSAddr), displayListenAddr(st.HTTPAddr))
		return ExitOK
	}
	result := startDaemonCommand(newDaemonServeCommand(executablePath(), *httpsAddr, *httpAddr), client)
	if result.Code == ExitOK {
		st, err := client.Status()
		if err != nil {
			fmt.Fprintf(stdout, "started · pid %d · https %s · http %s\n", result.PID, *httpsAddr, *httpAddr)
			return ExitOK
		}
		fmt.Fprintf(stdout, "started · pid %d · https %s · http %s\n", result.PID, displayListenAddr(st.HTTPSAddr), displayListenAddr(st.HTTPAddr))
		return ExitOK
	}
	return fail(stderr, false, result.Code, "start", result.Message)
}

func daemonRestart(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("restart", flag.ContinueOnError)
	httpsAddr := fs.String("https-addr", defaultDaemonHTTPSAddr, "HTTPS listen address")
	httpAddr := fs.String("http-addr", defaultDaemonHTTPAddr, "HTTP listen address")
	if handled, code := parseFlags(fs, "daemon restart", args, stdout, stderr); handled {
		return code
	}

	httpsSet, httpSet := flagSet(fs, "https-addr"), flagSet(fs, "http-addr")
	client := daemon.NewClient(paths.SocketPath())
	st, running := client.Status()
	if running == nil {
		*httpsAddr, *httpAddr = restartListenAddrs(st, *httpsAddr, *httpAddr, httpsSet, httpSet)
		if err := stopDaemonProcess(client, st.PID, 5*time.Second); err != nil {
			return fail(stderr, false, ExitError, "restart", err.Error())
		}
	}

	result := startDaemonCommand(newDaemonServeCommand(executablePath(), *httpsAddr, *httpAddr), client)
	if result.Code != ExitOK {
		return fail(stderr, false, result.Code, "restart", result.Message)
	}
	if st, err := client.Status(); err == nil {
		fmt.Fprintf(stdout, "restarted · pid %d · https %s · http %s\n", st.PID, displayListenAddr(st.HTTPSAddr), displayListenAddr(st.HTTPAddr))
		return ExitOK
	}
	fmt.Fprintf(stdout, "restarted · pid %d · https %s · http %s\n", result.PID, *httpsAddr, *httpAddr)
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

func startDaemonCommand(cmd *exec.Cmd, client *daemon.Client) daemonStartResult {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	logFile, logOffset, err := openDaemonLog()
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
			msg := daemonStartErrorMessage(err, daemonLogSince(logOffset))
			return daemonStartResult{Code: daemonStartExitCode(msg), Message: msg}
		case <-deadline:
			return daemonStartResult{Code: ExitError, Message: "daemon did not become ready"}
		case <-tick.C:
			if st, err := client.Status(); err == nil && (expectedPID < 0 || st.PID == expectedPID) {
				if err := os.WriteFile(pidPath(), []byte(strconv.Itoa(st.PID)), 0o600); err != nil {
					return daemonStartResult{Code: ExitError, Message: err.Error()}
				}
				return daemonStartResult{Code: ExitOK, PID: st.PID}
			}
		}
	}
}

func openDaemonLog() (*os.File, int64, error) {
	if err := os.MkdirAll(paths.StateDir(), 0o700); err != nil {
		return nil, 0, err
	}
	logPath := daemonLogPath()
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

func daemonLogPath() string {
	return filepath.Join(paths.StateDir(), "prx.log")
}

func daemonLogSince(offset int64) string {
	f, err := os.Open(daemonLogPath())
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

func daemonStop(stdout, stderr io.Writer) int {
	client := daemon.NewClient(paths.SocketPath())
	if st, err := client.Status(); err == nil {
		if err := stopDaemonProcess(client, st.PID, 2*time.Second); err != nil {
			return fail(stderr, false, ExitError, "stop", err.Error())
		}
		_ = os.Remove(pidPath())
		fmt.Fprintln(stdout, "stopped")
		return ExitOK
	}
	b, err := os.ReadFile(pidPath())
	if err != nil {
		fmt.Fprintln(stdout, "not running")
		return ExitOK
	}
	pid, err := strconv.Atoi(string(b))
	if err != nil {
		return fail(stderr, false, ExitError, "pidfile", "corrupt pid file")
	}
	if !isPrxDaemonPID(pid) {
		_ = os.Remove(pidPath())
		fmt.Fprintln(stdout, "not running")
		return ExitOK
	}
	proc, err := os.FindProcess(pid)
	if err == nil {
		_ = proc.Signal(syscall.SIGTERM)
	}
	_ = os.Remove(pidPath())
	fmt.Fprintln(stdout, "stopped")
	return ExitOK
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
	msg = strings.TrimPrefix(msg, "prx: ")
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

func isPrxDaemonPID(pid int) bool {
	//nolint:gosec // G204: fixed executable and fixed flags; pid is data, not a shell command.
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "args=").Output()
	if err != nil {
		return false
	}
	args := strings.TrimSpace(string(out))
	return args == "prx __serve" || strings.HasSuffix(args, "/prx __serve") || strings.Contains(args, " prx __serve")
}

func daemonLogs(stdout, stderr io.Writer) int {
	logPath := daemonLogPath()
	b, err := os.ReadFile(logPath)
	if err != nil {
		return fail(stderr, false, ExitError, "logs", "no log file at "+logPath)
	}
	_, _ = stdout.Write(b)
	return ExitOK
}

// Serve is the hidden `__serve` entrypoint: it runs the resident proxy and the
// control socket in the foreground until signalled. `prx daemon start` spawns it.
func Serve(args []string, _, stderr io.Writer) int {
	httpsAddr, httpAddr, code := parseServeFlags(args, stderr)
	if code != ExitOK {
		return code
	}
	caObj, err := ca.Load(paths.DataDir())
	if err != nil {
		return fail(stderr, false, ExitError, "ca", err.Error())
	}
	srv := proxy.New(caObj.GetCertificate, nil)
	if _, err := os.Stat(filepath.Join(paths.ConfigDir(), "registry.json")); err == nil {
		reg, rerr := registryStore().Read()
		if rerr != nil {
			return fail(stderr, false, ExitError, "registry", rerr.Error())
		}
		srv.SetRoutes(activeRoutes(reg))
	}
	d := &daemon.Daemon{
		Proxy:     srv,
		Socket:    paths.SocketPath(),
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

func parseServeFlags(args []string, stderr io.Writer) (string, string, int) {
	fs := flag.NewFlagSet("__serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	httpsAddr := fs.String("https-addr", defaultDaemonHTTPSAddr, "HTTPS listen address")
	httpAddr := fs.String("http-addr", defaultDaemonHTTPAddr, "HTTP listen address")
	if err := fs.Parse(args); err != nil {
		return "", "", ExitUsage
	}
	return *httpsAddr, *httpAddr, ExitOK
}

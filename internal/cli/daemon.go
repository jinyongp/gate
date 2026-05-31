package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
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

// Daemon dispatches `prx daemon start|stop|status|logs`.
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
		return daemonStart(stdout, stderr)
	case "stop":
		return daemonStop(stdout, stderr)
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
	fmt.Fprintf(stdout, "running · pid %d · uptime %ds · %d routes\n", st.PID, st.UptimeSec, st.Routes)
	return ExitOK
}

func daemonStart(stdout, stderr io.Writer) int {
	client := daemon.NewClient(paths.SocketPath())
	if client.IsRunning() {
		fmt.Fprintln(stdout, "already running")
		return ExitOK
	}
	exe, err := os.Executable()
	if err != nil {
		return fail(stderr, false, ExitError, "exec", err.Error())
	}
	//nolint:gosec // G204: exe is our own binary path, not user input.
	cmd := exec.Command(exe, "__serve")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fail(stderr, false, ExitError, "start", err.Error())
	}
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
			return fail(stderr, false, daemonStartExitCode(err), "start", err.Error())
		case <-deadline:
			return fail(stderr, false, ExitError, "start", "daemon did not become ready")
		case <-tick.C:
			if st, err := client.Status(); err == nil && st.PID == cmd.Process.Pid {
				if err := os.WriteFile(pidPath(), []byte(strconv.Itoa(cmd.Process.Pid)), 0o600); err != nil {
					return fail(stderr, false, ExitError, "pidfile", err.Error())
				}
				fmt.Fprintf(stdout, "started · pid %d\n", cmd.Process.Pid)
				return ExitOK
			}
		}
	}
}

func daemonStop(stdout, stderr io.Writer) int {
	client := daemon.NewClient(paths.SocketPath())
	if st, err := client.Status(); err == nil {
		proc, perr := os.FindProcess(st.PID)
		if perr == nil {
			_ = proc.Signal(syscall.SIGTERM)
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

func daemonStartExitCode(err error) int {
	if strings.Contains(err.Error(), "permission denied") {
		return ExitPerm
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
	logPath := filepath.Join(paths.StateDir(), "prx.log")
	b, err := os.ReadFile(logPath)
	if err != nil {
		return fail(stderr, false, ExitError, "logs", "no log file at "+logPath)
	}
	_, _ = stdout.Write(b)
	return ExitOK
}

// Serve is the hidden `__serve` entrypoint: it runs the resident proxy and the
// control socket in the foreground until signalled. `prx daemon start` spawns it.
func Serve(_ []string, _, stderr io.Writer) int {
	caObj, err := ca.Load(paths.DataDir())
	if err != nil {
		return fail(stderr, false, ExitError, "ca", err.Error())
	}
	srv := proxy.New(caObj.GetCertificate, nil)
	if reg, rerr := registryStore().Read(); rerr == nil {
		srv.SetRoutes(activeRoutes(reg))
	}
	d := &daemon.Daemon{
		Proxy:     srv,
		Socket:    paths.SocketPath(),
		HTTPSAddr: ":443",
		HTTPAddr:  ":80",
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := d.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return fail(stderr, false, ExitError, "serve", err.Error())
	}
	return ExitOK
}

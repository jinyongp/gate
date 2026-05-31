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
	"syscall"

	"prx/internal/ca"
	"prx/internal/daemon"
	"prx/internal/paths"
	"prx/internal/proxy"
)

func pidPath() string { return filepath.Join(paths.ConfigDir(), "prx.pid") }

// Daemon dispatches `prx daemon start|stop|status|logs`.
func Daemon(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return fail(stderr, false, ExitUsage, "usage", "usage: prx daemon start|stop|status|logs")
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
		return fail(stderr, false, ExitUsage, "usage", "unknown subcommand: "+sub)
	}
}

func daemonStatus(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonOut := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
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
	if daemon.NewClient(paths.SocketPath()).IsRunning() {
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
	if err := os.WriteFile(pidPath(), []byte(strconv.Itoa(cmd.Process.Pid)), 0o600); err != nil {
		return fail(stderr, false, ExitError, "pidfile", err.Error())
	}
	fmt.Fprintf(stdout, "started · pid %d\n", cmd.Process.Pid)
	return ExitOK
}

func daemonStop(stdout, stderr io.Writer) int {
	b, err := os.ReadFile(pidPath())
	if err != nil {
		fmt.Fprintln(stdout, "not running")
		return ExitOK
	}
	pid, err := strconv.Atoi(string(b))
	if err != nil {
		return fail(stderr, false, ExitError, "pidfile", "corrupt pid file")
	}
	proc, err := os.FindProcess(pid)
	if err == nil {
		_ = proc.Signal(syscall.SIGTERM)
	}
	_ = os.Remove(pidPath())
	fmt.Fprintln(stdout, "stopped")
	return ExitOK
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

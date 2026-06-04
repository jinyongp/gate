package expose

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

// Cloudflared exposes a route as a public URL via the cloudflared binary.
type Cloudflared struct {
	mu    sync.Mutex
	cmd   *exec.Cmd
	waitc chan error
}

var trycloudflareRe = regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)

// Expose starts a quick tunnel to the local HTTPS address for domain and
// returns the public URL cloudflared prints.
func (c *Cloudflared) Expose(ctx context.Context, domain string, _ Opts) (Result, error) {
	//nolint:gosec // G204: fixed binary; domain comes from the project config.
	cmd := exec.CommandContext(ctx, "cloudflared", "tunnel", "--url", "https://"+domain)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return Result{}, err
	}
	if err := cmd.Start(); err != nil {
		return Result{}, fmt.Errorf("expose: cloudflared not available: %w", err)
	}
	waitc := make(chan error, 1)
	go func() { waitc <- cmd.Wait() }()
	c.mu.Lock()
	c.cmd = cmd
	c.waitc = waitc
	c.mu.Unlock()

	scanner := bufio.NewScanner(stderr)
	for scanner.Scan() {
		if url := trycloudflareRe.FindString(scanner.Text()); url != "" {
			// Keep draining stderr so the pipe buffer never fills and stalls
			// the long-running tunnel process.
			go func() {
				for scanner.Scan() {
				}
			}()
			return Result{URL: url, PID: cmd.Process.Pid, Command: strings.Join(cmd.Args, " ")}, nil
		}
	}
	_ = cmd.Process.Kill()
	<-waitc
	return Result{}, fmt.Errorf("expose: cloudflared did not report a public URL")
}

func (c *Cloudflared) Status(_ context.Context, record Record) (string, error) {
	if cloudflaredProcessMatches(record) {
		return StatusLive, nil
	}
	return StatusDown, nil
}

func (c *Cloudflared) Stop(_ context.Context, record Record, _ StopOpts) error {
	if !cloudflaredProcessMatches(record) {
		return nil
	}
	proc, err := os.FindProcess(record.PID)
	if err != nil {
		return err
	}
	if err := proc.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return nil
}

// Close terminates the tunnel.
func (c *Cloudflared) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	cmd, waitc := c.cmd, c.waitc
	c.cmd, c.waitc = nil, nil
	if cmd != nil && cmd.Process != nil {
		err := cmd.Process.Kill()
		if waitc != nil {
			<-waitc
		}
		return err
	}
	return nil
}

// Tailscale exposes a route over a tailnet via `tailscale serve`.
type Tailscale struct{}

// Expose publishes the local HTTPS address through tailscale serve.
func (Tailscale) Expose(ctx context.Context, domain string, _ Opts) (Result, error) {
	//nolint:gosec // G204: fixed binary; domain comes from the project config.
	cmd := exec.CommandContext(ctx, "tailscale", "serve", "--bg", "https://"+domain)
	if out, err := cmd.CombinedOutput(); err != nil {
		return Result{}, fmt.Errorf("expose: tailscale serve failed: %w: %s", err, out)
	}
	return Result{URL: "https://" + domain}, nil
}

func (Tailscale) Status(context.Context, Record) (string, error) {
	return StatusUnverified, nil
}

func (Tailscale) Stop(_ context.Context, _ Record, opts StopOpts) error {
	if opts.Force {
		return nil
	}
	return fmt.Errorf("expose: tailscale teardown is manual; run tailscale serve reset or pass --force to forget the record")
}

// Close is a no-op; `tailscale serve reset` tears down manually.
func (Tailscale) Close() error { return nil }

func processExists(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return false
	}
	return true
}

func cloudflaredProcessMatches(record Record) bool {
	if record.PID <= 0 || !processExists(record.PID) {
		return false
	}
	args, err := processArgsForPID(record.PID)
	if err != nil {
		return false
	}
	args = strings.TrimSpace(args)
	if args == "" {
		return false
	}
	fields := strings.Fields(args)
	if len(fields) != 4 || filepath.Base(fields[0]) != "cloudflared" {
		return false
	}
	return fields[1] == "tunnel" &&
		fields[2] == "--url" &&
		fields[3] == "https://"+record.Target
}

var processArgsForPID = func(pid int) (string, error) {
	//nolint:gosec // G204: fixed executable and fixed flags; pid is process metadata.
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "args=").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

package expose

import (
	"context"
	"encoding/json"
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
	"time"
)

// Cloudflared exposes a route as a public URL via the cloudflared binary.
type Cloudflared struct {
	mu    sync.Mutex
	cmd   *exec.Cmd
	waitc chan error
}

var trycloudflareRe = regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)

var (
	cloudflaredStartupTimeout = 20 * time.Second
	cloudflaredCommand        = func(ctx context.Context, domain string) *exec.Cmd {
		//nolint:gosec // G204: fixed binary; domain comes from the project config.
		return exec.CommandContext(ctx, "cloudflared", "tunnel", "--url", "https://"+domain)
	}
	tailscaleStatusCommand = func(ctx context.Context) *exec.Cmd {
		return exec.CommandContext(ctx, "tailscale", "status", "--json")
	}
	tailscaleServeCommand = func(ctx context.Context, target string, httpsPort int) *exec.Cmd {
		if httpsPort > 0 {
			//nolint:gosec // G204: fixed binary; target comes from the project config.
			return exec.CommandContext(ctx, "tailscale", "serve", "--bg", fmt.Sprintf("--https=%d", httpsPort), target)
		}
		//nolint:gosec // G204: fixed binary; target comes from the project config.
		return exec.CommandContext(ctx, "tailscale", "serve", "--bg", target)
	}
	tailscaleServeResetCommand = func(ctx context.Context) *exec.Cmd {
		return exec.CommandContext(ctx, "tailscale", "serve", "reset")
	}
)

// Expose starts a quick tunnel to the local HTTPS address for domain and
// returns the public URL cloudflared prints.
func (c *Cloudflared) Expose(ctx context.Context, domain string, _ Opts) (Result, error) {
	cmd := cloudflaredCommand(ctx, domain)
	logFile, err := os.CreateTemp("", "gate-cloudflared-*.log")
	if err != nil {
		return Result{}, err
	}
	logPath := logFile.Name()
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		_ = os.Remove(logPath)
		if errors.Is(err, exec.ErrNotFound) {
			return Result{}, fmt.Errorf("expose: cloudflared not found in PATH; install cloudflared and retry")
		}
		return Result{}, fmt.Errorf("expose: cloudflared not available: %w", err)
	}
	_ = logFile.Close()
	waitc := make(chan error, 1)
	go func() { waitc <- cmd.Wait() }()
	c.mu.Lock()
	c.cmd = cmd
	c.waitc = waitc
	c.mu.Unlock()

	url, waitConsumed, err := waitForCloudflaredURL(ctx, logPath, waitc, cloudflaredStartupTimeout)
	if err != nil {
		if waitConsumed {
			c.clear(cmd)
		} else {
			_ = c.killAndClear(cmd, waitc)
		}
		return Result{}, err
	}
	return Result{URL: url, PID: cmd.Process.Pid, Command: strings.Join(cmd.Args, " ")}, nil
}

func waitForCloudflaredURL(ctx context.Context, logPath string, waitc <-chan error, timeout time.Duration) (string, bool, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if url := cloudflaredURLFromLog(logPath); url != "" {
			return url, false, nil
		}
		select {
		case <-ticker.C:
		case <-timer.C:
			return "", false, fmt.Errorf("expose: cloudflared did not report a public URL before startup timeout; log: %s", logPath)
		case <-ctx.Done():
			return "", false, ctx.Err()
		case err := <-waitc:
			if url := cloudflaredURLFromLog(logPath); url != "" {
				return "", true, cloudflaredExitError(fmt.Sprintf("expose: cloudflared exited after reporting %s", url), err, logPath)
			}
			return "", true, cloudflaredExitError("expose: cloudflared exited before reporting a public URL", err, logPath)
		}
	}
}

func cloudflaredExitError(prefix string, err error, logPath string) error {
	if err != nil {
		return fmt.Errorf("%s: %w; log: %s", prefix, err, logPath)
	}
	return fmt.Errorf("%s; log: %s", prefix, logPath)
}

func cloudflaredURLFromLog(logPath string) string {
	b, err := os.ReadFile(logPath)
	if err != nil {
		return ""
	}
	return trycloudflareRe.FindString(string(b))
}

func killAndWait(cmd *exec.Cmd, waitc <-chan error) error {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	if waitc != nil {
		return <-waitc
	}
	return nil
}

func (c *Cloudflared) killAndClear(cmd *exec.Cmd, waitc <-chan error) error {
	c.clear(cmd)
	return killAndWait(cmd, waitc)
}

func (c *Cloudflared) clear(cmd *exec.Cmd) {
	c.mu.Lock()
	if c.cmd == cmd {
		c.cmd = nil
		c.waitc = nil
	}
	c.mu.Unlock()
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

// Expose publishes the selected target through tailscale serve.
func (Tailscale) Expose(ctx context.Context, domain string, opts Opts) (Result, error) {
	publicURL, err := tailscaleNodeURL(ctx, opts.ServePort)
	if err != nil {
		return Result{}, err
	}
	target := strings.TrimSpace(opts.TargetURL)
	if target == "" {
		target = "https://" + domain
	}
	cmd := tailscaleServeCommand(ctx, target, opts.ServePort)
	if out, err := cmd.CombinedOutput(); err != nil {
		return Result{}, fmt.Errorf("expose: tailscale serve failed: %w: %s", err, out)
	}
	return Result{URL: publicURL, Command: strings.Join(cmd.Args, " ")}, nil
}

func tailscaleNodeURL(ctx context.Context, httpsPort int) (string, error) {
	cmd := tailscaleStatusCommand(ctx)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return "", fmt.Errorf("expose: tailscale not found in PATH; install tailscale and retry")
		}
		return "", fmt.Errorf("expose: tailscale status failed: %w: %s", err, out)
	}
	var status struct {
		Self struct {
			DNSName string `json:"DNSName"`
		} `json:"Self"`
	}
	if err := json.Unmarshal(out, &status); err != nil {
		return "", fmt.Errorf("expose: tailscale status returned invalid JSON: %w", err)
	}
	dnsName := strings.TrimSuffix(strings.TrimSpace(status.Self.DNSName), ".")
	if dnsName == "" {
		return "", fmt.Errorf("expose: tailscale status did not report this machine's DNS name")
	}
	if httpsPort > 0 && httpsPort != 443 {
		return fmt.Sprintf("https://%s:%d", dnsName, httpsPort), nil
	}
	return "https://" + dnsName, nil
}

func (Tailscale) Status(context.Context, Record) (string, error) {
	return StatusUnverified, nil
}

func (Tailscale) Stop(ctx context.Context, record Record, opts StopOpts) error {
	if !opts.Force && !tailscaleRecordOwned(record) {
		return fmt.Errorf("expose: tailscale serve state is not owned by gate; pass --force to reset anyway")
	}
	cmd := tailscaleServeResetCommand(ctx)
	if out, err := cmd.CombinedOutput(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return fmt.Errorf("expose: tailscale not found in PATH; install tailscale and retry")
		}
		return fmt.Errorf("expose: tailscale serve reset failed: %w: %s", err, out)
	}
	return nil
}

func tailscaleRecordOwned(record Record) bool {
	if record.Provider != ProviderTailscale {
		return false
	}
	command := strings.TrimSpace(record.Command)
	if command == "tailscale serve --bg https://"+record.Target {
		return true
	}
	if record.ServePort > 0 {
		want := fmt.Sprintf("tailscale serve --bg --https=%d https+insecure://%s", record.ServePort, record.Target)
		return command == want
	}
	return false
}

// Close is a no-op; Stop tears down Tailscale Serve.
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

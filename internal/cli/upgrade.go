package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"prx/internal/daemon"
	"prx/internal/paths"
	"prx/internal/ui"
)

const (
	upgradeScriptURL = "https://raw.githubusercontent.com/jinyongp/prx/main/scripts/install.sh"
	githubLatestAPI  = "https://api.github.com/repos/jinyongp/prx/releases/latest"
	defaultUserAgent = "prx-upgrade"
)

var currentVersion = "dev"

var restartDaemonAfterUpgradeFunc = restartDaemonAfterUpgrade

// SetVersion stores the currently running prx version for upgrade decisions.
func SetVersion(v string) {
	currentVersion = v
}

// Upgrade downloads and executes the upstream install script to replace the current
// prx binary with the latest release.
func Upgrade(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("upgrade", flag.ContinueOnError)
	var yes bool
	fs.BoolVar(&yes, "yes", false, "upgrade without the confirmation prompt")
	fs.BoolVar(&yes, "y", false, "upgrade without the confirmation prompt")
	if handled, code := parseFlags(fs, "upgrade", args, stdout, stderr); handled {
		return code
	}
	if fs.NArg() != 0 {
		return usageFail(stderr, false, "upgrade")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	latestTag, err := latestReleaseTag(ctx)
	if err != nil {
		printUpgradeWarning(stderr, "unable to check latest version: "+err.Error())
	}

	if latestTag != "" {
		if current := normalizedVersion(currentVersion); current != "" && current != "dev" {
			if normalizedVersion(latestTag) == current {
				daemonBefore, daemonWasRunning := daemonStatusBeforeUpgrade()
				return completeUpToDate(stdout, stderr, currentVersion, daemonBefore, daemonWasRunning)
			}
		}
	} else {
		printUpgradeVersion(stdout, "current", currentVersion)
	}

	if !yes && !confirmUpgrade(stdout, currentVersion, latestTag) {
		printUpgradeStatus(stdout, "upgrade canceled")
		return ExitOK
	}

	daemonBefore, daemonWasRunning := daemonStatusBeforeUpgrade()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, upgradeScriptURL, nil)
	if err != nil {
		return fail(stderr, false, ExitError, "upgrade", err.Error())
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return fail(stderr, false, ExitError, "upgrade", "failed to download install script: "+err.Error())
	}
	defer func() {
		_ = res.Body.Close()
	}()

	if res.StatusCode != http.StatusOK {
		return fail(stderr, false, ExitError, "upgrade", fmt.Sprintf("failed to download install script: %s", res.Status))
	}

	script, err := os.CreateTemp("", "prx-upgrade-*.sh")
	if err != nil {
		return fail(stderr, false, ExitError, "upgrade", err.Error())
	}
	defer func() {
		_ = os.Remove(script.Name())
	}()

	if _, err := io.Copy(script, res.Body); err != nil {
		return fail(stderr, false, ExitError, "upgrade", err.Error())
	}
	if err := script.Chmod(0o755); err != nil {
		return fail(stderr, false, ExitError, "upgrade", err.Error())
	}
	if err := script.Close(); err != nil {
		return fail(stderr, false, ExitError, "upgrade", err.Error())
	}

	//nolint:gosec // G204: executing trusted, repo-fixed upgrade script.
	cmd := exec.Command("sh", script.Name())
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return fail(stderr, false, ExitError, "upgrade", err.Error())
	}
	return completeUpgrade(stdout, stderr, daemonBefore, daemonWasRunning)
}

func daemonStatusBeforeUpgrade() (daemon.Status, bool) {
	st, err := daemon.NewClient(paths.SocketPath()).Status()
	return st, err == nil
}

func completeUpgrade(stdout, stderr io.Writer, daemonBefore daemon.Status, daemonWasRunning bool) int {
	if daemonWasRunning {
		if code := restartDaemonAfterUpgradeFunc(daemonBefore, stdout, stderr); code != ExitOK {
			return code
		}
	}
	printUpgradeStatus(stdout, "upgrade complete")
	return ExitOK
}

func completeUpToDate(stdout, stderr io.Writer, version string, daemonBefore daemon.Status, daemonWasRunning bool) int {
	if daemonWasRunning {
		if code := restartDaemonAfterUpgradeFunc(daemonBefore, stdout, stderr); code != ExitOK {
			return code
		}
	}
	printUpgradeStatus(stdout, fmt.Sprintf("up to date (%s)", version))
	return ExitOK
}

func restartDaemonAfterUpgrade(st daemon.Status, stdout, stderr io.Writer) int {
	client := daemon.NewClient(paths.SocketPath())
	if err := stopDaemonProcess(client, st.PID, 5*time.Second); err != nil {
		return fail(stderr, false, ExitError, "upgrade", "failed to restart daemon: "+err.Error())
	}

	httpsAddr := restartListenAddr(st.HTTPSAddr, defaultDaemonHTTPSAddr)
	httpAddr := restartListenAddr(st.HTTPAddr, defaultDaemonHTTPAddr)
	result := startDaemonCommand(newDaemonServeCommand(executablePath(), httpsAddr, httpAddr), client)
	if result.Code != ExitOK {
		return fail(stderr, false, result.Code, "upgrade", "failed to restart daemon: "+result.Message)
	}
	fmt.Fprintf(stdout, "daemon restarted · pid %d · https %s · http %s\n", result.PID, httpsAddr, httpAddr)
	return ExitOK
}

func restartListenAddr(actual, fallback string) string {
	if strings.TrimSpace(actual) == "" {
		return fallback
	}
	return actual
}

func printUpgradeVersion(stdout io.Writer, label, version string) {
	if richOut(stdout, false) {
		fmt.Fprintf(stdout, "%s  %s\n", ui.Dim.Render(label), ui.Tint(ui.Brand, version))
		return
	}
	fmt.Fprintf(stdout, "%-7s %s\n", label+":", version)
}

func printUpgradeStatus(stdout io.Writer, msg string) {
	if richOut(stdout, false) {
		fmt.Fprintf(stdout, "%s %s\n", ui.Tint(ui.Success, "✓"), msg)
		return
	}
	fmt.Fprintln(stdout, msg)
}

func printUpgradeWarning(stderr io.Writer, msg string) {
	if richOut(stderr, false) {
		fmt.Fprintf(stderr, "%s %s\n", ui.Tint(ui.Warn, "!"), msg)
		return
	}
	fmt.Fprintf(stderr, "warning: %s\n", msg)
}

// confirmUpgrade asks the user to confirm the upgrade on stdin. An empty line
// (just Enter) accepts; EOF / no input declines so non-interactive callers that
// forgot -y don't silently upgrade.
func confirmUpgrade(stdout io.Writer, current, latest string) bool {
	fmt.Fprintf(stdout, "%s? [Y/n]: ", upgradePrompt(current, latest))
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "", "y", "yes":
		return true
	default:
		return false
	}
}

func upgradePrompt(current, latest string) string {
	if latest != "" {
		return fmt.Sprintf("upgrade %s -> %s", current, latest)
	}
	return "upgrade prx to the latest release"
}

type githubRelease struct {
	TagName string `json:"tag_name"`
}

func latestReleaseTag(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, githubLatestAPI, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", defaultUserAgent)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = res.Body.Close()
	}()

	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to check latest release: %s", res.Status)
	}
	var release githubRelease
	if err := json.NewDecoder(res.Body).Decode(&release); err != nil {
		return "", err
	}
	if release.TagName == "" {
		return "", fmt.Errorf("latest release has empty tag_name")
	}
	return release.TagName, nil
}

func normalizedVersion(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(strings.ToLower(v), "v")
	return v
}

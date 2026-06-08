package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gate/internal/daemon"
	"gate/internal/listener"
	"gate/internal/ui"
)

const (
	upgradeScriptURL = "https://raw.githubusercontent.com/jinyongp/gate/main/scripts/install.sh"
	githubLatestAPI  = "https://api.github.com/repos/jinyongp/gate/releases/latest"
	defaultUserAgent = "gate-upgrade"
)

var currentVersion = "dev"

var (
	restartDaemonAfterUpgradeFunc = restartDaemonAfterUpgrade
	doctorAfterUpgradeFunc        = doctorReportAfterUpgrade
	upgradeExecutablePathFunc     = executablePath
	upgradeHomebrewUpdateFunc     = func(ctx context.Context) *exec.Cmd {
		return exec.CommandContext(ctx, "brew", "update", "--force", "--quiet")
	}
	upgradeHomebrewCommandFunc = func(ctx context.Context) *exec.Cmd {
		return exec.CommandContext(ctx, "brew", "upgrade", "jinyongp/tap/gate")
	}
	upgradeVersionCommandFunc = func(ctx context.Context, path string) *exec.Cmd {
		return exec.CommandContext(ctx, path, "--version")
	}
)

// SetVersion stores the currently running gate version for upgrade decisions.
func SetVersion(v string) {
	currentVersion = v
}

// Upgrade downloads and executes the upstream install script to replace the current
// gate binary with the latest release.
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

	activity := startActivity(stderr, false, "checking latest release")
	latestTag, err := latestReleaseTag(ctx)
	if err != nil {
		activity.Stop()
		printUpgradeWarning(stderr, "unable to check latest version: "+err.Error())
	} else {
		activity.Complete()
	}

	if latestTag != "" {
		if current := normalizedVersion(currentVersion); current != "" && current != "dev" {
			if normalizedVersion(latestTag) == current {
				return completeUpToDate(stdout, currentVersion)
			}
		}
	} else {
		printUpgradeVersion(stdout, "current", currentVersion)
	}

	if !yes && !confirmUpgrade(stdout, currentVersion, latestTag) {
		printUpgradeCancelled(stdout)
		return ExitOK
	}

	daemonsBefore := daemonStatusesBeforeUpgrade()

	if err := runUpgradeInstall(ctx, stdout, stderr, latestTag); err != nil {
		return fail(stderr, false, ExitError, "upgrade", err.Error())
	}
	return completeUpgrade(stdout, stderr, daemonsBefore)
}

func runUpgradeInstall(ctx context.Context, stdout, stderr io.Writer, expectedVersion string) error {
	_ = stdout
	if isHomebrewGatePath(upgradeExecutablePathFunc()) {
		if err := runUpgradeCommand(stderr, "updating Homebrew taps", "brew update", upgradeHomebrewUpdateFunc(ctx)); err != nil {
			return err
		}
		if err := runUpgradeCommand(stderr, "upgrading Homebrew package", "brew upgrade jinyongp/tap/gate", upgradeHomebrewCommandFunc(ctx)); err != nil {
			return err
		}
		return verifyUpgradedVersion(ctx, expectedVersion)
	}

	scriptPath, err := prepareUpgradeScript(ctx, stderr)
	if err != nil {
		return err
	}
	defer func() {
		_ = os.Remove(scriptPath)
	}()

	if err := runUpgradeCommand(stderr, "installing gate", "install script", upgradeInstallScriptCommand(ctx, scriptPath, upgradeExecutablePathFunc())); err != nil {
		return err
	}
	return verifyUpgradedVersion(ctx, expectedVersion)
}

func upgradeInstallScriptCommand(ctx context.Context, scriptPath, currentExecutable string) *exec.Cmd {
	//nolint:gosec // G204: executing trusted, repo-fixed upgrade script.
	cmd := exec.CommandContext(ctx, "sh", scriptPath)
	if dir := filepath.Dir(strings.TrimSpace(currentExecutable)); dir != "." && dir != "" {
		cmd.Env = append(os.Environ(), "GATE_BIN_DIR="+dir)
	}
	return cmd
}

func verifyUpgradedVersion(ctx context.Context, expectedVersion string) error {
	expected := normalizedVersion(expectedVersion)
	if expected == "" {
		return nil
	}
	path := upgradeExecutablePathFunc()
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("failed to verify upgraded version: current executable path is empty")
	}

	var output bytes.Buffer
	cmd := upgradeVersionCommandFunc(ctx, path)
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		return upgradeCommandError("verify upgraded version", err, output.String())
	}
	got := strings.TrimSpace(output.String())
	if normalizedVersion(got) != expected {
		if got == "" {
			got = "unknown"
		}
		return fmt.Errorf("upgrade did not install %s; current binary reports %s", expectedVersion, got)
	}
	return nil
}

func runUpgradeCommand(stderr io.Writer, label, action string, cmd *exec.Cmd) error {
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	activity := startActivity(stderr, false, label)
	err := cmd.Run()
	if err != nil {
		activity.Stop()
		return upgradeCommandError(action, err, output.String())
	}
	activity.Complete()
	return nil
}

func upgradeCommandError(action string, err error, output string) error {
	output = strings.TrimSpace(output)
	if output == "" {
		return fmt.Errorf("%s: %w", action, err)
	}
	return fmt.Errorf("%s: %w\n%s", action, err, output)
}

func prepareUpgradeScript(ctx context.Context, stderr io.Writer) (string, error) {
	activity := startActivity(stderr, false, "downloading installer")
	completed := false
	defer func() {
		if completed {
			activity.Complete()
		} else {
			activity.Stop()
		}
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, upgradeScriptURL, nil)
	if err != nil {
		return "", err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to download install script: %w", err)
	}
	defer func() {
		_ = res.Body.Close()
	}()

	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to download install script: %s", res.Status)
	}

	script, err := os.CreateTemp("", "gate-upgrade-*.sh")
	if err != nil {
		return "", err
	}
	scriptPath := script.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(scriptPath)
		}
	}()

	if _, err := io.Copy(script, res.Body); err != nil {
		_ = script.Close()
		return "", err
	}
	if err := script.Chmod(0o755); err != nil {
		_ = script.Close()
		return "", err
	}
	if err := script.Close(); err != nil {
		return "", err
	}
	cleanup = false
	completed = true
	return scriptPath, nil
}

func daemonStatusesBeforeUpgrade() []daemon.Status {
	refs, err := allListenerRefs()
	if err != nil {
		refs = []listenerDaemonRef{defaultListenerRef()}
	}
	var statuses []daemon.Status
	for _, ref := range refs {
		st, err := daemonClientForRef(ref).Status()
		if err != nil {
			continue
		}
		st.Scope = ref.String()
		st.ScopeKey = ref.fileKey()
		statuses = append(statuses, st)
	}
	return statuses
}

func completeUpgrade(stdout, stderr io.Writer, daemonsBefore []daemon.Status) int {
	code := ExitOK
	for _, st := range daemonsBefore {
		if nextCode := restartDaemonAfterUpgradeFunc(st, stdout, stderr); nextCode != ExitOK && code == ExitOK {
			code = nextCode
		}
	}
	if code != ExitOK {
		return code
	}
	printUpgradeStatus(stdout, "upgrade complete")
	printDoctorAfterUpgrade(stdout)
	return ExitOK
}

func completeUpToDate(stdout io.Writer, version string) int {
	printUpgradeStatus(stdout, fmt.Sprintf("up to date (%s)", version))
	return ExitOK
}

func doctorReportAfterUpgrade() doctorReport {
	report := doctorReport{Issues: runDoctorChecks(false)}
	report.OK = doctorReportOK(report)
	return report
}

func printDoctorAfterUpgrade(stdout io.Writer) {
	report := doctorAfterUpgradeFunc()
	if richOut(stdout, false) {
		fmt.Fprintln(stdout)
		fmt.Fprintln(stdout, ui.Section("doctor"))
	} else {
		fmt.Fprintln(stdout)
		fmt.Fprintln(stdout, "doctor")
	}
	printDoctorReport(stdout, report, false)
}

func restartDaemonAfterUpgrade(st daemon.Status, stdout, stderr io.Writer) int {
	ref := listenerRefFromDaemonStatus(st)
	client := daemonClientForRef(ref)
	activity := startActivity(stderr, false, "restarting daemon")
	if err := stopDaemonProcess(client, st.PID, 5*time.Second); err != nil {
		activity.Stop()
		return fail(stderr, false, ExitError, "upgrade", "failed to restart daemon: "+err.Error())
	}

	httpsAddr := restartListenAddr(st.HTTPSAddr, defaultDaemonHTTPSAddr)
	httpAddr := restartListenAddr(st.HTTPAddr, defaultDaemonHTTPAddr)
	pair := listener.FromFlags(httpsAddr, httpAddr)
	ref = listenerRefFor(pair)
	client = daemonClientForRef(ref)
	result := startDaemonCommand(newDaemonServeCommand(executablePath(), ref.socketPath(), httpsAddr, httpAddr), client, ref)
	if result.Code != ExitOK {
		activity.Stop()
		return fail(stderr, false, result.Code, "upgrade", "failed to restart daemon: "+result.Message)
	}
	if err := setListenerRoutesForRef(ref); err != nil {
		cleanupStartedDaemon(client, ref, result.PID)
		activity.Stop()
		return fail(stderr, false, ExitError, "upgrade", "failed to reload daemon routes: "+err.Error())
	}
	activity.Complete()
	printDaemonRunResult(stdout, "daemon restarted", result.PID, httpsAddr, httpAddr)
	return ExitOK
}

func listenerRefFromDaemonStatus(st daemon.Status) listenerDaemonRef {
	if st.HTTPSAddr != "" || st.HTTPAddr != "" {
		return listenerRefFor(listener.FromFlags(
			restartListenAddr(st.HTTPSAddr, defaultDaemonHTTPSAddr),
			restartListenAddr(st.HTTPAddr, defaultDaemonHTTPAddr),
		))
	}
	if strings.HasPrefix(st.ScopeKey, "listener-") {
		return listenerDaemonRef{Key: listener.Key(strings.TrimPrefix(st.ScopeKey, "listener-"))}
	}
	if strings.HasPrefix(st.Scope, "listener:") {
		return listenerDaemonRef{Key: listener.Key(strings.TrimPrefix(st.Scope, "listener:"))}
	}
	return defaultListenerRef()
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
	printSuccess(stdout, msg)
}

func printUpgradeCancelled(stdout io.Writer) {
	printCancelled(stdout, "upgrade")
}

func printUpgradeWarning(stderr io.Writer, msg string) {
	printWarning(stderr, msg)
}

// confirmUpgrade asks the user to confirm the upgrade on stdin. An empty line
// (just Enter) accepts; EOF / no input declines so non-interactive callers that
// forgot -y don't silently upgrade.
func confirmUpgrade(stdout io.Writer, current, latest string) bool {
	confirmed, err := confirmUpgradePrompt(bufio.NewReader(os.Stdin), stdout, current, latest)
	if err != nil {
		return false
	}
	return confirmed
}

func confirmUpgradePrompt(reader *bufio.Reader, stdout io.Writer, current, latest string) (bool, error) {
	if _, err := fmt.Fprint(stdout, renderUpgradePromptIntro(stdout, current, latest)); err != nil {
		return false, err
	}
	value, err := promptChoice(reader, stdout, "Upgrade now?", "yes", []string{"yes", "no"})
	if err != nil {
		return false, err
	}
	return value == "yes", nil
}

func renderUpgradePromptIntro(stdout io.Writer, current, latest string) string {
	if richOut(stdout, false) {
		return renderUpgradePromptIntroRich(current, latest)
	}
	return renderUpgradePromptIntroPlain(current, latest)
}

func renderUpgradePromptIntroRich(current, latest string) string {
	if latest != "" {
		return fmt.Sprintf("%s %s\n  %s  %s\n  %s   %s\n\n",
			ui.Tint(ui.Warn, "!"),
			"upgrade available",
			ui.Dim.Render("current"),
			current,
			ui.Dim.Render("latest"),
			ui.Tint(ui.Brand, latest),
		)
	}
	return fmt.Sprintf("%s %s\n%s\n\n",
		ui.Tint(ui.Warn, "!"),
		"upgrade available",
		ui.Dim.Render("gate can install the latest release"),
	)
}

func renderUpgradePromptIntroPlain(current, latest string) string {
	if latest != "" {
		return fmt.Sprintf("A newer gate release is available.\nCurrent version: %s\nLatest version: %s\n\n", current, latest)
	}
	return "gate can install the latest release.\n\n"
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

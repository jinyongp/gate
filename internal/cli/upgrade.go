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

	"gate/internal/daemon"
	"gate/internal/ui"
)

const (
	upgradeScriptURL = "https://raw.githubusercontent.com/jinyongp/gate/main/scripts/install.sh"
	githubLatestAPI  = "https://api.github.com/repos/jinyongp/gate/releases/latest"
	defaultUserAgent = "gate-upgrade"
)

var currentVersion = "dev"

var restartDaemonAfterUpgradeFunc = restartDaemonAfterUpgrade

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
	activity.Stop()
	if err != nil {
		printUpgradeWarning(stderr, "unable to check latest version: "+err.Error())
	}

	if latestTag != "" {
		if current := normalizedVersion(currentVersion); current != "" && current != "dev" {
			if normalizedVersion(latestTag) == current {
				daemonsBefore := daemonStatusesBeforeUpgrade()
				return completeUpToDate(stdout, stderr, currentVersion, daemonsBefore)
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

	scriptPath, err := prepareUpgradeScript(ctx, stderr)
	if err != nil {
		return fail(stderr, false, ExitError, "upgrade", err.Error())
	}
	defer func() {
		_ = os.Remove(scriptPath)
	}()

	//nolint:gosec // G204: executing trusted, repo-fixed upgrade script.
	cmd := exec.Command("sh", scriptPath)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return fail(stderr, false, ExitError, "upgrade", err.Error())
	}
	return completeUpgrade(stdout, stderr, daemonsBefore)
}

func prepareUpgradeScript(ctx context.Context, stderr io.Writer) (string, error) {
	activity := startActivity(stderr, false, "downloading installer")
	defer activity.Stop()

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
	return scriptPath, nil
}

func daemonStatusesBeforeUpgrade() []daemon.Status {
	scopes, err := allDaemonScopes()
	if err != nil {
		scope, serr := currentDaemonScope()
		if serr != nil {
			scope = globalDaemonScope()
		}
		scopes = []daemonScope{scope}
	}
	var statuses []daemon.Status
	for _, scope := range scopes {
		st, err := daemonClientFor(scope).Status()
		if err != nil {
			continue
		}
		st.Scope = scope.String()
		st.ScopeKey = scope.fileKey()
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
	return ExitOK
}

func completeUpToDate(stdout, stderr io.Writer, version string, daemonsBefore []daemon.Status) int {
	code := ExitOK
	for _, st := range daemonsBefore {
		if nextCode := restartDaemonAfterUpgradeFunc(st, stdout, stderr); nextCode != ExitOK && code == ExitOK {
			code = nextCode
		}
	}
	if code != ExitOK {
		return code
	}
	printUpgradeStatus(stdout, fmt.Sprintf("up to date (%s)", version))
	return ExitOK
}

func restartDaemonAfterUpgrade(st daemon.Status, stdout, stderr io.Writer) int {
	scope := scopeFromDaemonStatus(st)
	client := daemonClientFor(scope)
	activity := startActivity(stderr, false, "restarting daemon")
	if err := stopDaemonProcess(client, st.PID, 5*time.Second); err != nil {
		activity.Stop()
		return fail(stderr, false, ExitError, "upgrade", "failed to restart daemon: "+err.Error())
	}

	httpsAddr := restartListenAddr(st.HTTPSAddr, defaultDaemonHTTPSAddr)
	httpAddr := restartListenAddr(st.HTTPAddr, defaultDaemonHTTPAddr)
	result := startDaemonCommand(newDaemonServeCommand(executablePath(), scope.socketPath(), httpsAddr, httpAddr), client, scope)
	if result.Code != ExitOK {
		activity.Stop()
		return fail(stderr, false, result.Code, "upgrade", "failed to restart daemon: "+result.Message)
	}
	if err := setDaemonRoutesForScope(scope); err != nil {
		cleanupStartedDaemon(client, scope, result.PID)
		activity.Stop()
		return fail(stderr, false, ExitError, "upgrade", "failed to reload daemon routes: "+err.Error())
	}
	activity.Stop()
	fmt.Fprintf(stdout, "daemon restarted · pid %d · https %s · http %s\n", result.PID, httpsAddr, httpAddr)
	return ExitOK
}

func scopeFromDaemonStatus(st daemon.Status) daemonScope {
	if st.ScopeKey != "" {
		if strings.HasPrefix(st.ScopeKey, "project-") {
			return daemonScope{Kind: daemonScopeProject, Name: strings.TrimPrefix(st.Scope, "project:"), Key: st.ScopeKey}
		}
		return daemonScope{Kind: daemonScopeGlobal, Key: st.ScopeKey}
	}
	if strings.HasPrefix(st.Scope, "project:") {
		return projectDaemonScope(strings.TrimPrefix(st.Scope, "project:"))
	}
	return globalDaemonScope()
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

func printUpgradeCancelled(stdout io.Writer) {
	printCancelled(stdout, "upgrade")
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
	value, err := promptInput(reader, stdout, promptInputSpec{
		Label:       "Upgrade now?",
		Default:     "yes",
		Placeholder: "yes",
		Normalize:   normalizeConfirmAnswer,
		Validate:    validateConfirmAnswer,
	})
	if err != nil {
		return false, err
	}
	return value == "yes", nil
}

func normalizeConfirmAnswer(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "y":
		return "yes"
	case "n":
		return "no"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func validateConfirmAnswer(value string) error {
	if value == "yes" || value == "no" {
		return nil
	}
	return fmt.Errorf("type yes to upgrade, or no to cancel")
}

func renderUpgradePromptIntro(stdout io.Writer, current, latest string) string {
	if richOut(stdout, false) {
		return renderUpgradePromptIntroRich(current, latest)
	}
	return renderUpgradePromptIntroPlain(current, latest)
}

func renderUpgradePromptIntroRich(current, latest string) string {
	if latest != "" {
		return fmt.Sprintf("%s\n  %s  %s\n  %s   %s\n\n",
			ui.Header.Render("Upgrade available"),
			ui.Dim.Render("current"),
			ui.Dim.Render(current),
			ui.Dim.Render("latest"),
			ui.Tint(ui.Brand, latest),
		)
	}
	return fmt.Sprintf("%s\n%s\n\n",
		ui.Header.Render("Upgrade available"),
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

package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"time"
)

const upgradeScriptURL = "https://raw.githubusercontent.com/jinyongp/prx/main/scripts/install.sh"

// Upgrade downloads and executes the upstream install script to replace the current
// prx binary with the latest release.
func Upgrade(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("upgrade", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return parseExit(err)
	}
	if fs.NArg() != 0 {
		return fail(stderr, false, ExitUsage, "usage", "usage: prx upgrade")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, upgradeScriptURL, nil)
	if err != nil {
		return fail(stderr, false, ExitError, "upgrade", err.Error())
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return fail(stderr, false, ExitError, "upgrade", "failed to download install script: "+err.Error())
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return fail(stderr, false, ExitError, "upgrade", fmt.Sprintf("failed to download install script: %s", res.Status))
	}

	script, err := os.CreateTemp("", "prx-upgrade-*.sh")
	if err != nil {
		return fail(stderr, false, ExitError, "upgrade", err.Error())
	}
	defer os.Remove(script.Name())

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
	fmt.Fprintln(stdout, "upgrade complete")
	return ExitOK
}

package cli

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"gate/internal/ca"
	"gate/internal/dns"
	"gate/internal/paths"
	"gate/internal/ui"
)

var (
	uninstallExecutablePathFunc = executablePath
	uninstallRunHomebrewFunc    = runHomebrewUninstall
	uninstallHostsPath          = "/etc/hosts"
	uninstallSystemBinPaths     = []string{"/usr/local/bin/gate"}
)

// Uninstall removes gate's local machine state and, for Homebrew installs,
// uninstalls the Homebrew package as the final step.
func Uninstall(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	var yes bool
	keepTrust := fs.Bool("keep-trust", false, "leave trust store entries in place")
	keepBrew := fs.Bool("keep-brew", false, "do not run brew uninstall for Homebrew installs")
	fs.BoolVar(&yes, "yes", false, "uninstall without the confirmation prompt")
	fs.BoolVar(&yes, "y", false, "uninstall without the confirmation prompt")
	if handled, code := parseFlags(fs, "uninstall", args, stdout, stderr); handled {
		return code
	}
	if fs.NArg() != 0 {
		return usageFail(stderr, false, "uninstall")
	}

	targets := collectUninstallTargets()
	actions := collectUninstallActions(*keepTrust, !*keepBrew && isCurrentHomebrewGate())
	if len(targets) == 0 && len(actions) == 0 {
		printInfo(stdout, "No gate installation artifacts found.")
		return ExitOK
	}
	if !yes {
		printUninstallPlan(stdout, targets, actions)
		if !confirmUninstall(stdout) {
			printCancelled(stdout, "uninstall")
			return ExitOK
		}
	}

	failed := false
	permissionFailed := false
	found := false
	recordStep := func(step uninstallStep) {
		switch step {
		case uninstallStepFailed:
			failed = true
		case uninstallStepPermission:
			failed = true
			permissionFailed = true
		case uninstallStepChanged:
			found = true
		case uninstallStepNoop:
		}
	}
	if !*keepTrust {
		switch ok := uninstallTrust(stdout, stderr); ok {
		case uninstallStepFailed:
			printError(stderr, "gate uninstall completed with errors.")
			return ExitError
		case uninstallStepPermission:
			printError(stderr, "gate uninstall completed with errors.")
			return ExitPerm
		case uninstallStepChanged:
			found = true
		case uninstallStepNoop:
		}
	}
	recordStep(cleanupPathBlocks(stdout, stderr))
	recordStep(cleanupHostsBlock(stdout, stderr))
	recordStep(stopAllKnownDaemons(stdout, stderr))
	if len(targets) > 0 {
		if removeTargets(targets, stdout, stderr) {
			found = true
		} else {
			failed = true
		}
	}
	if !*keepBrew && isCurrentHomebrewGate() {
		if err := uninstallRunHomebrewFunc(stdout, stderr); err != nil {
			printError(stderr, "failed to uninstall Homebrew package: "+err.Error())
			failed = true
		} else {
			printUninstallStep(stdout, "removed Homebrew package gate")
			found = true
		}
	}

	if failed {
		printError(stderr, "gate uninstall completed with errors.")
		if permissionFailed {
			return ExitPerm
		}
		return ExitError
	}
	if !found {
		printInfo(stdout, "No gate installation artifacts found.")
		return ExitOK
	}
	printOK(stdout, "gate uninstalled.")
	return ExitOK
}

type uninstallStep int

const (
	uninstallStepNoop uninstallStep = iota
	uninstallStepChanged
	uninstallStepFailed
	uninstallStepPermission
)

func collectUninstallTargets() []string {
	seen := map[string]bool{}
	add := func(path string) {
		if strings.TrimSpace(path) == "" {
			return
		}
		if isHomebrewGatePath(path) {
			return
		}
		//nolint:gosec // Uninstall targets are fixed gate-owned paths or explicit GATE_BIN_DIR input.
		if _, err := os.Lstat(path); err == nil {
			seen[path] = true
		}
	}
	add(paths.ConfigDir())
	add(paths.DataDir())
	add(paths.StateDir())
	if binDir := os.Getenv("GATE_BIN_DIR"); binDir != "" {
		add(filepath.Join(binDir, "gate"))
	}
	home, err := os.UserHomeDir()
	if err == nil {
		add(filepath.Join(home, ".local", "bin", "gate"))
	}
	for _, binPath := range uninstallSystemBinPaths {
		if !isHomebrewGatePath(binPath) {
			add(binPath)
		}
	}

	out := make([]string, 0, len(seen))
	for path := range seen {
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

func collectUninstallActions(keepTrust, removeBrew bool) []string {
	var actions []string
	if !keepTrust {
		if _, err := ca.LoadCertificate(paths.DataDir()); err == nil {
			actions = append(actions, "trust store entry for gate root CA")
		}
	}
	if hasHostsBlock() {
		actions = append(actions, "managed hosts block in "+uninstallHostsPath)
	}
	for _, rc := range gateShellStartupFiles() {
		if fileContains(rc, "# >>> gate PATH >>>") {
			actions = append(actions, "gate PATH block in "+rc)
		}
	}
	if removeBrew {
		actions = append(actions, "Homebrew package gate")
	}
	return actions
}

func printUninstallPlan(stdout io.Writer, targets, actions []string) {
	if richOut(stdout, false) {
		printUninstallPlanRich(stdout, targets, actions)
		return
	}
	printUninstallPlanPlain(stdout, targets, actions)
}

func printUninstallPlanPlain(stdout io.Writer, targets, actions []string) {
	fmt.Fprintln(stdout, "Discovered artifacts")
	if len(targets) > 0 {
		fmt.Fprintln(stdout, "  Existing paths to remove:")
		for _, target := range targets {
			fmt.Fprintf(stdout, "  - %s\n", target)
		}
	}
	if len(actions) > 0 {
		fmt.Fprintln(stdout, "  Cleanup actions:")
		for _, action := range actions {
			fmt.Fprintf(stdout, "  - %s\n", action)
		}
	}
}

func printUninstallPlanRich(stdout io.Writer, targets, actions []string) {
	fmt.Fprintln(stdout, ui.Header.Render("Discovered artifacts"))
	if len(targets) > 0 {
		fmt.Fprintf(stdout, "  %s\n", ui.Dim.Render("Existing paths to remove"))
		for _, target := range targets {
			fmt.Fprintf(stdout, "  %s %s\n", ui.Tint(ui.Brand, "-"), target)
		}
	}
	if len(actions) > 0 {
		fmt.Fprintf(stdout, "  %s\n", ui.Dim.Render("Cleanup actions"))
		for _, action := range actions {
			fmt.Fprintf(stdout, "  %s %s\n", ui.Tint(ui.Brand, "-"), action)
		}
	}
}

func confirmUninstall(stdout io.Writer) bool {
	confirmed, err := confirmUninstallPrompt(bufio.NewReader(os.Stdin), stdout)
	if err != nil {
		return false
	}
	return confirmed
}

func confirmUninstallPrompt(reader *bufio.Reader, stdout io.Writer) (bool, error) {
	fmt.Fprintln(stdout)
	value, err := promptChoice(reader, stdout, "Proceed with uninstall?", "no", []string{"no", "yes"})
	if err != nil {
		return false, err
	}
	return value == "yes", nil
}

func printUninstallStep(stdout io.Writer, message string) {
	if richOut(stdout, false) {
		fmt.Fprintf(stdout, "%s %s\n", ui.Tint(ui.Success, "ok:"), message)
		return
	}
	fmt.Fprintln(stdout, message)
}

func uninstallTrust(stdout, stderr io.Writer) uninstallStep {
	authority, err := ca.LoadCertificate(paths.DataDir())
	if errors.Is(err, ca.ErrNotFound) {
		return uninstallStepNoop
	}
	if err != nil {
		printError(stderr, "failed to load gate root CA: "+err.Error())
		return uninstallStepFailed
	}
	if err := untrustAuthorityFunc(authority); err != nil {
		printError(stderr, "failed to remove trusted gate root CA: "+err.Error())
		if os.IsPermission(err) || errors.Is(err, os.ErrPermission) {
			return uninstallStepPermission
		}
		return uninstallStepFailed
	}
	printUninstallStep(stdout, "removed trusted gate root CA")
	return uninstallStepChanged
}

func cleanupPathBlocks(stdout, stderr io.Writer) uninstallStep {
	result := uninstallStepNoop
	for _, rc := range gateShellStartupFiles() {
		changed, err := removeMarkedBlock(rc, "# >>> gate PATH >>>", "# <<< gate PATH <<<")
		if err != nil {
			printError(stderr, fmt.Sprintf("failed to remove gate PATH block from %s: %v", rc, err))
			result = uninstallStepFailed
			continue
		}
		if changed && result != uninstallStepFailed {
			printUninstallStep(stdout, "removed gate PATH block from "+rc)
			result = uninstallStepChanged
		}
	}
	return result
}

func cleanupHostsBlock(stdout, stderr io.Writer) uninstallStep {
	if !hasHostsBlock() {
		return uninstallStepNoop
	}
	if err := (dns.Hosts{Path: uninstallHostsPath}).RemoveManagedBlock(); err != nil {
		printError(stderr, fmt.Sprintf("failed to remove gate block from %s: %v", uninstallHostsPath, err))
		if os.IsPermission(err) || errors.Is(err, os.ErrPermission) {
			return uninstallStepPermission
		}
		return uninstallStepFailed
	}
	printUninstallStep(stdout, "removed gate block from "+uninstallHostsPath)
	return uninstallStepChanged
}

func stopAllKnownDaemons(stdout, stderr io.Writer) uninstallStep {
	refs, err := allListenerRefs()
	if err != nil {
		printError(stderr, "failed to list daemons: "+err.Error())
		return uninstallStepFailed
	}
	result := uninstallStepNoop
	for _, ref := range refs {
		client := daemonClientForRef(ref)
		if _, err := client.Status(); err != nil {
			if _, statErr := os.Stat(ref.pidPath()); statErr != nil {
				continue
			}
		}
		if code := daemonStopRef(ref, stdout, stderr, len(refs) > 1); code != ExitOK {
			result = uninstallStepFailed
			continue
		}
		if result != uninstallStepFailed {
			result = uninstallStepChanged
		}
	}
	return result
}

func removeTargets(targets []string, stdout, stderr io.Writer) bool {
	ok := true
	for _, target := range targets {
		if _, err := os.Lstat(target); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			printError(stderr, fmt.Sprintf("failed to inspect %s: %v", target, err))
			ok = false
			continue
		}
		if err := os.RemoveAll(target); err != nil {
			printError(stderr, fmt.Sprintf("failed to remove %s: %v", target, err))
			ok = false
			continue
		}
		printUninstallStep(stdout, "removed "+target)
	}
	return ok
}

func gateShellStartupFiles() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return []string{
		filepath.Join(home, ".zshrc"),
		filepath.Join(home, ".bashrc"),
		filepath.Join(home, ".bash_profile"),
		filepath.Join(home, ".bash_login"),
		filepath.Join(home, ".profile"),
		filepath.Join(home, ".config", "fish", "config.fish"),
	}
}

func hasHostsBlock() bool {
	return fileContains(uninstallHostsPath, "# >>> gate managed >>>")
}

func fileContains(path, needle string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.Contains(string(b), needle)
}

func removeMarkedBlock(path, begin, end string) (bool, error) {
	changed, next, err := removeMarkedBlockBytes(path, begin, end)
	if err != nil || !changed {
		return changed, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	return true, os.WriteFile(path, next, info.Mode().Perm())
}

func removeMarkedBlockBytes(path, begin, end string) (bool, []byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil, nil
		}
		return false, nil, err
	}
	lines := strings.SplitAfter(string(b), "\n")
	var out strings.Builder
	changed := false
	ended := false
	skip := false
	for _, line := range lines {
		text := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		switch {
		case text == begin:
			if skip {
				return false, nil, fmt.Errorf("nested block starting %q", begin)
			}
			skip = true
			changed = true
			continue
		case text == end && skip:
			skip = false
			ended = true
			continue
		case !skip:
			out.WriteString(line)
		}
	}
	if skip || (changed && !ended) {
		return false, nil, fmt.Errorf("unterminated block starting %q", begin)
	}
	if !changed {
		return false, nil, nil
	}
	return true, []byte(out.String()), nil
}

func isCurrentHomebrewGate() bool {
	return isHomebrewGatePath(uninstallExecutablePathFunc())
}

func isHomebrewGatePath(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	path = filepath.ToSlash(path)
	return strings.Contains(path, "/Cellar/gate/") && strings.HasSuffix(path, "/bin/gate")
}

func runHomebrewUninstall(stdout, stderr io.Writer) error {
	cmd := exec.Command("brew", "uninstall", "gate")
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

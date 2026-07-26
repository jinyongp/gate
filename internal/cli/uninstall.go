package cli

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"gate/internal/ca"
	"gate/internal/config"
	"gate/internal/dns"
	"gate/internal/expose"
	"gate/internal/fsutil"
	"gate/internal/paths"
	"gate/internal/ui"
)

var (
	errUninstallCoordinationBusy            = errors.New("uninstall coordination is busy")
	uninstallExecutablePathFunc             = executablePath
	uninstallRunHomebrewFunc                = runHomebrewUninstall
	uninstallStopExposuresFunc              = stopAllKnownExposures
	uninstallStopDaemonsFunc                = stopAllKnownDaemons
	uninstallHasCapabilityArtifactsFunc     = platformHasLowPortCapabilityArtifacts
	uninstallCleanupCapabilityArtifactsFunc = platformCleanupLowPortCapabilityArtifacts
	uninstallAcquireCapabilityLockFunc      = platformAcquireUninstallCapabilityLock
	uninstallAcquireInstallLocksFunc        = platformAcquireStandaloneInstallLocks
	uninstallHostsPath                      = "/etc/hosts"
	uninstallSystemBinPaths                 = []string{"/usr/local/bin/gate"}
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
	restoreIsolatedRoot, err := normalizeUninstallIsolatedRoot()
	if err != nil {
		return fail(stderr, false, ExitUsage, "bad_isolated_root", err.Error())
	}
	defer restoreIsolatedRoot()
	if binDir := strings.TrimSpace(os.Getenv("GATE_BIN_DIR")); binDir != "" && !filepath.IsAbs(binDir) {
		return fail(stderr, false, ExitUsage, "bad_bin_dir", "GATE_BIN_DIR must be an absolute path")
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
	unlock, code := acquireStateMutation(stderr, false)
	if code != ExitOK {
		return code
	}
	defer unlock()
	capabilityLock, err := uninstallAcquireCapabilityLockFunc()
	if err != nil {
		var capabilityErr *lowPortCapabilityError
		if errors.As(err, &capabilityErr) && capabilityErr.Code == "capability_setup_busy" {
			return fail(stderr, false, ExitConflict, capabilityErr.Code, err.Error())
		}
		printError(stderr, "failed to lock Linux low-port setup during uninstall: "+err.Error())
		return ExitError
	}
	defer func() { _ = capabilityLock.Close() }()
	targets = collectUninstallTargets()
	installLocks, err := uninstallAcquireInstallLocksFunc(standaloneUninstallGatePaths())
	if err != nil {
		if errors.Is(err, errUninstallCoordinationBusy) {
			return fail(stderr, false, ExitConflict, "install_busy", err.Error())
		}
		printError(stderr, "failed to lock standalone gate installation during uninstall: "+err.Error())
		return ExitError
	}
	defer func() {
		for i := len(installLocks) - 1; i >= 0; i-- {
			_ = installLocks[i].Close()
		}
	}()

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
	exposureResult := uninstallStopExposuresFunc(stdout, stderr)
	recordStep(exposureResult)
	if exposureResult == uninstallStepFailed || exposureResult == uninstallStepPermission {
		printError(stderr, "gate uninstall stopped before deleting files because an exposure could not be stopped.")
		return ExitError
	}
	stopResult := uninstallStopDaemonsFunc(stdout, stderr)
	recordStep(stopResult)
	if stopResult == uninstallStepFailed || stopResult == uninstallStepPermission {
		printError(stderr, "gate uninstall stopped before deleting files because a daemon could not be stopped.")
		if stopResult == uninstallStepPermission {
			return ExitPerm
		}
		return ExitError
	}
	if os.Getenv("GATE_ISOLATED_ROOT") == "" {
		capabilityResult := uninstallCleanupCapabilityArtifactsFunc(stdout, stderr)
		recordStep(capabilityResult)
		if capabilityResult == uninstallStepFailed || capabilityResult == uninstallStepPermission {
			printError(stderr, "gate uninstall stopped before deleting files because interrupted low-port setup state could not be removed.")
			if capabilityResult == uninstallStepPermission {
				return ExitPerm
			}
			return ExitError
		}
		recordStep(cleanupPathBlocks(stdout, stderr))
		recordStep(cleanupHostsBlock(stdout, stderr))
	}
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
		if _, err := os.Lstat(path); err == nil {
			seen[path] = true
		}
	}
	add(paths.ConfigDir())
	add(paths.DataDir())
	add(paths.StateDir())
	add(paths.RuntimeDir())
	for _, path := range standaloneUninstallGatePaths() {
		add(path)
		if runtime.GOOS == "linux" {
			add(path + ".install.transaction")
		}
	}

	out := make([]string, 0, len(seen))
	for path := range seen {
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

func standaloneUninstallGatePaths() []string {
	seen := map[string]bool{}
	var out []string
	add := func(path string) {
		if strings.TrimSpace(path) == "" || seen[path] || isHomebrewGatePath(path) {
			return
		}
		seen[path] = true
		out = append(out, path)
	}
	if root := os.Getenv("GATE_ISOLATED_ROOT"); root != "" {
		if binDir := os.Getenv("GATE_BIN_DIR"); binDir != "" {
			canonicalBinDir, err := filepath.EvalSymlinks(binDir)
			if errors.Is(err, os.ErrNotExist) {
				return out
			}
			path := filepath.Join(canonicalBinDir, "gate")
			if rel, relErr := filepath.Rel(root, path); err == nil && relErr == nil &&
				rel != "." && rel != ".." &&
				!strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				add(path)
			}
		}
		sort.Strings(out)
		return out
	}
	if binDir := os.Getenv("GATE_BIN_DIR"); binDir != "" {
		//nolint:gosec // GATE_BIN_DIR is explicit user input already constrained to an absolute path.
		if info, err := os.Stat(binDir); err == nil && info.IsDir() {
			add(filepath.Join(binDir, "gate"))
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		binDir := filepath.Join(home, ".local", "bin")
		if info, statErr := os.Stat(binDir); statErr == nil && info.IsDir() {
			add(filepath.Join(binDir, "gate"))
		}
	}
	for _, binPath := range uninstallSystemBinPaths {
		add(binPath)
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
	if os.Getenv("GATE_ISOLATED_ROOT") == "" {
		if hasHostsBlock() {
			actions = append(actions, "managed hosts block in "+uninstallHostsPath)
		}
		for _, rc := range gateShellStartupFiles() {
			if fileContains(rc, "# >>> gate PATH >>>") {
				actions = append(actions, "gate PATH block in "+rc)
			}
		}
	}
	if removeBrew {
		actions = append(actions, "Homebrew package gate")
	}
	if os.Getenv("GATE_ISOLATED_ROOT") == "" && uninstallHasCapabilityArtifactsFunc() {
		actions = append(actions, "interrupted Linux low-port setup helper")
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
	return promptConfirm(reader, stdout, "Proceed with uninstall?")
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

func stopAllKnownExposures(stdout, stderr io.Writer) uninstallStep {
	records, err := exposureStore().Read()
	if err != nil {
		printError(stderr, "failed to list exposures: "+err.Error())
		return uninstallStepFailed
	}
	if len(records) == 0 {
		return uninstallStepNoop
	}
	blocked := map[string]bool{}
	for _, record := range records {
		blocked[config.CanonicalDomain(record.Target)] = true
		if alias := exposurePublicHost(record); alias != "" {
			blocked[config.CanonicalDomain(alias)] = true
		}
	}
	if err := reloadExposureRecordsTransitionBlocked(records, records, blocked, stderr, false); err != nil {
		printError(stderr, "failed to block exposure routes: "+err.Error())
		return uninstallStepFailed
	}
	for _, record := range records {
		provider, err := exposeProviderFor(record.Provider)
		if err != nil {
			_ = reloadExposureRecordsTransition(records, records, stderr, false)
			printError(stderr, "failed to load exposure provider: "+err.Error())
			return uninstallStepFailed
		}
		status, statusErr := provider.Status(context.Background(), record)
		if statusErr != nil {
			_ = provider.Close()
			_ = reloadExposureRecordsTransition(records, records, stderr, false)
			printError(stderr, fmt.Sprintf("failed to verify %s exposure for %s: %v", record.Provider, record.Service, statusErr))
			return uninstallStepFailed
		}
		if status != expose.StatusDown {
			err = provider.Stop(context.Background(), record, expose.StopOpts{})
		}
		_ = provider.Close()
		if err != nil {
			_ = reloadExposureRecordsTransition(records, records, stderr, false)
			printError(stderr, fmt.Sprintf("failed to stop %s exposure for %s: %v", record.Provider, record.Service, err))
			return uninstallStepFailed
		}
	}
	if err := exposureStore().Write(nil); err != nil {
		printError(stderr, "failed to clear exposure state: "+err.Error())
		return uninstallStepFailed
	}
	printUninstallStep(stdout, "stopped active exposures")
	return uninstallStepChanged
}

func removeTargets(targets []string, stdout, stderr io.Writer) bool {
	ok := true
	for _, target := range targets {
		if err := validateUninstallTarget(target); err != nil {
			printError(stderr, fmt.Sprintf("refusing unsafe uninstall target %s: %v", target, err))
			ok = false
			continue
		}
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

func normalizeUninstallIsolatedRoot() (func(), error) {
	raw := os.Getenv("GATE_ISOLATED_ROOT")
	if raw == "" {
		return func() {}, nil
	}
	canonical, err := paths.ValidateIsolatedRoot(raw)
	if err != nil {
		return nil, err
	}
	if canonical == raw {
		return func() {}, nil
	}
	if err := os.Setenv("GATE_ISOLATED_ROOT", canonical); err != nil {
		return nil, err
	}
	return func() { _ = os.Setenv("GATE_ISOLATED_ROOT", raw) }, nil
}

func validateUninstallTarget(target string) error {
	abs, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	abs = filepath.Clean(abs)
	canonicalTarget := abs
	if resolved, resolveErr := filepath.EvalSymlinks(abs); resolveErr == nil {
		canonicalTarget = filepath.Clean(resolved)
	}
	volumeRoot := filepath.Clean(filepath.VolumeName(abs) + string(filepath.Separator))
	if abs == volumeRoot || canonicalTarget == volumeRoot {
		return errors.New("filesystem root is never gate-owned")
	}
	if rawRoot := os.Getenv("GATE_ISOLATED_ROOT"); rawRoot != "" {
		rawRootAbs, absErr := filepath.Abs(rawRoot)
		if absErr != nil {
			return absErr
		}
		if abs == filepath.Clean(rawRootAbs) {
			return errors.New("isolated root itself is never a removable target")
		}
		root, err := paths.ValidateIsolatedRoot(rawRoot)
		if err != nil {
			return err
		}
		if canonicalTarget == root {
			return errors.New("isolated root itself is never a removable target")
		}
		rel, err := filepath.Rel(root, canonicalTarget)
		if err != nil || rel == "." || rel == ".." ||
			strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return errors.New("isolated uninstall target resolves outside its root")
		}
		for _, statePath := range []string{paths.ConfigDir(), paths.DataDir(), paths.StateDir(), paths.RuntimeDir()} {
			stateAbs, err := filepath.Abs(statePath)
			if err != nil {
				return err
			}
			if canonicalTarget != filepath.Clean(stateAbs) {
				continue
			}
			rel, err := filepath.Rel(root, canonicalTarget)
			if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return errors.New("isolated state target is not a strict descendant of its root")
			}
			return nil
		}
	}
	return nil
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
	writePath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false, err
	}
	info, err := os.Stat(writePath)
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("shell startup target is not a regular file: %s", writePath)
	}
	return true, fsutil.WriteAtomic(writePath, next, info.Mode().Perm())
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

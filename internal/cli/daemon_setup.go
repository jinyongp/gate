package cli

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const lowPortCapability = "cap_net_bind_service=ep"

var errLowPortCapabilityUnsupported = errors.New("low-port capability setup is available on Linux only")

type lowPortCapabilityState string

const (
	lowPortCapabilityMissing    lowPortCapabilityState = "missing"
	lowPortCapabilityConfigured lowPortCapabilityState = "configured"
	lowPortCapabilityUnexpected lowPortCapabilityState = "unexpected"
)

type lowPortCapabilityInspection struct {
	State lowPortCapabilityState
	Raw   string
}

type lowPortCapabilityManager interface {
	Inspect(target *lowPortCapabilityTarget) (lowPortCapabilityInspection, error)
	Apply(target *lowPortCapabilityTarget) error
}

type lowPortCapabilityTarget struct {
	Path string
	File *os.File
	Info os.FileInfo
}

func (t *lowPortCapabilityTarget) Close() {
	if t != nil && t.File != nil {
		_ = t.File.Close()
	}
}

func (t *lowPortCapabilityTarget) validateIdentity() error {
	if t == nil || t.File == nil || t.Info == nil {
		return nil
	}
	opened, err := t.File.Stat()
	if err != nil || !os.SameFile(t.Info, opened) {
		return &lowPortCapabilityError{Code: "capability_target_changed", Err: capabilityIdentityError(err)}
	}
	current, err := os.Stat(t.Path)
	if err != nil || !os.SameFile(t.Info, current) {
		return &lowPortCapabilityError{Code: "capability_target_changed", Err: capabilityIdentityError(err)}
	}
	return nil
}

func capabilityIdentityError(err error) error {
	if err != nil {
		return fmt.Errorf("gate executable changed during capability setup: %w", err)
	}
	return fmt.Errorf("gate executable changed during capability setup")
}

type lowPortCapabilityError struct {
	Code string
	Err  error
}

func (e *lowPortCapabilityError) Error() string { return e.Err.Error() }
func (e *lowPortCapabilityError) Unwrap() error { return e.Err }

type daemonSetupResult struct {
	Status     string `json:"status"`
	Executable string `json:"executable"`
	Capability string `json:"capability"`
	Changed    bool   `json:"changed"`
}

var (
	lowPortCapabilityManagerFunc = platformLowPortCapabilityManager
	lowPortCapabilityTargetFunc  = resolveLowPortCapabilityTarget
	confirmLowPortSetupFunc      = confirmLowPortSetup
)

func daemonSetup(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	check := fs.Bool("check", false, "check low-port capability without changing it")
	yes := fs.Bool("yes", false, "apply low-port capability without gate confirmation")
	fs.BoolVar(yes, "y", false, "apply low-port capability without gate confirmation")
	jsonOut := fs.Bool("json", false, "emit JSON")
	if handled, code := parseNoArgFlags(fs, "daemon setup", args, stdout, stderr); handled {
		return code
	}
	if *check && *yes {
		return fail(stderr, *jsonOut, ExitUsage, "usage", "--check and --yes cannot be combined")
	}
	if *jsonOut && !*check && !*yes {
		return fail(stderr, true, ExitUsage, "confirmation_required", "JSON setup requires --check or --yes")
	}
	if runtimeGOOS() != "linux" {
		return fail(stderr, *jsonOut, ExitUsage, "unsupported_platform", errLowPortCapabilityUnsupported.Error())
	}
	if os.Getenv("GATE_ISOLATED_ROOT") != "" {
		return fail(stderr, *jsonOut, ExitConflict, "isolated_setup", "low-port capability setup changes the installed executable and is unavailable with isolated state")
	}

	target, err := lowPortCapabilityTargetFunc(executablePath())
	if err != nil {
		return fail(stderr, *jsonOut, ExitError, "capability_target", err.Error())
	}
	defer target.Close()
	manager := lowPortCapabilityManagerFunc()
	inspection, err := manager.Inspect(target)
	if err != nil {
		return failLowPortCapability(stderr, *jsonOut, "capability_inspect", err)
	}
	if err := target.validateIdentity(); err != nil {
		return failLowPortCapability(stderr, *jsonOut, "capability_target_changed", err)
	}
	switch inspection.State {
	case lowPortCapabilityConfigured:
		return writeDaemonSetupSuccess(stdout, *jsonOut, target.Path, false)
	case lowPortCapabilityUnexpected:
		msg := fmt.Sprintf("gate executable has unexpected Linux capabilities: %s", inspection.Raw)
		return fail(stderr, *jsonOut, ExitConflict, "unexpected_capabilities", msg)
	case lowPortCapabilityMissing:
	default:
		return fail(stderr, *jsonOut, ExitError, "capability_inspect", "unknown low-port capability state")
	}

	if *check {
		return fail(stderr, *jsonOut, ExitPerm, "low_port_capability_missing", "low-port capability is not configured for "+target.Path)
	}
	if !*yes {
		if !stdinIsTTYFunc() {
			return fail(stderr, false, ExitUsage, "confirmation_required", "pass --yes to configure low-port access non-interactively")
		}
		confirmed, confirmErr := confirmLowPortSetupFunc(stdout)
		if confirmErr != nil {
			return fail(stderr, false, ExitError, "confirm_failed", confirmErr.Error())
		}
		if !confirmed {
			return fail(stderr, false, ExitError, "cancelled", "low-port setup cancelled")
		}
	}

	if err := manager.Apply(target); err != nil {
		return failLowPortCapability(stderr, *jsonOut, "capability_apply", err)
	}
	if err := target.validateIdentity(); err != nil {
		return failLowPortCapability(stderr, *jsonOut, "capability_target_changed", err)
	}
	inspection, err = manager.Inspect(target)
	if err != nil {
		return failLowPortCapability(stderr, *jsonOut, "capability_verify", err)
	}
	if err := target.validateIdentity(); err != nil {
		return failLowPortCapability(stderr, *jsonOut, "capability_target_changed", err)
	}
	if inspection.State != lowPortCapabilityConfigured {
		return fail(stderr, *jsonOut, ExitError, "capability_verify", "low-port capability verification failed")
	}
	return writeDaemonSetupSuccess(stdout, *jsonOut, target.Path, true)
}

func resolveLowPortCapabilityTarget(path string) (*lowPortCapabilityTarget, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("gate executable path is empty")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve gate executable: %w", err)
	}
	target, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, fmt.Errorf("resolve gate executable: %w", err)
	}
	file, err := os.Open(target)
	if err != nil {
		return nil, fmt.Errorf("open gate executable: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("inspect gate executable: %w", err)
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, fmt.Errorf("gate executable is not a regular file: %s", target)
	}
	if info.Mode().Perm()&0o111 == 0 {
		_ = file.Close()
		return nil, fmt.Errorf("gate executable is not executable: %s", target)
	}
	return &lowPortCapabilityTarget{Path: target, File: file, Info: info}, nil
}

func confirmLowPortSetup(stdout io.Writer) (bool, error) {
	answer, err := promptChoice(
		bufio.NewReader(os.Stdin),
		stdout,
		"Allow gate to bind local ports 80 and 443?",
		"no",
		[]string{"no", "yes"},
	)
	return answer == "yes", err
}

func writeDaemonSetupSuccess(stdout io.Writer, jsonOut bool, target string, changed bool) int {
	result := daemonSetupResult{
		Status:     string(lowPortCapabilityConfigured),
		Executable: target,
		Capability: lowPortCapability,
		Changed:    changed,
	}
	if jsonOut {
		return writeJSON(stdout, result)
	}
	if changed {
		printSuccess(stdout, "configured low-port access for "+target)
		return ExitOK
	}
	printOK(stdout, "low-port access already configured for "+target)
	return ExitOK
}

func failLowPortCapability(stderr io.Writer, jsonOut bool, fallbackCode string, err error) int {
	var capabilityErr *lowPortCapabilityError
	if errors.As(err, &capabilityErr) && capabilityErr.Code != "" {
		fallbackCode = capabilityErr.Code
	}
	return fail(stderr, jsonOut, ExitPerm, fallbackCode, err.Error())
}

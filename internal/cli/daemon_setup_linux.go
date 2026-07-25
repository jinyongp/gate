//go:build linux

package cli

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

type linuxLowPortCapabilityManager struct{}

var (
	linuxCapabilityCommand = exec.Command
	linuxCapabilityEUID    = os.Geteuid
	linuxCapabilityTool    = trustedLinuxCapabilityTool
)

func platformLowPortCapabilityManager() lowPortCapabilityManager {
	return linuxLowPortCapabilityManager{}
}

func (linuxLowPortCapabilityManager) Inspect(path string) (lowPortCapabilityInspection, error) {
	getcap, err := linuxCapabilityTool("getcap")
	if err != nil {
		return lowPortCapabilityInspection{}, err
	}
	cmd := linuxCapabilityCommand(getcap, path)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		return lowPortCapabilityInspection{}, &lowPortCapabilityError{
			Code: "getcap_failed",
			Err:  fmt.Errorf("inspect low-port capability: %w: %s", err, strings.TrimSpace(output.String())),
		}
	}
	raw := strings.TrimSpace(output.String())
	if raw == "" {
		return lowPortCapabilityInspection{State: lowPortCapabilityMissing}, nil
	}
	fields := strings.Fields(raw)
	if len(fields) < 2 {
		return lowPortCapabilityInspection{State: lowPortCapabilityUnexpected, Raw: raw}, nil
	}
	capabilities := fields[len(fields)-1]
	if capabilities == lowPortCapability {
		return lowPortCapabilityInspection{State: lowPortCapabilityConfigured, Raw: capabilities}, nil
	}
	return lowPortCapabilityInspection{State: lowPortCapabilityUnexpected, Raw: capabilities}, nil
}

func (linuxLowPortCapabilityManager) Apply(path string) error {
	before, err := os.Stat(path)
	if err != nil {
		return &lowPortCapabilityError{Code: "capability_target", Err: fmt.Errorf("inspect gate executable before setup: %w", err)}
	}
	setcap, err := linuxCapabilityTool("setcap")
	if err != nil {
		return err
	}
	current, err := os.Stat(path)
	if err != nil || !os.SameFile(before, current) {
		return &lowPortCapabilityError{Code: "capability_target_changed", Err: capabilityIdentityError(err)}
	}

	args := []string{lowPortCapability, path}
	executable := setcap
	if linuxCapabilityEUID() != 0 {
		sudo, findErr := linuxCapabilityTool("sudo")
		if findErr != nil {
			return findErr
		}
		executable = sudo
		args = append([]string{"--", setcap}, args...)
	}
	cmd := linuxCapabilityCommand(executable, args...)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(output.String())
		if message == "" {
			message = err.Error()
		}
		return &lowPortCapabilityError{
			Code: "setcap_failed",
			Err:  fmt.Errorf("configure low-port capability: %s", message),
		}
	}
	after, err := os.Stat(path)
	if err != nil || !os.SameFile(before, after) {
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

func trustedLinuxCapabilityTool(name string) (string, error) {
	var candidates []string
	switch name {
	case "getcap", "setcap":
		candidates = []string{
			filepath.Join("/usr/sbin", name),
			filepath.Join("/sbin", name),
			filepath.Join("/usr/bin", name),
			filepath.Join("/bin", name),
		}
	case "sudo":
		candidates = []string{"/usr/bin/sudo", "/bin/sudo"}
	default:
		return "", &lowPortCapabilityError{Code: "capability_tool", Err: fmt.Errorf("unsupported capability tool %q", name)}
	}
	for _, candidate := range candidates {
		path, err := trustedRootExecutable(candidate)
		if err == nil {
			return path, nil
		}
	}
	return "", &lowPortCapabilityError{
		Code: name + "_not_found",
		Err:  fmt.Errorf("trusted %s executable not found", name),
	}
}

func trustedRootExecutable(path string) (string, error) {
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(target)
	if err != nil {
		return "", err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 {
		return "", fmt.Errorf("tool is not root-owned: %s", target)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 || info.Mode().Perm()&0o022 != 0 {
		return "", fmt.Errorf("tool permissions are unsafe: %s", target)
	}
	return target, nil
}

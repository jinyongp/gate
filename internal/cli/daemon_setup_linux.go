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
	linuxCapabilityEval    = filepath.EvalSymlinks
	linuxCapabilityStat    = os.Stat
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

func (linuxLowPortCapabilityManager) Apply(target *lowPortCapabilityTarget) error {
	if target == nil || target.File == nil || target.Info == nil {
		return &lowPortCapabilityError{Code: "capability_target", Err: fmt.Errorf("stable gate executable handle is unavailable")}
	}
	if err := target.validateIdentity(); err != nil {
		return err
	}
	setcap, err := linuxCapabilityTool("setcap")
	if err != nil {
		return err
	}
	if err := target.validateIdentity(); err != nil {
		return err
	}

	args := []string{lowPortCapability, target.operationPath()}
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
		if unsupportedCapabilityFilesystem(target.Path, message) {
			return &lowPortCapabilityError{
				Code: "capability_filesystem_unsupported",
				Err: fmt.Errorf(
					"filesystem does not support Linux file capabilities for %s; "+
						"move or reinstall gate on a Linux-native filesystem, then rerun `gate daemon setup`: %s",
					target.Path,
					message,
				),
			}
		}
		return &lowPortCapabilityError{
			Code: "setcap_failed",
			Err:  fmt.Errorf("configure low-port capability: %s", message),
		}
	}
	if err := target.validateIdentity(); err != nil {
		return err
	}
	return nil
}

func unsupportedCapabilityFilesystem(path, message string) bool {
	lower := strings.ToLower(message)
	if strings.Contains(lower, "operation not supported") ||
		strings.Contains(lower, "not supported") ||
		strings.Contains(lower, "unsupported") {
		return true
	}
	cleaned := filepath.Clean(path)
	parts := strings.Split(strings.TrimPrefix(cleaned, string(filepath.Separator)), string(filepath.Separator))
	windowsMount := len(parts) >= 2 &&
		parts[0] == "mnt" &&
		len(parts[1]) == 1 &&
		parts[1][0] >= 'a' &&
		parts[1][0] <= 'z'
	return windowsMount && strings.Contains(lower, "invalid argument")
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
	target, err := linuxCapabilityEval(path)
	if err != nil {
		return "", err
	}
	info, err := linuxCapabilityStat(target)
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
	for parent := filepath.Dir(target); ; parent = filepath.Dir(parent) {
		parentInfo, err := linuxCapabilityStat(parent)
		if err != nil {
			return "", err
		}
		parentStat, ok := parentInfo.Sys().(*syscall.Stat_t)
		if !ok || parentStat.Uid != 0 || !parentInfo.IsDir() || parentInfo.Mode().Perm()&0o022 != 0 {
			return "", fmt.Errorf("tool ancestor permissions are unsafe: %s", parent)
		}
		if parent == string(filepath.Separator) {
			break
		}
	}
	return target, nil
}

//go:build linux

package cli

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

const (
	lowPortCapabilityXattr        = "security.capability"
	lowPortCapabilityHelperMarker = "gate-capability-helper:"
	lowPortCapabilityHelperName   = "__set-low-port-capability"
	vfsCapabilityRevision2        = uint32(0x02000000)
	vfsCapabilityEffective        = uint32(0x00000001)
)

type linuxLowPortCapabilityManager struct{}

var (
	linuxCapabilityCommand   = exec.Command
	linuxCapabilityEUID      = os.Geteuid
	linuxCapabilityTool      = trustedLinuxCapabilityTool
	linuxCapabilityEval      = filepath.EvalSymlinks
	linuxCapabilityStat      = os.Stat
	linuxCapabilitySelfStat  = func() (os.FileInfo, error) { return os.Stat("/proc/self/exe") }
	linuxCapabilityFgetxattr = unix.Fgetxattr
	linuxCapabilityFsetxattr = unix.Fsetxattr
)

func platformLowPortCapabilityManager() lowPortCapabilityManager {
	return linuxLowPortCapabilityManager{}
}

func (t *lowPortCapabilityTarget) operationPath() string {
	if t == nil {
		return ""
	}
	return fmt.Sprintf("/proc/%d/exe", os.Getpid())
}

func (linuxLowPortCapabilityManager) Inspect(target *lowPortCapabilityTarget) (lowPortCapabilityInspection, error) {
	if err := requireStableLowPortTarget(target); err != nil {
		return lowPortCapabilityInspection{}, err
	}
	raw, err := readLowPortCapabilityXattr(target)
	if err != nil {
		return lowPortCapabilityInspection{}, err
	}
	if len(raw) == 0 {
		return lowPortCapabilityInspection{State: lowPortCapabilityMissing}, nil
	}
	if exactLowPortCapabilityXattr(raw) {
		return lowPortCapabilityInspection{State: lowPortCapabilityConfigured, Raw: lowPortCapability}, nil
	}
	return lowPortCapabilityInspection{
		State: lowPortCapabilityUnexpected,
		Raw:   hex.EncodeToString(raw),
	}, nil
}

func (linuxLowPortCapabilityManager) Apply(target *lowPortCapabilityTarget) error {
	if err := requireStableLowPortTarget(target); err != nil {
		return err
	}
	if err := validateCapabilityHelperExecutable(target); err != nil {
		return err
	}
	if linuxCapabilityEUID() == 0 {
		if err := writeLowPortCapabilityXattr(target.File); err != nil {
			return classifyCapabilityXattrError(target.Path, err)
		}
		return target.validateIdentity()
	}

	sudo, err := linuxCapabilityTool("sudo")
	if err != nil {
		return err
	}
	if err := target.validateIdentity(); err != nil {
		return err
	}
	cmd := linuxCapabilityCommand(sudo, "--", target.operationPath(), lowPortCapabilityHelperName)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(output.String())
		if message == "" {
			message = err.Error()
		}
		if helperMessage, ok := strings.CutPrefix(message, lowPortCapabilityHelperMarker); ok {
			helperMessage = strings.TrimSpace(helperMessage)
			return classifyCapabilityHelperError(target.Path, helperMessage)
		}
		return &lowPortCapabilityError{
			Code: "sudo_failed",
			Err:  fmt.Errorf("authorize low-port capability setup: %s", message),
		}
	}
	return target.validateIdentity()
}

func LowPortCapabilityHelper(args []string, _ io.Writer, stderr io.Writer) int {
	if len(args) != 0 {
		fmt.Fprintln(stderr, lowPortCapabilityHelperMarker+"unexpected arguments")
		return ExitUsage
	}
	if linuxCapabilityEUID() != 0 {
		fmt.Fprintln(stderr, lowPortCapabilityHelperMarker+"root authorization required")
		return ExitPerm
	}
	executable, err := os.Open("/proc/self/exe")
	if err != nil {
		fmt.Fprintln(stderr, lowPortCapabilityHelperMarker+err.Error())
		return ExitError
	}
	defer func() { _ = executable.Close() }()
	if err := writeLowPortCapabilityXattr(executable); err != nil {
		fmt.Fprintln(stderr, lowPortCapabilityHelperMarker+capabilityHelperErrorMessage(err))
		return ExitPerm
	}
	return ExitOK
}

func requireStableLowPortTarget(target *lowPortCapabilityTarget) error {
	if target == nil || target.File == nil || target.Info == nil {
		return &lowPortCapabilityError{
			Code: "capability_target",
			Err:  fmt.Errorf("stable gate executable handle is unavailable"),
		}
	}
	return target.validateIdentity()
}

func validateCapabilityHelperExecutable(target *lowPortCapabilityTarget) error {
	running, err := linuxCapabilitySelfStat()
	if err != nil || !os.SameFile(target.Info, running) {
		return &lowPortCapabilityError{
			Code: "capability_target_changed",
			Err:  capabilityIdentityError(err),
		}
	}
	return nil
}

func readLowPortCapabilityXattr(target *lowPortCapabilityTarget) ([]byte, error) {
	buffer := make([]byte, 64)
	n, err := linuxCapabilityFgetxattr(int(target.File.Fd()), lowPortCapabilityXattr, buffer)
	if errors.Is(err, unix.ENODATA) {
		return nil, nil
	}
	if err != nil {
		return nil, classifyCapabilityXattrError(target.Path, err)
	}
	return buffer[:n], nil
}

func writeLowPortCapabilityXattr(file *os.File) error {
	if file == nil {
		return fmt.Errorf("stable gate executable handle is unavailable")
	}
	return linuxCapabilityFsetxattr(
		int(file.Fd()),
		lowPortCapabilityXattr,
		lowPortCapabilityXattrBytes(),
		0,
	)
}

func lowPortCapabilityXattrBytes() []byte {
	value := make([]byte, 20)
	binary.LittleEndian.PutUint32(value[0:4], vfsCapabilityRevision2|vfsCapabilityEffective)
	binary.LittleEndian.PutUint32(value[4:8], 1<<10)
	return value
}

func exactLowPortCapabilityXattr(value []byte) bool {
	if len(value) != 20 && len(value) != 24 {
		return false
	}
	magic := binary.LittleEndian.Uint32(value[0:4])
	revision := magic & 0xff000000
	if revision != vfsCapabilityRevision2 && revision != 0x03000000 {
		return false
	}
	if magic&0x00ffffff != vfsCapabilityEffective {
		return false
	}
	if len(value) == 24 && binary.LittleEndian.Uint32(value[20:24]) != 0 {
		return false
	}
	return binary.LittleEndian.Uint32(value[4:8]) == 1<<10 &&
		binary.LittleEndian.Uint32(value[8:12]) == 0 &&
		binary.LittleEndian.Uint32(value[12:16]) == 0 &&
		binary.LittleEndian.Uint32(value[16:20]) == 0
}

func classifyCapabilityHelperError(path, message string) error {
	if detail, ok := strings.CutPrefix(message, "filesystem_unsupported:"); ok {
		return unsupportedCapabilityFilesystemError(path, errors.New(strings.TrimSpace(detail)))
	}
	if detail, ok := strings.CutPrefix(message, "apply_failed:"); ok {
		message = strings.TrimSpace(detail)
	}
	return &lowPortCapabilityError{
		Code: "capability_apply_failed",
		Err:  fmt.Errorf("configure low-port capability: %s", message),
	}
}

func capabilityHelperErrorMessage(err error) string {
	if errors.Is(err, unix.EOPNOTSUPP) ||
		errors.Is(err, unix.ENOTSUP) ||
		errors.Is(err, unix.EINVAL) {
		return "filesystem_unsupported:" + err.Error()
	}
	return "apply_failed:" + err.Error()
}

func classifyCapabilityXattrError(path string, err error) error {
	if errors.Is(err, unix.EOPNOTSUPP) ||
		errors.Is(err, unix.ENOTSUP) ||
		errors.Is(err, unix.EINVAL) {
		return unsupportedCapabilityFilesystemError(path, err)
	}
	return &lowPortCapabilityError{
		Code: "capability_xattr_failed",
		Err:  fmt.Errorf("manage low-port capability for %s: %w", path, err),
	}
}

func unsupportedCapabilityFilesystemError(path string, err error) error {
	return &lowPortCapabilityError{
		Code: "capability_filesystem_unsupported",
		Err: fmt.Errorf(
			"filesystem does not support Linux file capabilities for %s; "+
				"move or reinstall gate on a Linux-native filesystem, then rerun `gate daemon setup`: %w",
			path,
			err,
		),
	}
}

func trustedLinuxCapabilityTool(name string) (string, error) {
	if name != "sudo" {
		return "", &lowPortCapabilityError{
			Code: "capability_tool",
			Err:  fmt.Errorf("unsupported capability tool %q", name),
		}
	}
	for _, candidate := range []string{"/usr/bin/sudo", "/bin/sudo"} {
		path, err := trustedRootExecutable(candidate)
		if err == nil {
			return path, nil
		}
	}
	return "", &lowPortCapabilityError{
		Code: "sudo_not_found",
		Err:  fmt.Errorf("trusted sudo executable not found"),
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

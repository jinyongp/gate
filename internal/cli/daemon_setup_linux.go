//go:build linux

package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"gate/internal/paths"

	"golang.org/x/sys/unix"
)

const (
	lowPortCapabilityXattr        = "security.capability"
	lowPortCapabilityHelperMarker = "gate-capability-helper:"
	lowPortCapabilityHelperName   = "__set-low-port-capability"
	lowPortCapabilityHelperPrefix = ".gate-capability-helper-"
	vfsCapabilityRevision2        = uint32(0x02000000)
	vfsCapabilityEffective        = uint32(0x00000001)
)

var lowPortCapabilityHelperTempDirs = []string{"/tmp", "/var/tmp"}

type linuxLowPortCapabilityManager struct{}

type lowPortCapabilityHelperCopy struct {
	Path string
	Hash string
	File *os.File
	Info os.FileInfo
}

func (h *lowPortCapabilityHelperCopy) Close() {
	if h != nil && h.File != nil {
		_ = h.File.Close()
	}
}

var (
	linuxCapabilityCommand        = exec.Command
	linuxCapabilityEUID           = os.Geteuid
	linuxCapabilityTool           = trustedLinuxCapabilityTool
	linuxCapabilityEval           = filepath.EvalSymlinks
	linuxCapabilityStat           = os.Stat
	linuxCapabilityAccess         = unix.Access
	linuxCapabilitySelfStat       = func() (os.FileInfo, error) { return os.Stat("/proc/self/exe") }
	linuxCapabilityExecutable     = os.Executable
	linuxCapabilityFgetxattr      = unix.Fgetxattr
	linuxCapabilityFsetxattr      = unix.Fsetxattr
	linuxCapabilityFremovexattr   = unix.Fremovexattr
	linuxCapabilityCreateHelper   = createLowPortCapabilityHelperCopy
	linuxCapabilityValidateHelper = validateLowPortCapabilityHelperCopy
	linuxCapabilityRemoveHelper   = removeLowPortCapabilityHelper
	linuxCapabilitySetupLock      = acquireLowPortCapabilitySetupLock
	linuxCapabilityCleanupHelpers = cleanupLowPortCapabilityHelpers
)

func currentLinuxUID() uint32 {
	// Linux exposes getuid(2) as uid_t, an unsigned 32-bit value.
	return uint32(os.Getuid()) //nolint:gosec // The kernel UID cannot exceed uint32.
}

func platformLowPortCapabilityManager() lowPortCapabilityManager {
	return linuxLowPortCapabilityManager{}
}

func (t *lowPortCapabilityTarget) operationPath() string {
	if t == nil || t.File == nil {
		return ""
	}
	return fmt.Sprintf("/proc/%d/fd/%d", os.Getpid(), t.File.Fd())
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
	setupLock, err := linuxCapabilitySetupLock()
	if err != nil {
		return err
	}
	defer func() { _ = setupLock.Close() }()
	if err := linuxCapabilityCleanupHelpers(sudo); err != nil {
		if linuxCapabilitySudoFailed(err) {
			return err
		}
		return &lowPortCapabilityError{
			Code: "capability_helper_cleanup",
			Err:  fmt.Errorf("clean interrupted low-port capability helper: %w", err),
		}
	}
	if err := target.validateIdentity(); err != nil {
		return err
	}
	if err := validateCapabilityHelperExecutable(target); err != nil {
		return err
	}
	stat, ok := selfInfoSys(target.Info)
	if !ok {
		return &lowPortCapabilityError{
			Code: "capability_target",
			Err:  fmt.Errorf("gate executable identity is unavailable"),
		}
	}
	remainingDirs := append([]string(nil), lowPortCapabilityHelperTempDirs...)
	for len(remainingDirs) > 0 {
		helper, createErr := linuxCapabilityCreateHelper(sudo, target, remainingDirs)
		if createErr != nil {
			if linuxCapabilitySudoFailed(createErr) {
				return createErr
			}
			return &lowPortCapabilityError{
				Code: "capability_helper_copy",
				Err:  fmt.Errorf("prepare low-port capability helper: %w", createErr),
			}
		}
		remainingDirs = removeLowPortCapabilityHelperDir(remainingDirs, filepath.Dir(helper.Path))
		if validateErr := linuxCapabilityValidateHelper(helper); validateErr != nil {
			helper.Close()
			_ = linuxCapabilityRemoveHelper(sudo, helper.Path)
			return &lowPortCapabilityError{
				Code: "capability_helper_prepare",
				Err:  fmt.Errorf("validate low-port capability helper: %w", validateErr),
			}
		}
		if identityErr := target.validateIdentity(); identityErr != nil {
			helper.Close()
			_ = linuxCapabilityRemoveHelper(sudo, helper.Path)
			return identityErr
		}
		runErr := runLinuxCapabilityAuthorizedSudo(
			sudo,
			helper.Path,
			lowPortCapabilityHelperName,
			"--target",
			target.Path,
			"--device",
			strconv.FormatUint(uint64(stat.Dev), 10),
			"--inode",
			strconv.FormatUint(stat.Ino, 10),
			"--sha256",
			helper.Hash,
		)
		helper.Close()
		cleanupErr := linuxCapabilityRemoveHelper(sudo, helper.Path)
		if runErr == nil {
			if cleanupErr != nil {
				if linuxCapabilitySudoFailed(cleanupErr) {
					return cleanupErr
				}
				return &lowPortCapabilityError{
					Code: "capability_helper_cleanup",
					Err:  fmt.Errorf("remove low-port capability helper: %w", cleanupErr),
				}
			}
			return target.validateIdentity()
		}
		message := runErr.Error()
		if cleanupErr != nil {
			if linuxCapabilitySudoFailed(cleanupErr) {
				return cleanupErr
			}
			return &lowPortCapabilityError{
				Code: "capability_helper_cleanup",
				Err:  fmt.Errorf("remove failed low-port capability helper: %w", cleanupErr),
			}
		}
		if helperMessage, markerOK := linuxCapabilityHelperMessage(message); markerOK {
			return classifyCapabilityHelperError(target.Path, helperMessage)
		}
		if retryLowPortCapabilityHelperExecution(message, helper.Path) && len(remainingDirs) > 0 {
			continue
		}
		return &lowPortCapabilityError{
			Code: "sudo_failed",
			Err:  fmt.Errorf("run authorized low-port capability helper: %s", message),
		}
	}
	return &lowPortCapabilityError{
		Code: "capability_helper_copy",
		Err:  fmt.Errorf("no executable trusted temporary directory is available"),
	}
}

func runLinuxCapabilitySudo(sudo string, args ...string) error {
	_, err := runLinuxCapabilitySudoOutput(sudo, args...)
	return err
}

func runLinuxCapabilityAuthorizedSudo(sudo string, command ...string) error {
	_, err := runLinuxCapabilityAuthorizedSudoOutput(sudo, command...)
	return err
}

func runLinuxCapabilityAuthorizedSudoOutput(sudo string, command ...string) (string, error) {
	output, err := runLinuxCapabilitySudoOutput(
		sudo,
		append([]string{"-n", "--"}, command...)...,
	)
	if err == nil || !linuxCapabilitySudoNeedsAuthentication(err) {
		return output, err
	}
	if authorizationErr := runLinuxCapabilitySudo(sudo, "-v"); authorizationErr != nil {
		return "", &lowPortCapabilityError{
			Code: "sudo_failed",
			Err:  fmt.Errorf("sudo authorization failed: %w", authorizationErr),
		}
	}
	output, err = runLinuxCapabilitySudoOutput(
		sudo,
		append([]string{"-n", "--"}, command...)...,
	)
	if linuxCapabilitySudoNeedsAuthentication(err) {
		return "", &lowPortCapabilityError{
			Code: "sudo_failed",
			Err:  fmt.Errorf("sudo authorization was not accepted for the requested command: %w", err),
		}
	}
	return output, err
}

func linuxCapabilitySudoNeedsAuthentication(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, fragment := range []string{
		"password is required",
		"no tty present",
		"a terminal is required",
		"authentication is required",
	} {
		if strings.Contains(message, fragment) {
			return true
		}
	}
	return false
}

func linuxCapabilitySudoFailed(err error) bool {
	var capabilityErr *lowPortCapabilityError
	return errors.As(err, &capabilityErr) && capabilityErr.Code == "sudo_failed"
}

func runLinuxCapabilitySudoOutput(sudo string, args ...string) (string, error) {
	cmd := linuxCapabilityCommand(sudo, args...)
	return runLinuxCapabilityCommand(cmd)
}

func runLinuxCapabilityCommand(cmd *exec.Cmd) (string, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	cmd.Env = append(cmd.Environ(), "LC_ALL=C")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = strings.TrimSpace(stdout.String())
		}
		if message == "" {
			message = err.Error()
		}
		return "", errors.New(message)
	}
	return strings.TrimSpace(stdout.String()), nil
}

func linuxCapabilityHelperMessage(output string) (string, bool) {
	for line := range strings.Lines(output) {
		line = strings.TrimSpace(line)
		if message, ok := strings.CutPrefix(line, lowPortCapabilityHelperMarker); ok {
			return strings.TrimSpace(message), true
		}
	}
	return "", false
}

func createLowPortCapabilityHelperCopy(
	sudo string,
	target *lowPortCapabilityTarget,
	tempDirs []string,
) (*lowPortCapabilityHelperCopy, error) {
	expectedHash, err := hashOpenFile(target.File)
	if err != nil {
		return nil, err
	}
	mktemp, err := linuxCapabilityTool("mktemp")
	if err != nil {
		return nil, err
	}
	install, err := linuxCapabilityTool("install")
	if err != nil {
		return nil, err
	}

	var path string
	for _, dir := range tempDirs {
		if !trustedLowPortCapabilityTempDir(dir) {
			continue
		}
		template := filepath.Join(
			dir,
			lowPortCapabilityHelperPrefix+strconv.Itoa(os.Getuid())+"-XXXXXXXX",
		)
		output, runErr := runLinuxCapabilityAuthorizedSudoOutput(sudo, mktemp, template)
		if runErr != nil {
			var capabilityErr *lowPortCapabilityError
			if errors.As(runErr, &capabilityErr) && capabilityErr.Code == "sudo_failed" {
				return nil, runErr
			}
			err = runErr
			continue
		}
		path = strings.TrimSpace(output)
		if !validLowPortCapabilityHelperPathForUID(path, strconv.Itoa(os.Getuid())) {
			err = fmt.Errorf("mktemp returned an invalid helper path")
			continue
		}
		if runErr := runLinuxCapabilityAuthorizedSudo(
			sudo, install, "-m", "0755", "--", target.operationPath(), path,
		); runErr != nil {
			if cleanupErr := removeLowPortCapabilityHelper(sudo, path); cleanupErr != nil {
				return nil, cleanupErr
			}
			if linuxCapabilitySudoFailed(runErr) {
				return nil, runErr
			}
			err = runErr
			continue
		}
		helper, openErr := os.Open(path)
		if openErr != nil {
			if cleanupErr := removeLowPortCapabilityHelper(sudo, path); cleanupErr != nil {
				return nil, cleanupErr
			}
			err = openErr
			continue
		}
		info, statErr := helper.Stat()
		if statErr != nil || linuxCapabilityAccess(path, unix.X_OK) != nil {
			_ = helper.Close()
			if cleanupErr := removeLowPortCapabilityHelper(sudo, path); cleanupErr != nil {
				return nil, cleanupErr
			}
			if statErr != nil {
				err = statErr
			} else {
				err = fmt.Errorf("temporary directory does not allow helper execution: %s", dir)
			}
			continue
		}
		return &lowPortCapabilityHelperCopy{
			Path: path,
			Hash: expectedHash,
			File: helper,
			Info: info,
		}, nil
	}
	if err == nil {
		err = fmt.Errorf("no trusted temporary directory is available")
	}
	return nil, err
}

func removeLowPortCapabilityHelperDir(dirs []string, used string) []string {
	out := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		if dir != used {
			out = append(out, dir)
		}
	}
	return out
}

func retryLowPortCapabilityHelperExecution(message, path string) bool {
	message = strings.ToLower(message)
	return strings.Contains(message, strings.ToLower(path)) &&
		(strings.Contains(message, "permission denied") ||
			strings.Contains(message, "operation not permitted"))
}

func validateLowPortCapabilityHelperCopy(helper *lowPortCapabilityHelperCopy) error {
	if helper == nil || helper.File == nil || helper.Info == nil {
		return fmt.Errorf("stable helper handle is unavailable")
	}
	info, err := os.Lstat(helper.Path)
	if err != nil {
		return err
	}
	stat, ok := selfInfoSys(info)
	opened, openedErr := helper.File.Stat()
	if !ok || stat.Uid != 0 || !info.Mode().IsRegular() ||
		info.Mode().Perm()&0o111 == 0 || info.Mode().Perm()&0o022 != 0 ||
		openedErr != nil || !os.SameFile(helper.Info, opened) || !os.SameFile(helper.Info, info) {
		return fmt.Errorf("helper ownership, permissions, or inode are unsafe")
	}
	actualHash, err := hashOpenFile(helper.File)
	if err != nil {
		return err
	}
	if actualHash != helper.Hash {
		return fmt.Errorf("helper content changed")
	}
	return nil
}

func removeLowPortCapabilityHelper(sudo, path string) error {
	if path == "" {
		return nil
	}
	rm, err := linuxCapabilityTool("rm")
	if err != nil {
		return err
	}
	return runLinuxCapabilityAuthorizedSudo(sudo, rm, "-f", "--", path)
}

func acquireLowPortCapabilitySetupLock() (io.Closer, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil || !filepath.IsAbs(cacheDir) {
		return nil, &lowPortCapabilityError{
			Code: "capability_setup_lock",
			Err:  fmt.Errorf("private low-port setup lock directory is unavailable"),
		}
	}
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return nil, err
	}
	if resolved, resolveErr := filepath.EvalSymlinks(cacheDir); resolveErr == nil {
		cacheDir = resolved
	}
	dir := filepath.Join(cacheDir, ".gate-capability-locks")
	if lowPortSetupLockOverlapsGateState(dir) {
		return nil, &lowPortCapabilityError{
			Code: "capability_setup_lock",
			Err:  fmt.Errorf("low-port setup lock must be outside removable gate state"),
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	dirFD, err := unix.Open(
		dir,
		unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW,
		0,
	)
	if err != nil {
		return nil, err
	}
	dirFile := os.NewFile(uintptr(dirFD), dir)
	defer func() { _ = dirFile.Close() }()
	if err := unix.Fchmod(dirFD, 0o700); err != nil {
		return nil, err
	}
	dirInfo, err := dirFile.Stat()
	dirStat, dirOK := selfInfoSys(dirInfo)
	if err != nil || !dirOK || !dirInfo.IsDir() ||
		dirStat.Uid != currentLinuxUID() || dirInfo.Mode().Perm() != 0o700 {
		return nil, &lowPortCapabilityError{
			Code: "capability_setup_lock",
			Err:  fmt.Errorf("low-port setup lock directory is unsafe"),
		}
	}
	path := filepath.Join(dir, "capability-setup.lock")
	fd, err := unix.Openat(
		dirFD,
		filepath.Base(path),
		unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW,
		0o600,
	)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	if err := unix.Fchmod(fd, 0o600); err != nil {
		_ = file.Close()
		return nil, err
	}
	info, statErr := file.Stat()
	stat, ok := selfInfoSys(info)
	if statErr != nil || !ok || !info.Mode().IsRegular() ||
		stat.Uid != currentLinuxUID() || info.Mode().Perm()&0o077 != 0 {
		_ = file.Close()
		return nil, &lowPortCapabilityError{
			Code: "capability_setup_lock",
			Err:  fmt.Errorf("low-port setup lock is unsafe"),
		}
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, &lowPortCapabilityError{
			Code: "capability_setup_busy",
			Err:  fmt.Errorf("another low-port capability setup is running: %w", err),
		}
	}
	return file, nil
}

func lowPortSetupLockOverlapsGateState(lockDir string) bool {
	lockDir = filepath.Clean(lockDir)
	for _, stateDir := range []string{
		paths.ConfigDir(),
		paths.DataDir(),
		paths.StateDir(),
		paths.RuntimeDir(),
	} {
		if resolved, err := filepath.EvalSymlinks(stateDir); err == nil {
			stateDir = resolved
		}
		stateDir = filepath.Clean(stateDir)
		rel, err := filepath.Rel(stateDir, lockDir)
		if err == nil && (rel == "." ||
			rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
			return true
		}
	}
	return false
}

func cleanupLowPortCapabilityHelpers(sudo string) error {
	return visitLowPortCapabilityHelpers(func(path string, info os.FileInfo) error {
		if !safeLowPortCapabilityHelper(info) {
			return fmt.Errorf("unsafe interrupted helper: %s", path)
		}
		return removeLowPortCapabilityHelper(sudo, path)
	})
}

func visitLowPortCapabilityHelpers(visit func(string, os.FileInfo) error) error {
	prefix := lowPortCapabilityHelperPrefix + strconv.Itoa(os.Getuid()) + "-"
	for _, dir := range lowPortCapabilityHelperTempDirs {
		if !trustedLowPortCapabilityTempDir(dir) {
			continue
		}
		matches, err := filepath.Glob(filepath.Join(dir, prefix+"*"))
		if err != nil {
			return err
		}
		for _, path := range matches {
			info, err := os.Lstat(path)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				return err
			}
			stat, ok := selfInfoSys(info)
			if !ok {
				return fmt.Errorf("cannot inspect interrupted helper: %s", path)
			}
			if stat.Uid != 0 {
				continue
			}
			if err := visit(path, info); err != nil {
				return err
			}
		}
	}
	return nil
}

func safeLowPortCapabilityHelper(info os.FileInfo) bool {
	return info != nil && info.Mode().IsRegular() && info.Mode().Perm()&0o022 == 0
}

func LowPortCapabilityHelper(args []string, _ io.Writer, stderr io.Writer) int {
	if linuxCapabilityEUID() != 0 {
		fmt.Fprintln(stderr, lowPortCapabilityHelperMarker+"root authorization required")
		return ExitPerm
	}
	selfPath, err := linuxCapabilityExecutable()
	if err != nil || !validLowPortCapabilityHelperPathForUID(
		selfPath,
		strings.TrimSpace(os.Getenv("SUDO_UID")),
	) {
		fmt.Fprintln(stderr, lowPortCapabilityHelperMarker+"invalid helper executable")
		return ExitPerm
	}
	self, err := os.Open(selfPath)
	if err != nil {
		fmt.Fprintln(stderr, lowPortCapabilityHelperMarker+"cannot open helper executable")
		return ExitPerm
	}
	defer func() { _ = self.Close() }()
	selfInfo, err := linuxCapabilityStat(selfPath)
	selfStat, ok := selfInfoSys(selfInfo)
	if err != nil || !ok || selfStat.Uid != 0 ||
		!selfInfo.Mode().IsRegular() || selfInfo.Mode().Perm()&0o111 == 0 ||
		selfInfo.Mode().Perm()&0o022 != 0 {
		fmt.Fprintln(stderr, lowPortCapabilityHelperMarker+"unsafe helper executable")
		return ExitPerm
	}

	fs := flag.NewFlagSet(lowPortCapabilityHelperName, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	targetPath := fs.String("target", "", "stable target path")
	expectedDevice := fs.Uint64("device", 0, "expected target device")
	expectedInode := fs.Uint64("inode", 0, "expected target inode")
	expectedHash := fs.String("sha256", "", "expected target digest")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 ||
		!targetPathValid(*targetPath) || *expectedDevice == 0 || *expectedInode == 0 ||
		!validSHA256(*expectedHash) {
		fmt.Fprintln(stderr, lowPortCapabilityHelperMarker+"invalid helper arguments")
		return ExitUsage
	}
	selfHash, err := hashOpenFile(self)
	if err != nil || selfHash != *expectedHash {
		fmt.Fprintln(stderr, lowPortCapabilityHelperMarker+"helper digest mismatch")
		return ExitPerm
	}
	if err := os.Remove(selfPath); err != nil {
		fmt.Fprintln(stderr, lowPortCapabilityHelperMarker+"cannot unlink helper executable")
		return ExitPerm
	}
	target, err := os.Open(*targetPath)
	if err != nil {
		fmt.Fprintln(stderr, lowPortCapabilityHelperMarker+err.Error())
		return ExitError
	}
	defer func() { _ = target.Close() }()
	info, err := target.Stat()
	targetStat, statOK := selfInfoSys(info)
	if err != nil || !statOK || !info.Mode().IsRegular() ||
		uint64(targetStat.Dev) != *expectedDevice || targetStat.Ino != *expectedInode {
		fmt.Fprintln(stderr, lowPortCapabilityHelperMarker+"invalid target executable")
		return ExitPerm
	}
	actualHash, err := hashOpenFile(target)
	if err != nil || actualHash != *expectedHash {
		fmt.Fprintln(stderr, lowPortCapabilityHelperMarker+"target digest mismatch")
		return ExitPerm
	}
	if err := writeLowPortCapabilityXattr(target); err != nil {
		fmt.Fprintln(stderr, lowPortCapabilityHelperMarker+capabilityHelperErrorMessage(err))
		return ExitPerm
	}
	postInfo, postStatErr := target.Stat()
	postStat, postStatOK := selfInfoSys(postInfo)
	actualHash, err = hashOpenFile(target)
	if postStatErr != nil || !postStatOK ||
		uint64(postStat.Dev) != *expectedDevice || postStat.Ino != *expectedInode ||
		!postInfo.Mode().IsRegular() || postInfo.Size() != info.Size() ||
		err != nil || actualHash != *expectedHash {
		_ = linuxCapabilityFremovexattr(int(target.Fd()), lowPortCapabilityXattr)
		fmt.Fprintln(stderr, lowPortCapabilityHelperMarker+"target changed during capability setup")
		return ExitPerm
	}
	return ExitOK
}

func targetPathValid(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validLowPortCapabilityHelperPathForUID(path, uid string) bool {
	if _, err := strconv.ParseUint(uid, 10, 32); err != nil {
		return false
	}
	cleaned := filepath.Clean(path)
	for _, dir := range lowPortCapabilityHelperTempDirs {
		if filepath.Dir(cleaned) == dir && trustedLowPortCapabilityTempDir(dir) &&
			strings.HasPrefix(filepath.Base(cleaned), lowPortCapabilityHelperPrefix+uid+"-") {
			return true
		}
	}
	return false
}

func trustedLowPortCapabilityTempDir(path string) bool {
	if filepath.Clean(path) != path {
		return false
	}
	info, err := linuxCapabilityStat(path)
	stat, ok := selfInfoSys(info)
	return err == nil && ok && info.IsDir() && info.Mode()&os.ModeSticky != 0 && stat.Uid == 0
}

func selfInfoSys(info os.FileInfo) (*syscall.Stat_t, bool) {
	if info == nil {
		return nil, false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return stat, ok
}

func hashOpenFile(file *os.File) (string, error) {
	hash := sha256.New()
	if _, err := io.Copy(hash, io.NewSectionReader(file, 0, 1<<63-1)); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
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
	candidates, ok := map[string][]string{
		"sudo":    {"/usr/bin/sudo", "/bin/sudo"},
		"true":    {"/usr/bin/true", "/bin/true"},
		"mktemp":  {"/usr/bin/mktemp", "/bin/mktemp"},
		"install": {"/usr/bin/install", "/bin/install"},
		"rm":      {"/usr/bin/rm", "/bin/rm"},
	}[name]
	if !ok {
		return "", &lowPortCapabilityError{
			Code: "capability_tool",
			Err:  fmt.Errorf("unsupported capability tool %q", name),
		}
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

//go:build linux

package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

func isolateLinuxCapabilityManager(t *testing.T) {
	t.Helper()
	oldCommand := linuxCapabilityCommand
	oldEUID := linuxCapabilityEUID
	oldTool := linuxCapabilityTool
	oldEval := linuxCapabilityEval
	oldStat := linuxCapabilityStat
	oldAccess := linuxCapabilityAccess
	oldSelfStat := linuxCapabilitySelfStat
	oldExecutable := linuxCapabilityExecutable
	oldFgetxattr := linuxCapabilityFgetxattr
	oldFsetxattr := linuxCapabilityFsetxattr
	oldFremovexattr := linuxCapabilityFremovexattr
	oldCreateHelper := linuxCapabilityCreateHelper
	oldValidateHelper := linuxCapabilityValidateHelper
	oldRemoveHelper := linuxCapabilityRemoveHelper
	oldSetupLock := linuxCapabilitySetupLock
	oldCleanupHelpers := linuxCapabilityCleanupHelpers
	t.Cleanup(func() {
		linuxCapabilityCommand = oldCommand
		linuxCapabilityEUID = oldEUID
		linuxCapabilityTool = oldTool
		linuxCapabilityEval = oldEval
		linuxCapabilityStat = oldStat
		linuxCapabilityAccess = oldAccess
		linuxCapabilitySelfStat = oldSelfStat
		linuxCapabilityExecutable = oldExecutable
		linuxCapabilityFgetxattr = oldFgetxattr
		linuxCapabilityFsetxattr = oldFsetxattr
		linuxCapabilityFremovexattr = oldFremovexattr
		linuxCapabilityCreateHelper = oldCreateHelper
		linuxCapabilityValidateHelper = oldValidateHelper
		linuxCapabilityRemoveHelper = oldRemoveHelper
		linuxCapabilitySetupLock = oldSetupLock
		linuxCapabilityCleanupHelpers = oldCleanupHelpers
	})
}

func writeExecutableLinuxCapabilityFixture(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	//nolint:gosec // Capability targets must be executable; the fixture remains owner-only.
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatal(err)
	}
}

func openLinuxCapabilityTarget(t *testing.T) *lowPortCapabilityTarget {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gate")
	writeExecutableLinuxCapabilityFixture(t, path, []byte("fixture"))
	target, err := resolveLowPortCapabilityTarget(path)
	if err != nil {
		t.Fatal(err)
	}
	linuxCapabilitySelfStat = func() (os.FileInfo, error) { return target.Info, nil }
	helperFile, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	linuxCapabilityCreateHelper = func(string, *lowPortCapabilityTarget, []string) (*lowPortCapabilityHelperCopy, error) {
		return &lowPortCapabilityHelperCopy{
			Path: "/tmp/.gate-capability-helper-test",
			Hash: strings.Repeat("a", sha256.Size*2),
			File: helperFile,
		}, nil
	}
	linuxCapabilityValidateHelper = func(*lowPortCapabilityHelperCopy) error { return nil }
	linuxCapabilityRemoveHelper = func(string, string) error { return nil }
	linuxCapabilitySetupLock = func() (io.Closer, error) {
		return io.NopCloser(strings.NewReader("")), nil
	}
	linuxCapabilityCleanupHelpers = func(string) error { return nil }
	t.Cleanup(func() { _ = helperFile.Close() })
	t.Cleanup(target.Close)
	return target
}

func successfulLinuxCapabilityCommand() *exec.Cmd {
	return exec.Command("/bin/sh", "-c", "exit 0")
}

func TestLinuxCapabilityInspectReadsExactCapabilityFromStableFD(t *testing.T) {
	isolateLinuxCapabilityManager(t)
	target := openLinuxCapabilityTarget(t)
	expected := lowPortCapabilityXattrBytes()
	linuxCapabilityFgetxattr = func(fd int, name string, dest []byte) (int, error) {
		if fd != int(target.File.Fd()) || name != lowPortCapabilityXattr {
			t.Fatalf("fgetxattr = fd %d name %q", fd, name)
		}
		return copy(dest, expected), nil
	}

	inspection, err := (linuxLowPortCapabilityManager{}).Inspect(target)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.State != lowPortCapabilityConfigured || inspection.Raw != lowPortCapability {
		t.Fatalf("inspection = %+v", inspection)
	}
}

func TestLinuxCapabilityInspectTreatsMissingXattrAsUnconfigured(t *testing.T) {
	isolateLinuxCapabilityManager(t)
	target := openLinuxCapabilityTarget(t)
	linuxCapabilityFgetxattr = func(int, string, []byte) (int, error) {
		return 0, unix.ENODATA
	}

	inspection, err := (linuxLowPortCapabilityManager{}).Inspect(target)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.State != lowPortCapabilityMissing {
		t.Fatalf("inspection = %+v", inspection)
	}
}

func TestExactLowPortCapabilityXattrRejectsAdditionalOrNamespacedCapability(t *testing.T) {
	additional := lowPortCapabilityXattrBytes()
	binary.LittleEndian.PutUint32(additional[4:8], (1<<10)|(1<<12))
	if exactLowPortCapabilityXattr(additional) {
		t.Fatal("accepted an additional permitted capability")
	}

	namespaced := append(lowPortCapabilityXattrBytes(), make([]byte, 4)...)
	binary.LittleEndian.PutUint32(namespaced[0:4], 0x03000000|vfsCapabilityEffective)
	binary.LittleEndian.PutUint32(namespaced[20:24], 1000)
	if exactLowPortCapabilityXattr(namespaced) {
		t.Fatal("accepted a capability rooted in another user namespace")
	}
}

func TestLinuxCapabilityApplyUsesStableExecutableAndFixedSudoArgv(t *testing.T) {
	isolateLinuxCapabilityManager(t)
	target := openLinuxCapabilityTarget(t)
	linuxCapabilityEUID = func() int { return 1000 }
	linuxCapabilityTool = func(name string) (string, error) {
		if name != "sudo" && name != "true" {
			t.Fatalf("unexpected tool = %q", name)
		}
		return "/trusted/" + name, nil
	}

	var executables []string
	var calls [][]string
	var commands []*exec.Cmd
	linuxCapabilityCommand = func(name string, commandArgs ...string) *exec.Cmd {
		executables = append(executables, name)
		calls = append(calls, append([]string{}, commandArgs...))
		cmd := successfulLinuxCapabilityCommand()
		commands = append(commands, cmd)
		return cmd
	}
	if err := (linuxLowPortCapabilityManager{}).Apply(target); err != nil {
		t.Fatal(err)
	}
	stat, _ := selfInfoSys(target.Info)
	wantCalls := [][]string{
		{"-n", "--", "/trusted/true"},
		{
			"-n", "--", "/tmp/.gate-capability-helper-test",
			lowPortCapabilityHelperName,
			"--target", target.Path,
			"--device", strconv.FormatUint(uint64(stat.Dev), 10),
			"--inode", strconv.FormatUint(stat.Ino, 10),
			"--sha256", strings.Repeat("a", sha256.Size*2),
		},
	}
	if !slices.Equal(executables, []string{"/trusted/sudo", "/trusted/sudo"}) ||
		len(calls) != 2 || !slices.Equal(calls[0], wantCalls[0]) ||
		!slices.Equal(calls[1], wantCalls[1]) {
		t.Fatalf("commands = %q %q, want %q", executables, calls, wantCalls)
	}
	for _, cmd := range commands {
		if cmd.SysProcAttr == nil || cmd.SysProcAttr.Pdeathsig != syscall.SIGKILL {
			t.Fatalf("sudo command lacks parent-death protection: %+v", cmd.SysProcAttr)
		}
	}
	if !strings.HasPrefix(target.operationPath(), "/proc/") || !strings.Contains(target.operationPath(), "/fd/") {
		t.Fatalf("helper target is not the stable gate descriptor: %q", target.operationPath())
	}
}

func TestLinuxCapabilityApplyFallsBackToInteractiveSudoAuthorization(t *testing.T) {
	isolateLinuxCapabilityManager(t)
	target := openLinuxCapabilityTarget(t)
	linuxCapabilityEUID = func() int { return 1000 }
	linuxCapabilityTool = func(name string) (string, error) { return "/trusted/" + name, nil }

	var calls [][]string
	linuxCapabilityCommand = func(_ string, commandArgs ...string) *exec.Cmd {
		calls = append(calls, append([]string{}, commandArgs...))
		if len(calls) == 1 {
			return exec.Command("/bin/sh", "-c", "echo 'sudo: a password is required' >&2; exit 1")
		}
		return successfulLinuxCapabilityCommand()
	}

	if err := (linuxLowPortCapabilityManager{}).Apply(target); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 ||
		!slices.Equal(calls[0], []string{"-n", "--", "/trusted/true"}) ||
		!slices.Equal(calls[1], []string{"-v"}) {
		t.Fatalf("sudo calls = %q", calls)
	}
}

func TestLinuxCapabilityApplyRetriesHelperOnExecutableTempDir(t *testing.T) {
	isolateLinuxCapabilityManager(t)
	target := openLinuxCapabilityTarget(t)
	linuxCapabilityEUID = func() int { return 1000 }
	linuxCapabilityTool = func(string) (string, error) { return "/trusted/sudo", nil }

	createCalls := 0
	linuxCapabilityCreateHelper = func(
		_ string,
		_ *lowPortCapabilityTarget,
		dirs []string,
	) (*lowPortCapabilityHelperCopy, error) {
		createCalls++
		wantDirs := []string{"/tmp", "/var/tmp"}
		path := "/tmp/.gate-capability-helper-test"
		if createCalls == 2 {
			wantDirs = []string{"/var/tmp"}
			path = "/var/tmp/.gate-capability-helper-test"
		}
		if !slices.Equal(dirs, wantDirs) {
			t.Fatalf("create dirs = %q, want %q", dirs, wantDirs)
		}
		file, err := os.Open(target.Path)
		if err != nil {
			t.Fatal(err)
		}
		return &lowPortCapabilityHelperCopy{
			Path: path,
			Hash: strings.Repeat("a", sha256.Size*2),
			File: file,
		}, nil
	}
	commandCall := 0
	linuxCapabilityCommand = func(string, ...string) *exec.Cmd {
		commandCall++
		if commandCall == 2 {
			return exec.Command(
				"/bin/sh",
				"-c",
				"echo 'sudo: unable to execute /tmp/.gate-capability-helper-test: Permission denied' >&2; exit 1",
			)
		}
		return successfulLinuxCapabilityCommand()
	}

	if err := (linuxLowPortCapabilityManager{}).Apply(target); err != nil {
		t.Fatal(err)
	}
	if createCalls != 2 || commandCall != 3 {
		t.Fatalf("create calls = %d, command calls = %d", createCalls, commandCall)
	}
}

func TestLinuxCapabilityApplyRejectsExecutableOtherThanRunningGate(t *testing.T) {
	isolateLinuxCapabilityManager(t)
	target := openLinuxCapabilityTarget(t)
	otherPath := filepath.Join(t.TempDir(), "other")
	writeExecutableLinuxCapabilityFixture(t, otherPath, []byte("other"))
	other, err := os.Stat(otherPath)
	if err != nil {
		t.Fatal(err)
	}
	linuxCapabilitySelfStat = func() (os.FileInfo, error) { return other, nil }

	err = (linuxLowPortCapabilityManager{}).Apply(target)
	var capabilityErr *lowPortCapabilityError
	if !errors.As(err, &capabilityErr) || capabilityErr.Code != "capability_target_changed" {
		t.Fatalf("error = %v, want capability_target_changed", err)
	}
}

func TestLinuxCapabilityApplyAsRootUsesFsetxattr(t *testing.T) {
	isolateLinuxCapabilityManager(t)
	target := openLinuxCapabilityTarget(t)
	linuxCapabilityEUID = func() int { return 0 }
	var gotName string
	var gotValue []byte
	linuxCapabilityFsetxattr = func(fd int, name string, value []byte, flags int) error {
		if fd != int(target.File.Fd()) || flags != 0 {
			t.Fatalf("fsetxattr = fd %d flags %d", fd, flags)
		}
		gotName = name
		gotValue = append([]byte{}, value...)
		return nil
	}

	if err := (linuxLowPortCapabilityManager{}).Apply(target); err != nil {
		t.Fatal(err)
	}
	if gotName != lowPortCapabilityXattr || !slices.Equal(gotValue, lowPortCapabilityXattrBytes()) {
		t.Fatalf("xattr = %q %x", gotName, gotValue)
	}
}

func TestLinuxCapabilityApplyDetectsPathReplacement(t *testing.T) {
	isolateLinuxCapabilityManager(t)
	target := openLinuxCapabilityTarget(t)
	replacement := filepath.Join(filepath.Dir(target.Path), "replacement")
	writeExecutableLinuxCapabilityFixture(t, replacement, []byte("replacement"))
	linuxCapabilityEUID = func() int { return 1000 }
	linuxCapabilityTool = func(string) (string, error) { return "/trusted/sudo", nil }
	linuxCapabilityCommand = func(string, ...string) *exec.Cmd {
		if err := os.Rename(replacement, target.Path); err != nil {
			t.Fatal(err)
		}
		return successfulLinuxCapabilityCommand()
	}

	err := (linuxLowPortCapabilityManager{}).Apply(target)
	var capabilityErr *lowPortCapabilityError
	if !errors.As(err, &capabilityErr) || capabilityErr.Code != "capability_target_changed" {
		t.Fatalf("error = %v, want capability_target_changed", err)
	}
}

func TestLinuxCapabilityApplyClassifiesUnsupportedFilesystem(t *testing.T) {
	isolateLinuxCapabilityManager(t)
	target := openLinuxCapabilityTarget(t)
	linuxCapabilityEUID = func() int { return 0 }
	linuxCapabilityFsetxattr = func(int, string, []byte, int) error {
		return unix.EOPNOTSUPP
	}

	err := (linuxLowPortCapabilityManager{}).Apply(target)
	var capabilityErr *lowPortCapabilityError
	if !errors.As(err, &capabilityErr) || capabilityErr.Code != "capability_filesystem_unsupported" {
		t.Fatalf("error = %v, want capability_filesystem_unsupported", err)
	}
	if !strings.Contains(err.Error(), "Linux-native filesystem") {
		t.Fatalf("error lacks recovery: %v", err)
	}
}

func TestLinuxCapabilityApplyReportsMissingSudo(t *testing.T) {
	isolateLinuxCapabilityManager(t)
	target := openLinuxCapabilityTarget(t)
	linuxCapabilityEUID = func() int { return 1000 }
	linuxCapabilityTool = func(string) (string, error) {
		return "", &lowPortCapabilityError{Code: "sudo_not_found", Err: errors.New("missing sudo")}
	}

	err := (linuxLowPortCapabilityManager{}).Apply(target)
	var capabilityErr *lowPortCapabilityError
	if !errors.As(err, &capabilityErr) || capabilityErr.Code != "sudo_not_found" {
		t.Fatalf("error = %v, want sudo_not_found", err)
	}
}

func TestLinuxCapabilityApplySeparatesSudoRejection(t *testing.T) {
	isolateLinuxCapabilityManager(t)
	target := openLinuxCapabilityTarget(t)
	linuxCapabilityEUID = func() int { return 1000 }
	linuxCapabilityTool = func(string) (string, error) { return "/trusted/sudo", nil }
	linuxCapabilityCommand = func(string, ...string) *exec.Cmd {
		return exec.Command("/bin/sh", "-c", "echo 'sudo: a password is required' >&2; exit 1")
	}

	err := (linuxLowPortCapabilityManager{}).Apply(target)
	var capabilityErr *lowPortCapabilityError
	if !errors.As(err, &capabilityErr) || capabilityErr.Code != "sudo_failed" {
		t.Fatalf("error = %v, want sudo_failed", err)
	}
}

func TestLinuxCapabilityApplyFindsHelperErrorAfterSudoDiagnostic(t *testing.T) {
	isolateLinuxCapabilityManager(t)
	target := openLinuxCapabilityTarget(t)
	linuxCapabilityEUID = func() int { return 1000 }
	linuxCapabilityTool = func(name string) (string, error) { return "/trusted/" + name, nil }
	call := 0
	linuxCapabilityCommand = func(string, ...string) *exec.Cmd {
		call++
		if call == 1 {
			return successfulLinuxCapabilityCommand()
		}
		return exec.Command(
			"/bin/sh",
			"-c",
			"printf '%s\\n' 'sudo: unable to resolve host' 'gate-capability-helper:filesystem_unsupported:operation not supported' >&2; exit 1",
		)
	}

	err := (linuxLowPortCapabilityManager{}).Apply(target)
	var capabilityErr *lowPortCapabilityError
	if !errors.As(err, &capabilityErr) || capabilityErr.Code != "capability_filesystem_unsupported" {
		t.Fatalf("error = %v, want capability_filesystem_unsupported", err)
	}
}

func TestLowPortCapabilityHelperWritesOnlyVerifiedTarget(t *testing.T) {
	isolateLinuxCapabilityManager(t)
	target := openLinuxCapabilityTarget(t)
	linuxCapabilityEUID = func() int { return 0 }
	t.Setenv("SUDO_UID", strconv.Itoa(os.Getuid()))
	helper, err := os.CreateTemp(
		"/tmp",
		lowPortCapabilityHelperPrefix+strconv.Itoa(os.Getuid())+"-*",
	)
	if err != nil {
		t.Fatal(err)
	}
	helperPath := helper.Name()
	if _, err := helper.Write([]byte("fixture")); err != nil {
		t.Fatal(err)
	}
	if err := helper.Chmod(0o700); err != nil {
		t.Fatal(err)
	}
	if err := helper.Close(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(helperPath) })
	linuxCapabilityExecutable = func() (string, error) {
		return helperPath, nil
	}
	rootInfo, err := os.Stat("/bin/sh")
	if err != nil {
		t.Fatal(err)
	}
	linuxCapabilityStat = func(path string) (os.FileInfo, error) {
		if path == helperPath {
			return rootInfo, nil
		}
		return os.Stat(path)
	}
	expectedHash, err := hashOpenFile(target.File)
	if err != nil {
		t.Fatal(err)
	}
	var gotName string
	var gotValue []byte
	linuxCapabilityFsetxattr = func(_ int, name string, value []byte, _ int) error {
		gotName = name
		gotValue = append([]byte{}, value...)
		return nil
	}

	var stderr bytes.Buffer
	targetStat, _ := selfInfoSys(target.Info)
	args := []string{
		"--target", target.Path,
		"--device", strconv.FormatUint(uint64(targetStat.Dev), 10),
		"--inode", strconv.FormatUint(targetStat.Ino, 10),
		"--sha256", expectedHash,
	}
	if code := LowPortCapabilityHelper(args, nil, &stderr); code != ExitOK {
		t.Fatalf("helper exit = %d, stderr = %q", code, stderr.String())
	}
	if gotName != lowPortCapabilityXattr || !slices.Equal(gotValue, lowPortCapabilityXattrBytes()) {
		t.Fatalf("xattr = %q %x", gotName, gotValue)
	}
}

func TestTrustedRootExecutableRejectsWritableAncestor(t *testing.T) {
	isolateLinuxCapabilityManager(t)
	rootToolInfo, err := os.Stat("/bin/sh")
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(os.TempDir(), "gate-root-tool")
	linuxCapabilityEval = func(string) (string, error) { return target, nil }
	linuxCapabilityStat = func(path string) (os.FileInfo, error) {
		if path == target {
			return rootToolInfo, nil
		}
		return os.Stat(path)
	}

	if _, err := trustedRootExecutable("/ignored"); err == nil || !strings.Contains(err.Error(), "ancestor") {
		t.Fatalf("writable ancestor accepted: %v", err)
	}
}

func TestHashOpenFileIncludesContentAppendedAfterInitialStat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gate")
	writeExecutableLinuxCapabilityFixture(t, path, []byte("before"))
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	if _, err := file.Stat(); err != nil {
		t.Fatal(err)
	}
	appendFile, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := appendFile.WriteString("-after"); err != nil {
		_ = appendFile.Close()
		t.Fatal(err)
	}
	if err := appendFile.Close(); err != nil {
		t.Fatal(err)
	}

	got, err := hashOpenFile(file)
	if err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256([]byte("before-after"))
	if got != hex.EncodeToString(want[:]) {
		t.Fatalf("hash = %s, want %x", got, want)
	}
}

func TestLowPortSetupLockRejectsRemovableStateOverlap(t *testing.T) {
	home := t.TempDir()
	configHome := filepath.Join(home, "config")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(configHome, "gate"))

	lock, err := acquireLowPortCapabilitySetupLock()
	if lock != nil {
		_ = lock.Close()
	}
	var capabilityErr *lowPortCapabilityError
	if !errors.As(err, &capabilityErr) || capabilityErr.Code != "capability_setup_lock" {
		t.Fatalf("error = %v, want capability_setup_lock", err)
	}
}

func TestLowPortSetupLockRejectsNonexistentCacheBelowSymlinkedState(t *testing.T) {
	home := t.TempDir()
	configHome := filepath.Join(home, "config")
	configDir := filepath.Join(configHome, "gate")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cacheLink := filepath.Join(home, "cache-link")
	if err := os.Symlink(configDir, cacheLink); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(cacheLink, "new-cache"))

	lock, err := acquireLowPortCapabilitySetupLock()
	if lock != nil {
		_ = lock.Close()
	}
	var capabilityErr *lowPortCapabilityError
	if !errors.As(err, &capabilityErr) || capabilityErr.Code != "capability_setup_lock" {
		t.Fatalf("error = %v, want capability_setup_lock", err)
	}
}

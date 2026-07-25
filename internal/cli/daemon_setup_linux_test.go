//go:build linux

package cli

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
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
	oldSelfStat := linuxCapabilitySelfStat
	oldFgetxattr := linuxCapabilityFgetxattr
	oldFsetxattr := linuxCapabilityFsetxattr
	t.Cleanup(func() {
		linuxCapabilityCommand = oldCommand
		linuxCapabilityEUID = oldEUID
		linuxCapabilityTool = oldTool
		linuxCapabilityEval = oldEval
		linuxCapabilityStat = oldStat
		linuxCapabilitySelfStat = oldSelfStat
		linuxCapabilityFgetxattr = oldFgetxattr
		linuxCapabilityFsetxattr = oldFsetxattr
	})
}

func openLinuxCapabilityTarget(t *testing.T) *lowPortCapabilityTarget {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gate")
	if err := os.WriteFile(path, []byte("fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
	target, err := resolveLowPortCapabilityTarget(path)
	if err != nil {
		t.Fatal(err)
	}
	linuxCapabilitySelfStat = func() (os.FileInfo, error) { return target.Info, nil }
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
		if name != "sudo" {
			t.Fatalf("tool = %q, want sudo", name)
		}
		return "/trusted/sudo", nil
	}

	var executable string
	var args []string
	linuxCapabilityCommand = func(name string, commandArgs ...string) *exec.Cmd {
		executable = name
		args = append([]string{}, commandArgs...)
		return successfulLinuxCapabilityCommand()
	}

	if err := (linuxLowPortCapabilityManager{}).Apply(target); err != nil {
		t.Fatal(err)
	}
	want := []string{"--", target.operationPath(), lowPortCapabilityHelperName}
	if executable != "/trusted/sudo" || !slices.Equal(args, want) {
		t.Fatalf("command = %q %q, want %q %q", executable, args, "/trusted/sudo", want)
	}
	if !strings.HasPrefix(target.operationPath(), "/proc/") || !strings.HasSuffix(target.operationPath(), "/exe") {
		t.Fatalf("helper executable is not the running gate inode: %q", target.operationPath())
	}
}

func TestLinuxCapabilityApplyRejectsExecutableOtherThanRunningGate(t *testing.T) {
	isolateLinuxCapabilityManager(t)
	target := openLinuxCapabilityTarget(t)
	otherPath := filepath.Join(t.TempDir(), "other")
	if err := os.WriteFile(otherPath, []byte("other"), 0o700); err != nil {
		t.Fatal(err)
	}
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
	if err := os.WriteFile(replacement, []byte("replacement"), 0o700); err != nil {
		t.Fatal(err)
	}
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

func TestLowPortCapabilityHelperWritesOnlyItsExecutable(t *testing.T) {
	isolateLinuxCapabilityManager(t)
	linuxCapabilityEUID = func() int { return 0 }
	var gotName string
	var gotValue []byte
	linuxCapabilityFsetxattr = func(_ int, name string, value []byte, _ int) error {
		gotName = name
		gotValue = append([]byte{}, value...)
		return nil
	}

	var stderr bytes.Buffer
	if code := LowPortCapabilityHelper(nil, nil, &stderr); code != ExitOK {
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

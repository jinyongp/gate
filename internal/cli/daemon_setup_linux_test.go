//go:build linux

package cli

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func isolateLinuxCapabilityManager(t *testing.T) {
	t.Helper()
	oldCommand := linuxCapabilityCommand
	oldEUID := linuxCapabilityEUID
	oldTool := linuxCapabilityTool
	oldEval := linuxCapabilityEval
	oldStat := linuxCapabilityStat
	t.Cleanup(func() {
		linuxCapabilityCommand = oldCommand
		linuxCapabilityEUID = oldEUID
		linuxCapabilityTool = oldTool
		linuxCapabilityEval = oldEval
		linuxCapabilityStat = oldStat
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
	t.Cleanup(target.Close)
	return target
}

func successfulLinuxCapabilityCommand() *exec.Cmd {
	return exec.Command("/bin/sh", "-c", "exit 0")
}

func TestLinuxCapabilityInspectParsesExactCapability(t *testing.T) {
	isolateLinuxCapabilityManager(t)
	linuxCapabilityTool = func(name string) (string, error) {
		if name != "getcap" {
			t.Fatalf("tool = %q, want getcap", name)
		}
		return "/trusted/getcap", nil
	}
	linuxCapabilityCommand = func(name string, args ...string) *exec.Cmd {
		if name != "/trusted/getcap" || !slices.Equal(args, []string{"/stable/gate"}) {
			t.Fatalf("command = %q %q", name, args)
		}
		return exec.Command("/bin/sh", "-c", "printf '/stable/gate cap_net_bind_service=ep\\n'")
	}

	inspection, err := (linuxLowPortCapabilityManager{}).Inspect("/stable/gate")
	if err != nil {
		t.Fatal(err)
	}
	if inspection.State != lowPortCapabilityConfigured || inspection.Raw != lowPortCapability {
		t.Fatalf("inspection = %+v", inspection)
	}
}

func TestLinuxCapabilityApplyUsesStableFDAndFixedSudoArgv(t *testing.T) {
	isolateLinuxCapabilityManager(t)
	target := openLinuxCapabilityTarget(t)
	linuxCapabilityEUID = func() int { return 1000 }
	linuxCapabilityTool = func(name string) (string, error) {
		return "/trusted/" + name, nil
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
	want := []string{"--", "/trusted/setcap", lowPortCapability, target.operationPath()}
	if executable != "/trusted/sudo" || !slices.Equal(args, want) {
		t.Fatalf("command = %q %q, want %q %q", executable, args, "/trusted/sudo", want)
	}
	if !strings.HasPrefix(target.operationPath(), "/proc/") || !strings.Contains(target.operationPath(), "/fd/") {
		t.Fatalf("capability target is not a stable proc fd: %q", target.operationPath())
	}
}

func TestLinuxCapabilityApplyAsRootSkipsSudo(t *testing.T) {
	isolateLinuxCapabilityManager(t)
	target := openLinuxCapabilityTarget(t)
	linuxCapabilityEUID = func() int { return 0 }
	linuxCapabilityTool = func(name string) (string, error) {
		if name == "sudo" {
			t.Fatal("root setup must not discover sudo")
		}
		return "/trusted/" + name, nil
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
	want := []string{lowPortCapability, target.operationPath()}
	if executable != "/trusted/setcap" || !slices.Equal(args, want) {
		t.Fatalf("command = %q %q, want %q %q", executable, args, "/trusted/setcap", want)
	}
}

func TestLinuxCapabilityApplyDetectsPathReplacement(t *testing.T) {
	isolateLinuxCapabilityManager(t)
	target := openLinuxCapabilityTarget(t)
	replacement := filepath.Join(filepath.Dir(target.Path), "replacement")
	if err := os.WriteFile(replacement, []byte("replacement"), 0o700); err != nil {
		t.Fatal(err)
	}
	linuxCapabilityEUID = func() int { return 0 }
	linuxCapabilityTool = func(name string) (string, error) { return "/trusted/" + name, nil }
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
	linuxCapabilityTool = func(name string) (string, error) { return "/trusted/" + name, nil }
	linuxCapabilityCommand = func(string, ...string) *exec.Cmd {
		return exec.Command("/bin/sh", "-c", "echo 'Operation not supported' >&2; exit 1")
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

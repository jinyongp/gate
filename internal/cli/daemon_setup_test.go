package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeLowPortCapabilityManager struct {
	inspections []lowPortCapabilityInspection
	inspectErr  error
	applyErr    error
	applied     int
}

func (m *fakeLowPortCapabilityManager) Inspect(string) (lowPortCapabilityInspection, error) {
	if m.inspectErr != nil {
		return lowPortCapabilityInspection{}, m.inspectErr
	}
	if len(m.inspections) == 0 {
		return lowPortCapabilityInspection{}, errors.New("unexpected inspection")
	}
	result := m.inspections[0]
	m.inspections = m.inspections[1:]
	return result, nil
}

func (m *fakeLowPortCapabilityManager) Apply(*lowPortCapabilityTarget) error {
	m.applied++
	return m.applyErr
}

func stubLowPortCapabilitySetup(t *testing.T, manager lowPortCapabilityManager) {
	t.Helper()
	oldGOOS := runtimeGOOS
	oldManager := lowPortCapabilityManagerFunc
	oldTarget := lowPortCapabilityTargetFunc
	oldConfirm := confirmLowPortSetupFunc
	oldTTY := stdinIsTTYFunc
	t.Cleanup(func() {
		runtimeGOOS = oldGOOS
		lowPortCapabilityManagerFunc = oldManager
		lowPortCapabilityTargetFunc = oldTarget
		confirmLowPortSetupFunc = oldConfirm
		stdinIsTTYFunc = oldTTY
	})
	runtimeGOOS = func() string { return "linux" }
	lowPortCapabilityManagerFunc = func() lowPortCapabilityManager { return manager }
	lowPortCapabilityTargetFunc = func(string) (*lowPortCapabilityTarget, error) {
		return &lowPortCapabilityTarget{Path: "/opt/gate/bin/gate"}, nil
	}
	confirmLowPortSetupFunc = func(io.Writer) (bool, error) { return true, nil }
	stdinIsTTYFunc = func() bool { return true }
	t.Setenv("GATE_ISOLATED_ROOT", "")
}

func TestDaemonSetupConfiguredIsIdempotent(t *testing.T) {
	manager := &fakeLowPortCapabilityManager{
		inspections: []lowPortCapabilityInspection{{State: lowPortCapabilityConfigured, Raw: lowPortCapability}},
	}
	stubLowPortCapabilitySetup(t, manager)

	var out, errb bytes.Buffer
	if code := daemonSetup([]string{"--check", "--json"}, &out, &errb); code != ExitOK {
		t.Fatalf("daemonSetup exit = %d, stderr=%s", code, errb.String())
	}
	var result daemonSetupResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != string(lowPortCapabilityConfigured) || result.Changed || result.Executable != "/opt/gate/bin/gate" {
		t.Fatalf("result = %+v", result)
	}
	if manager.applied != 0 {
		t.Fatalf("Apply called %d times", manager.applied)
	}
}

func TestDaemonSetupCheckReportsMissingAction(t *testing.T) {
	manager := &fakeLowPortCapabilityManager{
		inspections: []lowPortCapabilityInspection{{State: lowPortCapabilityMissing}},
	}
	stubLowPortCapabilitySetup(t, manager)

	var out, errb bytes.Buffer
	if code := daemonSetup([]string{"--check", "--json"}, &out, &errb); code != ExitPerm {
		t.Fatalf("daemonSetup exit = %d, stderr=%s", code, errb.String())
	}
	var envelope errEnvelope
	if err := json.Unmarshal(errb.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error.Code != "low_port_capability_missing" {
		t.Fatalf("error = %+v", envelope.Error)
	}
	if len(envelope.Error.NextActions) != 1 || envelope.Error.NextActions[0].Command != "gate daemon setup" {
		t.Fatalf("actions = %+v", envelope.Error.NextActions)
	}
	if manager.applied != 0 {
		t.Fatalf("Apply called %d times", manager.applied)
	}
}

func TestDaemonSetupAppliesAndVerifies(t *testing.T) {
	manager := &fakeLowPortCapabilityManager{
		inspections: []lowPortCapabilityInspection{
			{State: lowPortCapabilityMissing},
			{State: lowPortCapabilityConfigured, Raw: lowPortCapability},
		},
	}
	stubLowPortCapabilitySetup(t, manager)

	var out, errb bytes.Buffer
	if code := daemonSetup([]string{"--yes", "--json"}, &out, &errb); code != ExitOK {
		t.Fatalf("daemonSetup exit = %d, stderr=%s", code, errb.String())
	}
	var result daemonSetupResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.Changed || manager.applied != 1 {
		t.Fatalf("result = %+v, applied=%d", result, manager.applied)
	}
}

func TestDaemonSetupRejectsUnexpectedCapabilities(t *testing.T) {
	manager := &fakeLowPortCapabilityManager{
		inspections: []lowPortCapabilityInspection{{State: lowPortCapabilityUnexpected, Raw: "cap_net_admin=ep"}},
	}
	stubLowPortCapabilitySetup(t, manager)

	var out, errb bytes.Buffer
	if code := daemonSetup([]string{"--yes"}, &out, &errb); code != ExitConflict {
		t.Fatalf("daemonSetup exit = %d, stderr=%s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "unexpected Linux capabilities") || manager.applied != 0 {
		t.Fatalf("stderr=%q applied=%d", errb.String(), manager.applied)
	}
}

func TestDaemonSetupJSONRequiresExplicitMode(t *testing.T) {
	stubLowPortCapabilitySetup(t, &fakeLowPortCapabilityManager{})

	var out, errb bytes.Buffer
	if code := daemonSetup([]string{"--json"}, &out, &errb); code != ExitUsage {
		t.Fatalf("daemonSetup exit = %d, stderr=%s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "confirmation_required") {
		t.Fatalf("stderr = %q", errb.String())
	}
}

func TestDaemonSetupRejectsIsolatedStateBeforeInspection(t *testing.T) {
	manager := &fakeLowPortCapabilityManager{}
	stubLowPortCapabilitySetup(t, manager)
	t.Setenv("GATE_ISOLATED_ROOT", t.TempDir())

	var out, errb bytes.Buffer
	if code := daemonSetup([]string{"--yes"}, &out, &errb); code != ExitConflict {
		t.Fatalf("daemonSetup exit = %d, stderr=%s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "unavailable with isolated state") {
		t.Fatalf("stderr = %q", errb.String())
	}
}

func TestDaemonSetupUnsupportedPlatform(t *testing.T) {
	manager := &fakeLowPortCapabilityManager{}
	stubLowPortCapabilitySetup(t, manager)
	runtimeGOOS = func() string { return "darwin" }

	var out, errb bytes.Buffer
	if code := daemonSetup([]string{"--yes"}, &out, &errb); code != ExitUsage {
		t.Fatalf("daemonSetup exit = %d, stderr=%s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "Linux only") {
		t.Fatalf("stderr = %q", errb.String())
	}
}

func TestDaemonSetupHelpVisibilityFollowsPlatform(t *testing.T) {
	oldGOOS := runtimeGOOS
	t.Cleanup(func() { runtimeGOOS = oldGOOS })

	runtimeGOOS = func() string { return "darwin" }
	if got := commandInfoNames(commandsFor("daemon")); strings.Contains(got, "setup") {
		t.Fatalf("darwin daemon commands = %q", got)
	}
	if got := specFor("daemon").Args; strings.Contains(got, "setup") {
		t.Fatalf("darwin daemon usage = %q", got)
	}
	runtimeGOOS = func() string { return "linux" }
	if got := commandInfoNames(commandsFor("daemon")); !strings.Contains(got, "setup") {
		t.Fatalf("linux daemon commands = %q", got)
	}
	if got := specFor("daemon").Args; !strings.Contains(got, "setup") {
		t.Fatalf("linux daemon usage = %q", got)
	}
}

func TestUnsupportedCapabilityFilesystemProvidesNativeFilesystemRecovery(t *testing.T) {
	body := errorBodyFor(
		ExitPerm,
		"capability_filesystem_unsupported",
		"filesystem does not support Linux file capabilities",
	)
	if !strings.Contains(body.Hint, "Linux-native filesystem") {
		t.Fatalf("hint = %q", body.Hint)
	}
	if len(body.NextActions) != 1 ||
		!strings.Contains(body.NextActions[0].Label, "Linux-native filesystem") ||
		body.NextActions[0].Command != "gate daemon setup" {
		t.Fatalf("next actions = %+v", body.NextActions)
	}
}

func TestResolveLowPortCapabilityTargetCanonicalizesExecutable(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "gate-real")
	if err := os.WriteFile(target, []byte("binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	//nolint:gosec // G302: the capability target fixture must be executable; owner-only is intentional.
	if err := os.Chmod(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "gate")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	got, err := resolveLowPortCapabilityTarget(link)
	if err != nil {
		t.Fatal(err)
	}
	defer got.Close()
	want, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != want {
		t.Fatalf("target = %q, want %q", got.Path, want)
	}
}

func commandInfoNames(commands []CommandInfo) string {
	names := make([]string, 0, len(commands))
	for _, command := range commands {
		names = append(names, command.Name)
	}
	return strings.Join(names, ",")
}

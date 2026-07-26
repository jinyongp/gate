//go:build darwin || linux

package integrationtest

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

const bindServiceCapability = uint64(0x400)

func TestLinuxLowPortIntegration(t *testing.T) {
	run := parseBooleanEnvironment(t, "GATE_RUN_LINUX_LOW_PORT_TEST")
	required := parseBooleanEnvironment(t, "GATE_REQUIRE_LINUX_LOW_PORT_TEST")
	if !run {
		t.Skip("Linux low-port integration was not requested")
	}
	requireHostCapability := func(reason string) {
		t.Helper()
		if required {
			t.Fatalf("Linux low-port integration requirement unavailable: %s", reason)
		}
		t.Skipf("Linux low-port integration: %s", reason)
	}
	if runtime.GOOS != "linux" {
		requireHostCapability("Linux host required")
	}
	if os.Geteuid() == 0 {
		requireHostCapability("non-root test user required")
	}
	if _, err := os.Stat("/proc/self/status"); err != nil {
		requireHostCapability("/proc capability status unavailable")
	}

	tools := map[string]string{}
	for _, name := range []string{"getcap", "setcap", "sudo", "mktemp", "install"} {
		path := trustedExecutable(name)
		if path == "" {
			requireHostCapability("trusted getcap, setcap, sudo, mktemp, or install tool unavailable")
		}
		tools[name] = path
	}
	baseEnvironment := map[string]string{
		"HOME": os.Getenv("HOME"),
		"PATH": "/usr/bin:/bin:/usr/sbin:/sbin",
	}
	if result := runCombined(t, repositoryRoot(t), baseEnvironment, tools["sudo"], "-n", "true"); result.err != nil {
		requireHostCapability("passwordless non-interactive sudo unavailable")
	}

	repository := repositoryRoot(t)
	fixture := t.TempDir()
	gateBinary := filepath.Join(fixture, "gate")
	shellGateBinary := filepath.Join(fixture, "shell-bin", "gate")
	configHome := filepath.Join(fixture, "config")
	dataHome := filepath.Join(fixture, "data")
	stateHome := filepath.Join(fixture, "state")
	cacheHome := filepath.Join(fixture, "cache")
	testHome := filepath.Join(fixture, "home")
	projectDirectory := filepath.Join(fixture, "project")
	for _, directory := range []string{
		configHome,
		dataHome,
		stateHome,
		cacheHome,
		testHome,
		filepath.Dir(shellGateBinary),
		projectDirectory,
	} {
		if err := os.MkdirAll(directory, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(projectDirectory, "gate.toml"), `[project]
name = "capability-fixture"
base = "capability-fixture.localhost"

[services.probe]
port = 49191
`, 0o600)
	build := runCombined(t, repository, baseEnvironment, "go", "build", "-trimpath", "-o", gateBinary, "./cmd/gate")
	if build.err != nil {
		t.Fatalf("build gate: %v\n%s", build.err, build.output)
	}
	if err := copyExecutable(gateBinary, shellGateBinary); err != nil {
		t.Fatal(err)
	}
	fakeTool := filepath.Join(fixture, "fake-tool")
	buildFakeTool(t, repository, fakeTool)

	daemonStarted := false
	commandEnvironment := map[string]string{
		"PATH":            "/usr/bin:/bin:/usr/sbin:/sbin",
		"HOME":            testHome,
		"XDG_CONFIG_HOME": configHome,
		"XDG_DATA_HOME":   dataHome,
		"XDG_STATE_HOME":  stateHome,
		"XDG_CACHE_HOME":  cacheHome,
	}
	t.Cleanup(func() {
		if daemonStarted {
			_ = runCombined(t, repository, commandEnvironment, gateBinary, "daemon", "stop")
		}
		for _, path := range rootHelperResidues(os.Geteuid()) {
			_ = runCombined(t, repository, baseEnvironment, tools["sudo"], "-n", "--", "rm", "-f", "--", path)
		}
	})

	setup := runCombined(t, repository, commandEnvironment, gateBinary, "daemon", "setup", "--yes")
	if setup.err != nil {
		requireHostCapability("file capability setup failed: " + strings.TrimSpace(setup.output))
	}
	capability := runCombined(t, repository, baseEnvironment, tools["getcap"], gateBinary)
	if capability.err != nil || !strings.HasSuffix(strings.TrimSpace(capability.output), "cap_net_bind_service=ep") {
		t.Fatalf("unexpected gate capability: %q (%v)", capability.output, capability.err)
	}

	start := runCombined(
		t,
		repository,
		commandEnvironment,
		gateBinary,
		"up",
		"-d",
		"--config",
		filepath.Join(projectDirectory, "gate.toml"),
	)
	if start.err != nil {
		if strings.Contains(start.output, "address already in use") {
			requireHostCapability("TCP port 80 or 443 is already in use")
		}
		t.Fatalf("configured gate could not bind default ports: %v\n%s", start.err, start.output)
	}
	daemonStarted = true

	statusResult := runCombined(t, repository, commandEnvironment, gateBinary, "daemon", "status", "--json")
	if statusResult.err != nil {
		t.Fatalf("daemon status: %v\n%s", statusResult.err, statusResult.output)
	}
	var status struct {
		Running   bool   `json:"running"`
		PID       int    `json:"pid"`
		HTTPSAddr string `json:"https_addr"`
		HTTPAddr  string `json:"http_addr"`
	}
	if err := json.Unmarshal([]byte(statusResult.output), &status); err != nil {
		t.Fatalf("decode daemon status: %v\n%s", err, statusResult.output)
	}
	if !status.Running {
		t.Fatalf("daemon status does not report running: %+v", status)
	}
	assertPort(t, status.HTTPSAddr, "443")
	assertPort(t, status.HTTPAddr, "80")
	listenerCapabilities := readEffectiveCapabilities(t, status.PID)
	if listenerCapabilities&bindServiceCapability == 0 {
		t.Fatalf("gate listener lacks CAP_NET_BIND_SERVICE: %x", listenerCapabilities)
	}

	child := runCombined(
		t,
		repository,
		commandEnvironment,
		gateBinary,
		"run",
		"--up",
		"--quiet",
		"--config",
		filepath.Join(projectDirectory, "gate.toml"),
		"probe",
		"--",
		fakeTool,
		"print-cap-eff",
	)
	if child.err != nil {
		t.Fatalf("gate run child failed: %v\n%s", child.err, child.output)
	}
	childCapabilities, err := strconv.ParseUint(strings.TrimSpace(child.output), 16, 64)
	if err != nil {
		t.Fatalf("parse child CapEff %q: %v", child.output, err)
	}
	if childCapabilities&bindServiceCapability != 0 {
		t.Fatalf("gate run child inherited CAP_NET_BIND_SERVICE: %x", childCapabilities)
	}
	t.Logf("gate listener bound :443/:80 with CapEff=%x", listenerCapabilities)
	t.Logf("gate run child omitted CAP_NET_BIND_SERVICE with CapEff=%x", childCapabilities)

	stop := runCombined(t, repository, commandEnvironment, gateBinary, "daemon", "stop")
	if stop.err != nil {
		t.Fatalf("stop daemon: %v\n%s", stop.err, stop.output)
	}
	daemonStarted = false

	if result := runCombined(t, repository, baseEnvironment, tools["sudo"], "-n", "--", tools["setcap"], "-r", gateBinary); result.err != nil {
		t.Fatalf("remove gate capability: %v\n%s", result.err, result.output)
	}
	setupResidue := makeRootHelperResidue(t, repository, baseEnvironment, tools, gateBinary)
	if _, err := os.Stat(setupResidue); err != nil {
		t.Fatalf("setup residue is missing: %v", err)
	}
	setupRecovery := runCombined(t, repository, commandEnvironment, gateBinary, "daemon", "setup", "--yes")
	if setupRecovery.err != nil {
		t.Fatalf("setup recovery: %v\n%s", setupRecovery.err, setupRecovery.output)
	}
	assertNoRootHelperResidue(t)

	builtinResidue := makeRootHelperResidue(t, repository, baseEnvironment, tools, gateBinary)
	builtinEnvironment := cloneEnvironment(commandEnvironment)
	builtinEnvironment["GATE_BIN_DIR"] = fixture
	builtin := runCombined(t, repository, builtinEnvironment, gateBinary, "uninstall", "--yes", "--keep-trust")
	if builtin.err != nil {
		t.Fatalf("built-in uninstall: %v\n%s", builtin.err, builtin.output)
	}
	requireNotExists(t, gateBinary)
	requireNotExists(t, builtinResidue)

	shellResidue := makeRootHelperResidue(t, repository, baseEnvironment, tools, shellGateBinary)
	shellEnvironment := map[string]string{
		"PATH":            "/usr/bin:/bin:/usr/sbin:/sbin",
		"HOME":            testHome,
		"XDG_CONFIG_HOME": filepath.Join(fixture, "shell-config"),
		"XDG_DATA_HOME":   filepath.Join(fixture, "shell-data"),
		"XDG_STATE_HOME":  filepath.Join(fixture, "shell-state"),
		"XDG_CACHE_HOME":  filepath.Join(fixture, "shell-cache"),
		"GATE_BIN_DIR":    filepath.Dir(shellGateBinary),
	}
	shellUninstall := runCombined(
		t,
		repository,
		shellEnvironment,
		"sh",
		"scripts/uninstall.sh",
		"--yes",
		"--keep-trust",
	)
	if shellUninstall.err != nil {
		t.Fatalf("standalone uninstall: %v\n%s", shellUninstall.err, shellUninstall.output)
	}
	requireNotExists(t, shellGateBinary)
	requireNotExists(t, shellResidue)
	assertNoRootHelperResidue(t)
	t.Log("setup recovery and both uninstall paths removed root helper residue")
}

func parseBooleanEnvironment(t *testing.T, name string) bool {
	t.Helper()
	value := os.Getenv(name)
	switch value {
	case "", "0":
		return false
	case "1":
		return true
	default:
		t.Fatalf("%s must be 0 or 1", name)
		return false
	}
}

func trustedExecutable(name string) string {
	for _, directory := range []string{"/usr/sbin", "/sbin", "/usr/bin", "/bin"} {
		path := filepath.Join(directory, name)
		info, err := os.Stat(path)
		if err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0 {
			return path
		}
	}
	return ""
}

func assertPort(t *testing.T, address, wanted string) {
	t.Helper()
	_, port, err := net.SplitHostPort(address)
	if err != nil || port != wanted {
		t.Fatalf("address %q does not use port %s: %v", address, wanted, err)
	}
}

func readEffectiveCapabilities(t *testing.T, pid int) uint64 {
	t.Helper()
	if pid <= 0 {
		t.Fatalf("invalid daemon PID %d", pid)
	}
	content, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		t.Fatalf("read daemon capabilities: %v", err)
	}
	for _, line := range strings.Split(string(content), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "CapEff:" {
			value, err := strconv.ParseUint(fields[1], 16, 64)
			if err != nil {
				t.Fatalf("parse daemon CapEff: %v", err)
			}
			return value
		}
	}
	t.Fatal("daemon CapEff is missing")
	return 0
}

func makeRootHelperResidue(
	t *testing.T,
	repository string,
	environment map[string]string,
	tools map[string]string,
	source string,
) string {
	t.Helper()
	pattern := fmt.Sprintf("/tmp/.gate-capability-helper-%d-XXXXXXXX", os.Geteuid())
	temporary := runCombined(
		t,
		repository,
		environment,
		tools["sudo"],
		"-n",
		"--",
		tools["mktemp"],
		pattern,
	)
	if temporary.err != nil {
		t.Fatalf("create root helper residue: %v\n%s", temporary.err, temporary.output)
	}
	path := strings.TrimSpace(temporary.output)
	install := runCombined(
		t,
		repository,
		environment,
		tools["sudo"],
		"-n",
		"--",
		tools["install"],
		"-m",
		"0755",
		"--",
		source,
		path,
	)
	if install.err != nil {
		t.Fatalf("populate root helper residue: %v\n%s", install.err, install.output)
	}
	return path
}

func rootHelperResidues(uid int) []string {
	pattern := fmt.Sprintf(".gate-capability-helper-%d-*", uid)
	var paths []string
	for _, directory := range []string{"/tmp", "/var/tmp"} {
		matches, _ := filepath.Glob(filepath.Join(directory, pattern))
		paths = append(paths, matches...)
	}
	return paths
}

func assertNoRootHelperResidue(t *testing.T) {
	t.Helper()
	if paths := rootHelperResidues(os.Geteuid()); len(paths) > 0 {
		t.Fatalf("root-owned low-port helper residue remains: %v", paths)
	}
}

func copyExecutable(source, destination string) error {
	content, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	if err := os.WriteFile(destination, content, 0o600); err != nil { //nolint:gosec // test-owned destination
		return err
	}
	return os.Chmod(destination, 0o700) //nolint:gosec // executable fixture must be runnable
}

func cloneEnvironment(source map[string]string) map[string]string {
	cloned := make(map[string]string, len(source)+1)
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

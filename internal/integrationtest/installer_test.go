//go:build darwin || linux

package integrationtest

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type installerFixture struct {
	repository string
	root       string
	fakeBin    string
	asset      string
}

func newInstallerFixture(t *testing.T) installerFixture {
	t.Helper()
	repository := repositoryRoot(t)
	root := t.TempDir()
	fakeBin := filepath.Join(root, "fake-bin")
	if err := os.Mkdir(fakeBin, 0o750); err != nil {
		t.Fatal(err)
	}
	tool := filepath.Join(root, "fake-tool")
	if err := os.Mkdir(filepath.Join(root, "tmp"), 0o750); err != nil {
		t.Fatal(err)
	}
	buildFakeTool(t, repository, tool)
	for _, name := range []string{"uname", "curl", "sha256sum", "getcap", "flock"} {
		linkTool(t, tool, filepath.Join(fakeBin, name))
	}
	asset := filepath.Join(root, "gate-linux-amd64")
	linkTool(t, tool, asset)
	return installerFixture{
		repository: repository,
		root:       root,
		fakeBin:    fakeBin,
		asset:      asset,
	}
}

func (fixture installerFixture) environment(caseRoot string, extra map[string]string) map[string]string {
	environment := map[string]string{
		"PATH":                     fixture.fakeBin + ":/usr/bin:/bin",
		"HOME":                     filepath.Join(caseRoot, "home"),
		"SHELL":                    "/bin/sh",
		"GATE_BIN_DIR":             filepath.Join(caseRoot, "bin"),
		"GATE_TEST_ASSET":          fixture.asset,
		"GATE_TEST_SETUP_LOG":      filepath.Join(caseRoot, "setup.log"),
		"GATE_INSTALL_TEST_GETCAP": filepath.Join(fixture.fakeBin, "getcap"),
		"GATE_INSTALL_TEST_FLOCK":  filepath.Join(fixture.fakeBin, "flock"),
		"GATE_TEST_ROOT":           fixture.root,
		"GATE_TEST_TMP_ROOT":       os.TempDir(),
		"TMPDIR":                   filepath.Join(fixture.root, "tmp"),
	}
	for key, value := range extra {
		environment[key] = value
	}
	return environment
}

func (fixture installerFixture) prepareCase(t *testing.T, name string) string {
	t.Helper()
	caseRoot := filepath.Join(fixture.root, name)
	if err := os.MkdirAll(filepath.Join(caseRoot, "home"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(caseRoot, "bin"), 0o750); err != nil {
		t.Fatal(err)
	}
	return caseRoot
}

func (fixture installerFixture) run(
	t *testing.T,
	caseRoot string,
	extra map[string]string,
) commandResult {
	t.Helper()
	return runCombined(
		t,
		fixture.repository,
		fixture.environment(caseRoot, extra),
		"sh",
		"scripts/install.sh",
	)
}

func (fixture installerFixture) runPTY(
	caseRoot string,
	input string,
	extra map[string]string,
) commandResult {
	return runPTY(
		fixture.repository,
		fixture.environment(caseRoot, extra),
		input,
		"sh",
		"scripts/install.sh",
	)
}

func TestStandaloneInstallerIntegration(t *testing.T) {
	fixture := newInstallerFixture(t)

	t.Run("fresh install", func(t *testing.T) {
		caseRoot := fixture.prepareCase(t, "fresh")
		result := fixture.run(t, caseRoot, nil)
		if result.err != nil {
			t.Fatalf("installer failed: %v\n%s", result.err, result.output)
		}
		info, err := os.Stat(filepath.Join(caseRoot, "bin", "gate"))
		if err != nil || info.Mode()&0o111 == 0 {
			t.Fatalf("installed gate is not executable: %v", err)
		}
		requireNotExists(t, filepath.Join(caseRoot, "setup.log"))
		requireContains(t, result.output, "gate daemon setup")
	})

	t.Run("interactive choices and recoverable setup failure", func(t *testing.T) {
		available := true
		runCase := func(name, input string, extra map[string]string) (string, string) {
			t.Helper()
			caseRoot := fixture.prepareCase(t, name)
			result := fixture.runPTY(caseRoot, input, extra)
			if strings.Contains(result.errString(), "start PTY") ||
				strings.Contains(result.output, "non-interactive install") {
				available = false
				return caseRoot, result.output
			}
			if result.err != nil {
				t.Fatalf("interactive installer failed: %v\n%s", result.err, result.output)
			}
			return caseRoot, result.output
		}

		acceptRoot, acceptOutput := runCase("interactive-accept", "\nn\n", nil)
		if !available {
			if os.Getenv("GATE_REQUIRE_INSTALL_PTY_TEST") == "1" {
				t.Fatal("interactive installer PTY fixture is required but unavailable")
			}
			t.Skip("interactive installer PTY fixture unavailable")
		}
		setupPath := filepath.Join(acceptRoot, "setup.log")
		if _, err := os.Stat(setupPath); err != nil {
			t.Fatalf("interactive setup log missing: %v\n%s", err, acceptOutput)
		}
		if got := strings.TrimSpace(readFile(t, setupPath)); got != "daemon setup --yes" {
			t.Fatalf("setup log = %q", got)
		}
		requireContains(t, acceptOutput, "configured Linux low-port capability")

		declineRoot, declineOutput := runCase("interactive-decline", "n\nn\n", nil)
		requireNotExists(t, filepath.Join(declineRoot, "setup.log"))
		requireContains(t, declineOutput, "Linux low-port setup skipped")

		failureRoot, failureOutput := runCase(
			"interactive-setup-failure",
			"\nn\n",
			map[string]string{
				"GATE_TEST_SETUP_EXIT":    "1",
				"GATE_TEST_SETUP_MESSAGE": "Operation not supported",
			},
		)
		if _, err := os.Stat(filepath.Join(failureRoot, "bin", "gate")); err != nil {
			t.Fatal(err)
		}
		requireContains(t, failureOutput, "Linux low-port setup failed")
		requireContains(t, failureOutput, "gate daemon setup")
	})

	t.Run("configured upgrade", func(t *testing.T) {
		caseRoot := fixture.prepareCase(t, "upgrade")
		gatePath := filepath.Join(caseRoot, "bin", "gate")
		writeFile(t, gatePath, "old-gate\n", 0o700)
		result := fixture.run(t, caseRoot, map[string]string{"GATE_TEST_GETCAP_STATE": "configured"})
		if result.err != nil {
			t.Fatalf("upgrade failed: %v\n%s", result.err, result.output)
		}
		if got := strings.TrimSpace(readFile(t, filepath.Join(caseRoot, "setup.log"))); got != "daemon setup --yes" {
			t.Fatalf("setup log = %q", got)
		}
		if bytes.Equal([]byte(readFile(t, gatePath)), []byte("old-gate\n")) {
			t.Fatal("configured upgrade kept the old binary")
		}
	})

	t.Run("setup failure rolls back", func(t *testing.T) {
		caseRoot := fixture.prepareCase(t, "rollback")
		gatePath := filepath.Join(caseRoot, "bin", "gate")
		writeFile(t, gatePath, "old-gate\n", 0o700)
		result := fixture.run(t, caseRoot, map[string]string{
			"GATE_TEST_GETCAP_STATE": "configured",
			"GATE_TEST_SETUP_EXIT":   "1",
		})
		if result.err == nil {
			t.Fatal("configured upgrade succeeded after setup failure")
		}
		if got := readFile(t, gatePath); got != "old-gate\n" {
			t.Fatalf("rolled back gate = %q", got)
		}
		requireContains(t, result.output, "Restored the previous gate binary.")
	})

	t.Run("unsafe transaction symlink", func(t *testing.T) {
		caseRoot := fixture.prepareCase(t, "unsafe-transaction")
		external := filepath.Join(caseRoot, "external")
		if err := os.Mkdir(external, 0o750); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(external, "state"), "preserve-me\n", 0o600)
		if err := os.Symlink(external, filepath.Join(caseRoot, "bin", "gate.install.transaction")); err != nil {
			t.Fatal(err)
		}
		result := fixture.run(t, caseRoot, nil)
		if result.err == nil {
			t.Fatal("installer accepted an unsafe transaction path")
		}
		requireContains(t, result.output, "refusing unsafe gate install transaction")
		if got := readFile(t, filepath.Join(external, "state")); got != "preserve-me\n" {
			t.Fatalf("external state = %q", got)
		}
	})

	t.Run("post-replacement interruption rolls back", func(t *testing.T) {
		caseRoot := fixture.prepareCase(t, "interrupted")
		gatePath := filepath.Join(caseRoot, "bin", "gate")
		writeFile(t, gatePath, "old-gate\n", 0o700)
		result := fixture.run(t, caseRoot, map[string]string{
			"GATE_TEST_GETCAP_STATE":               "configured",
			"GATE_INSTALL_TEST_FAIL_AFTER_REPLACE": "1",
		})
		if result.err == nil {
			t.Fatal("installer succeeded after injected post-replacement failure")
		}
		if got := readFile(t, gatePath); got != "old-gate\n" {
			t.Fatalf("rolled back gate = %q", got)
		}
		requireContains(t, result.output, "restored the previous gate binary")
	})

	t.Run("active lock rejects install", func(t *testing.T) {
		caseRoot := fixture.prepareCase(t, "locked")
		gatePath := filepath.Join(caseRoot, "bin", "gate")
		writeFile(t, gatePath, "old-gate\n", 0o700)
		lockFile(t, gatePath+".install.lock")
		result := fixture.run(t, caseRoot, nil)
		if result.err == nil {
			t.Fatal("installer ignored an active destination lock")
		}
		requireContains(t, result.output, "another gate installation or upgrade")
		if got := readFile(t, gatePath); got != "old-gate\n" {
			t.Fatalf("locked gate = %q", got)
		}
	})

	t.Run("stale replacement recovery", func(t *testing.T) {
		caseRoot := fixture.prepareCase(t, "stale")
		gatePath := filepath.Join(caseRoot, "bin", "gate")
		transaction := gatePath + ".install.transaction"
		if err := os.Mkdir(transaction, 0o750); err != nil {
			t.Fatal(err)
		}
		writeFile(t, gatePath, "replacement\n", 0o700)
		oldPath := filepath.Join(caseRoot, "old-gate")
		writeFile(t, oldPath, "old-gate\n", 0o700)
		if err := os.Link(oldPath, filepath.Join(transaction, "previous")); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(oldPath); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(transaction, "state"), "replacing\n", 0o600)
		result := fixture.run(t, caseRoot, map[string]string{"GATE_TEST_GETCAP_STATE": "configured"})
		if result.err != nil {
			t.Fatalf("stale recovery failed: %v\n%s", result.err, result.output)
		}
		requireContains(t, result.output, "recovered the previous gate binary")
		if got := strings.TrimSpace(readFile(t, filepath.Join(caseRoot, "setup.log"))); got != "daemon setup --yes" {
			t.Fatalf("setup log = %q", got)
		}
		if readFile(t, gatePath) == "old-gate\n" {
			t.Fatal("stale recovery did not commit replacement")
		}
	})

	t.Run("pre-replacement crash is not misclassified", func(t *testing.T) {
		caseRoot := fixture.prepareCase(t, "stale-before-replace")
		gatePath := filepath.Join(caseRoot, "bin", "gate")
		transaction := gatePath + ".install.transaction"
		if err := os.Mkdir(transaction, 0o750); err != nil {
			t.Fatal(err)
		}
		writeFile(t, gatePath, "old-gate\n", 0o700)
		if err := os.Link(gatePath, filepath.Join(transaction, "previous")); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(transaction, "state"), "replacing\n", 0o600)
		result := fixture.run(t, caseRoot, map[string]string{"GATE_TEST_GETCAP_STATE": "configured"})
		if result.err != nil {
			t.Fatalf("recovery failed: %v\n%s", result.err, result.output)
		}
		if strings.Contains(result.output, "recovered the previous gate binary") {
			t.Fatal("pre-replacement crash was misclassified")
		}
	})

	t.Run("concurrent installer", func(t *testing.T) {
		caseRoot := fixture.prepareCase(t, "concurrent")
		gatePath := filepath.Join(caseRoot, "bin", "gate")
		writeFile(t, gatePath, "old-gate\n", 0o700)
		var firstOutput bytes.Buffer
		first := exec.Command("sh", "scripts/install.sh")
		first.Dir = fixture.repository
		first.Env = mergedEnvironment(fixture.environment(caseRoot, map[string]string{
			"GATE_TEST_GETCAP_STATE": "configured",
			"GATE_TEST_SETUP_DELAY":  "1",
		}))
		first.Stdout = &firstOutput
		first.Stderr = &firstOutput
		if err := first.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if first.Process != nil {
				_ = first.Process.Kill()
			}
		})
		if !waitForPath(filepath.Join(caseRoot, "setup.log"), 5*time.Second) {
			t.Fatalf("first installer did not acquire lock\n%s", firstOutput.String())
		}
		second := fixture.run(t, caseRoot, nil)
		if second.err == nil {
			t.Fatal("concurrent installer bypassed the destination lock")
		}
		requireContains(t, second.output, "another gate installation or upgrade")
		if err := first.Wait(); err != nil {
			t.Fatalf("first installer failed: %v\n%s", err, firstOutput.String())
		}
		if got := strings.TrimSpace(readFile(t, filepath.Join(caseRoot, "setup.log"))); got != "daemon setup --yes" {
			t.Fatalf("setup log = %q", got)
		}
		if readFile(t, gatePath) == "old-gate\n" {
			t.Fatal("concurrent install did not commit replacement")
		}
	})

	for _, name := range []string{
		"interactive-accept",
		"interactive-decline",
		"interactive-setup-failure",
		"upgrade",
		"rollback",
		"interrupted",
		"stale",
		"stale-before-replace",
		"concurrent",
	} {
		requireNotExists(t, filepath.Join(fixture.root, name, "bin", "gate.install.transaction"))
	}

	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Fatalf("unsupported integration host %s", runtime.GOOS)
	}
}

func (result commandResult) errString() string {
	if result.err == nil {
		return ""
	}
	return result.err.Error()
}

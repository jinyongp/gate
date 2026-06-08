package cli

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gate/internal/ca"
	"gate/internal/paths"
)

func isolateUninstall(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg-config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "xdg-data"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "xdg-state"))
	t.Setenv("GATE_BIN_DIR", "")
	t.Cleanup(func() {
		uninstallExecutablePathFunc = executablePath
		uninstallRunHomebrewFunc = runHomebrewUninstall
		uninstallHostsPath = "/etc/hosts"
		uninstallSystemBinPaths = []string{"/usr/local/bin/gate"}
		untrustAuthorityFunc = func(authority *ca.CA) error { return authority.Untrust() }
	})
	uninstallSystemBinPaths = nil
	uninstallExecutablePathFunc = func() string { return filepath.Join(home, "bin", "gate") }
	uninstallRunHomebrewFunc = func(io.Writer, io.Writer) error {
		t.Fatal("brew uninstall should not run")
		return nil
	}
	uninstallHostsPath = filepath.Join(home, "hosts")
	return home
}

func TestUninstallRemovesLocalArtifactsAndPathBlock(t *testing.T) {
	home := isolateUninstall(t)
	for _, dir := range []string{
		filepath.Join(home, "xdg-config", "gate"),
		filepath.Join(home, "xdg-data", "gate"),
		filepath.Join(home, "xdg-state", "gate"),
	} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(home, ".local", "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	binPath := filepath.Join(home, ".local", "bin", "gate")
	if err := os.WriteFile(binPath, []byte("bin"), 0o600); err != nil {
		t.Fatal(err)
	}
	rc := filepath.Join(home, ".zshrc")
	body := "before\n# >>> gate PATH >>>\nexport PATH=\"$HOME/.local/bin:$PATH\"\n# <<< gate PATH <<<\nafter\n"
	if err := os.WriteFile(rc, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(uninstallHostsPath, []byte("127.0.0.1 localhost\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	if code := Uninstall([]string{"-y", "--keep-trust"}, &out, &errb); code != ExitOK {
		t.Fatalf("exit = %d; stdout=%s stderr=%s", code, out.String(), errb.String())
	}
	for _, path := range []string{
		filepath.Join(home, "xdg-config", "gate"),
		filepath.Join(home, "xdg-data", "gate"),
		filepath.Join(home, "xdg-state", "gate"),
		filepath.Join(home, ".local", "bin", "gate"),
	} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("%s still exists or stat failed with %v", path, err)
		}
	}
	got, err := os.ReadFile(rc)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "gate PATH") {
		t.Fatalf("PATH block remains:\n%s", got)
	}
	if !strings.Contains(out.String(), "gate uninstalled") {
		t.Fatalf("stdout missing completion:\n%s", out.String())
	}
}

func TestUninstallRunsBrewForHomebrewInstall(t *testing.T) {
	home := isolateUninstall(t)
	if err := os.MkdirAll(filepath.Join(home, "xdg-config", "gate"), 0o700); err != nil {
		t.Fatal(err)
	}
	uninstallExecutablePathFunc = func() string {
		return "/opt/homebrew/Cellar/gate/1.2.3/bin/gate"
	}
	called := false
	uninstallRunHomebrewFunc = func(io.Writer, io.Writer) error {
		called = true
		return nil
	}

	var out, errb bytes.Buffer
	if code := Uninstall([]string{"-y", "--keep-trust"}, &out, &errb); code != ExitOK {
		t.Fatalf("exit = %d; stdout=%s stderr=%s", code, out.String(), errb.String())
	}
	if !called {
		t.Fatal("brew uninstall was not called")
	}
	if !strings.Contains(out.String(), "removed Homebrew package gate") {
		t.Fatalf("stdout missing brew removal:\n%s", out.String())
	}
}

func TestUninstallKeepBrewSkipsHomebrewPackage(t *testing.T) {
	home := isolateUninstall(t)
	if err := os.MkdirAll(filepath.Join(home, "xdg-config", "gate"), 0o700); err != nil {
		t.Fatal(err)
	}
	uninstallExecutablePathFunc = func() string {
		return "/opt/homebrew/Cellar/gate/1.2.3/bin/gate"
	}
	uninstallRunHomebrewFunc = func(io.Writer, io.Writer) error {
		t.Fatal("brew uninstall should not run with --keep-brew")
		return nil
	}

	var out, errb bytes.Buffer
	if code := Uninstall([]string{"-y", "--keep-trust", "--keep-brew"}, &out, &errb); code != ExitOK {
		t.Fatalf("exit = %d; stdout=%s stderr=%s", code, out.String(), errb.String())
	}
	if strings.Contains(out.String(), "Homebrew package") {
		t.Fatalf("stdout unexpectedly mentions brew:\n%s", out.String())
	}
}

func TestUninstallStopsBeforeDeletingCAWhenUntrustFails(t *testing.T) {
	home := isolateUninstall(t)
	if _, err := ca.Load(paths.DataDir()); err != nil {
		t.Fatal(err)
	}
	untrustAuthorityFunc = func(*ca.CA) error {
		return os.ErrPermission
	}

	var out, errb bytes.Buffer
	if code := Uninstall([]string{"-y"}, &out, &errb); code != ExitPerm {
		t.Fatalf("exit = %d, want permission; stdout=%s stderr=%s", code, out.String(), errb.String())
	}
	if _, err := os.Stat(filepath.Join(home, "xdg-data", "gate", "ca", "root.crt")); err != nil {
		t.Fatalf("root cert was not preserved: %v", err)
	}
	if !strings.Contains(errb.String(), "failed to remove trusted gate root CA") {
		t.Fatalf("stderr missing trust failure:\n%s", errb.String())
	}
}

func TestUninstallKeepBrewSkipsHomebrewManagedGateBinDir(t *testing.T) {
	home := isolateUninstall(t)
	if err := os.MkdirAll(filepath.Join(home, "xdg-config", "gate"), 0o700); err != nil {
		t.Fatal(err)
	}
	cellarBin := filepath.Join(home, "Cellar", "gate", "1.2.3", "bin")
	if err := os.MkdirAll(cellarBin, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(cellarBin, "gate")
	if err := os.WriteFile(target, []byte("bin"), 0o600); err != nil {
		t.Fatal(err)
	}
	linkDir := filepath.Join(home, "homebrew-bin")
	if err := os.MkdirAll(linkDir, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(linkDir, "gate")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	t.Setenv("GATE_BIN_DIR", linkDir)
	uninstallRunHomebrewFunc = func(io.Writer, io.Writer) error {
		t.Fatal("brew uninstall should not run with --keep-brew")
		return nil
	}

	var out, errb bytes.Buffer
	if code := Uninstall([]string{"-y", "--keep-trust", "--keep-brew"}, &out, &errb); code != ExitOK {
		t.Fatalf("exit = %d; stdout=%s stderr=%s", code, out.String(), errb.String())
	}
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("homebrew-managed symlink removed: %v", err)
	}
}

func TestConfirmUninstallPromptAcceptsChoices(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{name: "yes", input: "yes\n", want: true},
		{name: "no", input: "no\n", want: false},
		{name: "empty", input: "\n", want: false},
		{name: "invalid then no", input: "later\nno\n", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			got, err := confirmUninstallPrompt(bufio.NewReader(strings.NewReader(tc.input)), &out)
			if err != nil {
				t.Fatalf("confirmUninstallPrompt: %v", err)
			}
			if got != tc.want {
				t.Fatalf("confirmUninstallPrompt(%q) = %v, want %v", tc.input, got, tc.want)
			}
			if !strings.Contains(out.String(), "Proceed with uninstall?") {
				t.Fatalf("stdout missing prompt:\n%s", out.String())
			}
		})
	}
}

func TestPrintUninstallPlanUsesRichLayoutWhenForced(t *testing.T) {
	t.Setenv("FORCE_COLOR", "1")
	t.Setenv("NO_COLOR", "")
	t.Setenv("CLICOLOR", "")
	t.Setenv("CLICOLOR_FORCE", "")

	var out bytes.Buffer
	printUninstallPlan(&out, []string{"/tmp/gate"}, []string{"Homebrew package gate"})

	got := out.String()
	if !strings.Contains(got, "Discovered artifacts") || !strings.Contains(got, "Homebrew package gate") {
		t.Fatalf("stdout missing plan content:\n%s", got)
	}
	if strings.Contains(got, "Existing paths to remove:") || strings.Contains(got, "Cleanup actions:") {
		t.Fatalf("stdout used plain labels:\n%s", got)
	}
}

func TestRemoveMarkedBlockRejectsLaterUnterminatedBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rc")
	body := "before\n# >>> gate PATH >>>\none\n# <<< gate PATH <<<\nmiddle\n# >>> gate PATH >>>\ntwo\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	changed, err := removeMarkedBlock(path, "# >>> gate PATH >>>", "# <<< gate PATH <<<")
	if err == nil {
		t.Fatal("expected unterminated block error")
	}
	if changed {
		t.Fatal("changed = true after malformed block")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Fatalf("file changed despite malformed block:\n%s", got)
	}
}

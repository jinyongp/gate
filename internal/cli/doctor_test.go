package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gate/internal/ui/uitest"
)

func isolateDoctor(t *testing.T) (string, string) {
	t.Helper()
	configHome := t.TempDir()
	stateHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Chdir(t.TempDir())
	return filepath.Join(configHome, "gate"), filepath.Join(stateHome, "gate")
}

func TestDoctorNoIssues(t *testing.T) {
	isolateDoctor(t)
	var out, errb bytes.Buffer
	if code := Doctor(nil, &out, &errb); code != ExitOK {
		t.Fatalf("Doctor exit = %d, stderr=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "no issues found") {
		t.Fatalf("unexpected output: %s", out.String())
	}
}

func TestDoctorMigratesLegacyAdhocRegistry(t *testing.T) {
	configDir, _ := isolateDoctor(t)
	registryPath := filepath.Join(configDir, "registry.json")
	if err := os.MkdirAll(filepath.Dir(registryPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(registryPath, []byte(`{
  "version": 1,
  "services": {
    "/web.localhost": {
      "service": "web.localhost",
      "domain": "web.localhost",
      "port": 4312,
      "adhoc": true,
      "active": true
    }
  }
}
`), 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	if code := Doctor([]string{"--json"}, &out, &errb); code != ExitError {
		t.Fatalf("Doctor check exit = %d, want %d; stderr=%s", code, ExitError, errb.String())
	}
	var report doctorReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	if report.OK || len(report.Issues) != 1 || report.Issues[0].Code != "legacy_registry_adhoc" {
		t.Fatalf("unexpected report: %+v", report)
	}
	if errb.Len() != 0 {
		t.Fatalf("doctor --json wrote stderr: %s", errb.String())
	}

	out.Reset()
	errb.Reset()
	if code := Doctor([]string{"--fix", "--json"}, &out, &errb); code != ExitOK {
		t.Fatalf("Doctor fix exit = %d, stderr=%s", code, errb.String())
	}
	report = doctorReport{}
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	if !report.OK || len(report.Issues) != 1 || !report.Issues[0].Fixed {
		t.Fatalf("unexpected fixed report: %+v", report)
	}
	if errb.Len() != 0 {
		t.Fatalf("doctor --fix --json wrote stderr: %s", errb.String())
	}
	b, err := os.ReadFile(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"adhoc"`) {
		t.Fatalf("adhoc field was not removed:\n%s", string(b))
	}
	if !strings.Contains(string(b), `"standalone": true`) {
		t.Fatalf("standalone field was not added:\n%s", string(b))
	}
}

func TestPrintDoctorReportGroupsIssuesAndIndentsPaths(t *testing.T) {
	uitest.ClearColorEnv(t)
	report := doctorReport{Issues: []doctorIssue{
		{
			Code:    "old_scoped_daemon_files",
			Message: "4 old scoped daemon file(s) found",
			Paths:   []string{"/tmp/gate/project-demo.pid", "/tmp/gate/project-demo.sock"},
		},
		{
			Code:    "stale_scoped_pid_files",
			Message: "1 stale scoped daemon pid file(s) found",
			Paths:   []string{"/tmp/gate/project-demo.pid"},
		},
	}}

	var out bytes.Buffer
	printDoctorReport(&out, report, false)
	got := out.String()
	if !strings.Contains(got, "issue old_scoped_daemon_files: 4 old scoped daemon file(s) found") {
		t.Fatalf("missing first issue heading:\n%s", got)
	}
	if !strings.Contains(got, "\n\nissue stale_scoped_pid_files: 1 stale scoped daemon pid file(s) found") {
		t.Fatalf("issues should be separated by a blank line:\n%s", got)
	}
	if !strings.Contains(got, "    path  /tmp/gate/project-demo.pid") {
		t.Fatalf("paths should be visibly nested:\n%s", got)
	}
	if strings.Contains(got, " · ") {
		t.Fatalf("doctor output should not use the old dense separator:\n%s", got)
	}
}

func TestPrintDoctorReportRichStylesIssueAndPaths(t *testing.T) {
	uitest.ForceColor(t)
	report := doctorReport{Issues: []doctorIssue{{
		Code:    "old_scoped_daemon_files",
		Message: "1 old scoped daemon file(s) found",
		Paths:   []string{"/tmp/gate/project-demo.pid"},
	}}}

	var out bytes.Buffer
	printDoctorReport(&out, report, false)
	got := out.String()
	if !strings.Contains(got, "! issue  old_scoped_daemon_files") {
		t.Fatalf("rich doctor output missing emphasized issue heading:\n%q", got)
	}
	if !strings.Contains(got, "    path  /tmp/gate/project-demo.pid") {
		t.Fatalf("rich doctor output should nest path details:\n%q", got)
	}
	if strings.Contains(got, " · ") {
		t.Fatalf("rich doctor output should not use the old dense separator:\n%q", got)
	}
}

func TestDoctorFixRemovesLegacyDaemonFiles(t *testing.T) {
	configDir, stateDir := isolateDoctor(t)
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	paths := []string{
		filepath.Join(configDir, "gate.sock"),
		filepath.Join(configDir, "gate.pid"),
		filepath.Join(stateDir, "gate.log"),
	}
	for _, path := range paths {
		if err := os.WriteFile(path, []byte("not-a-pid"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	var out, errb bytes.Buffer
	if code := Doctor([]string{"--fix"}, &out, &errb); code != ExitOK {
		t.Fatalf("Doctor fix exit = %d, stderr=%s", code, errb.String())
	}
	for _, path := range paths {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s still exists or stat failed: %v", path, err)
		}
	}
}

func TestDoctorFixRemovesDefaultLegacyLogWhenXDGStateIsSet(t *testing.T) {
	configDir, stateDir := isolateDoctor(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	oldGOOS := runtimeGOOS
	t.Cleanup(func() { runtimeGOOS = oldGOOS })
	runtimeGOOS = func() string { return "linux" }

	defaultLog := filepath.Join(home, ".local", "state", "gate", "gate.log")
	paths := []string{
		filepath.Join(configDir, "gate.pid"),
		filepath.Join(stateDir, "gate.log"),
		defaultLog,
	}
	for _, path := range paths {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("legacy"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	var out, errb bytes.Buffer
	if code := Doctor([]string{"--fix"}, &out, &errb); code != ExitOK {
		t.Fatalf("Doctor fix exit = %d, stderr=%s", code, errb.String())
	}
	for _, path := range paths {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s still exists or stat failed: %v", path, err)
		}
	}
}

func TestDoctorDoesNotStopScopedDaemonFromLegacyPID(t *testing.T) {
	configDir, _ := isolateDoctor(t)
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	legacyPID := filepath.Join(configDir, "gate.pid")
	if err := os.WriteFile(legacyPID, []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}

	oldArgs := processArgsForPID
	t.Cleanup(func() { processArgsForPID = oldArgs })
	processArgsForPID = func(pid int) (string, error) {
		if pid != 12345 {
			t.Fatalf("unexpected pid lookup: %d", pid)
		}
		return "gate __serve --socket " + filepath.Join(configDir, "daemons", "global.sock"), nil
	}

	var out, errb bytes.Buffer
	if code := Doctor([]string{"--fix"}, &out, &errb); code != ExitOK {
		t.Fatalf("Doctor fix exit = %d, stderr=%s", code, errb.String())
	}
	if _, err := os.Stat(legacyPID); !os.IsNotExist(err) {
		t.Fatalf("%s still exists or stat failed: %v", legacyPID, err)
	}
}

func TestIsLegacyDaemonArgsRequiresLegacySocket(t *testing.T) {
	legacySocket := "/tmp/gate/gate.sock"
	if !isLegacyDaemonArgs("gate __serve --socket "+legacySocket, legacySocket) {
		t.Fatal("legacy socket args should match")
	}
	if !isLegacyDaemonArgs("gate __serve --socket="+legacySocket, legacySocket) {
		t.Fatal("legacy socket equals args should match")
	}
	if isLegacyDaemonArgs("gate __serve --socket /tmp/gate/daemons/global.sock", legacySocket) {
		t.Fatal("scoped socket args should not match legacy daemon")
	}
	if isLegacyDaemonArgs("gate __serve --socket "+legacySocket+".bak", legacySocket) {
		t.Fatal("socket prefix should not match legacy daemon")
	}
}

func TestDoctorFixRemovesStaleScopedPIDFiles(t *testing.T) {
	configDir, _ := isolateDoctor(t)
	daemonDir := filepath.Join(configDir, "daemons")
	if err := os.MkdirAll(daemonDir, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(daemonDir, "project-demo.pid")
	if err := os.WriteFile(stale, []byte("99999999"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	if code := Doctor([]string{"--fix"}, &out, &errb); code != ExitOK {
		t.Fatalf("Doctor fix exit = %d, stderr=%s", code, errb.String())
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("%s still exists or stat failed: %v", stale, err)
	}
}

func TestDoctorFixRemovesOldScopedDaemonFilesButKeepsListenerFiles(t *testing.T) {
	configDir, stateDir := isolateDoctor(t)
	configDaemonDir := filepath.Join(configDir, "daemons")
	stateDaemonDir := filepath.Join(stateDir, "daemons")
	for _, dir := range []string{configDaemonDir, stateDaemonDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	oldFiles := []string{
		filepath.Join(configDaemonDir, "global.sock"),
		filepath.Join(configDaemonDir, "project-demo.pid"),
		filepath.Join(stateDaemonDir, "project-demo.log"),
	}
	keepFiles := []string{
		filepath.Join(configDaemonDir, "listener-https-443-http-80.sock"),
		filepath.Join(configDaemonDir, "listener-https-443-http-80.pid"),
		filepath.Join(stateDaemonDir, "listener-https-443-http-80.log"),
	}
	for _, path := range append(oldFiles, keepFiles...) {
		if err := os.WriteFile(path, []byte("state"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	var out, errb bytes.Buffer
	if code := Doctor([]string{"--fix"}, &out, &errb); code != ExitOK {
		t.Fatalf("Doctor fix exit = %d, stderr=%s", code, errb.String())
	}
	for _, path := range oldFiles {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s still exists or stat failed: %v", path, err)
		}
	}
	for _, path := range keepFiles {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%s missing: %v", path, err)
		}
	}
}

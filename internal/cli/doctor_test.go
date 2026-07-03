package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gate/internal/registry"
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
	if report.OK || report.Status != "fail" || len(report.Issues) != 1 || report.Issues[0].Code != "legacy_registry_adhoc" {
		t.Fatalf("unexpected report: %+v", report)
	}
	if report.Issues[0].Severity != "fixable" || !report.Issues[0].Fixable || report.Issues[0].SuggestedCommand != "gate doctor --fix" {
		t.Fatalf("unexpected issue classification: %+v", report.Issues[0])
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
	if !report.OK || report.Status != "pass" || len(report.Issues) != 1 || !report.Issues[0].Fixed {
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

func TestDoctorReportsFutureRegistrySchemaWithoutFixing(t *testing.T) {
	configDir, _ := isolateDoctor(t)
	registryPath := filepath.Join(configDir, "registry.json")
	if err := os.MkdirAll(filepath.Dir(registryPath), 0o700); err != nil {
		t.Fatal(err)
	}
	original := []byte(`{
  "version": 999,
  "services": {}
}
`)
	if err := os.WriteFile(registryPath, original, 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	if code := Doctor([]string{"--fix", "--json"}, &out, &errb); code != ExitError {
		t.Fatalf("Doctor exit = %d, stderr=%s", code, errb.String())
	}
	var report doctorReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	if report.OK || len(report.Issues) != 1 || report.Issues[0].Code != "registry_unsupported_schema" || report.Issues[0].Fixed {
		t.Fatalf("unexpected report: %+v", report)
	}
	got, err := os.ReadFile(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("future registry changed:\n%s", got)
	}
}

func TestDoctorReportsRegistryIntegrityIssueCodes(t *testing.T) {
	configDir, _ := isolateDoctor(t)
	registryPath := filepath.Join(configDir, "registry.json")
	if err := os.MkdirAll(filepath.Dir(registryPath), 0o700); err != nil {
		t.Fatal(err)
	}
	original := []byte(`{
  "version": 2,
  "services": {
    "wrong/key": {
      "project": "demo",
      "service": "web",
      "domain": "web.localhost",
      "port": 4312,
      "adhoc": true
    },
    "demo/api": {
      "project": "demo",
      "service": "api",
      "domain": "web.localhost",
      "port": 4313
    },
    "demo/empty": {
      "project": "demo",
      "service": "empty",
      "domain": "",
      "port": -1
    }
  }
}
`)
	if err := os.WriteFile(registryPath, original, 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	if code := Doctor([]string{"--json"}, &out, &errb); code != ExitError {
		t.Fatalf("Doctor exit = %d, stderr=%s", code, errb.String())
	}
	var report doctorReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	codes := map[string]bool{}
	for _, issue := range report.Issues {
		codes[issue.Code] = true
		if issue.Fixed {
			t.Fatalf("registry integrity issue should not be fixed: %+v", issue)
		}
		if issue.Severity != "fatal" || issue.Fixable || issue.SuggestedCommand != "" {
			t.Fatalf("registry integrity issue should be non-fixable fatal: %+v", issue)
		}
	}
	for _, code := range []string{"registry_key_mismatch", "registry_duplicate_domain", "registry_empty_domain", "registry_invalid_port"} {
		if !codes[code] {
			t.Fatalf("missing code %s in report %+v", code, report)
		}
	}

	out.Reset()
	errb.Reset()
	if code := Doctor([]string{"--fix", "--json"}, &out, &errb); code != ExitError {
		t.Fatalf("Doctor --fix exit = %d, stderr=%s", code, errb.String())
	}
	report = doctorReport{}
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	if report.OK {
		t.Fatalf("doctor --fix should not repair registry integrity issues: %+v", report)
	}
	for _, issue := range report.Issues {
		if issue.Fixed {
			t.Fatalf("registry integrity issue should remain report-only: %+v", issue)
		}
	}
	got, err := os.ReadFile(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("doctor --fix rewrote malformed registry:\n%s", got)
	}
}

func TestDoctorReportsStaleServiceReservationWithoutRemoving(t *testing.T) {
	configDir, _ := isolateDoctor(t)
	projectDir := t.TempDir()
	configPath := filepath.Join(projectDir, "gate.toml")
	if err := os.WriteFile(configPath, []byte(`[project]
name = "demo"

[services.api]
domain = "api.localhost"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := registry.Open(filepath.Join(configDir, "registry.json")).Update(func(reg *registry.Registry) error {
		return reg.Reserve(registry.Reservation{
			Project: "demo", Service: "web", Domain: "web.localhost", Port: 4400,
			ConfigPath: configPath, Active: true,
		})
	}); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	if code := Doctor([]string{"--fix", "--json"}, &out, &errb); code != ExitError {
		t.Fatalf("Doctor exit = %d, stderr=%s", code, errb.String())
	}
	var report doctorReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	if report.OK || len(report.Issues) != 1 || report.Issues[0].Code != "registry_stale_service" || report.Issues[0].Fixed {
		t.Fatalf("unexpected report: %+v", report)
	}
	if report.Issues[0].Severity != "fatal" || report.Issues[0].Fixable || report.Issues[0].SuggestedCommand != "" {
		t.Fatalf("stale service should be non-fixable fatal: %+v", report.Issues[0])
	}
	reg, err := registry.Open(filepath.Join(configDir, "registry.json")).Read()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Get(registry.Key("demo", "web")); !ok {
		t.Fatal("stale reservation was removed")
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

func TestDoctorReportOKAllowsWarningAndInfoIssues(t *testing.T) {
	report := doctorReport{Issues: []doctorIssue{
		{Code: "daemon_not_running", Severity: "warning", Message: "daemon is not running"},
		{Code: "note", Severity: "info", Message: "informational note"},
	}}
	if !doctorReportOK(report) {
		t.Fatalf("warning/info report should be ok: %+v", report)
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
	if code := Doctor([]string{"--json"}, &out, &errb); code != ExitError {
		t.Fatalf("Doctor check exit = %d, stderr=%s", code, errb.String())
	}
	var report doctorReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("doctor json: %v\n%s", err, out.String())
	}
	if len(report.Issues) != 1 || report.Issues[0].Code != "stale_scoped_pid_files" {
		t.Fatalf("stale pid should be classified precisely: %+v", report.Issues)
	}

	out.Reset()
	errb.Reset()
	if code := Doctor([]string{"--fix", "--json"}, &out, &errb); code != ExitOK {
		t.Fatalf("Doctor fix exit = %d, stderr=%s", code, errb.String())
	}
	report = doctorReport{}
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("doctor fix json: %v\n%s", err, out.String())
	}
	if !report.OK || len(report.Issues) != 1 || report.Issues[0].Code != "stale_scoped_pid_files" || !report.Issues[0].Fixed {
		t.Fatalf("doctor fix report = %+v", report)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("%s still exists or stat failed: %v", stale, err)
	}
}

func TestClassifyDoctorIssueWithFixErrorDoesNotSuggestRetry(t *testing.T) {
	issue := classifyDoctorIssue(doctorIssue{
		Code:  "legacy_daemon_files",
		Error: "permission denied",
	})
	if !issue.Fixable {
		t.Fatalf("fixable capability should remain true: %+v", issue)
	}
	if issue.SuggestedCommand != "" {
		t.Fatalf("failed fix should not suggest immediate retry: %+v", issue)
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

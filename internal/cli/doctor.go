package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"gate/internal/daemon"
	"gate/internal/paths"
)

type doctorReport struct {
	OK     bool          `json:"ok"`
	Issues []doctorIssue `json:"issues"`
}

type doctorIssue struct {
	Code    string   `json:"code"`
	Message string   `json:"message"`
	Paths   []string `json:"paths,omitempty"`
	Fixed   bool     `json:"fixed"`
	Error   string   `json:"error,omitempty"`
}

// Doctor checks local gate-owned state and optionally repairs stale state left
// by older development builds.
func Doctor(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fix := fs.Bool("fix", false, "repair issues that can be fixed without sudo")
	jsonOut := fs.Bool("json", false, "emit JSON")
	if handled, code := parseFlags(fs, "doctor", args, stdout, stderr); handled {
		return code
	}
	if len(fs.Args()) != 0 {
		return usageFail(stderr, *jsonOut, "doctor")
	}

	var activity activityHandle
	if *fix {
		activity = startActivity(stderr, *jsonOut, "repairing local state")
	}
	report := doctorReport{Issues: runDoctorChecks(*fix)}
	if activity != nil {
		activity.Stop()
	}
	report.OK = doctorReportOK(report)
	if *jsonOut {
		if code := writeJSON(stdout, report); code != ExitOK {
			return code
		}
	} else {
		printDoctorReport(stdout, report, *fix)
	}
	if report.OK {
		return ExitOK
	}
	return ExitError
}

func runDoctorChecks(fix bool) []doctorIssue {
	var issues []doctorIssue
	if issue, ok := checkLegacyDaemonState(fix); ok {
		issues = append(issues, issue)
	}
	if issue, ok := checkLegacyRegistryAdhoc(fix); ok {
		issues = append(issues, issue)
	}
	if issue, ok := checkOldScopedDaemonState(fix); ok {
		issues = append(issues, issue)
	}
	if issue, ok := checkStaleScopedPIDs(fix); ok {
		issues = append(issues, issue)
	}
	return issues
}

func doctorReportOK(report doctorReport) bool {
	for _, issue := range report.Issues {
		if !issue.Fixed || issue.Error != "" {
			return false
		}
	}
	return true
}

func printDoctorReport(stdout io.Writer, report doctorReport, fix bool) {
	if len(report.Issues) == 0 {
		fmt.Fprintln(stdout, "ok · no issues found")
		return
	}
	for _, issue := range report.Issues {
		status := "issue"
		if issue.Fixed && issue.Error == "" {
			status = "fixed"
		}
		fmt.Fprintf(stdout, "%s · %s · %s\n", status, issue.Code, issue.Message)
		for _, p := range issue.Paths {
			fmt.Fprintf(stdout, "  path %s\n", p)
		}
		if issue.Error != "" {
			fmt.Fprintf(stdout, "  error %s\n", issue.Error)
		}
	}
	if !fix && !report.OK {
		fmt.Fprintln(stdout, "fix: gate doctor --fix")
	}
}

func checkLegacyDaemonState(fix bool) (doctorIssue, bool) {
	configDir := paths.ConfigDir()
	legacySocket := filepath.Join(configDir, "gate.sock")
	legacyPID := filepath.Join(configDir, "gate.pid")
	files := existingFiles(append([]string{legacySocket, legacyPID}, legacyLogPaths()...)...)

	client := daemon.NewClient(legacySocket)
	st, running := client.Status()
	pid := st.PID
	if running != nil && doctorPathExists(legacyPID) {
		if parsed, err := readPIDFile(legacyPID); err == nil && isLegacyDaemonPID(parsed, legacySocket) {
			pid = parsed
		}
	}
	if running == nil && !containsPath(files, legacySocket) {
		files = append(files, legacySocket)
	}
	if len(files) == 0 {
		return doctorIssue{}, false
	}

	issue := doctorIssue{
		Code:    "legacy_daemon_files",
		Message: "legacy single-daemon state found",
		Paths:   files,
	}
	if !fix {
		return issue, true
	}

	if pid > 0 && isLegacyDaemonPID(pid, legacySocket) {
		if err := stopLegacyDaemon(client, pid); err != nil {
			issue.Error = err.Error()
			return issue, true
		}
	}
	for _, p := range files {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			issue.Error = err.Error()
			return issue, true
		}
	}
	issue.Fixed = true
	issue.Message = "removed legacy single-daemon state"
	return issue, true
}

func legacyLogPaths() []string {
	known := []string{filepath.Join(paths.StateDir(), "gate.log")}
	home, err := os.UserHomeDir()
	if err != nil {
		return known
	}
	switch runtimeGOOS() {
	case "darwin":
		known = append(known, filepath.Join(home, "Library", "Logs", "gate", "gate.log"))
	default:
		known = append(known, filepath.Join(home, ".local", "state", "gate", "gate.log"))
	}
	return uniquePaths(known)
}

var runtimeGOOS = func() string { return runtime.GOOS }

func isLegacyDaemonPID(pid int, legacySocket string) bool {
	args, err := processArgsForPID(pid)
	if err != nil {
		return false
	}
	return isLegacyDaemonArgs(args, legacySocket)
}

func isLegacyDaemonArgs(args, legacySocket string) bool {
	args = strings.TrimSpace(args)
	if !isGateDaemonArgs(args) {
		return false
	}
	parts := strings.Fields(args)
	for i, part := range parts {
		if part == "--socket" {
			return i+1 < len(parts) && parts[i+1] == legacySocket
		}
		if strings.HasPrefix(part, "--socket=") {
			return strings.TrimPrefix(part, "--socket=") == legacySocket
		}
	}
	return true
}

func stopLegacyDaemon(client *daemon.Client, pid int) error {
	if client.IsRunning() {
		return stopDaemonProcess(client, pid, 2*time.Second)
	}
	proc, err := os.FindProcess(pid)
	if err == nil {
		_ = proc.Signal(syscall.SIGTERM)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !processExists(pid) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	if processExists(pid) {
		return errors.New("legacy daemon did not stop")
	}
	return nil
}

func checkLegacyRegistryAdhoc(fix bool) (doctorIssue, bool) {
	registryPath := filepath.Join(paths.ConfigDir(), "registry.json")
	b, err := os.ReadFile(registryPath)
	if errors.Is(err, fs.ErrNotExist) {
		return doctorIssue{}, false
	}
	if err != nil {
		return doctorIssue{Code: "registry_read_error", Message: "registry could not be read", Paths: []string{registryPath}, Error: err.Error()}, true
	}
	_, count, err := migrateAdhocRegistryJSON(b)
	if err != nil {
		return doctorIssue{Code: "registry_invalid_json", Message: "registry JSON is invalid", Paths: []string{registryPath}, Error: err.Error()}, true
	}
	if count == 0 {
		return doctorIssue{}, false
	}

	issue := doctorIssue{
		Code:    "legacy_registry_adhoc",
		Message: fmt.Sprintf("registry contains %d legacy adhoc reservation(s)", count),
		Paths:   []string{registryPath},
	}
	if !fix {
		return issue, true
	}

	fixedCount := 0
	if err := registryStore().UpdateRaw(func(current []byte) ([]byte, bool, error) {
		if len(current) == 0 {
			return nil, false, nil
		}
		migrated, currentCount, err := migrateAdhocRegistryJSON(current)
		if err != nil {
			return nil, false, err
		}
		fixedCount = currentCount
		if currentCount == 0 {
			return nil, false, nil
		}
		return migrated, true, nil
	}); err != nil {
		issue.Error = err.Error()
		return issue, true
	}
	issue.Fixed = true
	if fixedCount == 0 {
		issue.Message = "legacy adhoc reservation(s) already migrated"
	} else {
		issue.Message = fmt.Sprintf("converted %d adhoc reservation(s) to standalone", fixedCount)
	}
	return issue, true
}

func migrateAdhocRegistryJSON(b []byte) ([]byte, int, error) {
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		return nil, 0, err
	}
	services, ok := root["services"].(map[string]any)
	if !ok {
		return b, 0, nil
	}
	count := 0
	for _, raw := range services {
		service, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		adhoc, exists := service["adhoc"]
		if !exists {
			continue
		}
		count++
		if enabled, ok := adhoc.(bool); ok && enabled {
			service["standalone"] = true
		}
		delete(service, "adhoc")
	}
	if count == 0 {
		return b, 0, nil
	}
	next, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, 0, err
	}
	next = append(next, '\n')
	return next, count, nil
}

func checkStaleScopedPIDs(fix bool) (doctorIssue, bool) {
	dir := filepath.Join(paths.ConfigDir(), "daemons")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return doctorIssue{}, false
	}
	if err != nil {
		return doctorIssue{Code: "scoped_pid_scan_error", Message: "scoped daemon pid directory could not be read", Paths: []string{dir}, Error: err.Error()}, true
	}

	var stale []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".pid") {
			continue
		}
		if strings.HasPrefix(entry.Name(), "listener-") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if scopedPIDStale(path) {
			stale = append(stale, path)
		}
	}
	if len(stale) == 0 {
		return doctorIssue{}, false
	}

	issue := doctorIssue{
		Code:    "stale_scoped_pid_files",
		Message: fmt.Sprintf("%d stale scoped daemon pid file(s) found", len(stale)),
		Paths:   stale,
	}
	if !fix {
		return issue, true
	}
	for _, path := range stale {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			issue.Error = err.Error()
			return issue, true
		}
	}
	issue.Fixed = true
	issue.Message = fmt.Sprintf("removed %d stale scoped daemon pid file(s)", len(stale))
	return issue, true
}

func checkOldScopedDaemonState(fix bool) (doctorIssue, bool) {
	files, err := oldScopedDaemonFiles()
	if err != nil {
		return doctorIssue{Code: "old_scoped_daemon_scan_error", Message: "old scoped daemon state could not be read", Error: err.Error()}, true
	}
	if len(files) == 0 {
		return doctorIssue{}, false
	}
	issue := doctorIssue{
		Code:    "old_scoped_daemon_files",
		Message: fmt.Sprintf("%d old scoped daemon file(s) found", len(files)),
		Paths:   files,
	}
	if !fix {
		return issue, true
	}
	for _, path := range files {
		if strings.HasSuffix(path, ".pid") {
			client := daemon.NewClient(strings.TrimSuffix(path, ".pid") + ".sock")
			if pid, err := readPIDFile(path); err == nil && oldScopedDaemonPIDMatchesSocket(client, pid) {
				_ = stopDaemonProcess(client, pid, 2*time.Second)
			}
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			issue.Error = err.Error()
			return issue, true
		}
	}
	issue.Fixed = true
	issue.Message = fmt.Sprintf("removed %d old scoped daemon file(s)", len(files))
	return issue, true
}

func oldScopedDaemonFiles() ([]string, error) {
	var out []string
	for _, dir := range []string{filepath.Join(paths.ConfigDir(), "daemons"), filepath.Join(paths.StateDir(), "daemons")} {
		entries, err := os.ReadDir(dir)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if strings.HasPrefix(name, "listener-") {
				continue
			}
			if strings.HasSuffix(name, ".sock") || strings.HasSuffix(name, ".pid") || strings.HasSuffix(name, ".log") {
				out = append(out, filepath.Join(dir, name))
			}
		}
	}
	return uniquePaths(out), nil
}

func oldScopedDaemonPIDMatchesSocket(client *daemon.Client, pid int) bool {
	if pid <= 0 || !isGateDaemonPID(pid) {
		return false
	}
	st, err := client.Status()
	return err == nil && st.PID == pid
}

func scopedPIDStale(path string) bool {
	pid, err := readPIDFile(path)
	if err != nil || pid <= 0 {
		return true
	}
	if !processExists(pid) {
		return true
	}
	return !isGateDaemonPID(pid)
}

func readPIDFile(path string) (int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(b)))
}

func processExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func existingFiles(paths ...string) []string {
	var out []string
	for _, p := range uniquePaths(paths) {
		if doctorPathExists(p) {
			out = append(out, p)
		}
	}
	return out
}

func uniquePaths(paths []string) []string {
	seen := make(map[string]bool, len(paths))
	var out []string
	for _, p := range paths {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

func doctorPathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func containsPath(paths []string, path string) bool {
	for _, p := range paths {
		if p == path {
			return true
		}
	}
	return false
}

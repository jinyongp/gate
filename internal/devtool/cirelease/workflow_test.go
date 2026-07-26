package cirelease

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestReleaseWorkflowUsesGoDomainCommandsAndDeletedScriptsStayAbsent(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate workflow fixture")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
	workflowPath := filepath.Join(root, ".github", "workflows", "release.yml")
	data, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(data)
	commands := []string{
		"detect-release-tag",
		"wait-for-ci",
		"build-release-artifacts",
		"checksums",
		"publish-release",
		"verify-release-tag-target",
		"wait-release-assets",
		"generate-homebrew-formula",
	}
	for _, command := range commands {
		if !strings.Contains(workflow, "gate-dev\" ci "+command) {
			t.Errorf("release workflow does not use gate-dev ci %s", command)
		}
	}

	deleted := []string{
		".github/scripts/build-release-artifacts.sh",
		".github/scripts/checksums.sh",
		".github/scripts/detect-release-tag.sh",
		".github/scripts/generate-homebrew-binary-formula.sh",
		".github/scripts/publish-release.sh",
		".github/scripts/verify-release-tag-target.sh",
		".github/scripts/wait-for-ci.sh",
		".github/scripts/wait-release-assets.sh",
		"scripts/dev/test-wait-for-ci.sh",
	}
	for _, path := range deleted {
		if strings.Contains(workflow, path) {
			t.Errorf("release workflow still references %s", path)
		}
		if _, err := os.Stat(filepath.Join(root, path)); !os.IsNotExist(err) {
			t.Errorf("migrated script still exists: %s (%v)", path, err)
		}
	}
}

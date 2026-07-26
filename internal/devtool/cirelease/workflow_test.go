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
	ciData, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	ciWorkflow := string(ciData)
	for _, contract := range []string{
		"run-name: CI ${{ inputs.checkout_ref || github.sha }} ${{ inputs.request_id || '' }}",
		"workflow_dispatch:",
		"checkout_ref:",
		"group: ci-${{ github.workflow }}-${{ inputs.request_id || inputs.checkout_ref || github.ref }}",
		"ref: ${{ inputs.checkout_ref || github.sha }}",
		"uses: extractions/setup-just@",
		"run: just lint",
		"run: just linux-low-port-test",
	} {
		if !strings.Contains(ciWorkflow, contract) {
			t.Errorf("CI workflow is missing exact-SHA recovery contract %q", contract)
		}
	}
	commands := []string{
		"detect-release-tag",
		"dispatch-ci",
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
	for _, contract := range []string{
		"repository_dispatch:",
		"- release",
		"GATE_RELEASE_TAG: ${{ github.event.client_payload.tag }}",
		"GATE_RELEASE_TARGET_SHA: ${{ github.event.client_payload.target_sha }}",
		"GATE_RELEASE_TAG_OBJECT: ${{ github.event.client_payload.tag_object }}",
		`GATE_REQUIRE_RELEASE_TAG: "1"`,
		"queue: max",
		"ref: ${{ github.workflow_sha }}",
		"ref: ${{ needs.release_tag.outputs.target }}",
		"GATE_RELEASE_TARGET_SHA: ${{ needs.release_tag.outputs.target }}",
		"GATE_RELEASE_TAG_OBJECT: ${{ needs.release_tag.outputs.object }}",
		`wait-for-ci "${{ needs.release_tag.outputs.target }}"`,
		`CI_REQUEST_ID: release-${{ github.run_id }}-${{ github.run_attempt }}`,
	} {
		if !strings.Contains(workflow, contract) {
			t.Errorf("release recovery workflow is missing %q", contract)
		}
	}
	for _, forbiddenTrigger := range []string{"workflow_dispatch:", "workflow_run:", "push:"} {
		if strings.Contains(workflow, forbiddenTrigger) {
			t.Errorf("privileged release workflow allows unsafe trigger %q", forbiddenTrigger)
		}
	}
	if strings.Count(workflow, "actions: write") != 1 {
		t.Errorf("release workflow must grant actions:write only to the CI dispatch job")
	}
	ciGateStart := strings.Index(workflow, "\n  ci_gate:")
	buildStart := strings.Index(workflow, "\n  build:")
	if ciGateStart < 0 || buildStart <= ciGateStart {
		t.Fatal("locate CI gate job")
	}
	if strings.Contains(workflow[ciGateStart:buildStart], "actions: write") {
		t.Error("CI wait job has write access to Actions")
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

package cirelease

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWorkflowsRunSharedPreflightAndDeletedScriptsStayAbsent(t *testing.T) {
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
		"run-name: CI · PR #${{ github.event.pull_request.number }}",
		"pull_request:",
		"group: ci-pr-${{ github.event.pull_request.number }}",
		"fail-fast: false",
		"ref: ${{ github.sha }}",
		"uses: ./tooling/.github/actions/preflight",
		`cross-build: "true"`,
	} {
		if !strings.Contains(ciWorkflow, contract) {
			t.Errorf("CI workflow is missing pull-request contract %q", contract)
		}
	}
	for _, forbiddenTrigger := range []string{"workflow_dispatch:", "push:", "repository_dispatch:"} {
		if strings.Contains(ciWorkflow, forbiddenTrigger) {
			t.Errorf("CI workflow allows non-PR trigger %q", forbiddenTrigger)
		}
	}

	preflightData, err := os.ReadFile(filepath.Join(root, ".github", "actions", "preflight", "action.yml"))
	if err != nil {
		t.Fatal(err)
	}
	preflight := string(preflightData)
	for _, contract := range []string{
		"run: just fmt-check",
		"run: just vet",
		"run: just lint",
		"run: just vuln",
		"run: just scripts-check",
		"run: just linux-low-port-test",
		"run: just node-check",
		"run: just cover",
		"run: just build-all ci",
	} {
		if !strings.Contains(preflight, contract) {
			t.Errorf("shared preflight action is missing %q", contract)
		}
	}
	commands := []string{
		"detect-release-tag",
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
		"preflight:",
		"fail-fast: false",
		"os: [ubuntu-latest, macos-15]",
		"uses: ./tooling/.github/actions/preflight",
		"source-sha: ${{ needs.release_tag.outputs.target }}",
		"needs: [release_tag, preflight]",
	} {
		if !strings.Contains(workflow, contract) {
			t.Errorf("release workflow is missing %q", contract)
		}
	}
	for _, forbiddenCommand := range []string{"dispatch-ci", "wait-for-ci"} {
		if strings.Contains(workflow, forbiddenCommand) {
			t.Errorf("release workflow still uses %q", forbiddenCommand)
		}
	}
	for _, forbiddenTrigger := range []string{"workflow_dispatch:", "workflow_run:", "push:"} {
		if strings.Contains(workflow, forbiddenTrigger) {
			t.Errorf("privileged release workflow allows unsafe trigger %q", forbiddenTrigger)
		}
	}
	if strings.Contains(workflow, "actions: write") {
		t.Error("release workflow no longer needs actions:write")
	}
	preflightStart := strings.Index(workflow, "\n  preflight:")
	buildStart := strings.Index(workflow, "\n  build:")
	if preflightStart < 0 || buildStart <= preflightStart {
		t.Fatal("release preflight must precede build")
	}
	if strings.Contains(workflow[preflightStart:buildStart], "contents: write") {
		t.Error("release preflight has write access to repository contents")
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

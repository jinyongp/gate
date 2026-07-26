package devcmd

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"gate/internal/devtool/runner"
)

var summaryShellFiles = []string{
	".github/scripts/build-summary.sh",
	".github/scripts/check-summary.sh",
	".github/scripts/homebrew-summary.sh",
	".github/scripts/npm-summary.sh",
	".github/scripts/release-summary.sh",
}

var retainedShellFiles = append(append([]string(nil), summaryShellFiles...),
	"scripts/install.sh",
	"scripts/uninstall.sh",
)

func (service *Service) scriptsCheck(ctx context.Context) error {
	if err := service.stream(ctx, runner.Command{
		Name: "sh",
		Args: []string{"-n", "scripts/install.sh", "scripts/uninstall.sh"},
	}); err != nil {
		return err
	}
	if err := service.stream(ctx, runner.Command{
		Name: "bash",
		Args: append([]string{"-n"}, summaryShellFiles...),
	}); err != nil {
		return err
	}
	if err := service.stream(ctx, runner.Command{
		Name: "node",
		Args: []string{"scripts/node/check-publish-packages.mjs"},
	}); err != nil {
		return err
	}
	if err := service.runGoTool(
		ctx,
		"actionlint",
		"github.com/rhysd/actionlint/cmd/actionlint@v1.7.12",
		nil,
	); err != nil {
		return err
	}
	if err := service.validateRepositoryContracts(ctx); err != nil {
		return err
	}
	if err := service.runOptional(
		ctx,
		"shellcheck",
		append([]string{"-S", "warning"}, retainedShellFiles...),
	); err != nil {
		return err
	}
	return service.runOptional(ctx, "shfmt", append([]string{"-d"}, retainedShellFiles...))
}

func (service *Service) runOptional(ctx context.Context, name string, args []string) error {
	executable, err := service.LookPath(name)
	if err != nil {
		return nil
	}
	return service.stream(ctx, runner.Command{Name: executable, Args: args})
}

func (service *Service) runGoTool(
	ctx context.Context,
	name, module string,
	args []string,
) error {
	executable, err := service.LookPath(name)
	if err == nil {
		return service.stream(ctx, runner.Command{Name: executable, Args: args})
	}
	return service.stream(
		ctx,
		runner.Command{Name: "go", Args: append([]string{"run", module}, args...)},
	)
}

func (service *Service) validateRepositoryContracts(ctx context.Context) error {
	goMod, err := service.ReadFile(service.repositoryPath("go.mod"))
	if err != nil {
		return fmt.Errorf("read go.mod: %w", err)
	}
	languageMatch := regexp.MustCompile(`(?m)^go ([0-9]+\.[0-9]+)$`).FindSubmatch(goMod)
	if languageMatch == nil {
		return fmt.Errorf("go.mod must declare a major.minor Go language version")
	}
	goMinor := string(languageMatch[1])
	if !regexp.MustCompile(`(?m)^toolchain go[0-9]+\.[0-9]+\.[0-9]+$`).Match(goMod) {
		return fmt.Errorf("go.mod must pin a patch-level Go toolchain")
	}

	just, err := service.readContractFile("justfile")
	if err != nil {
		return err
	}
	ci, err := service.readContractFile(".github/workflows/ci.yml")
	if err != nil {
		return err
	}
	release, err := service.readContractFile(".github/workflows/release.yml")
	if err != nil {
		return err
	}
	workflows := ci + "\n" + release
	setupGoCount := strings.Count(workflows, "uses: actions/setup-go@")
	if setupGoCount == 0 ||
		setupGoCount != strings.Count(workflows, `go-version: "`+goMinor+`.x"`) ||
		setupGoCount != strings.Count(workflows, "check-latest: true") {
		return fmt.Errorf("GitHub Actions must use the current Go minor's latest patch release")
	}
	setupNodeCount := strings.Count(workflows, "uses: actions/setup-node@")
	if setupNodeCount == 0 ||
		setupNodeCount != strings.Count(workflows, "node-version-file: .node-version") {
		return fmt.Errorf("GitHub Actions must use the repository .node-version")
	}

	contracts := []struct {
		label     string
		content   string
		fragments []string
	}{
		{
			label:   "Just",
			content: just,
			fragments: []string{
				"go run ./cmd/gate-dev lint",
				"go run ./cmd/gate-dev run \"$@\"",
				"go run ./cmd/gate-dev scripts-check",
				"go run ./cmd/gate-dev release",
				"set positional-arguments",
			},
		},
		{
			label:   "CI",
			content: ci,
			fragments: []string{
				`"$RUNNER_TEMP/gate-dev" lint`,
				`"$RUNNER_TEMP/gate-dev" scripts-check`,
				"GATE_RUN_LINUX_LOW_PORT_TEST",
				"GATE_REQUIRE_INSTALL_PTY_TEST",
			},
		},
		{
			label:   "release workflow",
			content: release,
			fragments: []string{
				`"$RUNNER_TEMP/gate-dev" ci detect-release-tag`,
				`"$RUNNER_TEMP/gate-dev" ci wait-for-ci`,
				`"$RUNNER_TEMP/gate-dev" ci build-release-artifacts`,
				`"$RUNNER_TEMP/gate-dev" ci checksums`,
				`"$RUNNER_TEMP/gate-dev" ci publish-release`,
				`"$RUNNER_TEMP/gate-dev" ci verify-release-tag-target`,
				`"$RUNNER_TEMP/gate-dev" ci wait-release-assets`,
				`"$RUNNER_TEMP/gate-dev" ci generate-homebrew-formula`,
				`node scripts/node/publish-packages.mjs "${VERSION_TAG}" bin`,
				"needs: [release_tag, ci_gate]",
			},
		},
	}
	for _, contract := range contracts {
		for _, fragment := range contract.fragments {
			if !strings.Contains(contract.content, fragment) {
				return fmt.Errorf("%s is missing required command contract %q", contract.label, fragment)
			}
		}
	}
	if regexp.MustCompile(`(?m)^  check:`).MatchString(release) {
		return fmt.Errorf("release workflow must not rerun the CI check job")
	}
	return service.validateShellAllowlist(ctx)
}

func (service *Service) validateShellAllowlist(ctx context.Context) error {
	raw, err := service.output(ctx, runner.Command{
		Name: "git",
		Args: []string{
			"ls-files",
			"-z",
			"--cached",
			"--others",
			"--exclude-standard",
			"--",
			"*.sh",
		},
	})
	if err != nil {
		return err
	}
	var actual []string
	for _, path := range strings.Split(raw, "\x00") {
		path = filepath.ToSlash(strings.TrimSpace(path))
		if path != "" && service.PathExists(service.repositoryPath(path)) {
			actual = append(actual, path)
		}
	}
	slices.Sort(actual)
	expected := append([]string(nil), retainedShellFiles...)
	slices.Sort(expected)
	if !slices.Equal(actual, expected) {
		return fmt.Errorf("shell allowlist mismatch: got %v, want %v", actual, expected)
	}
	return nil
}

func (service *Service) readContractFile(path string) (string, error) {
	data, err := service.ReadFile(service.repositoryPath(path))
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return string(data), nil
}

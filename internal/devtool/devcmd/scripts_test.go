package devcmd

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"gate/internal/devtool/platform"
	"gate/internal/devtool/runner"
)

func TestScriptsCheckValidatesOnlyTheRetainedShellAllowlist(t *testing.T) {
	fake := &fakeRunner{run: func(command runner.Command) error {
		if command.Name == "git" {
			_, _ = io.WriteString(command.Stdout, strings.Join(retainedShellFiles, "\x00")+"\x00")
		}
		return nil
	}}
	service, _, _ := newTestService(fake, platform.Darwin{})
	service.ReadFile = validRepositoryContractFixture
	service.LookPath = func(name string) (string, error) { return "/tools/" + name, nil }
	if code := service.Run(context.Background(), []string{"scripts-check"}); code != 0 {
		t.Fatalf("Run = %d", code)
	}
	var commands []string
	for _, command := range fake.commands {
		commands = append(commands, command.Name+" "+strings.Join(command.Args, " "))
	}
	got := strings.Join(commands, "\n")
	for _, expected := range []string{
		"sh -n scripts/install.sh scripts/uninstall.sh",
		"bash -n .github/scripts/build-summary.sh",
		"node scripts/node/check-publish-packages.mjs",
		"/tools/actionlint ",
		"/tools/shellcheck -S warning",
		"/tools/shfmt -d",
	} {
		if !strings.Contains(got, expected) {
			t.Errorf("commands lack %q:\n%s", expected, got)
		}
	}
	if strings.Contains(got, "scripts/dev/") || strings.Contains(got, "scripts/lib/") {
		t.Fatalf("deleted shell path was executed:\n%s", got)
	}
}

func TestShellAllowlistRejectsAdditionalScript(t *testing.T) {
	actual := append(append([]string(nil), retainedShellFiles...), "scripts/new-domain.sh")
	fake := &fakeRunner{run: func(command runner.Command) error {
		_, _ = io.WriteString(command.Stdout, strings.Join(actual, "\x00")+"\x00")
		return nil
	}}
	service, _, _ := newTestService(fake, platform.Linux{})
	err := service.validateShellAllowlist(context.Background())
	if err == nil || !strings.Contains(err.Error(), "scripts/new-domain.sh") {
		t.Fatalf("error = %v", err)
	}
}

func TestRepositoryContractsRejectMissingReleaseCommand(t *testing.T) {
	fake := &fakeRunner{}
	service, _, _ := newTestService(fake, platform.Linux{})
	service.ReadFile = func(path string) ([]byte, error) {
		data, err := validRepositoryContractFixture(path)
		if err != nil {
			return nil, err
		}
		if strings.HasSuffix(path, ".github/workflows/release.yml") {
			data = []byte(strings.ReplaceAll(
				string(data),
				`"$RUNNER_TEMP/gate-dev" ci publish-release`,
				"",
			))
		}
		return data, nil
	}
	err := service.validateRepositoryContracts(context.Background())
	if err == nil || !strings.Contains(err.Error(), "publish-release") {
		t.Fatalf("error = %v", err)
	}
}

func TestRepositoryContractsRequireReleasePreflightAndRequiredLowPortMode(t *testing.T) {
	tests := []struct {
		name        string
		pathSuffix  string
		old         string
		replacement string
		want        string
	}{
		{
			name:        "release target",
			pathSuffix:  ".github/workflows/release.yml",
			old:         "source-sha: ${{ needs.release_tag.outputs.target }}",
			replacement: "source-sha: ${{ github.sha }}",
			want:        "source-sha",
		},
		{
			name:       "required low port",
			pathSuffix: ".github/actions/preflight/action.yml",
			old:        linuxLowPortCIContract,
			replacement: strings.Replace(
				linuxLowPortCIContract,
				`GATE_REQUIRE_LINUX_LOW_PORT_TEST: "1"`,
				"",
				1,
			),
			want: "required Linux low-port contract",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, _, _ := newTestService(&fakeRunner{}, platform.Linux{})
			service.ReadFile = func(path string) ([]byte, error) {
				data, err := validRepositoryContractFixture(path)
				if err != nil {
					return nil, err
				}
				if strings.HasSuffix(path, test.pathSuffix) {
					data = []byte(strings.Replace(string(data), test.old, test.replacement, 1))
				}
				return data, nil
			}
			err := service.validateRepositoryContracts(context.Background())
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestScriptsCheckFallsBackToPinnedActionlintAndSkipsOptionalFormatters(t *testing.T) {
	fake := &fakeRunner{run: func(command runner.Command) error {
		if command.Name == "git" {
			_, _ = io.WriteString(command.Stdout, strings.Join(retainedShellFiles, "\x00")+"\x00")
		}
		return nil
	}}
	service, _, _ := newTestService(fake, platform.Darwin{})
	service.ReadFile = validRepositoryContractFixture
	service.LookPath = func(string) (string, error) { return "", os.ErrNotExist }
	if code := service.Run(context.Background(), []string{"scripts-check"}); code != 0 {
		t.Fatalf("Run = %d", code)
	}
	foundActionlint := false
	for _, command := range fake.commands {
		if strings.HasPrefix(command.Name, "/tools/") {
			t.Fatalf("optional tool ran: %s", command.Name)
		}
		if command.Name == "go" &&
			strings.Contains(strings.Join(command.Args, " "), "actionlint@v1.7.12") {
			foundActionlint = true
		}
	}
	if !foundActionlint {
		t.Fatal("missing pinned actionlint fallback")
	}
}

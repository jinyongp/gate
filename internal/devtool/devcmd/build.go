package devcmd

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	devbuild "gate/internal/devtool/build"
	"gate/internal/devtool/runner"
	"gate/internal/ui"
)

var buildVersionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]*$`)

func (service *Service) build(ctx context.Context) error {
	version, err := service.output(ctx, runner.Command{
		Name: "git",
		Args: []string{"describe", "--tags", "--always", "--dirty"},
	})
	if err != nil {
		version = "dev"
	}
	if !buildVersionPattern.MatchString(version) {
		return fmt.Errorf("git describe returned an unsafe build version %q", version)
	}
	if err := service.MkdirAll(service.repositoryPath("bin"), 0o750); err != nil {
		return fmt.Errorf("create bin directory: %w", err)
	}
	output := filepath.Join("bin", "gate")
	if err := service.stream(ctx, gateBuildCommand(version, output, nil)); err != nil {
		return err
	}
	return nil
}

func (service *Service) runGate(ctx context.Context, args []string) error {
	commandArgs := []string{"run", "./cmd/gate"}
	commandArgs = append(commandArgs, args...)
	return service.stream(ctx, runner.Command{Name: "go", Args: commandArgs})
}

func (service *Service) buildAll(ctx context.Context, args []string) error {
	if len(args) > 2 {
		return &usageError{message: "build-all accepts at most [version] [output-directory]"}
	}
	version := "dev"
	outputDirectory := "."
	if len(args) >= 1 && args[0] != "" {
		version = args[0]
	}
	if len(args) == 2 && args[1] != "" {
		outputDirectory = args[1]
	}
	if !buildVersionPattern.MatchString(version) {
		return &usageError{message: fmt.Sprintf("invalid build version %q", version)}
	}
	if strings.ContainsAny(outputDirectory, "\x00\r\n") {
		return &usageError{message: "output directory contains a control character"}
	}
	resolvedDirectory := service.repositoryPath(outputDirectory)
	if err := service.MkdirAll(resolvedDirectory, 0o750); err != nil {
		return fmt.Errorf("create build output directory: %w", err)
	}
	console := ui.NewConsole(service.Out, service.Err)
	for _, target := range devbuild.ReleaseTargets() {
		output := filepath.Join(outputDirectory, target.BinaryName())
		if err := service.stream(ctx, gateBuildCommand(version, output, target.Environment())); err != nil {
			return fmt.Errorf("build %s: %w", target.BinaryName(), err)
		}
		console.OK("built " + output)
	}
	return nil
}

func gateBuildCommand(version, output string, environment []string) runner.Command {
	return runner.Command{
		Name: "go",
		Args: []string{
			"build",
			"-trimpath",
			"-ldflags",
			"-s -w -X main.version=" + version,
			"-o",
			output,
			"./cmd/gate",
		},
		Env: environment,
	}
}

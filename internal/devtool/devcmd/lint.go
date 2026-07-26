package devcmd

import (
	"context"

	"gate/internal/devtool/runner"
)

const golangciLintModule = "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2"

func (service *Service) lint(ctx context.Context, jsonOutput bool, extraArgs []string) error {
	if jsonOutput {
		return service.runLinter(ctx, "", true, extraArgs)
	}
	if err := service.runLinter(ctx, "", false, extraArgs); err != nil {
		return err
	}
	switch service.Platform.GOOS() {
	case "darwin":
		return service.runLinter(ctx, "linux", false, extraArgs)
	case "linux":
		return service.runLinter(ctx, "darwin", false, extraArgs)
	default:
		if err := service.runLinter(ctx, "darwin", false, extraArgs); err != nil {
			return err
		}
		return service.runLinter(ctx, "linux", false, extraArgs)
	}
}

func (service *Service) runLinter(
	ctx context.Context,
	targetOS string,
	jsonOutput bool,
	extraArgs []string,
) error {
	args := []string{"run"}
	if targetOS != "" {
		args = append(args, "-exec", "/usr/bin/env GOOS="+targetOS+" GOARCH=amd64")
	}
	args = append(args, golangciLintModule, "run", "./...")
	if jsonOutput {
		args = append(
			args,
			"--output.text.path=stderr",
			"--output.text.colors=false",
			"--output.json.path=stdout",
		)
	}
	args = append(args, extraArgs...)
	return service.stream(ctx, runner.Command{Name: "go", Args: args})
}

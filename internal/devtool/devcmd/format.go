package devcmd

import (
	"context"
	"fmt"
	"strings"

	"gate/internal/devtool/runner"
)

func (service *Service) formatCheck(ctx context.Context) error {
	files, err := service.goFiles(ctx, false)
	if err != nil || len(files) == 0 {
		return err
	}
	args := append([]string{"-l"}, files...)
	output, err := service.output(ctx, runner.Command{Name: "gofmt", Args: args})
	if err != nil {
		return err
	}
	if output == "" {
		return nil
	}
	fmt.Fprintln(service.Out, "unformatted: "+output)
	return &reportedError{err: fmt.Errorf("go files are not formatted"), code: 1}
}

func (service *Service) format(ctx context.Context) error {
	files, err := service.goFiles(ctx, true)
	if err != nil || len(files) == 0 {
		return err
	}
	gofmtArgs := append([]string{"-w"}, files...)
	if err := service.stream(ctx, runner.Command{Name: "gofmt", Args: gofmtArgs}); err != nil {
		return err
	}
	goimportsArgs := []string{"run", "golang.org/x/tools/cmd/goimports@v0.47.0", "-w"}
	goimportsArgs = append(goimportsArgs, files...)
	return service.stream(ctx, runner.Command{Name: "go", Args: goimportsArgs})
}

func (service *Service) goFiles(ctx context.Context, includeUntracked bool) ([]string, error) {
	args := []string{"ls-files"}
	if includeUntracked {
		args = append(args, "--cached", "--others", "--exclude-standard")
	}
	args = append(args, "*.go")
	output, err := service.output(ctx, runner.Command{Name: "git", Args: args})
	if err != nil {
		return nil, fmt.Errorf("list Go files: %w", err)
	}
	var files []string
	for _, file := range strings.Split(output, "\n") {
		if file == "" || strings.HasPrefix(file, "internal/truststore/") {
			continue
		}
		files = append(files, file)
	}
	return files, nil
}

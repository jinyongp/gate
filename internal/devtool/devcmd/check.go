package devcmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"gate/internal/devtool/runner"
	"gate/internal/ui"
)

type checkStep struct {
	label string
	run   func(context.Context, *Service) error
}

func (service *Service) check(ctx context.Context) error {
	steps := []checkStep{
		{
			label: "checking documentation boundaries",
			run: func(ctx context.Context, child *Service) error {
				return child.docsCheck(ctx)
			},
		},
		{
			label: "checking Go formatting",
			run: func(ctx context.Context, child *Service) error {
				return child.formatCheck(ctx)
			},
		},
		{
			label: "running Go vet",
			run: func(ctx context.Context, child *Service) error {
				return child.stream(ctx, runner.Command{Name: "go", Args: []string{"vet", "./..."}})
			},
		},
		{
			label: "running Go tests and coverage",
			run: func(ctx context.Context, child *Service) error {
				return child.goTest(ctx, true, nil)
			},
		},
		{
			label: "running Node checks",
			run: func(ctx context.Context, child *Service) error {
				return child.stream(ctx, runner.Command{Name: "pnpm", Args: []string{"node:check"}})
			},
		},
		{
			label: "linting Go for Darwin and Linux",
			run: func(ctx context.Context, child *Service) error {
				return child.lint(ctx, false, nil)
			},
		},
		{
			label: "scanning Go vulnerabilities",
			run: func(ctx context.Context, child *Service) error {
				return child.stream(ctx, runner.Command{
					Name: "go",
					Args: []string{"run", "golang.org/x/vuln/cmd/govulncheck@v1.3.0", "./..."},
				})
			},
		},
		{
			label: "checking scripts and workflows",
			run: func(ctx context.Context, child *Service) error {
				return child.scriptsCheck(ctx)
			},
		},
	}
	console := ui.NewConsole(service.Out, service.Err)
	for _, step := range steps {
		status := ui.StartActivityStatus(service.Out, step.label, ui.ActivityOptions{
			Enabled: ui.ActivityEnabled(service.Out, false),
		})
		var output bytes.Buffer
		child := *service
		child.Out = &output
		child.Err = &output
		err := step.run(ctx, &child)
		if err == nil {
			status.Complete()
			continue
		}
		status.Stop()
		if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		console.Error(step.label)
		if output.Len() > 0 {
			_, _ = service.Err.Write(output.Bytes())
			if output.Bytes()[output.Len()-1] != '\n' {
				fmt.Fprintln(service.Err)
			}
		}
		code := runner.ExitCode(err)
		if code < 0 {
			code = 1
		}
		return &reportedError{err: err, code: code}
	}
	return nil
}

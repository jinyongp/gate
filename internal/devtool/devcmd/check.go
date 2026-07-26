package devcmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

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
			label: "Documentation boundaries",
			run: func(ctx context.Context, child *Service) error {
				return child.docsCheck(ctx)
			},
		},
		{
			label: "Go formatting",
			run: func(ctx context.Context, child *Service) error {
				return child.formatCheck(ctx)
			},
		},
		{
			label: "Go vet",
			run: func(ctx context.Context, child *Service) error {
				return child.stream(ctx, runner.Command{Name: "go", Args: []string{"vet", "./..."}})
			},
		},
		{
			label: "Go tests and coverage",
			run: func(ctx context.Context, child *Service) error {
				return child.goTest(ctx, true, nil)
			},
		},
		{
			label: "Node checks",
			run: func(ctx context.Context, child *Service) error {
				return child.stream(ctx, runner.Command{Name: "pnpm", Args: []string{"node:check"}})
			},
		},
		{
			label: "Go lint (Darwin + Linux)",
			run: func(ctx context.Context, child *Service) error {
				return child.lint(ctx, false, nil)
			},
		},
		{
			label: "Vulnerability scan",
			run: func(ctx context.Context, child *Service) error {
				return child.stream(ctx, runner.Command{
					Name: "go",
					Args: []string{"run", "golang.org/x/vuln/cmd/govulncheck@v1.3.0", "./..."},
				})
			},
		},
		{
			label: "Shell and workflow checks",
			run: func(ctx context.Context, child *Service) error {
				return child.stream(ctx, runner.Command{Name: "scripts/dev/check-scripts.sh"})
			},
		},
	}
	console := ui.NewConsole(service.Out, service.Err)
	for index, step := range steps {
		started := service.Now()
		fmt.Fprintf(service.Out, "[%d/%d] %s\n", index+1, len(steps), step.label)
		var output bytes.Buffer
		child := *service
		child.Out = &output
		child.Err = &output
		err := step.run(ctx, &child)
		elapsed := elapsedSeconds(started, service.Now())
		if err == nil {
			console.StatusOK(fmt.Sprintf("[%d/%d] %s (%ds)", index+1, len(steps), step.label, elapsed))
			continue
		}
		if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		console.Error(fmt.Sprintf("[%d/%d] %s (%ds)", index+1, len(steps), step.label, elapsed))
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

func elapsedSeconds(started, finished time.Time) int {
	if finished.Before(started) {
		return 0
	}
	return int(finished.Sub(started).Seconds())
}

package devcmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gate/internal/devtool/platform"
	"gate/internal/devtool/runner"
	"gate/internal/ui"
)

type Service struct {
	In         io.Reader
	Out        io.Writer
	Err        io.Writer
	Dir        string
	Runner     runner.Runner
	Platform   platform.Host
	Getenv     func(string) string
	ReadFile   func(string) ([]byte, error)
	MkdirAll   func(string, os.FileMode) error
	Now        func() time.Time
	LookPath   func(string) (string, error)
	PathExists func(string) bool
}

func New(
	in io.Reader,
	out, errOut io.Writer,
	commandRunner runner.Runner,
	host platform.Host,
) *Service {
	return &Service{
		In:       in,
		Out:      out,
		Err:      errOut,
		Dir:      ".",
		Runner:   commandRunner,
		Platform: host,
		Getenv:   os.Getenv,
		ReadFile: os.ReadFile,
		MkdirAll: os.MkdirAll,
		Now:      time.Now,
		LookPath: exec.LookPath,
		PathExists: func(path string) bool {
			_, err := os.Lstat(path)
			return err == nil
		},
	}
}

func Handles(command string) bool {
	switch command {
	case "build", "run", "test", "cover", "fmt-check", "vet", "lint",
		"lint-json", "vuln", "docs-check", "fmt", "check", "build-all", "scripts-check":
		return true
	default:
		return false
	}
}

func (service *Service) Run(ctx context.Context, args []string) int {
	if len(args) == 0 {
		ui.NewConsole(service.Out, service.Err).Error("development command is required")
		return 2
	}
	err := service.execute(ctx, args[0], args[1:])
	if err == nil {
		return 0
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		ui.NewConsole(service.Out, service.Err).Info("Aborted.")
		return 130
	}
	var usageErr *usageError
	if errors.As(err, &usageErr) {
		ui.NewConsole(service.Out, service.Err).Error(usageErr.Error())
		return 2
	}
	code := runner.ExitCode(err)
	var reported *reportedError
	if errors.As(err, &reported) {
		return reported.ExitCode()
	}
	if code >= 0 {
		return code
	}
	ui.NewConsole(service.Out, service.Err).Error(err.Error())
	return 1
}

func (service *Service) execute(ctx context.Context, command string, args []string) error {
	switch command {
	case "build":
		if err := requireNoArgs(command, args); err != nil {
			return err
		}
		return service.build(ctx)
	case "run":
		return service.runGate(ctx, args)
	case "test":
		return service.goTest(ctx, false, args)
	case "cover":
		return service.goTest(ctx, true, args)
	case "fmt-check":
		if err := requireNoArgs(command, args); err != nil {
			return err
		}
		return service.formatCheck(ctx)
	case "vet":
		if err := requireNoArgs(command, args); err != nil {
			return err
		}
		return service.stream(ctx, runner.Command{Name: "go", Args: []string{"vet", "./..."}})
	case "lint":
		return service.lint(ctx, false, args)
	case "lint-json":
		return service.lint(ctx, true, args)
	case "vuln":
		if err := requireNoArgs(command, args); err != nil {
			return err
		}
		return service.stream(ctx, runner.Command{
			Name: "go",
			Args: []string{"run", "golang.org/x/vuln/cmd/govulncheck@v1.3.0", "./..."},
		})
	case "docs-check":
		if err := requireNoArgs(command, args); err != nil {
			return err
		}
		return service.docsCheck(ctx)
	case "fmt":
		if err := requireNoArgs(command, args); err != nil {
			return err
		}
		return service.format(ctx)
	case "check":
		if err := requireNoArgs(command, args); err != nil {
			return err
		}
		return service.check(ctx)
	case "build-all":
		return service.buildAll(ctx, args)
	case "scripts-check":
		if err := requireNoArgs(command, args); err != nil {
			return err
		}
		return service.scriptsCheck(ctx)
	default:
		return &usageError{message: fmt.Sprintf("unknown development command %q", command)}
	}
}

func requireNoArgs(command string, args []string) error {
	if len(args) == 0 {
		return nil
	}
	return &usageError{message: fmt.Sprintf("%s does not accept arguments", command)}
}

func (service *Service) stream(ctx context.Context, command runner.Command) error {
	command.Dir = service.Dir
	command.Stdin = service.In
	command.Stdout = service.Out
	command.Stderr = service.Err
	if err := service.Runner.Run(ctx, command); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	return nil
}

func (service *Service) output(ctx context.Context, command runner.Command) (string, error) {
	var stdout, stderr bytes.Buffer
	command.Dir = service.Dir
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := service.Runner.Run(ctx, command); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		detail := strings.TrimSpace(stderr.String())
		if detail != "" {
			return "", fmt.Errorf("%w: %s", err, detail)
		}
		return "", err
	}
	return strings.TrimRight(stdout.String(), "\r\n"), nil
}

func (service *Service) repositoryPath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(service.Dir, path)
}

type usageError struct {
	message string
}

func (err *usageError) Error() string { return err.message }

type reportedError struct {
	err  error
	code int
}

func (err *reportedError) Error() string { return err.err.Error() }
func (err *reportedError) Unwrap() error { return err.err }
func (err *reportedError) ExitCode() int { return err.code }

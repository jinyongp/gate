package cirelease

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gate/internal/devtool/runner"
	"gate/internal/ui"
)

var (
	commitSHAPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
	repoSlugPattern  = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	notFoundPattern  = regexp.MustCompile(`(^|[^0-9])404([^0-9]|$)`)
)

type Service struct {
	Out       io.Writer
	Err       io.Writer
	Dir       string
	Runner    runner.Runner
	Getenv    func(string) string
	Now       func() time.Time
	ReadFile  func(string) ([]byte, error)
	WriteFile func(string, []byte, os.FileMode) error
	MkdirTemp func(string, string) (string, error)
	RemoveAll func(string) error
}

func New(out, errOut io.Writer, commandRunner runner.Runner) *Service {
	return &Service{
		Out:       out,
		Err:       errOut,
		Dir:       ".",
		Runner:    commandRunner,
		Getenv:    os.Getenv,
		Now:       time.Now,
		ReadFile:  os.ReadFile,
		WriteFile: os.WriteFile,
		MkdirTemp: os.MkdirTemp,
		RemoveAll: os.RemoveAll,
	}
}

func (service *Service) Run(ctx context.Context, args []string) int {
	if len(args) == 0 {
		service.console().Error("CI release command is required")
		return 2
	}
	err := service.execute(ctx, args[0], args[1:])
	if err == nil {
		return 0
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		service.console().Info("Aborted.")
		return 130
	}
	var usageErr *usageError
	if errors.As(err, &usageErr) {
		service.console().Error(usageErr.Error())
		return 2
	}
	service.console().Error(err.Error())
	return 1
}

func (service *Service) execute(ctx context.Context, command string, args []string) error {
	switch command {
	case "detect-release-tag":
		if len(args) != 0 {
			return usage("detect-release-tag does not accept arguments")
		}
		return service.detectReleaseTag(ctx)
	case "build-release-artifacts":
		if len(args) > 1 {
			return usage("usage: gate-dev ci build-release-artifacts [vX.Y.Z]")
		}
		version := ""
		for _, argument := range args {
			version = argument
		}
		return service.buildReleaseArtifacts(ctx, version)
	case "checksums":
		if len(args) != 0 {
			return usage("checksums does not accept arguments")
		}
		return service.writeChecksums()
	case "publish-release":
		if len(args) != 1 {
			return usage("usage: gate-dev ci publish-release vX.Y.Z")
		}
		return service.publishRelease(ctx, args[0])
	default:
		return usage(fmt.Sprintf("unknown CI release command %q", command))
	}
}

func (service *Service) console() ui.Console {
	return ui.NewConsole(service.Out, service.Err)
}

func (service *Service) output(ctx context.Context, name string, args ...string) (string, error) {
	var stdout, stderr bytes.Buffer
	err := service.Runner.Run(ctx, runner.Command{
		Name:   name,
		Args:   args,
		Dir:    service.Dir,
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if ctxErr := ctx.Err(); ctxErr != nil {
		return "", ctxErr
	}
	if err != nil {
		return "", &commandError{
			name:   name,
			err:    err,
			stderr: strings.TrimSpace(stderr.String()),
		}
	}
	return strings.TrimRight(stdout.String(), "\r\n"), nil
}

func (service *Service) stream(ctx context.Context, name string, args ...string) error {
	err := service.Runner.Run(ctx, runner.Command{
		Name:   name,
		Args:   args,
		Dir:    service.Dir,
		Stdout: service.Out,
		Stderr: service.Err,
	})
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return err
}

func (service *Service) repositoryPath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(service.Dir, path)
}

func (service *Service) appendGitHubOutput(values ...outputValue) error {
	path := service.Getenv("GITHUB_OUTPUT")
	if path == "" {
		return errors.New("GITHUB_OUTPUT is required")
	}
	var output strings.Builder
	for _, value := range values {
		if strings.ContainsAny(value.Value, "\x00\r\n") {
			return fmt.Errorf("GitHub output %s contains a control character", value.Name)
		}
		fmt.Fprintf(&output, "%s=%s\n", value.Name, value.Value)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return fmt.Errorf("open GITHUB_OUTPUT: %w", err)
	}
	defer func() { _ = file.Close() }()
	if _, err := io.WriteString(file, output.String()); err != nil {
		return fmt.Errorf("write GITHUB_OUTPUT: %w", err)
	}
	return nil
}

func usage(message string) error {
	return &usageError{message: message}
}

type usageError struct {
	message string
}

func (err *usageError) Error() string { return err.message }

type commandError struct {
	name   string
	err    error
	stderr string
}

func (err *commandError) Error() string {
	if err.stderr != "" {
		return fmt.Sprintf("%s: %s", err.name, err.stderr)
	}
	return fmt.Sprintf("%s: %v", err.name, err.err)
}

func (err *commandError) Unwrap() error { return err.err }

type outputValue struct {
	Name  string
	Value string
}

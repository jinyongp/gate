package runner

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
)

type Command struct {
	Name   string
	Args   []string
	Dir    string
	Env    []string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

type Runner interface {
	Run(context.Context, Command) error
}

type OS struct{}

func (OS) Run(ctx context.Context, command Command) error {
	if command.Name == "" {
		return errors.New("command name is required")
	}
	// Callers validate domain inputs before constructing the argument slice.
	cmd := exec.CommandContext(ctx, command.Name, command.Args...) //nolint:gosec // bounded command and structured arguments; no shell parsing
	cmd.Dir = command.Dir
	if command.Env != nil {
		cmd.Env = append(os.Environ(), command.Env...)
	}
	cmd.Stdin = command.Stdin
	cmd.Stdout = command.Stdout
	cmd.Stderr = command.Stderr
	err := cmd.Run()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return err
}

func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

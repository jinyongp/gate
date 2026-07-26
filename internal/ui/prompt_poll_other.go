//go:build !darwin && !linux

package ui

import (
	"context"
	"errors"
	"os"
)

func waitReadable(ctx context.Context, _ *os.File) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return errors.New("context-aware prompts are supported only on macOS and Linux")
}

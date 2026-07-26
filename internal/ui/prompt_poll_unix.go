//go:build darwin || linux

package ui

import (
	"context"
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func waitReadable(ctx context.Context, input *os.File) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		descriptors := []unix.PollFd{{
			Fd:     int32(input.Fd()), //nolint:gosec // supported file descriptors fit PollFd.Fd
			Events: unix.POLLIN,
		}}
		count, err := unix.Poll(descriptors, 100)
		if err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}
			return err
		}
		if count > 0 {
			return nil
		}
	}
}

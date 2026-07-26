// Command gate-dev provides repository development and release tooling. It is
// not included in gate release artifacts.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"gate/internal/devtool"
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return devtool.New(os.Stdin, os.Stdout, os.Stderr).Run(ctx, os.Args[1:])
}

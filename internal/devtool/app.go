package devtool

import (
	"context"
	"fmt"
	"io"

	"gate/internal/devtool/cirelease"
	"gate/internal/devtool/devcmd"
	"gate/internal/devtool/platform"
	"gate/internal/devtool/release"
	"gate/internal/devtool/runner"
	"gate/internal/ui"
)

type App struct {
	In       io.Reader
	Out      io.Writer
	Err      io.Writer
	Runner   runner.Runner
	Platform platform.Host
}

func New(in io.Reader, out, errOut io.Writer) *App {
	return &App{
		In:       in,
		Out:      out,
		Err:      errOut,
		Runner:   runner.OS{},
		Platform: platform.Current(),
	}
}

func (app *App) Run(ctx context.Context, args []string) int {
	switch {
	case len(args) == 0, args[0] == "help", args[0] == "-h", args[0] == "--help":
		app.usage()
		return 0
	case args[0] == "release":
		service := release.New(app.In, app.Out, app.Err, app.Runner)
		return service.Run(ctx, args[1:])
	case args[0] == "ci":
		service := cirelease.New(app.Out, app.Err, app.Runner)
		return service.Run(ctx, args[1:])
	case devcmd.Handles(args[0]):
		service := devcmd.New(app.In, app.Out, app.Err, app.Runner, app.Platform)
		return service.Run(ctx, args)
	default:
		ui.NewConsole(app.Out, app.Err).Error(fmt.Sprintf("unknown gate-dev command %q", args[0]))
		app.usage()
		return 2
	}
}

func (app *App) usage() {
	fmt.Fprintln(app.Out, `gate-dev — repository development tool

usage:
  gate-dev <command> [args]

commands:
  build      build gate for the current host
  run        run gate from source
  test       run race-enabled Go tests
  cover      run race-enabled Go tests with coverage
  fmt-check  report unformatted Go files
  vet        run go vet
  lint       lint current and alternate supported hosts
  lint-json  emit structured lint diagnostics
  vuln       run the Go vulnerability scan
  scripts-check validate retained shell and workflow contracts
  docs-check enforce documentation boundaries
  fmt        format Go files
  check      run the full staged repository check
  build-all  cross-build all release targets
  release    create and atomically push a release tag
  ci         run bounded GitHub release workflow operations
  help       show this help`)
}

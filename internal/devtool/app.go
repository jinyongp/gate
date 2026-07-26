package devtool

import (
	"context"
	"fmt"
	"io"

	"gate/internal/devtool/platform"
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

func (app *App) Run(_ context.Context, args []string) int {
	switch {
	case len(args) == 0, args[0] == "help", args[0] == "-h", args[0] == "--help":
		app.usage()
		return 0
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
  help  show this help`)
}

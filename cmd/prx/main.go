// Command prx is a local-development global HTTPS reverse proxy and port
// registry, shipped as a single Go binary.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"prx/internal/cli"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// command implements a single prx subcommand and returns a process exit code.
type command func(args []string, stdout, stderr io.Writer) int

// commands is the subcommand dispatch table. Subcommands register here as
// features land across the implementation phases.
var commands = map[string]command{
	"up":      cli.Up,
	"down":    cli.Down,
	"ls":      cli.Ls,
	"port":    cli.Port,
	"add":     cli.Add,
	"rm":      cli.Rm,
	"prune":   cli.Prune,
	"run":     cli.Run,
	"daemon":  cli.Daemon,
	"trust":   cli.Trust,
	"ca":      cli.Ca,
	"expose":  cli.Expose,
	"upgrade": cli.Upgrade,
	"skill":   cli.Skill,
	"__serve": cli.Serve,
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("prx", flag.ContinueOnError)
	fs.SetOutput(stderr)
	showVersion := fs.Bool("version", false, "print version and exit")
	fs.Usage = func() { usage(stderr) }
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *showVersion {
		fmt.Fprintln(stdout, version)
		return 0
	}

	rest := fs.Args()
	if len(rest) == 0 {
		usage(stdout)
		return 0
	}
	cmd, ok := commands[rest[0]]
	if !ok {
		fmt.Fprintf(stderr, "prx: unknown command %q\n", rest[0])
		usage(stderr)
		return 2
	}
	return cmd(rest[1:], stdout, stderr)
}

// commandHelp lists public subcommands in display order with a one-line
// summary. Keep in sync with the commands dispatch table; internal commands
// (prefixed "__") are intentionally omitted.
var commandHelp = []struct{ name, summary string }{
	{"up", "bring up the current project: reserve ports, render routes, reload"},
	{"down", "tear down the current project's routes and free its ports"},
	{"ls", "list all reservations with live/down status"},
	{"port", "print the reserved port for a service"},
	{"add", "reserve a port for a domain in the current project"},
	{"rm", "remove a domain reservation from the current project"},
	{"prune", "remove reservations whose project config no longer exists"},
	{"run", "run a child process with PORT injected from the reservation"},
	{"daemon", "control the background proxy daemon (start/stop/status/restart)"},
	{"trust", "install the local CA into the OS and browser trust stores"},
	{"ca", "export the local CA certificate"},
	{"expose", "publish a local service through a public tunnel provider"},
	{"upgrade", "upgrade prx to the latest GitHub release"},
	{"skill", "locate or print the bundled agent skill (path|print)"},
}

func usage(w io.Writer) {
	fmt.Fprint(w, `prx — local-dev HTTPS reverse proxy + port registry

usage:
  prx [--version] <command> [args]

commands:
`)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, c := range commandHelp {
		fmt.Fprintf(tw, "  %s\t%s\n", c.name, c.summary)
	}
	if err := tw.Flush(); err != nil {
		fmt.Fprintln(w, "prx: failed to render usage table", err)
	}
	fmt.Fprint(w, "\nRun 'prx <command> -h' for command-specific flags.\n")
}

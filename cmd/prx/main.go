// Command prx is a local-development global HTTPS reverse proxy and port
// registry, shipped as a single Go binary.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/jinyongp/prx/internal/cli"
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
	"ls":    cli.Ls,
	"port":  cli.Port,
	"add":   cli.Add,
	"rm":    cli.Rm,
	"prune": cli.Prune,
	"run":   cli.Run,
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("prx", flag.ContinueOnError)
	fs.SetOutput(stderr)
	showVersion := fs.Bool("version", false, "print version and exit")
	fs.Usage = func() { usage(stderr) }
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *showVersion {
		fmt.Fprintln(stdout, version)
		return 0
	}

	rest := fs.Args()
	if len(rest) == 0 {
		usage(stderr)
		return 2
	}
	cmd, ok := commands[rest[0]]
	if !ok {
		fmt.Fprintf(stderr, "prx: unknown command %q\n", rest[0])
		usage(stderr)
		return 2
	}
	return cmd(rest[1:], stdout, stderr)
}

func usage(w io.Writer) {
	fmt.Fprint(w, `prx — local-dev HTTPS reverse proxy + port registry

usage:
  prx [--version] <command> [args]

Subcommands are added as features land. See docs/IMPLEMENTATION.md.
`)
}

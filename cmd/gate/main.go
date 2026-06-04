// Command gate is a local-development global HTTPS reverse proxy and port
// registry, shipped as a single Go binary.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"gate/internal/cli"
	"gate/internal/ui"

	"github.com/spf13/cobra"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	cli.SetVersion(version)
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// command implements a single gate subcommand and returns a process exit code.
type command func(args []string, stdout, stderr io.Writer) int

type exitCodeError struct{ code int }

func (e exitCodeError) Error() string {
	return fmt.Sprintf("gate: command exited with code %d", e.code)
}

// commands is the subcommand dispatch table. Subcommands register here as
// features land across the implementation phases.
var commands = map[string]command{
	"init":      cli.Init,
	"up":        cli.Up,
	"down":      cli.Down,
	"ls":        cli.Ls,
	"port":      cli.Port,
	"add":       cli.Add,
	"rm":        cli.Rm,
	"clear":     cli.Clear,
	"prune":     cli.Prune,
	"run":       cli.Run,
	"daemon":    cli.Daemon,
	"doctor":    cli.Doctor,
	"trust":     cli.Trust,
	"untrust":   cli.Untrust,
	"uninstall": cli.Uninstall,
	"ca":        cli.Ca,
	"expose":    cli.Expose,
	"upgrade":   cli.Upgrade,
	"skill":     cli.Skill,
	"__serve":   cli.Serve,
}

func run(args []string, stdout, stderr io.Writer) int {
	cobra.EnableCommandSorting = false
	root := &cobra.Command{
		Use:           "gate",
		Short:         "local-dev HTTPS reverse proxy + port registry",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, cmdArgs []string) error {
			if len(cmdArgs) == 0 {
				usage(cmd.OutOrStdout())
				return nil
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "gate: unknown command %q\n", cmdArgs[0])
			usage(cmd.ErrOrStderr())
			return exitCodeError{code: 2}
		},
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetArgs(args)
	root.Version = version
	root.SetVersionTemplate("{{.Version}}\n")
	root.SetHelpCommand(&cobra.Command{Use: "help", Short: "show help for a command"})
	// Override help for the root only; subcommands (e.g. the built-in
	// completion command) keep cobra's default help so their own argument
	// usage is shown instead of gate's top-level usage.
	defaultHelp := root.HelpFunc()
	root.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		if cmd == root {
			usage(cmd.OutOrStdout())
			return
		}
		defaultHelp(cmd, args)
	})
	defaultUsage := root.UsageFunc()
	root.SetUsageFunc(func(cmd *cobra.Command) error {
		if cmd == root {
			usage(cmd.ErrOrStderr())
			return nil
		}
		return defaultUsage(cmd)
	})

	for _, spec := range cli.Specs {
		name := spec.Name
		if name == "completion" {
			root.InitDefaultCompletionCmd()
			continue
		}
		commandFn, ok := commands[name]
		if !ok {
			continue
		}
		sub := &cobra.Command{
			Use:                name,
			Short:              commandSummary(name),
			Args:               cobra.ArbitraryArgs,
			DisableFlagParsing: true,
			SilenceUsage:       true,
			SilenceErrors:      true,
			RunE: func(cmd *cobra.Command, cmdArgs []string) error {
				code := commandFn(cmdArgs, cmd.OutOrStdout(), cmd.ErrOrStderr())
				if code == 0 {
					return nil
				}
				return exitCodeError{code: code}
			},
		}
		sub.Hidden = strings.HasPrefix(name, "__")
		root.AddCommand(sub)
	}
	for _, name := range extraCommandNames() {
		commandFn := commands[name]
		sub := &cobra.Command{
			Use:                name,
			Short:              commandSummary(name),
			Args:               cobra.ArbitraryArgs,
			DisableFlagParsing: true,
			SilenceUsage:       true,
			SilenceErrors:      true,
			RunE: func(cmd *cobra.Command, cmdArgs []string) error {
				code := commandFn(cmdArgs, cmd.OutOrStdout(), cmd.ErrOrStderr())
				if code == 0 {
					return nil
				}
				return exitCodeError{code: code}
			},
		}
		sub.Hidden = strings.HasPrefix(name, "__")
		root.AddCommand(sub)
	}

	configureCompletions(root)

	if err := root.Execute(); err != nil {
		var codeErr exitCodeError
		if errors.As(err, &codeErr) {
			return codeErr.code
		}
		fmt.Fprintln(stderr, err)
		usage(stderr)
		return 2
	}

	return 0
}

func extraCommandNames() []string {
	seen := map[string]bool{}
	for _, spec := range cli.Specs {
		seen[spec.Name] = true
	}
	var extra []string
	for name := range commands {
		if !seen[name] {
			extra = append(extra, name)
		}
	}
	sort.Strings(extra)
	return extra
}

// commandSummary returns a subcommand's one-line summary from cli.Specs, the
// single source of truth shared with per-command help.
func commandSummary(name string) string {
	for _, s := range cli.Specs {
		if s.Name == name {
			return s.Summary
		}
	}
	return ""
}

// commandArgs returns a subcommand's positional-argument signature from cli.Specs.
func commandArgs(name string) string {
	for _, s := range cli.Specs {
		if s.Name == name {
			return s.Args
		}
	}
	return ""
}

func usage(w io.Writer) {
	if ui.Enabled(w) {
		usageRich(w)
		return
	}
	fmt.Fprint(w, `gate — local-dev HTTPS reverse proxy + port registry

usage:
  gate [-h|--help] [-v|--version] <command> [args]

flags:
  -h, --help     show help
  -v, --version  print version

commands:
`)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, c := range cli.Specs {
		fmt.Fprintf(tw, "  %s\t%s\n", c.Name, c.Summary)
	}
	if err := tw.Flush(); err != nil {
		fmt.Fprintln(w, "gate: failed to render usage table", err)
	}
	fmt.Fprint(w, "\nRun 'gate <command> -h' for command-specific flags.\n")
}

// commandGroups arranges commands into labelled sections for the rich usage
// screen. Every cli.Specs entry should appear in some group; any that does
// not is collected under "MISC" so nothing silently disappears.
var commandGroups = []struct {
	title string
	names []string
}{
	{"PROJECT", []string{"init", "up", "ls", "port", "run", "down"}},
	{"SHARE", []string{"expose"}},
	{"DAEMON", []string{"daemon"}},
	{"REGISTRY", []string{"add", "rm", "clear", "prune"}},
	{"TLS", []string{"trust", "untrust", "ca"}},
	{"MAINTENANCE", []string{"doctor", "upgrade", "completion", "skill", "uninstall"}},
}

// usageRich renders a styled, grouped usage screen for TTYs.
func usageRich(w io.Writer) {
	summary := make(map[string]string, len(cli.Specs))
	width := 0
	for _, c := range cli.Specs {
		summary[c.Name] = c.Summary
		if len(c.Name) > width {
			width = len(c.Name)
		}
	}

	fmt.Fprintln(w, ui.Title("gate", "local-dev HTTPS reverse proxy + port registry"))
	fmt.Fprintf(w, "\n%s\n  gate [-h|--help] [-v|--version] <command> [args]\n", ui.Section("USAGE"))
	fmt.Fprintf(w, "\n%s\n", ui.Section("FLAGS"))
	fmt.Fprintf(w, "  %s  %s\n", ui.Command("-h, --help", 13), "show help")
	fmt.Fprintf(w, "  %s  %s\n", ui.Command("-v, --version", 13), "print version")

	grouped := map[string]bool{}
	for _, g := range commandGroups {
		fmt.Fprintf(w, "\n%s\n", ui.Section(g.title))
		for _, name := range g.names {
			grouped[name] = true
			fmt.Fprintf(w, "  %s  %s\n", ui.Command(name, width), summary[name])
		}
	}

	var misc []string
	for _, c := range cli.Specs {
		if !grouped[c.Name] {
			misc = append(misc, c.Name)
		}
	}
	if len(misc) > 0 {
		fmt.Fprintf(w, "\n%s\n", ui.Section("MISC"))
		for _, name := range misc {
			fmt.Fprintf(w, "  %s  %s\n", ui.Command(name, width), summary[name])
		}
	}

	fmt.Fprintf(w, "\n%s\n", ui.Dim.Render("Run 'gate <command> -h' for command-specific flags."))
}

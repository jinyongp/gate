package cli

import (
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"gate/internal/ui"
)

// CmdSpec is the single source of truth for a subcommand's name, positional
// argument signature, and one-line summary. It drives both the root usage
// screen (cmd/gate) and per-command help/usage rendering here.
type CmdSpec struct {
	Name    string
	Args    string // positional signature, e.g. "<domain> <port>"; "" if none
	Summary string
}

// Specs lists every public subcommand in display order. Keep in sync with the
// dispatch table in cmd/gate/main.go.
var Specs = []CmdSpec{
	{"init", "", "scaffold a starter gate.toml in the current directory"},
	{"up", "", "bring up the current project: reserve ports, render routes, reload"},
	{"down", "", "tear down the current project's routes and keep reservations"},
	{"ls", "", "list current project reservations with live/down status"},
	{"port", "[service]", "print one service port, or list reserved ports"},
	{"add", "<domain> <port>", "reserve a port for a domain in the current project"},
	{"rm", "<domain>|--project [name]", "remove a domain or project reservations"},
	{"prune", "", "remove reservations whose project config no longer exists"},
	{"run", "<service> -- <cmd> [args]", "run a child process with PORT injected from the reservation"},
	{"daemon", "start|stop|restart|status|logs", "control the background proxy daemon"},
	{"trust", "", "install the local CA into the OS and browser trust stores"},
	{"ca", "export [--out <path>]", "export the local CA certificate"},
	{"expose", "<service> --via <provider>", "publish a local service through a public tunnel provider"},
	{"completion", "<bash|zsh|fish>", "print shell completion script (bash|zsh|fish)"},
	{"upgrade", "", "upgrade gate to the latest GitHub release"},
	{"skill", "path|print", "locate or print the bundled agent skill (path|print)"},
}

func specFor(name string) CmdSpec {
	for _, s := range Specs {
		if s.Name == name {
			return s
		}
	}
	return CmdSpec{}
}

// FlagInfo is one row of a command's FLAGS section.
type FlagInfo struct {
	Name, Usage, Default string
}

func collectFlags(fs *flag.FlagSet) []FlagInfo {
	var out []FlagInfo
	fs.VisitAll(func(f *flag.Flag) {
		out = append(out, FlagInfo{Name: f.Name, Usage: f.Usage, Default: f.DefValue})
	})
	return out
}

func groupFlagAliases(flags []FlagInfo) []FlagInfo {
	out := make([]FlagInfo, 0, len(flags))
	used := make([]bool, len(flags))
	for i, f := range flags {
		if used[i] {
			continue
		}
		group := f
		used[i] = true
		if len(f.Name) == 1 {
			for j := i + 1; j < len(flags); j++ {
				if used[j] {
					continue
				}
				if len(flags[j].Name) > 1 && flags[j].Usage == f.Usage && flags[j].Default == f.Default {
					group.Name = f.Name + ", " + flags[j].Name
					used[j] = true
					break
				}
			}
		}
		out = append(out, group)
	}
	return out
}

func section(w io.Writer, label string) string {
	if ui.Enabled(w) {
		return ui.Section(label)
	}
	return label
}

// signature builds "gate <name> <args> [flags]".
func signature(name, args string, hasFlags bool) string {
	sig := "gate " + name
	if args != "" {
		sig += " " + args
	}
	if hasFlags {
		sig += " [flags]"
	}
	return sig
}

// usageLine prints the short "usage: ..." line shown when required arguments are
// missing. The caller is responsible for returning ExitUsage.
func usageLine(w io.Writer, name string) {
	sp := specFor(name)
	fmt.Fprintf(w, "usage: %s\n", signature(name, sp.Args, false))
}

// usageFail reports a missing/invalid-argument usage error. Under --json it
// keeps the JSON error envelope contract; otherwise it prints the unified usage
// line. It returns ExitUsage so callers can `return usageFail(...)`.
func usageFail(stderr io.Writer, jsonOut bool, name string) int {
	if jsonOut {
		sp := specFor(name)
		return fail(stderr, true, ExitUsage, "usage", "usage: "+signature(name, sp.Args, false))
	}
	usageLine(stderr, name)
	return ExitUsage
}

// WriteHelp renders a subcommand's detailed help in gate's unified style: a title
// line, a USAGE section, and an optional FLAGS section. Exported so cmd/gate can
// render the cobra-managed `completion` command identically.
func WriteHelp(w io.Writer, name, args, summary string, flags []FlagInfo) {
	title := "gate " + name
	switch {
	case !ui.Enabled(w) && summary != "":
		fmt.Fprintf(w, "%s — %s\n", title, summary)
	case !ui.Enabled(w):
		fmt.Fprintln(w, title)
	case summary != "":
		fmt.Fprintf(w, "%s — %s\n", ui.Tint(ui.Brand, title), ui.Dim.Render(summary))
	default:
		fmt.Fprintln(w, ui.Tint(ui.Brand, title))
	}

	fmt.Fprintf(w, "\n%s\n  %s\n", section(w, "USAGE"), signature(name, args, len(flags) > 0))

	if len(flags) > 0 {
		fmt.Fprintf(w, "\n%s\n", section(w, "FLAGS"))
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		for _, f := range groupFlagAliases(flags) {
			desc := f.Usage
			// Show a default only when it is meaningful — skip zero values
			// (bool false, empty string, 0) the way flag.PrintDefaults does.
			if f.Default != "" && f.Default != "false" && f.Default != "0" {
				desc = fmt.Sprintf("%s (default %q)", desc, f.Default)
			}
			fmt.Fprintf(tw, "  %s\t%s\n", formatFlagNames(f.Name), desc)
		}
		_ = tw.Flush()
	}
}

func formatFlagNames(names string) string {
	parts := strings.Split(names, ", ")
	for i, name := range parts {
		dash := "--"
		if len(name) == 1 {
			dash = "-"
		}
		parts[i] = dash + name
	}
	return strings.Join(parts, ", ")
}

// parseFlags is the unified flag-parsing front door for subcommands. It handles
// -h/--help by rendering detailed help to stdout (exit 0) and parse errors by
// printing the usage line to stderr (exit 2). When it returns handled=false the
// caller proceeds with fs.Args(). name need not be in Specs (sub-actions like
// "daemon status" render without a summary).
func parseFlags(fs *flag.FlagSet, name string, argv []string, stdout, stderr io.Writer) (handled bool, code int) {
	for _, a := range argv {
		if a == "--" {
			break
		}
		if a == "-h" || a == "--help" {
			sp := specFor(name)
			WriteHelp(stdout, name, sp.Args, sp.Summary, collectFlags(fs))
			return true, ExitOK
		}
	}
	fs.SetOutput(stderr)
	fs.Usage = func() { usageLine(stderr, name) }
	argv = normalizeFlags(fs, argv)
	if err := fs.Parse(argv); err != nil {
		return true, ExitUsage
	}
	return false, 0
}

func normalizeFlags(fs *flag.FlagSet, argv []string) []string {
	var flags, positionals []string
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		if a == "--" {
			positionals = append(positionals, argv[i:]...)
			break
		}
		name, ok := flagName(a)
		if !ok {
			positionals = append(positionals, a)
			continue
		}
		flags = append(flags, a)
		f := fs.Lookup(name)
		if f == nil || isBoolFlag(f) || strings.Contains(a, "=") {
			continue
		}
		if i+1 < len(argv) {
			i++
			flags = append(flags, argv[i])
		}
	}
	return append(flags, positionals...)
}

func flagName(arg string) (string, bool) {
	if !strings.HasPrefix(arg, "-") || arg == "-" {
		return "", false
	}
	arg = strings.TrimLeft(arg, "-")
	name, _, _ := strings.Cut(arg, "=")
	return name, name != ""
}

func isBoolFlag(f *flag.Flag) bool {
	bf, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && bf.IsBoolFlag()
}

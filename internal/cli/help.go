package cli

import (
	"flag"
	"fmt"
	"io"
	"sort"
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

// CommandInfo is one row of a command's COMMANDS section.
type CommandInfo struct {
	Name, Summary string
}

// Specs lists every public subcommand in display order. Keep in sync with the
// dispatch table in cmd/gate/main.go.
var Specs = []CmdSpec{
	{"init", "", "scaffold a starter gate.toml in the current directory"},
	{"up", "", "bring up scoped reservations: reserve ports, render routes, reload"},
	{"ls", "", "list scoped reservations with route and upstream status"},
	{"port", "[service]", "print one scoped service port, or list reserved ports"},
	{"run", "<service> -- <cmd> [args]", "run a child process with PORT injected from the reservation"},
	{"down", "", "tear down scoped routes and keep reservations"},
	{"expose", "<service> --via <provider> | ls | stop <service>", "publish, list, or stop external access"},
	{"daemon", "status|start|stop|restart|logs", "control listener background proxy daemons"},
	{"add", "<service> <domain> <port>", "reserve a port for a scoped service"},
	{"rm", "<service>", "remove one scoped service reservation"},
	{"clear", "", "remove all reservations in the selected scope"},
	{"prune", "", "remove reservations whose project config no longer exists"},
	{"trust", "", "install the local CA into the OS and browser trust stores"},
	{"untrust", "", "remove the local CA from OS and browser trust stores"},
	{"ca", "export [--out <path>]", "export the local CA certificate"},
	{"doctor", "", "check and repair local gate state"},
	{"upgrade", "", "upgrade gate to the latest GitHub release"},
	{"completion", "<bash|zsh|fish>", "print shell completion script (bash|zsh|fish)"},
	{"skill", "path|print", "locate or print the bundled agent skill (path|print)"},
	{"uninstall", "", "remove gate state, binaries, and Homebrew package when applicable"},
}

func specFor(name string) CmdSpec {
	for _, s := range Specs {
		if s.Name == name {
			return s
		}
	}
	switch name {
	case "expose ls":
		return CmdSpec{Name: name, Summary: "list exposure records"}
	case "expose stop":
		return CmdSpec{Name: name, Args: "<service>", Summary: "stop one exposure record"}
	}
	return CmdSpec{}
}

func commandsFor(name string) []CommandInfo {
	switch name {
	case "daemon":
		return []CommandInfo{
			{Name: "status", Summary: "show listener daemon status"},
			{Name: "start", Summary: "start or reuse the default listener daemon"},
			{Name: "stop", Summary: "stop listener daemon(s)"},
			{Name: "restart", Summary: "restart the default listener daemon"},
			{Name: "logs", Summary: "print listener daemon logs"},
		}
	case "expose":
		return []CommandInfo{
			{Name: "ls", Summary: "list exposure records"},
			{Name: "stop", Summary: "stop one exposure record"},
		}
	case "ca":
		return []CommandInfo{
			{Name: "export", Summary: "export the local CA certificate"},
		}
	case "skill":
		return []CommandInfo{
			{Name: "path", Summary: "print the bundled agent skill path"},
			{Name: "print", Summary: "print the bundled agent skill"},
		}
	case "completion":
		return []CommandInfo{
			{Name: "bash", Summary: "print bash completion script"},
			{Name: "zsh", Summary: "print zsh completion script"},
			{Name: "fish", Summary: "print fish completion script"},
		}
	default:
		return nil
	}
}

// FlagInfo is one row of a command's FLAGS section.
type FlagInfo struct {
	Name, Usage, Default string
}

func collectFlags(fs *flag.FlagSet) []FlagInfo {
	var out []FlagInfo
	fs.VisitAll(func(f *flag.Flag) {
		if hiddenHelpFlag(f.Name) {
			return
		}
		out = append(out, FlagInfo{Name: f.Name, Usage: f.Usage, Default: f.DefValue})
	})
	return out
}

func hiddenHelpFlag(name string) bool {
	return name == "https-addr" || name == "http-addr"
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

	if commands := commandsFor(name); len(commands) > 0 {
		fmt.Fprintf(w, "\n%s\n", section(w, "COMMANDS"))
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		for _, cmd := range commands {
			fmt.Fprintf(tw, "  %s\t%s\n", cmd.Name, cmd.Summary)
		}
		_ = tw.Flush()
	}

	if len(flags) > 0 {
		fmt.Fprintf(w, "\n%s\n", section(w, "FLAGS"))
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		for _, f := range sortFlagGroups(groupFlagAliases(flags)) {
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

func sortFlagGroups(flags []FlagInfo) []FlagInfo {
	out := append([]FlagInfo{}, flags...)
	sort.SliceStable(out, func(i, j int) bool {
		return flagDisplayRank(out[i].Name) < flagDisplayRank(out[j].Name)
	})
	return out
}

func flagDisplayRank(names string) int {
	switch canonicalFlagName(names) {
	case "daemon":
		return 10
	case "dns":
		return 20
	case "route":
		return 30
	case "upstream":
		return 40
	case "via":
		return 50
	case "auth":
		return 60
	case "force":
		return 70
	case "fix":
		return 80
	case "name":
		return 90
	case "out":
		return 100
	case "keep-trust":
		return 110
	case "keep-brew":
		return 120
	case "global":
		return 200
	case "project":
		return 210
	case "all":
		return 220
	case "json":
		return 900
	case "yes":
		return 910
	case "help":
		return 990
	case "version":
		return 991
	default:
		return 500
	}
}

func canonicalFlagName(names string) string {
	parts := strings.Split(names, ", ")
	for _, part := range parts {
		if len(part) > 1 {
			return part
		}
	}
	if len(parts) == 0 {
		return names
	}
	return parts[0]
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

package main

import (
	"bytes"
	"sort"
	"strings"

	"gate/internal/cli"

	"github.com/spf13/cobra"
)

func configureCompletions(root *cobra.Command) {
	for _, spec := range completionSpecs() {
		if cmd := findDirectCommand(root, spec.Command); cmd != nil {
			applyCompletionSpec(cmd, spec, spec.Command, nil)
		}
	}

	if completionCmd, _, err := root.Find([]string{"completion"}); err == nil {
		completionCmd.Short = commandSummary("completion")
		for _, sub := range completionCmd.Commands() {
			switch sub.Name() {
			case "bash":
				sub.Short = "print bash completion script"
			case "zsh":
				sub.Short = "print zsh completion script"
			case "fish":
				sub.Short = "print fish completion script"
			case "powershell":
				completionCmd.RemoveCommand(sub)
			}
		}
		completionCmd.SetHelpFunc(func(cmd *cobra.Command, _ []string) {
			cli.WriteHelp(cmd.OutOrStdout(), "completion", commandArgs("completion"), commandSummary("completion"), nil)
		})
		configureZshCompletion(completionCmd)
	}
}

func configureZshCompletion(completionCmd *cobra.Command) {
	zsh := findDirectCommand(completionCmd, "zsh")
	if zsh == nil {
		return
	}
	zsh.RunE = func(cmd *cobra.Command, _ []string) error {
		var script bytes.Buffer
		noDesc, _ := cmd.Flags().GetBool("no-descriptions")
		if noDesc {
			if err := cmd.Root().GenZshCompletionNoDesc(&script); err != nil {
				return err
			}
		} else if err := cmd.Root().GenZshCompletion(&script); err != nil {
			return err
		}
		_, err := cmd.OutOrStdout().Write([]byte(patchZshNoFileFallback(script.String())))
		return err
	}
}

func patchZshNoFileFallback(script string) string {
	const old = `                # We must return an error code here to let zsh know that there were no
                # completions found by _describe; this is what will trigger other
                # matching algorithms to attempt to find completions.
                # For example zsh can match letters in the middle of words.
                return 1`
	const replacement = `                # No file completion means gate owns this argument position.
                # Return success so zsh does not fall back to filename completion.
                return 0`
	return strings.Replace(script, old, replacement, 1)
}

func applyCompletionSpec(cmd *cobra.Command, spec completionSpec, dispatchName string, dispatchPrefix []string) {
	cmd.Flags().SortFlags = false
	for _, flag := range sortedCompletionFlags(spec) {
		addCompletionFlag(cmd, flag)
	}
	if spec.Args != nil || spec.DisableFileCompletion || spec.StopAfterDashDash {
		cmd.ValidArgsFunction = completionArgsFunction(spec)
	}
	for _, childSpec := range spec.Children {
		child := findDirectCommand(cmd, childSpec.Command)
		if child == nil {
			child = completionChildCommand(childSpec.Command, dispatchName, append(dispatchPrefix, childSpec.Command))
			cmd.AddCommand(child)
		}
		applyCompletionSpec(child, childSpec, dispatchName, append(dispatchPrefix, childSpec.Command))
	}
}

func sortedCompletionFlags(spec completionSpec) []completionFlagSpec {
	var flags []completionFlagSpec
	for _, group := range spec.FlagGroups {
		flags = append(flags, completionFlagGroupSpecs(group)...)
	}
	flags = append(flags, spec.Flags...)
	sort.SliceStable(flags, func(i, j int) bool {
		return completionFlagRank(flags[i].Name) < completionFlagRank(flags[j].Name)
	})
	return flags
}

func completionFlagRank(name string) int {
	switch name {
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
	default:
		return 500
	}
}

func completionArgsFunction(spec completionSpec) cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if spec.StopAfterDashDash && containsDashDash(args) {
			return nil, cobra.ShellCompDirectiveDefault
		}
		if values, directive, ok := completePendingFlagValue(spec, cmd, args, toComplete); ok {
			return values, directive
		}
		if spec.Args == nil {
			if spec.DisableFileCompletion {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			return nil, cobra.ShellCompDirectiveDefault
		}
		ctx := newCompletionContext(cmd, args, toComplete)
		values := filterCompletionValues(spec.Args(ctx), toComplete)
		directive := cobra.ShellCompDirectiveDefault
		if spec.DisableFileCompletion {
			directive = cobra.ShellCompDirectiveNoFileComp
		}
		return values, directive
	}
}

func completePendingFlagValue(spec completionSpec, cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective, bool) {
	name, valuePrefix, contextArgs, ok := pendingFlagValueCandidate(args, toComplete)
	if !ok {
		return nil, cobra.ShellCompDirectiveDefault, false
	}
	for _, flag := range expandedCompletionFlags(spec) {
		if flag.Name != name && flag.Short != name {
			continue
		}
		if flag.Kind != completionFlagString {
			return nil, cobra.ShellCompDirectiveDefault, false
		}
		if flag.Files {
			return nil, cobra.ShellCompDirectiveDefault, true
		}
		if flag.Complete != nil {
			ctx := newCompletionContext(cmd, contextArgs, valuePrefix)
			return filterCompletionValues(flag.Complete(ctx), valuePrefix), cobra.ShellCompDirectiveNoFileComp, true
		}
		return nil, cobra.ShellCompDirectiveNoFileComp, true
	}
	return nil, cobra.ShellCompDirectiveDefault, false
}

func pendingFlagValueCandidate(args []string, toComplete string) (string, string, []string, bool) {
	if len(args) > 0 {
		name, valuePrefix, ok := pendingFlagValue(args[len(args)-1], toComplete)
		if ok {
			return name, valuePrefix, args[:len(args)-1], true
		}
	}
	if strings.HasPrefix(toComplete, "-") && strings.Contains(toComplete, "=") {
		name, valuePrefix, ok := pendingFlagValue(toComplete, toComplete)
		if ok {
			return name, valuePrefix, args, true
		}
	}
	return "", "", nil, false
}

func pendingFlagValue(arg string, toComplete string) (string, string, bool) {
	if !strings.HasPrefix(arg, "-") || arg == "-" {
		return "", "", false
	}
	name, value, hasValue := strings.Cut(strings.TrimLeft(arg, "-"), "=")
	if name == "" {
		return "", "", false
	}
	if hasValue {
		return name, value, true
	}
	return name, toComplete, true
}

func expandedCompletionFlags(spec completionSpec) []completionFlagSpec {
	var out []completionFlagSpec
	for _, group := range spec.FlagGroups {
		out = append(out, completionFlagGroupSpecs(group)...)
	}
	out = append(out, spec.Flags...)
	return out
}

func completionChildCommand(name, dispatchName string, dispatchPrefix []string) *cobra.Command {
	return &cobra.Command{
		Use:                name,
		Short:              commandSummary(dispatchName),
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		SilenceUsage:       true,
		SilenceErrors:      true,
		RunE: func(cmd *cobra.Command, args []string) error {
			fn := commands[dispatchName]
			code := fn(append(append([]string{}, dispatchPrefix...), args...), cmd.OutOrStdout(), cmd.ErrOrStderr())
			if code == 0 {
				return nil
			}
			return exitCodeError{code: code}
		},
	}
}

func completionFlagGroupSpecs(group completionFlagGroup) []completionFlagSpec {
	switch group {
	case flagsHelp:
		return []completionFlagSpec{boolFlag("help", "h", "help for this command")}
	case flagsJSON:
		return []completionFlagSpec{boolFlag("json", "", "emit JSON")}
	case flagsScope:
		return []completionFlagSpec{
			boolFlag("global", "g", "target global reservations"),
			stringFlag("project", "p", "target project reservations", completeProjects),
		}
	case flagsScopeAll:
		return []completionFlagSpec{
			boolFlag("global", "g", "target global reservations"),
			stringFlag("project", "p", "target project reservations", completeProjects),
			boolFlag("all", "a", "target all reservation scopes"),
		}
	case flagsYes:
		return []completionFlagSpec{boolFlag("yes", "y", "skip confirmation")}
	case flagsDaemonListen:
		return []completionFlagSpec{
			noValueFlag("http-addr", "", "daemon HTTP listen address"),
			noValueFlag("https-addr", "", "daemon HTTPS listen address"),
		}
	default:
		return nil
	}
}

func addCompletionFlag(cmd *cobra.Command, spec completionFlagSpec) {
	if spec.Name == "" || cmd.Flags().Lookup(spec.Name) != nil {
		return
	}
	switch spec.Kind {
	case completionFlagString:
		if spec.Short != "" {
			cmd.Flags().StringP(spec.Name, spec.Short, "", spec.Usage)
		} else {
			cmd.Flags().String(spec.Name, "", spec.Usage)
		}
	case completionFlagBool:
		if spec.Short != "" {
			cmd.Flags().BoolP(spec.Name, spec.Short, false, spec.Usage)
		} else {
			cmd.Flags().Bool(spec.Name, false, spec.Usage)
		}
	}
	if spec.Files {
		return
	}
	if spec.Complete != nil {
		_ = cmd.RegisterFlagCompletionFunc(spec.Name, completeFunc(spec.Complete))
		return
	}
	if spec.Kind == completionFlagString || spec.NoValues {
		_ = cmd.RegisterFlagCompletionFunc(spec.Name, func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
			return nil, cobra.ShellCompDirectiveNoFileComp
		})
	}
}

func findDirectCommand(parent *cobra.Command, name string) *cobra.Command {
	for _, cmd := range parent.Commands() {
		if cmd.Name() == name {
			return cmd
		}
	}
	return nil
}

func containsDashDash(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			return true
		}
	}
	return false
}

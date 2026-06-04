package main

import "github.com/spf13/cobra"

type completionFlagGroup string

const (
	flagsHelp         completionFlagGroup = "help"
	flagsJSON         completionFlagGroup = "json"
	flagsScope        completionFlagGroup = "scope"
	flagsScopeAll     completionFlagGroup = "scope-all"
	flagsYes          completionFlagGroup = "yes"
	flagsDaemonListen completionFlagGroup = "daemon-listen"
)

type completionFlagKind int

const (
	completionFlagBool completionFlagKind = iota
	completionFlagString
)

type completionFlagSpec struct {
	Name     string
	Short    string
	Usage    string
	Kind     completionFlagKind
	Values   []string
	Complete func(*completionContext) []string
	Files    bool
	NoValues bool
}

type completionSpec struct {
	Command               string
	Summary               string
	Children              []completionSpec
	FlagGroups            []completionFlagGroup
	Flags                 []completionFlagSpec
	Args                  func(*completionContext) []string
	DisableFileCompletion bool
	StopAfterDashDash     bool
}

func completionSpecs() []completionSpec {
	scopedService := completeScopedNames
	noArgs := func(*completionContext) []string { return nil }
	return []completionSpec{
		{Command: "init", FlagGroups: []completionFlagGroup{flagsHelp, flagsJSON, flagsYes}, Flags: []completionFlagSpec{
			stringFlag("name", "", "project name", nil),
			boolFlag("force", "", "overwrite an existing gate.toml"),
		}, Args: noArgs, DisableFileCompletion: true},
		{Command: "up", FlagGroups: []completionFlagGroup{flagsHelp, flagsJSON, flagsScope}, Flags: []completionFlagSpec{
			boolFlag("daemon", "d", "start the background daemon before reloading routes"),
			stringFlag("dns", "", "force DNS mode: localhost|hosts", staticCompletion("localhost", "hosts")),
		}, Args: noArgs, DisableFileCompletion: true},
		{Command: "down", FlagGroups: []completionFlagGroup{flagsHelp, flagsJSON, flagsScope}, Args: noArgs, DisableFileCompletion: true},
		{Command: "ls", FlagGroups: []completionFlagGroup{flagsHelp, flagsJSON, flagsScopeAll}, Flags: []completionFlagSpec{
			stringFlag("route", "", "filter by route: active|inactive", staticCompletion("active", "inactive")),
			stringFlag("upstream", "", "filter by upstream: live|down", staticCompletion("live", "down")),
		}, Args: noArgs, DisableFileCompletion: true},
		{Command: "port", FlagGroups: []completionFlagGroup{flagsHelp, flagsJSON, flagsScopeAll}, Args: func(ctx *completionContext) []string {
			if ctx.hasAnyFlag("a", "all") {
				return nil
			}
			return completeScopedNames(ctx)
		}, DisableFileCompletion: true},
		{Command: "add", FlagGroups: []completionFlagGroup{flagsHelp, flagsJSON, flagsScope}, Args: scopedService, DisableFileCompletion: true},
		{Command: "rm", FlagGroups: []completionFlagGroup{flagsHelp, flagsJSON, flagsScope}, Args: scopedService, DisableFileCompletion: true},
		{Command: "clear", FlagGroups: []completionFlagGroup{flagsHelp, flagsJSON, flagsScope, flagsYes}, Args: noArgs, DisableFileCompletion: true},
		{Command: "prune", FlagGroups: []completionFlagGroup{flagsHelp, flagsJSON}, Args: noArgs, DisableFileCompletion: true},
		{Command: "run", FlagGroups: []completionFlagGroup{flagsHelp, flagsScope}, Args: scopedService, DisableFileCompletion: true, StopAfterDashDash: true},
		{Command: "daemon", FlagGroups: []completionFlagGroup{flagsHelp}, Args: noArgs, DisableFileCompletion: true, Children: []completionSpec{
			{Command: "status", Summary: "show listener daemon status", FlagGroups: []completionFlagGroup{flagsHelp, flagsJSON}, Flags: []completionFlagSpec{boolFlag("all", "a", "target all known listener daemons")}, Args: noArgs, DisableFileCompletion: true},
			{Command: "start", Summary: "start or reuse the default listener daemon", FlagGroups: []completionFlagGroup{flagsHelp}, Args: noArgs, DisableFileCompletion: true},
			{Command: "stop", Summary: "stop listener daemon(s)", FlagGroups: []completionFlagGroup{flagsHelp}, Flags: []completionFlagSpec{boolFlag("all", "a", "target all known listener daemons")}, Args: noArgs, DisableFileCompletion: true},
			{Command: "restart", Summary: "restart the default listener daemon", FlagGroups: []completionFlagGroup{flagsHelp}, Args: noArgs, DisableFileCompletion: true},
			{Command: "logs", Summary: "print listener daemon logs", FlagGroups: []completionFlagGroup{flagsHelp}, Flags: []completionFlagSpec{boolFlag("all", "a", "target all known listener daemons")}, Args: noArgs, DisableFileCompletion: true},
		}},
		{Command: "doctor", FlagGroups: []completionFlagGroup{flagsHelp, flagsJSON}, Flags: []completionFlagSpec{
			boolFlag("fix", "", "repair issues that can be fixed without sudo"),
		}, Args: noArgs, DisableFileCompletion: true},
		{Command: "trust", FlagGroups: []completionFlagGroup{flagsHelp}, Args: noArgs, DisableFileCompletion: true},
		{Command: "untrust", FlagGroups: []completionFlagGroup{flagsHelp}, Args: noArgs, DisableFileCompletion: true},
		{Command: "uninstall", FlagGroups: []completionFlagGroup{flagsHelp, flagsYes}, Flags: []completionFlagSpec{
			boolFlag("keep-brew", "", "do not run brew uninstall for Homebrew installs"),
			boolFlag("keep-trust", "", "leave trust store entries in place"),
		}, Args: noArgs, DisableFileCompletion: true},
		{Command: "ca", FlagGroups: []completionFlagGroup{flagsHelp}, Args: noArgs, DisableFileCompletion: true, Children: []completionSpec{
			{Command: "export", Summary: "export the local CA certificate", FlagGroups: []completionFlagGroup{flagsHelp}, Flags: []completionFlagSpec{
				fileFlag("out", "", "output path"),
			}},
		}},
		{Command: "expose", FlagGroups: []completionFlagGroup{flagsHelp, flagsJSON, flagsScope}, Flags: []completionFlagSpec{
			stringFlag("via", "", "provider: local|lan|cloudflared|tailscale", staticCompletion("local", "lan", "cloudflared", "tailscale")),
			noValueFlag("auth", "", "require basic auth as user:pass"),
		}, Args: func(ctx *completionContext) []string {
			return scopedService(ctx)
		}, DisableFileCompletion: true, Children: []completionSpec{
			{Command: "ls", Summary: "list exposure records", FlagGroups: []completionFlagGroup{flagsHelp, flagsJSON, flagsScopeAll}, Flags: []completionFlagSpec{
				stringFlag("via", "", "provider: local|lan|cloudflared|tailscale", staticCompletion("local", "lan", "cloudflared", "tailscale")),
			}, Args: noArgs, DisableFileCompletion: true},
			{Command: "stop", Summary: "stop one exposure record", FlagGroups: []completionFlagGroup{flagsHelp, flagsJSON, flagsScope}, Flags: []completionFlagSpec{
				stringFlag("via", "", "provider: local|lan|cloudflared|tailscale", staticCompletion("local", "lan", "cloudflared", "tailscale")),
				boolFlag("force", "", "forget stale exposure record"),
			}, Args: scopedService, DisableFileCompletion: true},
		}},
		{Command: "upgrade", FlagGroups: []completionFlagGroup{flagsHelp, flagsYes}, Args: noArgs, DisableFileCompletion: true},
		{Command: "skill", FlagGroups: []completionFlagGroup{flagsHelp}, Args: noArgs, DisableFileCompletion: true, Children: []completionSpec{
			{Command: "path", Summary: "print the bundled agent skill path", FlagGroups: []completionFlagGroup{flagsHelp}, Args: noArgs, DisableFileCompletion: true},
			{Command: "print", Summary: "print the bundled agent skill", FlagGroups: []completionFlagGroup{flagsHelp}, Args: noArgs, DisableFileCompletion: true},
		}},
	}
}

func boolFlag(name, short, usage string) completionFlagSpec {
	return completionFlagSpec{Name: name, Short: short, Usage: usage, Kind: completionFlagBool}
}

func stringFlag(name, short, usage string, complete func(*completionContext) []string) completionFlagSpec {
	return completionFlagSpec{Name: name, Short: short, Usage: usage, Kind: completionFlagString, Complete: complete}
}

func noValueFlag(name, short, usage string) completionFlagSpec {
	return completionFlagSpec{Name: name, Short: short, Usage: usage, Kind: completionFlagString, NoValues: true}
}

func fileFlag(name, short, usage string) completionFlagSpec {
	return completionFlagSpec{Name: name, Short: short, Usage: usage, Kind: completionFlagString, Files: true}
}

func staticCompletion(values ...string) func(*completionContext) []string {
	return func(*completionContext) []string { return values }
}

func completeFunc(fn func(*completionContext) []string) cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		ctx := newCompletionContext(cmd, args, toComplete)
		return filterCompletionValues(fn(ctx), toComplete), orderedNoFileDirective()
	}
}

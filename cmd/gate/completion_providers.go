package main

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gate/internal/config"
	"gate/internal/paths"
	"gate/internal/registry"

	"github.com/spf13/cobra"
)

type completionContext struct {
	cmd        *cobra.Command
	args       []string
	toComplete string
}

func newCompletionContext(cmd *cobra.Command, args []string, toComplete string) *completionContext {
	return &completionContext{cmd: cmd, args: args, toComplete: toComplete}
}

func (c *completionContext) hasAnyFlag(names ...string) bool {
	if c.cmd != nil {
		for _, want := range names {
			if flag := c.cmd.Flags().Lookup(want); flag != nil && flag.Changed {
				return true
			}
		}
	}
	for _, arg := range c.args {
		if arg == "--" {
			return false
		}
		name, ok := completionFlagName(arg)
		if !ok {
			continue
		}
		for _, want := range names {
			if name == want {
				return true
			}
		}
	}
	return false
}

func (c *completionContext) flagValue(names ...string) (string, bool) {
	if c.cmd != nil {
		for _, want := range names {
			if flag := c.cmd.Flags().Lookup(want); flag != nil && flag.Changed {
				return flag.Value.String(), true
			}
		}
	}
	for i := 0; i < len(c.args); i++ {
		arg := c.args[i]
		if arg == "--" {
			return "", false
		}
		name, value, hasValue, ok := completionFlagParts(arg)
		if !ok {
			continue
		}
		for _, want := range names {
			if name != want {
				continue
			}
			if hasValue {
				return value, true
			}
			if i+1 < len(c.args) {
				return c.args[i+1], true
			}
			return "", false
		}
	}
	return "", false
}

func completionFlagName(arg string) (string, bool) {
	name, _, _, ok := completionFlagParts(arg)
	return name, ok
}

func completionFlagParts(arg string) (name, value string, hasValue, ok bool) {
	if !strings.HasPrefix(arg, "-") || arg == "-" {
		return "", "", false, false
	}
	arg = strings.TrimLeft(arg, "-")
	name, value, hasValue = strings.Cut(arg, "=")
	return name, value, hasValue, name != ""
}

func completeProjects(ctx *completionContext) []string {
	if ctx != nil {
		if configPath, ok := ctx.flagValue("config"); ok {
			if ctx.hasAnyFlag("g", "global", "a", "all") {
				return nil
			}
			project, err := loadCompletionConfig(configPath)
			if err != nil {
				return nil
			}
			return []string{project.Name}
		}
	}
	reg, err := readCompletionRegistry()
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	for _, key := range reg.Keys() {
		res := reg.Services[key]
		if strings.TrimSpace(res.Project) != "" {
			seen[res.Project] = true
		}
	}
	return sortedKeys(seen)
}

func completeScopedNames(ctx *completionContext) []string {
	if configPath, ok := ctx.flagValue("config"); ok {
		if ctx.hasAnyFlag("g", "global", "a", "all") {
			return nil
		}
		return completeConfigServices(configPath, ctx)
	}
	if project, ok := ctx.flagValue("p", "project"); ok {
		if strings.TrimSpace(project) == "" {
			return nil
		}
		return completeNamedProjectServices(project)
	}
	if ctx.hasAnyFlag("g", "global") {
		return completeGlobalNames()
	}
	project, err := currentCompletionProject()
	if err == nil {
		return sortedProjectServices(project)
	}
	if errors.Is(err, config.ErrNotFound) {
		return completeGlobalNames()
	}
	return nil
}

func completeConfigServices(configPath string, ctx *completionContext) []string {
	configPath = strings.TrimSpace(configPath)
	if configPath == "" {
		return nil
	}
	project, err := loadCompletionConfig(configPath)
	if err != nil {
		return nil
	}
	if projectName, ok := ctx.flagValue("p", "project"); ok && strings.TrimSpace(projectName) != project.Name {
		return nil
	}
	return sortedProjectServices(project)
}

func completeGlobalNames() []string {
	reg, err := readCompletionRegistry()
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	for _, key := range reg.Keys() {
		res := reg.Services[key]
		if res.Project == "" && res.Standalone && strings.TrimSpace(res.Service) != "" {
			seen[res.Service] = true
		}
	}
	return sortedKeys(seen)
}

func completeNamedProjectServices(projectName string) []string {
	seen := map[string]bool{}
	if current, err := currentCompletionProject(); err == nil && current.Name == projectName {
		for name := range current.Services {
			seen[name] = true
		}
	}
	reg, err := readCompletionRegistry()
	if err != nil {
		return sortedKeys(seen)
	}
	configPaths := map[string]bool{}
	for _, key := range reg.Keys() {
		res := reg.Services[key]
		if res.Project != projectName {
			continue
		}
		if strings.TrimSpace(res.Service) != "" {
			seen[res.Service] = true
		}
		if strings.TrimSpace(res.ConfigPath) != "" {
			configPaths[res.ConfigPath] = true
		}
	}
	for path := range configPaths {
		project, err := config.Load(path)
		if err != nil || project.Name != projectName {
			continue
		}
		for name := range project.Services {
			seen[name] = true
		}
	}
	return sortedKeys(seen)
}

func currentCompletionProject() (*config.Project, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	path, err := config.Discover(cwd)
	if err != nil {
		return nil, err
	}
	return config.Load(path)
}

func loadCompletionConfig(path string) (*config.Project, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	return config.Load(abs)
}

func readCompletionRegistry() (*registry.Registry, error) {
	return registry.Open(filepath.Join(paths.ConfigDir(), "registry.json")).Read()
}

func sortedProjectServices(project *config.Project) []string {
	seen := map[string]bool{}
	for name := range project.Services {
		seen[name] = true
	}
	return sortedKeys(seen)
}

func sortedKeys(values map[string]bool) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func filterCompletionValues(values []string, prefix string) []string {
	if prefix == "" {
		return append([]string{}, values...)
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.HasPrefix(value, prefix) {
			out = append(out, value)
		}
	}
	return out
}

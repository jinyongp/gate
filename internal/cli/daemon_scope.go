package cli

import (
	"errors"
	"flag"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"gate/internal/config"
	"gate/internal/daemon"
	"gate/internal/paths"
	"gate/internal/registry"
)

const (
	daemonScopeGlobal  = "global"
	daemonScopeProject = "project"
)

type daemonScope struct {
	Kind string
	Name string
	Key  string
}

type daemonScopeFlags struct {
	global  *bool
	project *daemonProjectFlag
	all     *bool
}

type daemonProjectFlag struct {
	value string
	set   bool
}

func (f *daemonProjectFlag) String() string {
	return f.value
}

func (f *daemonProjectFlag) Set(value string) error {
	f.value = value
	f.set = true
	return nil
}

func globalDaemonScope() daemonScope {
	return daemonScope{Kind: daemonScopeGlobal}
}

func projectDaemonScope(name string) daemonScope {
	return daemonScope{Kind: daemonScopeProject, Name: strings.TrimSpace(name)}
}

func (s daemonScope) String() string {
	if s.Kind == daemonScopeProject {
		return "project:" + s.Name
	}
	return daemonScopeGlobal
}

func (s daemonScope) fileKey() string {
	if strings.TrimSpace(s.Key) != "" {
		return s.Key
	}
	if s.Kind == daemonScopeProject {
		return "project-" + slug(s.Name)
	}
	return daemonScopeGlobal
}

func (s daemonScope) socketPath() string {
	return paths.DaemonSocketPath(s.fileKey())
}

func (s daemonScope) pidPath() string {
	return paths.DaemonPIDPath(s.fileKey())
}

func (s daemonScope) logPath() string {
	return paths.DaemonLogPath(s.fileKey())
}

func defineDaemonScopeFlags(fs *flag.FlagSet, allowAll bool) daemonScopeFlags {
	project := &daemonProjectFlag{}
	flags := daemonScopeFlags{
		global:  fs.Bool("global", false, "target the global daemon"),
		project: project,
	}
	fs.BoolVar(flags.global, "g", false, "target the global daemon")
	fs.Var(project, "project", "target a project daemon")
	fs.Var(project, "p", "target a project daemon")
	if allowAll {
		flags.all = fs.Bool("all", false, "target all known daemons")
		fs.BoolVar(flags.all, "a", false, "target all known daemons")
	}
	return flags
}

func currentDaemonScope() (daemonScope, error) {
	project, err := currentProject()
	if err == nil {
		return projectDaemonScope(project.Name), nil
	}
	if errors.Is(err, config.ErrNotFound) {
		return globalDaemonScope(), nil
	}
	return daemonScope{}, err
}

func daemonScopesFromCurrentDirAndFlags(flags daemonScopeFlags, allowAll bool) ([]daemonScope, error) {
	globalSet := flags.global != nil && *flags.global
	projectSet := flags.project != nil && flags.project.set
	allSet := flags.all != nil && *flags.all
	setCount := 0
	for _, set := range []bool{globalSet, projectSet, allSet} {
		if set {
			setCount++
		}
	}
	if setCount > 1 {
		return nil, fmt.Errorf("scope flags are mutually exclusive")
	}
	if allSet && !allowAll {
		return nil, fmt.Errorf("--all is not supported for this command")
	}
	if projectSet && strings.TrimSpace(flags.project.value) == "" {
		return nil, fmt.Errorf("project name is required")
	}
	if globalSet {
		return []daemonScope{globalDaemonScope()}, nil
	}
	if projectSet {
		return []daemonScope{projectDaemonScope(flags.project.value)}, nil
	}
	if allSet {
		return allDaemonScopes()
	}
	scope, err := currentDaemonScope()
	if err != nil {
		return nil, err
	}
	return []daemonScope{scope}, nil
}

func singleDaemonScopeFromFlags(flags daemonScopeFlags) (daemonScope, error) {
	scopes, err := daemonScopesFromCurrentDirAndFlags(flags, false)
	if err != nil {
		return daemonScope{}, err
	}
	return scopes[0], nil
}

func allDaemonScopes() ([]daemonScope, error) {
	seen := map[string]daemonScope{globalDaemonScope().fileKey(): globalDaemonScope()}
	if reg, err := registryStore().Read(); err == nil {
		for _, key := range reg.Keys() {
			res := reg.Services[key]
			if strings.TrimSpace(res.Project) == "" {
				continue
			}
			scope := projectDaemonScope(res.Project)
			seen[scope.fileKey()] = scope
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	for _, dir := range []string{
		filepath.Join(paths.RuntimeDir(), "daemons"),
		filepath.Join(paths.ConfigDir(), "daemons"),
	} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, entry := range entries {
			name := entry.Name()
			key := strings.TrimSuffix(strings.TrimSuffix(name, ".sock"), ".pid")
			if key == daemonScopeGlobal {
				seen[key] = globalDaemonScope()
				continue
			}
			if strings.HasPrefix(key, "project-") {
				if _, ok := seen[key]; !ok {
					seen[key] = daemonScope{Kind: daemonScopeProject, Name: strings.TrimPrefix(key, "project-"), Key: key}
				}
			}
		}
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]daemonScope, 0, len(keys))
	for _, key := range keys {
		out = append(out, seen[key])
	}
	return out, nil
}

func daemonClientFor(scope daemonScope) *daemon.Client {
	return daemon.NewClient(scope.socketPath())
}

func scopeForReservation(res registry.Reservation) daemonScope {
	if res.Project != "" {
		return projectDaemonScope(res.Project)
	}
	return globalDaemonScope()
}

func slug(s string) string {
	original := strings.TrimSpace(s)
	s = strings.ToLower(original)
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if unicode.IsSpace(r) || r == '-' || r == '_' || r == '.' || r == '/' || r == ':' {
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
			continue
		}
		if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	hash := slugHash(original)
	if out == "" {
		return "x" + hash
	}
	return out + "-" + hash
}

func slugHash(s string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return fmt.Sprintf("%08x", h.Sum32())
}

package cli

import (
	"errors"
	"flag"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"gate/internal/config"
	"gate/internal/daemon"
	"gate/internal/listener"
	"gate/internal/paths"
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

type daemonStateRef interface {
	String() string
	fileKey() string
	socketPath() string
	pidPath() string
	logPath() string
}

type listenerDaemonRef struct {
	Pair listener.Pair
	Key  listener.Key
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

func defaultListenerRef() listenerDaemonRef {
	return listenerRefFor(listener.DefaultPair())
}

func listenerRefFor(pair listener.Pair) listenerDaemonRef {
	pair = listener.Normalize(pair)
	return listenerDaemonRef{Pair: pair, Key: listener.KeyFor(pair)}
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

func (r listenerDaemonRef) String() string {
	return "listener:" + string(r.Key)
}

func (r listenerDaemonRef) fileKey() string {
	return "listener-" + string(r.Key)
}

func (r listenerDaemonRef) socketPath() string {
	return paths.ListenerDaemonSocketPath(string(r.Key))
}

func (r listenerDaemonRef) pidPath() string {
	return paths.ListenerDaemonPIDPath(string(r.Key))
}

func (r listenerDaemonRef) logPath() string {
	return paths.ListenerDaemonLogPath(string(r.Key))
}

func defineDaemonScopeFlags(fs *flag.FlagSet, allowAll bool) daemonScopeFlags {
	project := &daemonProjectFlag{}
	flags := daemonScopeFlags{
		global:  fs.Bool("global", false, "target global reservations"),
		project: project,
	}
	fs.BoolVar(flags.global, "g", false, "target global reservations")
	fs.Var(project, "project", "target project reservations")
	fs.Var(project, "p", "target project reservations")
	if allowAll {
		flags.all = fs.Bool("all", false, "target all reservation scopes")
		fs.BoolVar(flags.all, "a", false, "target all reservation scopes")
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

func daemonClientForRef(ref daemonStateRef) *daemon.Client {
	return daemon.NewClient(ref.socketPath())
}

func allListenerRefs() ([]listenerDaemonRef, error) {
	seen := map[string]listenerDaemonRef{defaultListenerRef().fileKey(): defaultListenerRef()}
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
			key := strings.TrimSuffix(strings.TrimSuffix(entry.Name(), ".sock"), ".pid")
			if !strings.HasPrefix(key, "listener-") {
				continue
			}
			listenerKey := listener.Key(strings.TrimPrefix(key, "listener-"))
			ref := listenerDaemonRef{Key: listenerKey, Pair: listenerPairForKey(listenerKey)}
			seen[ref.fileKey()] = ref
		}
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]listenerDaemonRef, 0, len(keys))
	for _, key := range keys {
		out = append(out, seen[key])
	}
	return out, nil
}

func listenerPairForKey(key listener.Key) listener.Pair {
	rest, ok := strings.CutPrefix(string(key), "https-")
	if !ok {
		return listener.Pair{}
	}
	httpsKey, httpKey, ok := strings.Cut(rest, "-http-")
	if !ok {
		return listener.Pair{}
	}
	httpsPort, httpsOK := listenerPortKey(httpsKey)
	httpPort, httpOK := listenerPortKey(httpKey)
	if !httpsOK || !httpOK {
		return listener.Pair{}
	}
	return listener.Pair{HTTPSAddr: ":" + httpsPort, HTTPAddr: ":" + httpPort}
}

func listenerPortKey(key string) (string, bool) {
	port, err := strconv.Atoi(key)
	if err != nil || port <= 0 || port > 65535 {
		return "", false
	}
	return key, true
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

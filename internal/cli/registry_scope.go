package cli

import (
	"errors"
	"fmt"
	"strings"

	"gate/internal/config"
	"gate/internal/registry"
)

type registryScopeSelection struct {
	Scope                  daemonScope
	All                    bool
	CurrentProject         *config.Project
	CurrentProjectPath     string
	CurrentProjectSelected bool
	ExplicitProject        bool
	ExplicitGlobal         bool
}

func registryScopeFromFlags(flags daemonScopeFlags, allowAll bool) (registryScopeSelection, error) {
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
		return registryScopeSelection{}, fmt.Errorf("scope flags are mutually exclusive")
	}
	if allSet && !allowAll {
		return registryScopeSelection{}, fmt.Errorf("--all is not supported for this command")
	}
	if projectSet && strings.TrimSpace(flags.project.value) == "" {
		return registryScopeSelection{}, fmt.Errorf("project name is required")
	}
	if allSet {
		return registryScopeSelection{All: true}, nil
	}
	if globalSet {
		return registryScopeSelection{Scope: globalDaemonScope(), ExplicitGlobal: true}, nil
	}
	if projectSet {
		return registryScopeSelection{Scope: projectDaemonScope(flags.project.value), ExplicitProject: true}, nil
	}
	project, path, err := currentProjectPath()
	if err == nil {
		return registryScopeSelection{
			Scope:                  projectDaemonScope(project.Name),
			CurrentProject:         project,
			CurrentProjectPath:     path,
			CurrentProjectSelected: true,
		}, nil
	}
	if errors.Is(err, config.ErrNotFound) {
		return registryScopeSelection{Scope: globalDaemonScope()}, nil
	}
	return registryScopeSelection{}, err
}

func reservationMatchesScope(res registry.Reservation, sel registryScopeSelection) bool {
	if sel.All {
		return true
	}
	if sel.Scope.Kind == daemonScopeProject {
		return res.Project == sel.Scope.Name
	}
	return res.Project == "" && res.Standalone
}

func reservationsForScope(reg *registry.Registry, sel registryScopeSelection) []projectReservation {
	out := make([]projectReservation, 0, len(reg.Services))
	for _, key := range reg.Keys() {
		res := reg.Services[key]
		if reservationMatchesScope(res, sel) {
			out = append(out, projectReservation{Key: key, Reservation: res})
		}
	}
	return out
}

func lookupScopedReservation(name string, sel registryScopeSelection) (registry.Reservation, *reservationLookupError) {
	if sel.All {
		return registry.Reservation{}, &reservationLookupError{Exit: ExitUsage, Code: "bad_scope", Message: "--all can only list reservations"}
	}
	name = strings.TrimSpace(name)
	if err := validateRegistryName(name, "service"); err != nil {
		return registry.Reservation{}, &reservationLookupError{Exit: ExitUsage, Code: "bad_service", Message: err.Error()}
	}
	reg, err := registryStore().Read()
	if err != nil {
		return registry.Reservation{}, &reservationLookupError{Exit: ExitError, Code: "registry_error", Message: err.Error()}
	}
	projectName := ""
	if sel.Scope.Kind == daemonScopeProject {
		projectName = sel.Scope.Name
		if sel.CurrentProjectSelected && sel.CurrentProject != nil {
			if _, ok := sel.CurrentProject.Services[name]; !ok {
				return registry.Reservation{}, &reservationLookupError{Exit: ExitError, Code: "no_service", Message: fmt.Sprintf("no service %q in project", name)}
			}
		}
	}
	res, ok := reg.Get(registry.Key(projectName, name))
	if !ok || res.Port == 0 {
		scope := "global"
		if projectName != "" {
			scope = "project " + strconvQuote(projectName)
		}
		return registry.Reservation{}, &reservationLookupError{Exit: ExitError, Code: "not_allocated", Message: fmt.Sprintf("no reservation for %q in %s", name, scope)}
	}
	return res, nil
}

func projectConfigForName(name string) (*config.Project, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, "", fmt.Errorf("project name is required")
	}
	project, path, err := currentProjectPath()
	if err == nil {
		if project.Name == name {
			return project, path, nil
		}
	} else if !errors.Is(err, config.ErrNotFound) {
		return nil, "", err
	}

	reg, err := registryStore().Read()
	if err != nil {
		return nil, "", err
	}
	for _, key := range reg.Keys() {
		res := reg.Services[key]
		if res.Project != name || strings.TrimSpace(res.ConfigPath) == "" {
			continue
		}
		project, err := config.Load(res.ConfigPath)
		if err != nil {
			return nil, "", err
		}
		if project.Name != name {
			return nil, "", fmt.Errorf("config %s belongs to project %q, not %q", res.ConfigPath, project.Name, name)
		}
		return project, res.ConfigPath, nil
	}
	return nil, "", fmt.Errorf("project config for %q is unknown; run from that project or run gate up there first", name)
}

func validateRegistryName(name, label string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("%s is required", label)
	}
	if label == "service" && config.IsReservedServiceName(name) {
		return fmt.Errorf("reserved service name %q", name)
	}
	if err := config.ValidateServiceName(name); err != nil {
		return fmt.Errorf("invalid %s %q", label, name)
	}
	return nil
}

func strconvQuote(s string) string {
	return `"` + s + `"`
}

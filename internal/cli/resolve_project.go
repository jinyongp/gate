package cli

import (
	"flag"
	"io"
	"sort"

	"gate/internal/registry"
)

type resolvedProjectService struct {
	Service string `json:"service"`
	Domain  string `json:"domain"`
}

// ResolveProject is a hidden, read-only contract used by language clients to
// resolve the exact domains gate would activate for a scope.
func ResolveProject(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("__resolve-project", flag.ContinueOnError)
	scopeFlags := defineDaemonScopeFlags(fs, false)
	if handled, code := parseNoArgFlags(fs, "__resolve-project", args, stdout, stderr); handled {
		return code
	}
	sel, err := registryScopeFromFlags(scopeFlags, false)
	if err != nil {
		return fail(stderr, true, ExitUsage, "bad_scope", err.Error())
	}

	services := []resolvedProjectService{}
	if sel.CurrentProjectSelected && sel.CurrentProject != nil {
		for _, name := range sortedServices(sel.CurrentProject) {
			domain, err := sel.CurrentProject.ServiceDomain(name)
			if err != nil {
				return fail(stderr, true, ExitError, "config", err.Error())
			}
			services = append(services, resolvedProjectService{Service: name, Domain: domain})
		}
	} else {
		reg, err := registryStore().Read()
		if err != nil {
			return fail(stderr, true, ExitError, "registry_error", err.Error())
		}
		services = resolvedServicesForScope(reg, sel)
	}

	out := map[string]any{"scope": sel.Scope.Kind, "services": services}
	if sel.Scope.Kind == daemonScopeProject {
		out["project"] = sel.Scope.Name
	}
	return writeJSON(stdout, out)
}

func resolvedServicesForScope(reg *registry.Registry, sel registryScopeSelection) []resolvedProjectService {
	services := make([]resolvedProjectService, 0)
	for _, item := range reservationsForScope(reg, sel) {
		services = append(services, resolvedProjectService{
			Service: item.Service,
			Domain:  item.Domain,
		})
	}
	sort.Slice(services, func(i, j int) bool { return services[i].Service < services[j].Service })
	return services
}

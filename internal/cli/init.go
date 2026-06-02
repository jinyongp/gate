package cli

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gate/internal/config"
	portx "gate/internal/port"
	"gate/internal/registry"
	"gate/internal/ui"

	"golang.org/x/term"
)

var (
	initPortOwner = portx.ListenerOwner
	initPortBound = portx.IsBound
	initRegistry  = registryStore
)

// Init scaffolds a starter gate.toml in the current directory.
func Init(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit JSON")
	force := fs.Bool("force", false, "overwrite an existing gate.toml")
	name := fs.String("name", "", "project name (default: current directory name)")
	yes := fs.Bool("yes", false, "create a default gate.toml without prompts")
	fs.BoolVar(yes, "y", false, "create a default gate.toml without prompts")
	if handled, code := parseFlags(fs, "init", args, stdout, stderr); handled {
		return code
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fail(stderr, *jsonOut, ExitError, "cwd", err.Error())
	}
	if *name == "" {
		*name = filepath.Base(cwd)
	}

	path := filepath.Join(cwd, config.Filename)
	if _, err := os.Stat(path); err == nil && !*force {
		return fail(stderr, *jsonOut, ExitError, "exists", "gate.toml already exists (use --force to overwrite)")
	}

	spec := defaultInitSpec(*name)
	if !*yes {
		if !stdinIsTerminal() {
			return fail(stderr, *jsonOut, ExitUsage, "interactive_required", "run `gate init -y` to create a default config non-interactively")
		}
		spec, err = promptInitSpec(stdout, *name)
		if err != nil {
			if errors.Is(err, errPromptInterrupted) && !*jsonOut {
				printCancelled(stdout, "init")
				return ExitOK
			}
			return fail(stderr, *jsonOut, ExitError, "init", err.Error())
		}
	}

	content := renderInitSpec(spec)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil { //nolint:gosec // project config, not a secret
		return fail(stderr, *jsonOut, ExitError, "write", err.Error())
	}

	if *jsonOut {
		return writeJSON(stdout, map[string]any{"path": path, "project": spec.ProjectName, "created": true})
	}
	if richOut(stdout, false) {
		fmt.Fprintf(stdout, "%s created %s\n", ui.Tint(ui.Success, "✓"), config.Filename)
	} else {
		fmt.Fprintf(stdout, "created %s\n", config.Filename)
	}
	fmt.Fprintf(stdout, "  project: %s\n", spec.ProjectName)
	for _, svc := range spec.Services {
		if svc.Port == 0 {
			fmt.Fprintf(stdout, "  service: %s -> %s\n", svc.Name, svc.Domain)
		} else {
			fmt.Fprintf(stdout, "  service: %s -> %s (:%d)\n", svc.Name, svc.Domain, svc.Port)
		}
	}
	fmt.Fprintln(stderr, "next: run `gate up -d`")
	return ExitOK
}

type initSpec struct {
	ProjectName string
	Services    []initService
}

type initService struct {
	Name   string
	Domain string
	Port   int
}

func defaultInitSpec(projectName string) initSpec {
	label := domainLabel(projectName)
	return initSpec{
		ProjectName: projectName,
		Services: []initService{{
			Name:   "web",
			Domain: defaultServiceDomain("web", label, "localhost", ""),
		}},
	}
}

func promptInitSpec(stdout io.Writer, defaultName string) (initSpec, error) {
	reader := bufio.NewReader(os.Stdin)
	projectName, err := promptString(reader, stdout, "What is the project name?", defaultName)
	if err != nil {
		return initSpec{}, err
	}
	mode, err := promptChoice(reader, stdout, "How should gate resolve domains?", "localhost", []string{"localhost", "custom"})
	if err != nil {
		return initSpec{}, err
	}
	servicesRaw, err := promptString(reader, stdout, "Which services should gate configure?", "web")
	if err != nil {
		return initSpec{}, err
	}
	serviceNames := splitServiceNames(servicesRaw)
	if len(serviceNames) == 0 {
		serviceNames = []string{"web"}
	}

	baseDomain := ""
	if mode == "custom" {
		baseDomain, err = promptCustomBaseDomain(reader, stdout, projectName)
		if err != nil {
			return initSpec{}, err
		}
	}

	spec := initSpec{ProjectName: projectName, Services: make([]initService, 0, len(serviceNames))}
	projectLabel := domainLabel(projectName)
	for _, name := range serviceNames {
		defaultDomain := defaultServiceDomain(name, projectLabel, mode, baseDomain)
		domainLabel := fmt.Sprintf("What domain should %s use?", name)
		if mode == "localhost" {
			domainLabel = fmt.Sprintf("What localhost name should %s use?", name)
		}
		domain, err := promptServiceDomain(reader, stdout, domainLabel, defaultDomain, mode)
		if err != nil {
			return initSpec{}, err
		}
		port, err := promptOptionalPort(reader, stdout, fmt.Sprintf("What fixed port should %s use?", name), projectName, name)
		if err != nil {
			return initSpec{}, err
		}
		spec.Services = append(spec.Services, initService{Name: name, Domain: domain, Port: port})
	}
	return spec, nil
}

func promptOptionalPort(reader *bufio.Reader, stdout io.Writer, label, projectName, serviceName string) (int, error) {
	for {
		raw, err := promptInput(reader, stdout, portPromptSpec(label))
		if err != nil {
			return 0, err
		}
		port, err := parseOptionalPort(raw)
		if err != nil {
			return 0, err
		}
		if err := reservedPortError(port, projectName, serviceName); err != nil {
			fmt.Fprintln(stdout, renderErrorMessage(stdout, err.Error()))
			continue
		}
		confirmed, err := confirmFixedPort(reader, stdout, port)
		if err != nil {
			return 0, err
		}
		if confirmed {
			return port, nil
		}
	}
}

func confirmFixedPort(reader *bufio.Reader, stdout io.Writer, fixedPort int) (bool, error) {
	if fixedPort == 0 {
		return true, nil
	}
	label := occupiedPortPrompt(fixedPort)
	if label == "" {
		return true, nil
	}
	answer, err := promptChoice(reader, stdout, label, "no", []string{"no", "yes"})
	if err != nil {
		return false, err
	}
	return answer == "yes", nil
}

func reservedPortError(fixedPort int, projectName, serviceName string) error {
	if fixedPort == 0 {
		return nil
	}
	store := initRegistry()
	reg, err := store.Read()
	if err != nil {
		return fmt.Errorf("read registry: %w", err)
	}
	self := registry.Key(projectName, serviceName)
	for _, key := range reg.Keys() {
		res := reg.Services[key]
		if res.Port != fixedPort || key == self {
			continue
		}
		return fmt.Errorf("port %d is already reserved by %s", fixedPort, displayReservationOwner(res))
	}
	return nil
}

func displayReservationOwner(res registry.Reservation) string {
	if res.Project != "" {
		if res.Service != "" {
			return res.Project + "/" + res.Service
		}
		return res.Project
	}
	if res.Standalone {
		if res.Service != "" {
			return "standalone/" + res.Service
		}
		return "standalone"
	}
	if res.Service != "" {
		return res.Service
	}
	return "-"
}

func occupiedPortPrompt(fixedPort int) string {
	if owner, ok := initPortOwner(fixedPort); ok {
		if owner.Command != "" && owner.PID != 0 {
			return fmt.Sprintf("Port %d is already used by %s (pid %d). Use it anyway?", fixedPort, owner.Command, owner.PID)
		}
		if owner.PID != 0 {
			return fmt.Sprintf("Port %d is already used by pid %d. Use it anyway?", fixedPort, owner.PID)
		}
		if owner.Command != "" {
			return fmt.Sprintf("Port %d is already used by %s. Use it anyway?", fixedPort, owner.Command)
		}
	}
	if initPortBound(fixedPort) {
		return fmt.Sprintf("Port %d is already in use. Use it anyway?", fixedPort)
	}
	return ""
}

func promptServiceDomain(reader *bufio.Reader, stdout io.Writer, label, def, mode string) (string, error) {
	if mode == "localhost" {
		return promptLocalhostDomainPrefix(reader, stdout, label, def)
	}
	return promptInput(reader, stdout, customDomainPromptSpec(label, def))
}

func promptCustomBaseDomain(reader *bufio.Reader, stdout io.Writer, projectName string) (string, error) {
	return promptInput(reader, stdout, customDomainPromptSpec("What base custom domain should gate use?", "local."+domainLabel(projectName)+".test"))
}

func promptLocalhostDomainPrefix(reader *bufio.Reader, stdout io.Writer, label, def string) (string, error) {
	defaultPrefix := strings.TrimSuffix(canonicalPromptDomain(def), ".localhost")
	return promptInput(reader, stdout, localhostPromptSpec(label, defaultPrefix))
}

func domainPromptSpec(label, def string) promptInputSpec {
	return promptInputSpec{
		Label:       label,
		Default:     def,
		Placeholder: def,
		Normalize:   canonicalPromptDomain,
		Validate:    validatePromptDomain,
	}
}

func customDomainPromptSpec(label, def string) promptInputSpec {
	spec := domainPromptSpec(label, def)
	spec.Validate = validateCustomPromptDomain
	return spec
}

func localhostPromptSpec(label, defaultPrefix string) promptInputSpec {
	return promptInputSpec{
		Label:            label,
		Default:          defaultPrefix,
		Placeholder:      defaultPrefix + ".localhost",
		Suffix:           ".localhost",
		Normalize:        localhostDomainValue,
		Validate:         validateLocalhostDomain,
		LiveDisplay:      localhostLiveDisplay,
		ConfirmedDisplay: func(value string) string { return value },
	}
}

func portPromptSpec(label string) promptInputSpec {
	return promptInputSpec{
		Label:       label,
		Default:     "auto",
		Placeholder: "auto",
		AcceptRune: func(r rune) bool {
			return r >= '0' && r <= '9'
		},
		Normalize: normalizePromptPort,
		Validate:  validatePromptPort,
	}
}

func localhostDomainValue(raw string) string {
	prefix := localhostPrefix(raw)
	if prefix == "" {
		return ".localhost"
	}
	return prefix + ".localhost"
}

func localhostLiveDisplay(raw, value string) string {
	return strings.TrimSuffix(value, ".localhost")
}

func validatePromptDomain(domain string) error {
	return config.ValidateDomain(domain)
}

func validateCustomPromptDomain(domain string) error {
	if err := validatePromptDomain(domain); err != nil {
		return err
	}
	if !strings.Contains(domain, ".") {
		return fmt.Errorf("custom domain %q must include at least one dot", domain)
	}
	return nil
}

func validateLocalhostDomain(domain string) error {
	if strings.TrimSuffix(domain, ".localhost") == "" {
		return errors.New("localhost prefix is required")
	}
	return validatePromptDomain(domain)
}

func localhostPrefix(raw string) string {
	return strings.TrimSuffix(canonicalPromptDomain(raw), ".localhost")
}

func normalizePromptPort(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "auto"
	}
	return raw
}

func validatePromptPort(raw string) error {
	_, err := parseOptionalPort(raw)
	return err
}

func splitServiceNames(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for _, part := range parts {
		name := domainLabel(strings.TrimSpace(part))
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

func defaultServiceDomain(serviceName, projectLabel, mode, baseDomain string) string {
	if mode == "custom" {
		return serviceName + "." + baseDomain
	}
	return serviceName + "." + projectLabel + ".localhost"
}

func parseOptionalPort(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.EqualFold(raw, "auto") {
		return 0, nil
	}
	port, err := strconv.Atoi(raw)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("invalid port %q", raw)
	}
	return port, nil
}

func canonicalPromptDomain(domain string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")
}

func renderInitSpec(spec initSpec) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[project]\nname = %q\n", spec.ProjectName)
	for _, svc := range spec.Services {
		fmt.Fprintf(&b, "\n[services.%s]\ndomain = %q\n", svc.Name, svc.Domain)
		if svc.Port != 0 {
			fmt.Fprintf(&b, "port = %d\n", svc.Port)
		}
	}
	return b.String()
}

func stdinIsTerminal() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// domainLabel turns an arbitrary project name into a safe DNS label
// (lowercase, [a-z0-9-], no leading/trailing dash).
func domainLabel(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevDash = false
		case !prevDash:
			b.WriteByte('-')
			prevDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "app"
	}
	return out
}

package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gate/internal/config"
	portx "gate/internal/port"
	"gate/internal/registry"
)

func TestInitCreatesValidConfig(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	var out, errb bytes.Buffer
	if code := Init([]string{"-y", "--name", "demo"}, &out, &errb); code != ExitOK {
		t.Fatalf("Init exit = %d, stderr=%s", code, errb.String())
	}
	path := filepath.Join(dir, config.Filename)
	p, err := config.Load(path)
	if err != nil {
		t.Fatalf("generated gate.toml invalid: %v", err)
	}
	if p.Name != "demo" {
		t.Fatalf("project name = %q", p.Name)
	}
	svc, ok := p.Services["web"]
	if !ok || svc.Domain != "web.demo.localhost" {
		t.Fatalf("web service = %+v", p.Services)
	}
}

func TestInitNoClobber(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	var out, errb bytes.Buffer
	if code := Init([]string{"-y"}, &out, &errb); code != ExitOK {
		t.Fatalf("first Init exit = %d", code)
	}
	if code := Init([]string{"-y"}, &out, &errb); code != ExitError {
		t.Fatalf("second Init exit = %d, want error (no clobber)", code)
	}
	if code := Init([]string{"-y", "--force"}, &out, &errb); code != ExitOK {
		t.Fatalf("Init --force exit = %d", code)
	}
}

func TestInitJSON(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	var out, errb bytes.Buffer
	if code := Init([]string{"--json", "-y", "--name", "My App"}, &out, &errb); code != ExitOK {
		t.Fatalf("Init --json exit = %d", code)
	}
	var got struct {
		Project string `json:"project"`
		Created bool   `json:"created"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out.String())
	}
	if !got.Created || got.Project != "My App" {
		t.Fatalf("unexpected: %+v", got)
	}
	// "My App" must yield a valid DNS label.
	p, err := config.Load(filepath.Join(dir, config.Filename))
	if err != nil {
		t.Fatalf("invalid config from spaced name: %v", err)
	}
	if p.Services["web"].Domain != "web.my-app.localhost" {
		t.Fatalf("domain = %q", p.Services["web"].Domain)
	}
}

func TestInitNonInteractiveRequiresYes(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	var out, errb bytes.Buffer
	if code := Init(nil, &out, &errb); code != ExitUsage {
		t.Fatalf("Init exit = %d, want usage; stderr=%s", code, errb.String())
	}
}

func TestRenderInteractiveCustomDomainSpec(t *testing.T) {
	spec := initSpec{
		ProjectName: "demo",
		Services: []initService{
			{Name: "web", Domain: "local.project.test"},
			{Name: "api", Domain: "api.local.project.test", Port: 3001},
		},
	}
	got := renderInitSpec(spec)
	path := filepath.Join(t.TempDir(), config.Filename)
	if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := config.Load(path)
	if err != nil {
		t.Fatalf("generated gate.toml invalid: %v\n%s", err, got)
	}
	if p.Services["web"].Domain != "local.project.test" {
		t.Fatalf("web domain = %q", p.Services["web"].Domain)
	}
	if p.Services["api"].Domain != "api.local.project.test" || p.Services["api"].Port != 3001 {
		t.Fatalf("api service = %+v", p.Services["api"])
	}
}

func TestDefaultServiceDomainIncludesServiceName(t *testing.T) {
	if got := defaultServiceDomain("web", "gate", "localhost", ""); got != "web.gate.localhost" {
		t.Fatalf("web localhost domain = %q", got)
	}
	if got := defaultServiceDomain("app", "gate", "localhost", ""); got != "app.gate.localhost" {
		t.Fatalf("app localhost domain = %q", got)
	}
	if got := defaultServiceDomain("web", "gate", "custom", "local.gate.test"); got != "web.local.gate.test" {
		t.Fatalf("web custom domain = %q", got)
	}
}

func TestParseOptionalPortAuto(t *testing.T) {
	for _, raw := range []string{"", "auto", "AUTO"} {
		got, err := parseOptionalPort(raw)
		if err != nil {
			t.Fatalf("parseOptionalPort(%q): %v", raw, err)
		}
		if got != 0 {
			t.Fatalf("parseOptionalPort(%q) = %d", raw, got)
		}
	}
}

func TestPromptChoiceRejectsUnknownFallbackInput(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("bogus\ncustom\n"))
	var out bytes.Buffer
	got, err := promptChoice(reader, &out, "Domain mode", "localhost", []string{"localhost", "custom"})
	if err != nil {
		t.Fatalf("promptChoice: %v", err)
	}
	if got != "custom" {
		t.Fatalf("choice = %q", got)
	}
	if !strings.Contains(out.String(), "Choose one of: localhost, custom") {
		t.Fatalf("missing retry message:\n%s", out.String())
	}
}

func TestPromptOptionalPortRejectsInvalidFallbackInput(t *testing.T) {
	oldOwner := initPortOwner
	oldBound := initPortBound
	oldRegistry := initRegistry
	t.Cleanup(func() {
		initPortOwner = oldOwner
		initPortBound = oldBound
		initRegistry = oldRegistry
	})
	initPortOwner = func(int) (portx.ProcessOwner, bool) {
		return portx.ProcessOwner{}, false
	}
	initPortBound = func(int) bool {
		return false
	}
	initRegistry = func() *registry.Store {
		return registry.Open(filepath.Join(t.TempDir(), "registry.json"))
	}

	reader := bufio.NewReader(strings.NewReader("abc\n0\n70000\n4312\n"))
	var out bytes.Buffer
	got, err := promptOptionalPort(reader, &out, "Fixed port for web", "demo", "web")
	if err != nil {
		t.Fatalf("promptOptionalPort: %v", err)
	}
	if got != 4312 {
		t.Fatalf("port = %d", got)
	}
	if strings.Count(out.String(), "invalid port") != 3 {
		t.Fatalf("expected three invalid port messages:\n%s", out.String())
	}
}

func TestPromptOptionalPortConfirmsOccupiedPort(t *testing.T) {
	oldOwner := initPortOwner
	oldBound := initPortBound
	oldRegistry := initRegistry
	t.Cleanup(func() {
		initPortOwner = oldOwner
		initPortBound = oldBound
		initRegistry = oldRegistry
	})
	initPortOwner = func(p int) (portx.ProcessOwner, bool) {
		return portx.ProcessOwner{PID: 1234, Command: "node"}, true
	}
	initPortBound = func(int) bool {
		return true
	}
	initRegistry = func() *registry.Store {
		return registry.Open(filepath.Join(t.TempDir(), "registry.json"))
	}

	reader := bufio.NewReader(strings.NewReader("4312\nno\n4313\nyes\n"))
	var out bytes.Buffer
	got, err := promptOptionalPort(reader, &out, "Fixed port for web", "demo", "web")
	if err != nil {
		t.Fatalf("promptOptionalPort: %v", err)
	}
	if got != 4313 {
		t.Fatalf("port = %d", got)
	}
	if !strings.Contains(out.String(), "Port 4312 is already used by node (pid 1234). Use it anyway?") {
		t.Fatalf("missing occupied port prompt:\n%s", out.String())
	}
}

func TestPromptOptionalPortAllowsLowPortWithOccupiedConfirmation(t *testing.T) {
	oldOwner := initPortOwner
	oldBound := initPortBound
	oldRegistry := initRegistry
	t.Cleanup(func() {
		initPortOwner = oldOwner
		initPortBound = oldBound
		initRegistry = oldRegistry
	})
	initPortOwner = func(p int) (portx.ProcessOwner, bool) {
		if p != 80 {
			return portx.ProcessOwner{}, false
		}
		return portx.ProcessOwner{PID: 80, Command: "nginx"}, true
	}
	initPortBound = func(int) bool {
		return false
	}
	initRegistry = func() *registry.Store {
		return registry.Open(filepath.Join(t.TempDir(), "registry.json"))
	}

	reader := bufio.NewReader(strings.NewReader("80\nyes\n"))
	var out bytes.Buffer
	got, err := promptOptionalPort(reader, &out, "Fixed port for web", "demo", "web")
	if err != nil {
		t.Fatalf("promptOptionalPort: %v", err)
	}
	if got != 80 {
		t.Fatalf("port = %d", got)
	}
	if !strings.Contains(out.String(), "Port 80 is already used by nginx (pid 80). Use it anyway?") {
		t.Fatalf("missing occupied port prompt:\n%s", out.String())
	}
}

func TestPromptOptionalPortRejectsReservedPort(t *testing.T) {
	oldOwner := initPortOwner
	oldBound := initPortBound
	oldRegistry := initRegistry
	t.Cleanup(func() {
		initPortOwner = oldOwner
		initPortBound = oldBound
		initRegistry = oldRegistry
	})
	initPortOwner = func(int) (portx.ProcessOwner, bool) {
		return portx.ProcessOwner{}, false
	}
	initPortBound = func(int) bool {
		return false
	}
	store := registry.Open(filepath.Join(t.TempDir(), "registry.json"))
	if err := store.Update(func(r *registry.Registry) error {
		return r.Reserve(registry.Reservation{Project: "other", Service: "api", Domain: "api.localhost", Port: 4312})
	}); err != nil {
		t.Fatal(err)
	}
	initRegistry = func() *registry.Store {
		return store
	}

	reader := bufio.NewReader(strings.NewReader("4312\n4313\n"))
	var out bytes.Buffer
	got, err := promptOptionalPort(reader, &out, "Fixed port for web", "demo", "web")
	if err != nil {
		t.Fatalf("promptOptionalPort: %v", err)
	}
	if got != 4313 {
		t.Fatalf("port = %d", got)
	}
	if !strings.Contains(out.String(), "port 4312 is already reserved by other/api") {
		t.Fatalf("missing reserved port prompt:\n%s", out.String())
	}
}

func TestPromptOptionalPortFailsOnRegistryReadError(t *testing.T) {
	oldOwner := initPortOwner
	oldBound := initPortBound
	oldRegistry := initRegistry
	t.Cleanup(func() {
		initPortOwner = oldOwner
		initPortBound = oldBound
		initRegistry = oldRegistry
	})
	initPortOwner = func(int) (portx.ProcessOwner, bool) {
		return portx.ProcessOwner{}, false
	}
	initPortBound = func(int) bool {
		return false
	}
	path := filepath.Join(t.TempDir(), "registry.json")
	if err := os.WriteFile(path, []byte("not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	initRegistry = func() *registry.Store {
		return registry.Open(path)
	}

	reader := bufio.NewReader(strings.NewReader("4312\n"))
	var out bytes.Buffer
	if _, err := promptOptionalPort(reader, &out, "Fixed port for web", "demo", "web"); err == nil {
		t.Fatal("expected registry read error, got nil")
	}
}

func TestRenderPortPromptShowsInvalidPort(t *testing.T) {
	var out bytes.Buffer
	frame := promptInputFrame{Prompt: "What fixed port should web use? "}
	if err := renderPromptInput(&out, &frame, "0", portPromptSpec("Fixed port")); err != nil {
		t.Fatalf("renderPromptInput: %v", err)
	}
	if !strings.Contains(out.String(), `invalid port "0"`) {
		t.Fatalf("missing live invalid port message:\n%s", out.String())
	}
}

func TestPromptServiceDomainAppendsLocalhostSuffixInLocalhostMode(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("app\n"))
	var out bytes.Buffer
	got, err := promptServiceDomain(reader, &out, "Localhost prefix for web", "web.demo.localhost", "localhost")
	if err != nil {
		t.Fatalf("promptServiceDomain: %v", err)
	}
	if got != "app.localhost" {
		t.Fatalf("domain = %q", got)
	}
}

func TestPromptServiceDomainDoesNotDuplicateLocalhostSuffix(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("app.localhost\n"))
	var out bytes.Buffer
	got, err := promptServiceDomain(reader, &out, "Localhost prefix for web", "web.demo.localhost", "localhost")
	if err != nil {
		t.Fatalf("promptServiceDomain: %v", err)
	}
	if got != "app.localhost" {
		t.Fatalf("domain = %q", got)
	}
}

func TestPromptServiceDomainRejectsInvalidLocalhostPrefix(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("web.gate...\napp\n"))
	var out bytes.Buffer
	got, err := promptServiceDomain(reader, &out, "Localhost prefix for web", "web.demo.localhost", "localhost")
	if err != nil {
		t.Fatalf("promptServiceDomain: %v", err)
	}
	if got != "app.localhost" {
		t.Fatalf("domain = %q", got)
	}
	if !strings.Contains(out.String(), `invalid domain "web.gate...localhost"`) {
		t.Fatalf("missing invalid domain message:\n%s", out.String())
	}
}

func TestPromptCustomBaseDomainRejectsInvalidDomain(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("asdfdasf...fdafsafds\ncustom\nlocal.demo.test\n"))
	var out bytes.Buffer
	got, err := promptCustomBaseDomain(reader, &out, "demo")
	if err != nil {
		t.Fatalf("promptCustomBaseDomain: %v", err)
	}
	if got != "local.demo.test" {
		t.Fatalf("domain = %q", got)
	}
	if !strings.Contains(out.String(), `invalid domain "asdfdasf...fdafsafds"`) {
		t.Fatalf("missing invalid domain message:\n%s", out.String())
	}
	if !strings.Contains(out.String(), `custom domain "custom" must include at least one dot`) {
		t.Fatalf("missing single-label custom domain message:\n%s", out.String())
	}
}

func TestRenderCustomBaseDomainPromptShowsInvalidDomain(t *testing.T) {
	var out bytes.Buffer
	frame := promptInputFrame{Prompt: "What base custom domain should gate use? "}
	if err := renderPromptInput(&out, &frame, "custom", customDomainPromptSpec("Base custom domain", "local.demo.test")); err != nil {
		t.Fatalf("renderPromptInput: %v", err)
	}
	if !strings.Contains(out.String(), `custom domain "custom" must include at least one dot`) {
		t.Fatalf("missing live invalid domain message:\n%s", out.String())
	}
}

func TestRenderLocalhostPrefixPromptShowsInvalidDomain(t *testing.T) {
	var out bytes.Buffer
	frame := promptInputFrame{Prompt: "What localhost name should web use? "}
	if err := renderPromptInput(&out, &frame, "web.gate...", localhostPromptSpec("Localhost prefix", "web.gate")); err != nil {
		t.Fatalf("renderPromptInput: %v", err)
	}
	if !strings.Contains(out.String(), `invalid domain "web.gate...localhost"`) {
		t.Fatalf("missing live invalid domain message:\n%s", out.String())
	}
}

func TestPromptServiceDomainAllowsCustomDomainInCustomMode(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("app.example.test\n"))
	var out bytes.Buffer
	got, err := promptServiceDomain(reader, &out, "Domain for web", "web.local.demo.test", "custom")
	if err != nil {
		t.Fatalf("promptServiceDomain: %v", err)
	}
	if got != "app.example.test" {
		t.Fatalf("domain = %q", got)
	}
}

func TestPromptServiceDomainRejectsInvalidCustomDomain(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("bad name\ncustom\napp.example.test\n"))
	var out bytes.Buffer
	got, err := promptServiceDomain(reader, &out, "Domain for web", "web.local.demo.test", "custom")
	if err != nil {
		t.Fatalf("promptServiceDomain: %v", err)
	}
	if got != "app.example.test" {
		t.Fatalf("domain = %q", got)
	}
	if !strings.Contains(out.String(), `invalid domain "bad name"`) {
		t.Fatalf("missing invalid domain message:\n%s", out.String())
	}
	if !strings.Contains(out.String(), `custom domain "custom" must include at least one dot`) {
		t.Fatalf("missing single-label custom domain message:\n%s", out.String())
	}
}

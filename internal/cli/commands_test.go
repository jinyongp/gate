package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jinyongp/prx/internal/registry"
)

// isolate points prx's config dir at a temp dir for the duration of the test.
func isolate(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

func TestAddThenLsJSON(t *testing.T) {
	isolate(t)
	var out, errb bytes.Buffer
	if code := Add([]string{"web.localhost", "4312"}, &out, &errb); code != ExitOK {
		t.Fatalf("Add exit = %d, stderr=%s", code, errb.String())
	}

	out.Reset()
	if code := Ls([]string{"--json"}, &out, &errb); code != ExitOK {
		t.Fatalf("Ls exit = %d", code)
	}
	var got struct {
		Services []struct {
			Domain string `json:"domain"`
			Port   int    `json:"port"`
		} `json:"services"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	if len(got.Services) != 1 || got.Services[0].Domain != "web.localhost" || got.Services[0].Port != 4312 {
		t.Fatalf("unexpected ls: %+v", got.Services)
	}
}

func TestAddPortConflictExit4(t *testing.T) {
	isolate(t)
	var out, errb bytes.Buffer
	if code := Add([]string{"a.localhost", "4312"}, &out, &errb); code != ExitOK {
		t.Fatalf("first Add exit = %d", code)
	}
	errb.Reset()
	code := Add([]string{"b.localhost", "4312"}, &out, &errb)
	if code != ExitConflict {
		t.Fatalf("conflict exit = %d, want %d", code, ExitConflict)
	}
}

func TestAddJSONErrorEnvelope(t *testing.T) {
	isolate(t)
	var out, errb bytes.Buffer
	_ = Add([]string{"a.localhost", "4312", "--json"}, &out, &errb) // parse note: flags before args
	// Force a conflict with JSON envelope.
	out.Reset()
	errb.Reset()
	_ = Add([]string{"--json", "a.localhost", "4312"}, &out, &errb)
	errb.Reset()
	code := Add([]string{"--json", "b.localhost", "4312"}, &out, &errb)
	if code != ExitConflict {
		t.Fatalf("exit = %d", code)
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(errb.Bytes(), &env); err != nil {
		t.Fatalf("stderr not JSON: %v\n%s", err, errb.String())
	}
	if env.Error.Code != "port_conflict" {
		t.Fatalf("error code = %q", env.Error.Code)
	}
}

func TestRmRemoves(t *testing.T) {
	isolate(t)
	var out, errb bytes.Buffer
	_ = Add([]string{"x.localhost", "4312"}, &out, &errb)
	out.Reset()
	if code := Rm([]string{"x.localhost"}, &out, &errb); code != ExitOK {
		t.Fatalf("Rm exit = %d", code)
	}
	if code := Rm([]string{"x.localhost"}, &out, &errb); code != ExitError {
		t.Fatalf("second Rm exit = %d, want error", code)
	}
}

func setupProject(t *testing.T) {
	t.Helper()
	isolate(t)
	dir := t.TempDir()
	toml := "[project]\nname = \"demo\"\n\n[services.web]\ndomain = \"app.localhost\"\n"
	if err := os.WriteFile(filepath.Join(dir, "prx.toml"), []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	// reserve a port for demo/web
	err := registryStore().Update(func(r *registry.Registry) error {
		return r.Reserve(registry.Reservation{Project: "demo", Service: "web", Domain: "app.localhost", Port: 4400})
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPortReadsReservation(t *testing.T) {
	setupProject(t)
	var out, errb bytes.Buffer
	if code := Port([]string{"web"}, &out, &errb); code != ExitOK {
		t.Fatalf("Port exit = %d, stderr=%s", code, errb.String())
	}
	if strings.TrimSpace(out.String()) != "4400" {
		t.Fatalf("port out = %q", out.String())
	}
}

func TestRunInjectsPort(t *testing.T) {
	setupProject(t)
	var out, errb bytes.Buffer
	code := Run([]string{"web", "--", "sh", "-c", `printf %s "$PORT"`}, &out, &errb)
	if code != ExitOK {
		t.Fatalf("Run exit = %d, stderr=%s", code, errb.String())
	}
	if out.String() != "4400" {
		t.Fatalf("PORT = %q", out.String())
	}
}

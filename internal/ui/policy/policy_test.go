package policy

import (
	"bytes"
	"os"
	"testing"
)

func resetHooks(t *testing.T) {
	t.Helper()
	oldGetenv := getenv
	oldTerminal := isTerminal
	t.Cleanup(func() {
		getenv = oldGetenv
		isTerminal = oldTerminal
	})
}

func TestColorEnabledGatesOutput(t *testing.T) {
	resetHooks(t)
	env := map[string]string{}
	getenv = func(key string) string { return env[key] }

	var buf bytes.Buffer
	if ColorEnabled(&buf) {
		t.Fatal("buffer writer should disable colour by default")
	}

	isTerminal = func(*os.File) bool { return true }
	if !ColorEnabled(os.Stdout) {
		t.Fatal("terminal writer should enable colour by default")
	}

	env[clicolor] = "0"
	if ColorEnabled(os.Stdout) {
		t.Fatal("CLICOLOR=0 should disable default terminal colour")
	}

	env[forceColor] = "1"
	if !ColorEnabled(&buf) {
		t.Fatal("FORCE_COLOR=1 should enable colour for non-TTY writers")
	}

	env[noColor] = "1"
	if ColorEnabled(&buf) {
		t.Fatal("NO_COLOR should win over FORCE_COLOR")
	}
}

func TestColorEnabledForceAliases(t *testing.T) {
	resetHooks(t)
	env := map[string]string{}
	getenv = func(key string) string { return env[key] }
	var buf bytes.Buffer

	for _, key := range []string{forceColor, clicolorForce} {
		env[key] = "1"
		if !ColorEnabled(&buf) {
			t.Fatalf("%s=1 should enable colour", key)
		}
		env[key] = "0"
		if ColorEnabled(&buf) {
			t.Fatalf("%s=0 should not force colour", key)
		}
		delete(env, key)
	}
}

func TestActivityEnabledGatesOutput(t *testing.T) {
	var buf bytes.Buffer
	env := map[string]string{}
	getenv := func(key string) string { return env[key] }
	terminal := func(*os.File) bool { return true }

	if ActivityEnabled(&buf, false, getenv, terminal) {
		t.Fatal("buffer writer should disable activity")
	}
	if !ActivityEnabled(os.Stderr, false, getenv, terminal) {
		t.Fatal("terminal stderr should enable activity")
	}
	if ActivityEnabled(os.Stderr, true, getenv, terminal) {
		t.Fatal("json output should disable activity")
	}
	for _, key := range []string{noColor, ci, gateNoIndicator, gateNoSpinner} {
		env[key] = "1"
		if ActivityEnabled(os.Stderr, false, getenv, terminal) {
			t.Fatalf("%s should disable activity", key)
		}
		delete(env, key)
	}
	env[ci] = "false"
	if !ActivityEnabled(os.Stderr, false, getenv, terminal) {
		t.Fatal("CI=false should not disable activity")
	}
	env[ci] = "0"
	if !ActivityEnabled(os.Stderr, false, getenv, terminal) {
		t.Fatal("CI=0 should not disable activity")
	}
}

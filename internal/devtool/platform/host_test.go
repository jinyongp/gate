package platform

import (
	"errors"
	"runtime"
	"testing"
)

func TestHostAdapters(t *testing.T) {
	darwin, err := ForGOOS("darwin")
	if err != nil {
		t.Fatal(err)
	}
	if darwin.GOOS() != "darwin" || darwin.SupportsLowPortIntegration() {
		t.Fatalf("unexpected Darwin adapter: %#v", darwin)
	}
	if path, ok := darwin.ProcessStatusPath(1); ok || path != "" {
		t.Fatalf("Darwin status path = %q, %v", path, ok)
	}

	linux, err := ForGOOS("linux")
	if err != nil {
		t.Fatal(err)
	}
	if linux.GOOS() != "linux" || !linux.SupportsLowPortIntegration() {
		t.Fatalf("unexpected Linux adapter: %#v", linux)
	}
	if path, ok := linux.ProcessStatusPath(42); !ok || path != "/proc/42/status" {
		t.Fatalf("Linux status path = %q, %v", path, ok)
	}
	if _, ok := linux.ProcessStatusPath(0); ok {
		t.Fatal("Linux adapter accepted invalid pid")
	}
}

func TestCurrentHostAdapter(t *testing.T) {
	host := Current()
	if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
		if host.GOOS() != runtime.GOOS {
			t.Fatalf("Current GOOS = %q, want %q", host.GOOS(), runtime.GOOS)
		}
	}
}

func TestUnsupportedHost(t *testing.T) {
	_, err := ForGOOS("windows")
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("ForGOOS error = %v", err)
	}
}

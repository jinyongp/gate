package build

import (
	"reflect"
	"testing"
)

func TestReleaseTargets(t *testing.T) {
	got := ReleaseTargets()
	want := []Target{
		{GOOS: "darwin", GOARCH: "arm64"},
		{GOOS: "darwin", GOARCH: "amd64"},
		{GOOS: "linux", GOARCH: "arm64"},
		{GOOS: "linux", GOARCH: "amd64"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReleaseTargets = %#v, want %#v", got, want)
	}
	got[0].GOOS = "changed"
	if ReleaseTargets()[0].GOOS != "darwin" {
		t.Fatal("ReleaseTargets returned shared mutable storage")
	}
}

func TestTargetContract(t *testing.T) {
	target := Target{GOOS: "linux", GOARCH: "amd64"}
	if got, want := target.BinaryName(), "gate-linux-amd64"; got != want {
		t.Fatalf("BinaryName = %q, want %q", got, want)
	}
	if got, want := target.Environment(), []string{"CGO_ENABLED=0", "GOOS=linux", "GOARCH=amd64"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Environment = %#v, want %#v", got, want)
	}
}

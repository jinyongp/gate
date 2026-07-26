package build

import "fmt"

type Target struct {
	GOOS   string
	GOARCH string
}

func (t Target) BinaryName() string {
	return fmt.Sprintf("gate-%s-%s", t.GOOS, t.GOARCH)
}

func (t Target) Environment() []string {
	return []string{"CGO_ENABLED=0", "GOOS=" + t.GOOS, "GOARCH=" + t.GOARCH}
}

var releaseTargets = []Target{
	{GOOS: "darwin", GOARCH: "arm64"},
	{GOOS: "darwin", GOARCH: "amd64"},
	{GOOS: "linux", GOARCH: "arm64"},
	{GOOS: "linux", GOARCH: "amd64"},
}

func ReleaseTargets() []Target {
	return append([]Target(nil), releaseTargets...)
}

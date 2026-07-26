package platform

import (
	"errors"
	"fmt"
)

var ErrUnsupported = errors.New("unsupported host operating system")

type Host interface {
	GOOS() string
	SupportsLowPortIntegration() bool
	ProcessStatusPath(pid int) (string, bool)
}

type Darwin struct{}

func (Darwin) GOOS() string                         { return "darwin" }
func (Darwin) SupportsLowPortIntegration() bool     { return false }
func (Darwin) ProcessStatusPath(int) (string, bool) { return "", false }

type Linux struct{}

func (Linux) GOOS() string                     { return "linux" }
func (Linux) SupportsLowPortIntegration() bool { return true }
func (Linux) ProcessStatusPath(pid int) (string, bool) {
	if pid <= 0 {
		return "", false
	}
	return fmt.Sprintf("/proc/%d/status", pid), true
}

func ForGOOS(goos string) (Host, error) {
	switch goos {
	case "darwin":
		return Darwin{}, nil
	case "linux":
		return Linux{}, nil
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnsupported, goos)
	}
}

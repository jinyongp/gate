//go:build !linux

package cli

import (
	"fmt"
	"io"
)

type unsupportedLowPortCapabilityManager struct{}

func platformLowPortCapabilityManager() lowPortCapabilityManager {
	return unsupportedLowPortCapabilityManager{}
}

func (unsupportedLowPortCapabilityManager) Inspect(*lowPortCapabilityTarget) (lowPortCapabilityInspection, error) {
	return lowPortCapabilityInspection{}, &lowPortCapabilityError{
		Code: "unsupported_platform",
		Err:  errLowPortCapabilityUnsupported,
	}
}

func (unsupportedLowPortCapabilityManager) Apply(*lowPortCapabilityTarget) error {
	return &lowPortCapabilityError{
		Code: "unsupported_platform",
		Err:  errLowPortCapabilityUnsupported,
	}
}

func LowPortCapabilityHelper(_ []string, _ io.Writer, stderr io.Writer) int {
	fmt.Fprintln(stderr, "gate: internal low-port capability helper is available on Linux only")
	return ExitUsage
}

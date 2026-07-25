//go:build !linux

package cli

type unsupportedLowPortCapabilityManager struct{}

func platformLowPortCapabilityManager() lowPortCapabilityManager {
	return unsupportedLowPortCapabilityManager{}
}

func (unsupportedLowPortCapabilityManager) Inspect(string) (lowPortCapabilityInspection, error) {
	return lowPortCapabilityInspection{}, &lowPortCapabilityError{
		Code: "unsupported_platform",
		Err:  errLowPortCapabilityUnsupported,
	}
}

func (unsupportedLowPortCapabilityManager) Apply(string) error {
	return &lowPortCapabilityError{
		Code: "unsupported_platform",
		Err:  errLowPortCapabilityUnsupported,
	}
}

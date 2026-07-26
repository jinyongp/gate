//go:build !darwin && !linux

package platform

func Current() Host { return unsupportedHost{} }

type unsupportedHost struct{}

func (unsupportedHost) GOOS() string                         { return "unsupported" }
func (unsupportedHost) SupportsLowPortIntegration() bool     { return false }
func (unsupportedHost) ProcessStatusPath(int) (string, bool) { return "", false }

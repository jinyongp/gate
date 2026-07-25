//go:build !linux

package cli

import "io"

func platformHasLowPortCapabilityArtifacts() bool {
	return false
}

func platformCleanupLowPortCapabilityArtifacts(io.Writer, io.Writer) uninstallStep {
	return uninstallStepNoop
}

func platformAcquireUninstallCapabilityLock() (io.Closer, error) {
	return io.NopCloser(&emptyReader{}), nil
}

func platformAcquireStandaloneInstallLocks([]string) ([]io.Closer, error) {
	return nil, nil
}

type emptyReader struct{}

func (*emptyReader) Read([]byte) (int, error) {
	return 0, io.EOF
}

//go:build linux

package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

func platformHasLowPortCapabilityArtifacts() bool {
	if os.Getenv("GATE_ISOLATED_ROOT") != "" {
		return false
	}
	found := false
	_ = visitLowPortCapabilityHelpers(func(_ string, _ os.FileInfo) error {
		found = true
		return nil
	})
	return found
}

func platformCleanupLowPortCapabilityArtifacts(stdout, stderr io.Writer) uninstallStep {
	if os.Getenv("GATE_ISOLATED_ROOT") != "" {
		return uninstallStepNoop
	}
	if !platformHasLowPortCapabilityArtifacts() {
		return uninstallStepNoop
	}

	var err error
	if linuxCapabilityEUID() == 0 {
		err = cleanupLowPortCapabilityHelpersDirect()
	} else {
		var sudo string
		sudo, err = linuxCapabilityTool("sudo")
		if err == nil {
			err = runLinuxCapabilitySudo(sudo, "-v")
			if err != nil {
				printError(stderr, "failed to authorize interrupted low-port setup cleanup: "+err.Error())
				return uninstallStepPermission
			}
		}
		if err == nil {
			err = linuxCapabilityCleanupHelpers(sudo)
		}
	}
	if err != nil {
		printError(stderr, "failed to remove interrupted low-port setup helper: "+err.Error())
		var capabilityErr *lowPortCapabilityError
		if os.IsPermission(err) || errors.As(err, &capabilityErr) &&
			(capabilityErr.Code == "sudo_failed" || capabilityErr.Code == "sudo_not_found") {
			return uninstallStepPermission
		}
		return uninstallStepFailed
	}
	printUninstallStep(stdout, "removed interrupted Linux low-port setup helper")
	return uninstallStepChanged
}

func platformAcquireUninstallCapabilityLock() (io.Closer, error) {
	if os.Getenv("GATE_ISOLATED_ROOT") != "" {
		return io.NopCloser(strings.NewReader("")), nil
	}
	return linuxCapabilitySetupLock()
}

func platformAcquireStandaloneInstallLocks(gatePaths []string) ([]io.Closer, error) {
	var locks []io.Closer
	closeLocks := func() {
		for i := len(locks) - 1; i >= 0; i-- {
			_ = locks[i].Close()
		}
	}
	for _, gatePath := range gatePaths {
		lockPath := gatePath + ".install.lock"
		if !managedStandaloneInstallPath(gatePath) &&
			!anyPathExists(gatePath, lockPath, gatePath+".install.transaction") {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
			closeLocks()
			return nil, err
		}
		fd, err := unix.Open(
			lockPath,
			unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW,
			0o600,
		)
		if err != nil {
			closeLocks()
			return nil, err
		}
		file := os.NewFile(uintptr(fd), lockPath)
		if err := unix.Fchmod(fd, 0o600); err != nil {
			_ = file.Close()
			closeLocks()
			return nil, err
		}
		info, statErr := file.Stat()
		stat, ok := selfInfoSys(info)
		if statErr != nil || !ok || !info.Mode().IsRegular() ||
			stat.Uid != currentLinuxUID() || info.Mode().Perm()&0o077 != 0 {
			_ = file.Close()
			closeLocks()
			return nil, fmt.Errorf("unsafe standalone install lock: %s", lockPath)
		}
		if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
			_ = file.Close()
			closeLocks()
			return nil, fmt.Errorf(
				"%w: another installation is using %s",
				errUninstallCoordinationBusy,
				gatePath,
			)
		}
		locks = append(locks, file)
	}
	return locks, nil
}

func managedStandaloneInstallPath(gatePath string) bool {
	if binDir := os.Getenv("GATE_BIN_DIR"); binDir != "" &&
		gatePath == filepath.Join(binDir, "gate") {
		return true
	}
	home, err := os.UserHomeDir()
	return err == nil && gatePath == filepath.Join(home, ".local", "bin", "gate")
}

func anyPathExists(paths ...string) bool {
	for _, path := range paths {
		if _, err := os.Lstat(path); err == nil {
			return true
		}
	}
	return false
}

func cleanupLowPortCapabilityHelpersDirect() error {
	return visitLowPortCapabilityHelpers(func(path string, info os.FileInfo) error {
		if !safeLowPortCapabilityHelper(info) {
			return fmt.Errorf("unsafe interrupted helper: %s", path)
		}
		return os.Remove(path)
	})
}

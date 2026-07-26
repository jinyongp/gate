//go:build darwin || linux

// Command fake provides deterministic external-tool fixtures for the
// installer integration tests. It is built only by tests and is never shipped.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

func main() {
	var err error
	switch filepath.Base(os.Args[0]) {
	case "uname":
		err = fakeUname(os.Args[1:])
	case "curl":
		err = fakeCurl(os.Args[1:])
	case "sha256sum":
		err = fakeSHA256Sum(os.Args[1:])
	case "getcap":
		err = fakeGetcap(os.Args[1:])
	case "flock":
		err = fakeFlock(os.Args[1:])
	default:
		err = fakeGate(os.Args[1:])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func fakeUname(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("uname expects one argument")
	}
	switch args[0] {
	case "-s":
		fmt.Println("Linux")
	case "-m":
		fmt.Println("x86_64")
	default:
		return fmt.Errorf("unsupported uname argument %q", args[0])
	}
	return nil
}

func fakeCurl(args []string) error {
	var output, url string
	for index := 0; index < len(args); index++ {
		switch {
		case args[index] == "-o" && index+1 < len(args):
			index++
			output = args[index]
		case strings.HasPrefix(args[index], "http://"), strings.HasPrefix(args[index], "https://"):
			url = args[index]
		}
	}
	switch {
	case strings.HasPrefix(url, "https://api.github.com/"):
		fmt.Println(`{"assets":[{"browser_download_url":"https://example.test/gate-linux-amd64"},{"browser_download_url":"https://example.test/checksums.txt"}]}`)
		return nil
	case url == "https://example.test/gate-linux-amd64":
		return copyFile(os.Getenv("GATE_TEST_ASSET"), output)
	case url == "https://example.test/checksums.txt":
		output, err := fixturePath(output)
		if err != nil {
			return err
		}
		return os.WriteFile(output, []byte("test-checksum  gate-linux-amd64\n"), 0o600) //nolint:gosec // confined to GATE_TEST_ROOT
	default:
		return fmt.Errorf("unsupported curl URL %q", url)
	}
}

func fakeSHA256Sum(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("sha256sum expects one path")
	}
	fmt.Printf("test-checksum  %s\n", args[0])
	return nil
}

func fakeGetcap(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("getcap expects one path")
	}
	if os.Getenv("GATE_TEST_GETCAP_STATE") == "configured" {
		fmt.Printf("%s cap_net_bind_service=ep\n", args[0])
	}
	return nil
}

func fakeFlock(args []string) error {
	if len(args) != 2 || args[0] != "-n" {
		return fmt.Errorf("flock expects -n and a descriptor")
	}
	descriptor, err := strconv.Atoi(args[1])
	if err != nil {
		return fmt.Errorf("parse flock descriptor: %w", err)
	}
	if err := unix.Flock(descriptor, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return fmt.Errorf("lock descriptor %d: %w", descriptor, err)
	}
	return nil
}

func fakeGate(args []string) error {
	if len(args) == 1 && args[0] == "print-cap-eff" {
		return printEffectiveCapabilities()
	}
	logPath := os.Getenv("GATE_TEST_SETUP_LOG")
	if logPath == "" {
		return fmt.Errorf("GATE_TEST_SETUP_LOG is required")
	}
	logPath, err := fixturePath(logPath)
	if err != nil {
		return err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // confined to GATE_TEST_ROOT
	if err != nil {
		return fmt.Errorf("open setup log: %w", err)
	}
	if _, err := fmt.Fprintln(logFile, strings.Join(args, " ")); err != nil {
		_ = logFile.Close()
		return fmt.Errorf("write setup log: %w", err)
	}
	if err := logFile.Close(); err != nil {
		return fmt.Errorf("close setup log: %w", err)
	}
	if delay := os.Getenv("GATE_TEST_SETUP_DELAY"); delay != "" {
		seconds, err := strconv.Atoi(delay)
		if err != nil {
			return fmt.Errorf("parse setup delay: %w", err)
		}
		time.Sleep(time.Duration(seconds) * time.Second)
	}
	if message := os.Getenv("GATE_TEST_SETUP_MESSAGE"); message != "" {
		fmt.Fprintln(os.Stderr, message)
	}
	if rawExit := os.Getenv("GATE_TEST_SETUP_EXIT"); rawExit != "" {
		code, err := strconv.Atoi(rawExit)
		if err != nil {
			return fmt.Errorf("parse setup exit: %w", err)
		}
		os.Exit(code)
	}
	return nil
}

func printEffectiveCapabilities() error {
	content, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return fmt.Errorf("read process status: %w", err)
	}
	for _, line := range strings.Split(string(content), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "CapEff:" {
			fmt.Print(fields[1])
			return nil
		}
	}
	return fmt.Errorf("CapEff is missing")
}

func copyFile(source, destination string) error {
	source, err := fixturePath(source)
	if err != nil {
		return err
	}
	destination, err = fixturePath(destination)
	if err != nil {
		return err
	}
	input, err := os.Open(source) //nolint:gosec // confined to GATE_TEST_ROOT
	if err != nil {
		return err
	}
	defer func() {
		_ = input.Close()
	}()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600) //nolint:gosec // confined to GATE_TEST_ROOT
	if err != nil {
		return err
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		return err
	}
	return output.Close()
}

func fixturePath(path string) (string, error) {
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("fixture path is not absolute")
	}
	for _, rawRoot := range []string{os.Getenv("GATE_TEST_ROOT"), os.Getenv("GATE_TEST_TMP_ROOT")} {
		root := filepath.Clean(rawRoot)
		if root == "." || !filepath.IsAbs(root) {
			continue
		}
		relative, err := filepath.Rel(root, path)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return path, nil
		}
	}
	return "", fmt.Errorf("fixture path escapes test roots: %s", path)
}

package port

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

// Exec runs name+args as a child process with PORT=port injected into its
// environment, forwarding the provided stdio, and returns the child's exit
// code. No file is touched, so the caller's .env is never clobbered.
func Exec(port int, extraEnv []string, name string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	//nolint:gosec // G204: executing user-requested command is required for `gate run`.
	cmd := exec.Command(name, args...)
	cmd.Env = mergeEnv(os.Environ(), append(extraEnv, "PORT="+strconv.Itoa(port)))
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			if status, ok := ee.Sys().(syscall.WaitStatus); ok && status.Signaled() {
				return 128 + int(status.Signal())
			}
			return ee.ExitCode()
		}
		fmt.Fprintln(stderr, "gate run:", err)
		return 1
	}
	return 0
}

func mergeEnv(base []string, overrides []string) []string {
	values := map[string]string{}
	for _, pair := range base {
		key, value, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		values[key] = value
	}
	for _, pair := range overrides {
		key, value, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		values[key] = value
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+values[key])
	}
	return out
}

package port

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
)

// Exec runs name+args as a child process with PORT=port injected into its
// environment, forwarding the provided stdio, and returns the child's exit
// code. No file is touched, so the caller's .env is never clobbered.
func Exec(port int, name string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	//nolint:gosec // G204: launching a user-named command is the whole point of `prx run`.
	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), "PORT="+strconv.Itoa(port))
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode()
		}
		fmt.Fprintln(stderr, "prx run:", err)
		return 1
	}
	return 0
}

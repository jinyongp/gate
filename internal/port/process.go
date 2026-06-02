package port

import (
	"os/exec"
	"strconv"
	"strings"
)

// ProcessOwner is the process currently listening on a local TCP port.
type ProcessOwner struct {
	PID     int
	Command string
}

// ListenerOwner reports the first process lsof can identify for a listening TCP
// port. The lookup is best-effort because lsof may be unavailable or restricted.
func ListenerOwner(p int) (ProcessOwner, bool) {
	cmd := exec.Command("lsof", "-nP", "-iTCP:"+strconv.Itoa(p), "-sTCP:LISTEN", "-Fpct") //nolint:gosec // fixed lsof command; p is an int.
	out, err := cmd.Output()
	if err != nil {
		return ProcessOwner{}, false
	}
	return parseLsofOwner(string(out))
}

func parseLsofOwner(output string) (ProcessOwner, bool) {
	var owner ProcessOwner
	for _, line := range strings.Split(output, "\n") {
		if line == "" {
			continue
		}
		switch line[0] {
		case 'p':
			if owner.PID != 0 || owner.Command != "" {
				return owner, true
			}
			pid, err := strconv.Atoi(line[1:])
			if err == nil {
				owner.PID = pid
			}
		case 'c':
			owner.Command = line[1:]
		}
	}
	if owner.PID == 0 && owner.Command == "" {
		return ProcessOwner{}, false
	}
	return owner, true
}

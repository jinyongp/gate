package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	gate "gate"
	"gate/internal/fsutil"
	"gate/internal/paths"
)

// Skill dispatches `gate skill path|print`. Installation itself is delegated to
// skills.sh (`npx skills add jinyongp/gate`) or apm; this only locates or prints
// the bundled SKILL.md for manual use and debugging.
func Skill(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("skill", flag.ContinueOnError)
	if handled, code := parseFlags(fs, "skill", args, stdout, stderr); handled {
		return code
	}
	if fs.NArg() > 1 {
		return usageFail(stderr, false, "skill")
	}
	sub := "path"
	if fs.NArg() > 0 {
		sub = fs.Arg(0)
	}
	switch sub {
	case "path":
		dest := filepath.Join(paths.ConfigDir(), "skills", "gate", "SKILL.md")
		if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
			return fail(stderr, false, ExitError, "skill", err.Error())
		}
		if err := fsutil.WriteAtomic(dest, gate.SkillMD, 0o644); err != nil {
			return fail(stderr, false, ExitError, "skill", err.Error())
		}
		fmt.Fprintln(stdout, dest)
		return ExitOK
	case "print":
		_, _ = stdout.Write(gate.SkillMD)
		return ExitOK
	default:
		return usageFail(stderr, false, "skill")
	}
}

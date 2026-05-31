package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	prx "prx"
	"prx/internal/fsutil"
	"prx/internal/paths"
)

// Skill dispatches `prx skill path|print`. Installation itself is delegated to
// skills.sh (`npx skills add jinyongp/prx`) or apm; this only locates or prints
// the bundled SKILL.md for manual use and debugging.
func Skill(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("skill", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	sub := "path"
	if fs.NArg() > 0 {
		sub = fs.Arg(0)
	}
	switch sub {
	case "path":
		dest := filepath.Join(paths.ConfigDir(), "skills", "prx", "SKILL.md")
		if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
			return fail(stderr, false, ExitError, "skill", err.Error())
		}
		if err := fsutil.WriteAtomic(dest, prx.SkillMD, 0o644); err != nil {
			return fail(stderr, false, ExitError, "skill", err.Error())
		}
		fmt.Fprintln(stdout, dest)
		return ExitOK
	case "print":
		_, _ = stdout.Write(prx.SkillMD)
		return ExitOK
	default:
		return fail(stderr, false, ExitUsage, "usage", "usage: prx skill path|print")
	}
}

package release

import (
	"fmt"
	"strings"
)

type Options struct {
	DryRun   bool
	AutoPush bool
	TagInput string
	Since    string
	SinceSet bool
}

func ParseOptions(args []string) (Options, error) {
	var options Options
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg == "" {
			continue
		}
		switch {
		case arg == "--dry-run" || arg == "-n":
			options.DryRun = true
		case arg == "--yes" || arg == "-y":
			options.AutoPush = true
		case strings.HasPrefix(arg, "tag="):
			options.TagInput = strings.TrimPrefix(arg, "tag=")
		case strings.HasPrefix(arg, "--since="):
			options.Since = strings.TrimPrefix(arg, "--since=")
			options.SinceSet = true
		case arg == "--since":
			remaining := args[index+1:]
			if len(remaining) == 0 {
				return Options{}, fmt.Errorf("--since requires a tag")
			}
			options.Since = remaining[0]
			options.SinceSet = true
			index++
		case isBump(arg):
			options.TagInput = arg
		case strings.HasPrefix(arg, "v"):
			if _, err := ParseVersion(arg); err != nil {
				return Options{}, fmt.Errorf("version tag must be strict vX.Y.Z: %s", arg)
			}
			options.TagInput = arg
		default:
			return Options{}, fmt.Errorf("unknown argument: %s", arg)
		}
	}
	return options, nil
}

func isBump(value string) bool {
	switch value {
	case "patch", "minor", "major":
		return true
	default:
		return false
	}
}

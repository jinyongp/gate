package release

import (
	"fmt"
	"regexp"
	"strconv"
)

var strictVersionPattern = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

type Version struct {
	Major int
	Minor int
	Patch int
}

func ParseVersion(tag string) (Version, error) {
	match := strictVersionPattern.FindStringSubmatch(tag)
	if match == nil {
		return Version{}, fmt.Errorf("invalid strict semver tag %q", tag)
	}
	parts := [3]int{}
	for index := range parts {
		value, err := strconv.Atoi(match[index+1])
		if err != nil {
			return Version{}, fmt.Errorf("parse version %q: %w", tag, err)
		}
		parts[index] = value
	}
	return Version{Major: parts[0], Minor: parts[1], Patch: parts[2]}, nil
}

func (version Version) String() string {
	return fmt.Sprintf("v%d.%d.%d", version.Major, version.Minor, version.Patch)
}

func (version Version) Next(bump string) (Version, error) {
	switch bump {
	case "major":
		return Version{Major: version.Major + 1}, nil
	case "minor":
		return Version{Major: version.Major, Minor: version.Minor + 1}, nil
	case "patch":
		return Version{Major: version.Major, Minor: version.Minor, Patch: version.Patch + 1}, nil
	default:
		return Version{}, fmt.Errorf("unknown bump type: %s", bump)
	}
}

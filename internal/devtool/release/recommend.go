package release

import (
	"regexp"
	"strings"
)

var (
	breakingSubjectPattern = regexp.MustCompile(`^[A-Za-z]+(\([^)]*\))?!:`)
	featureSubjectPattern  = regexp.MustCompile(`^feat(\([^)]*\))?:`)
	breakingBodyPattern    = regexp.MustCompile(`(^|[[:space:]])BREAKING CHANGE:`)
)

func recommendBump(subjects []string, messages string) string {
	for _, subject := range subjects {
		if breakingSubjectPattern.MatchString(subject) {
			return "major"
		}
	}
	for _, line := range strings.Split(messages, "\n") {
		if breakingSubjectPattern.MatchString(line) {
			return "major"
		}
	}
	if breakingBodyPattern.MatchString(messages) {
		return "major"
	}
	for _, subject := range subjects {
		if featureSubjectPattern.MatchString(subject) {
			return "minor"
		}
	}
	return "patch"
}

func recommendationReason(bump string, commitLines []string, messages string) string {
	switch bump {
	case "major":
		for _, line := range commitLines {
			fields := strings.SplitN(line, " ", 2)
			if len(fields) == 2 && breakingSubjectPattern.MatchString(fields[1]) {
				return line
			}
		}
		for _, line := range strings.Split(messages, "\n") {
			if strings.Contains(line, "BREAKING CHANGE:") {
				return line
			}
		}
	case "minor":
		for _, line := range commitLines {
			fields := strings.SplitN(line, " ", 2)
			if len(fields) == 2 && featureSubjectPattern.MatchString(fields[1]) {
				return line
			}
		}
	default:
		if len(commitLines) > 0 {
			return commitLines[0]
		}
	}
	return ""
}

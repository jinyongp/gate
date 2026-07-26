package cirelease

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	localrelease "gate/internal/devtool/release"
	"gate/internal/devtool/runner"
)

func (service *Service) detectReleaseTag(ctx context.Context) error {
	tag := ""
	tagType := "none"
	target := ""
	releaseState := "not-applicable"
	onMain := "false"

	if service.Getenv("GITHUB_REF_TYPE") == "tag" {
		tag = service.Getenv("GITHUB_REF_NAME")
	} else {
		tags, err := service.output(ctx, "git", "tag", "--points-at", "HEAD", "--list", "v*", "--sort=-v:refname")
		if err != nil {
			return err
		}
		for _, candidate := range lines(tags) {
			if _, err := localrelease.ParseVersion(candidate); err == nil {
				tag = candidate
				break
			}
		}
	}
	if _, err := localrelease.ParseVersion(tag); err != nil {
		tag = ""
	}

	if tag != "" {
		annotated := service.gitCommandSucceeds(ctx, "cat-file", "-e", tag+"^{tag}")
		if annotated {
			tagType = "annotated"
			var err error
			target, err = service.output(ctx, "git", "rev-parse", tag+"^{commit}")
			if err != nil {
				return err
			}
		} else {
			tagType = "lightweight"
			var err error
			target, err = service.output(ctx, "git", "rev-parse", tag)
			if err != nil {
				return err
			}
		}
		target = strings.TrimSpace(target)
		if !commitSHAPattern.MatchString(target) {
			return fmt.Errorf("release tag resolved to invalid commit SHA %q", target)
		}

		if service.Getenv("GITHUB_REF_TYPE") == "tag" {
			head, err := service.output(ctx, "git", "rev-parse", "HEAD")
			if err != nil {
				return err
			}
			head = strings.TrimSpace(head)
			if target != head {
				return fmt.Errorf("release tag target does not match checked-out HEAD: tag=%s head=%s", target, head)
			}
		}

		allTags, err := service.output(ctx, "git", "tag", "--list", "v*")
		if err != nil {
			return err
		}
		stable := strictVersions(lines(allTags))
		sort.Slice(stable, func(i, j int) bool {
			return compareVersions(stable[i].Version, stable[j].Version) < 0
		})
		if len(stable) > 0 && stable[len(stable)-1].Tag != tag {
			return fmt.Errorf(
				"refusing to publish older tag %s; highest stable tag is %s",
				tag,
				stable[len(stable)-1].Tag,
			)
		}

		if err := service.stream(
			ctx,
			"git",
			"fetch",
			"--force",
			"--quiet",
			"origin",
			"refs/heads/main:refs/remotes/origin/main",
		); err != nil {
			return err
		}
		if service.gitCommandSucceeds(ctx, "merge-base", "--is-ancestor", target, "refs/remotes/origin/main") {
			onMain = "true"
		}

		state, err := service.githubReleaseState(ctx, tag)
		if err != nil {
			if isGitHubNotFound(err) {
				releaseState = "missing"
			} else {
				releaseState = "unknown"
			}
		} else {
			if state.Draft || state.Prerelease {
				return fmt.Errorf(
					"stable release tag has non-final GitHub release state (draft=%t, prerelease=%t): %s",
					state.Draft,
					state.Prerelease,
					tag,
				)
			}
			releaseState = "existing"
		}
	}

	return service.appendGitHubOutput(
		outputValue{Name: "tag", Value: tag},
		outputValue{Name: "type", Value: tagType},
		outputValue{Name: "target", Value: target},
		outputValue{Name: "release", Value: releaseState},
		outputValue{Name: "on_main", Value: onMain},
	)
}

func (service *Service) verifyReleaseTagTarget(ctx context.Context, tag, expected string) error {
	if _, err := localrelease.ParseVersion(tag); err != nil || !commitSHAPattern.MatchString(strings.ToLower(expected)) {
		return usage("invalid release tag or expected SHA")
	}
	target, err := service.output(ctx, "git", "ls-remote", "origin", "refs/tags/"+tag+"^{}")
	if err != nil {
		return err
	}
	target = firstField(target)
	if target == "" {
		target, err = service.output(ctx, "git", "ls-remote", "origin", "refs/tags/"+tag)
		if err != nil {
			return err
		}
		target = firstField(target)
	}
	if target == "" {
		return fmt.Errorf("release tag is missing from origin: %s", tag)
	}
	if target != strings.ToLower(expected) {
		return fmt.Errorf("release tag target moved: tag=%s expected=%s", target, expected)
	}
	return nil
}

func (service *Service) gitCommandSucceeds(ctx context.Context, args ...string) bool {
	err := service.Runner.Run(ctx, runner.Command{Name: "git", Args: args, Dir: service.Dir})
	return err == nil
}

type taggedVersion struct {
	Tag     string
	Version localrelease.Version
}

func strictVersions(tags []string) []taggedVersion {
	var versions []taggedVersion
	for _, tag := range tags {
		version, err := localrelease.ParseVersion(tag)
		if err == nil {
			versions = append(versions, taggedVersion{Tag: tag, Version: version})
		}
	}
	return versions
}

func compareVersions(a, b localrelease.Version) int {
	switch {
	case a.Major != b.Major:
		return a.Major - b.Major
	case a.Minor != b.Minor:
		return a.Minor - b.Minor
	default:
		return a.Patch - b.Patch
	}
}

func lines(value string) []string {
	var out []string
	for line := range strings.Lines(value) {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

func firstField(value string) string {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func isGitHubNotFound(err error) bool {
	var commandErr *commandError
	if !errors.As(err, &commandErr) {
		return false
	}
	message := strings.ToLower(commandErr.stderr)
	return strings.Contains(message, "not found") ||
		strings.Contains(message, "could not resolve to a release") ||
		notFoundPattern.MatchString(message)
}

package release

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"gate/internal/devtool/runner"
	"gate/internal/ui"
)

const Usage = "usage: gate-dev release [--dry-run|-n] [--yes|-y] [--since vX.Y.Z] [patch|minor|major|vX.Y.Z]"

var errStopped = errors.New("release stopped")

type Service struct {
	In                     io.Reader
	Out                    io.Writer
	Err                    io.Writer
	Dir                    string
	Runner                 runner.Runner
	HTTPClient             *http.Client
	APIBase                string
	Getenv                 func(string) string
	Check                  func(context.Context) error
	PrepareReleaseDispatch func(context.Context) (string, error)
	DispatchRelease        func(context.Context, string, string, string, string) error
	latestTagFn            func(context.Context, string, string) (string, error)

	reader  *bufio.Reader
	console ui.Console
}

func New(in io.Reader, out, errOut io.Writer, commandRunner runner.Runner) *Service {
	service := &Service{
		In:         in,
		Out:        out,
		Err:        errOut,
		Dir:        ".",
		Runner:     commandRunner,
		HTTPClient: http.DefaultClient,
		APIBase:    "https://api.github.com",
		Getenv:     os.Getenv,
	}
	service.Check = func(ctx context.Context) error {
		return service.Runner.Run(ctx, runner.Command{
			Name:   "just",
			Args:   []string{"check"},
			Dir:    service.Dir,
			Stdin:  service.In,
			Stdout: service.Out,
			Stderr: service.Err,
		})
	}
	service.latestTagFn = func(ctx context.Context, repo, token string) (string, error) {
		return latestReleaseTag(ctx, service.HTTPClient, service.APIBase, repo, token)
	}
	service.PrepareReleaseDispatch = service.prepareReleaseDispatch
	service.DispatchRelease = service.dispatchRelease
	return service
}

func (service *Service) Run(ctx context.Context, args []string) int {
	service.reader = bufio.NewReader(service.In)
	service.console = ui.NewConsole(service.Out, service.Err)
	options, err := ParseOptions(args)
	if err != nil {
		service.console.Error(err.Error())
		fmt.Fprintln(service.Err, Usage)
		return 1
	}
	err = service.execute(ctx, options)
	switch {
	case err == nil, errors.Is(err, errStopped):
		return 0
	case errors.Is(err, errStoppedWithFailure):
		return 1
	case errors.Is(err, context.Canceled), errors.Is(err, ui.ErrPromptInterrupted):
		service.console.Info("Aborted.")
		return 130
	default:
		service.console.Error(err.Error())
		return 1
	}
}

func (service *Service) execute(ctx context.Context, options Options) error {
	dirty, err := service.gitOutput(ctx, "status", "--porcelain")
	if err != nil {
		return fmt.Errorf("read working tree status: %w", err)
	}
	if dirty != "" {
		if !options.DryRun {
			service.console.Error("Release requires a clean working tree so checks match the tagged commit.")
			service.printLines(dirty)
			return errStoppedWithFailure
		}
		if err := service.confirmDirty(ctx, options, dirty); err != nil {
			return err
		}
	}

	if err := service.syncTags(ctx); err != nil {
		if ctx.Err() != nil || errors.Is(err, context.Canceled) {
			return context.Canceled
		}
		return errors.New("failed to fetch tags from origin; aborting to avoid releasing from stale local tags")
	}
	publishedTag, err := service.publishedBase(ctx, options)
	if err != nil {
		return err
	}
	notesRange := commitRange(publishedTag)

	latestTag, err := service.latestStableTag(ctx)
	if err != nil {
		return err
	}
	baseTag := latestTag
	if baseTag == "" {
		baseTag = "v0.0.0"
	}
	releaseRange := commitRange(latestTag)
	commits, err := service.commitLines(ctx, releaseRange)
	if err != nil {
		return err
	}

	tagInput := options.TagInput
	if tagInput == "" {
		service.printReleaseBase(latestTag)
		service.console.Section("Version commits since " + baseTag)
		service.printItems(commits)
		if len(commits) == 0 {
			service.console.Info("No commits to release.")
			return errStopped
		}
		tagInput, err = service.selectBump(ctx, baseTag, releaseRange, commits)
		if err != nil {
			return err
		}
	} else {
		service.console.Section("Release base")
		service.console.KV("Last tag", baseTag)
		service.console.Section("Version commits since " + baseTag)
		service.printItems(commits)
	}

	service.printNotesBase(publishedTag, baseTag)
	notesCommits, err := service.commitLines(ctx, notesRange)
	if err != nil {
		return err
	}
	service.console.Section("Release notes commits")
	service.printItems(notesCommits)

	resolvedTag, err := resolveTag(baseTag, tagInput)
	if err != nil {
		return err
	}
	if tagInput != resolvedTag {
		service.console.Section("Resolved version")
		service.console.KV("Tag", resolvedTag+" (from "+tagInput+" bump)")
	}

	branch, err := service.gitOutput(ctx, "symbolic-ref", "--short", "HEAD")
	if err != nil || branch != "main" {
		return errors.New("release must run on branch 'main'")
	}
	targetSHA, err := service.gitOutput(ctx, "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("resolve release target: %w", err)
	}
	if err := service.validateReleaseCommitSigning(ctx, targetSHA); err != nil {
		return err
	}
	if service.gitSucceeds(ctx, "rev-parse", "-q", "--verify", "refs/tags/"+resolvedTag) {
		return fmt.Errorf("tag already exists: %s", resolvedTag)
	}
	if !options.DryRun && service.gitSucceeds(ctx, "ls-remote", "--exit-code", "--tags", "origin", "refs/tags/"+resolvedTag) {
		return fmt.Errorf("remote tag already exists: %s", resolvedTag)
	}
	if options.DryRun {
		service.console.Section("Dry run")
		service.console.KV("Tag", resolvedTag)
		service.console.KV("Target", targetSHA)
		service.console.Info("No tag or push was created.")
		return nil
	}

	fmt.Fprintln(service.Out)
	dispatchAccessStatus := service.startActivityStatus("checking GitHub release access")
	repository, err := service.PrepareReleaseDispatch(ctx)
	if err != nil {
		dispatchAccessStatus.Stop()
		if ctx.Err() != nil || errors.Is(err, context.Canceled) {
			return context.Canceled
		}
		return fmt.Errorf("prepare GitHub release dispatch: %w", err)
	}
	dispatchAccessStatus.Complete()

	if err := service.Check(ctx); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("checks failed; aborting release")
	}
	confirmed, err := service.confirmPush(ctx, resolvedTag, options)
	if err != nil {
		return err
	}
	if !confirmed {
		service.console.Info("Aborted. No tag created.")
		return errStopped
	}

	notes := releaseNotes(resolvedTag, notesCommits)
	tagStatus := service.startActivityStatus("creating release tag " + resolvedTag)
	createdTagObject, err := service.createAnnotatedTag(
		ctx,
		resolvedTag,
		targetSHA,
		notes,
	)
	if err != nil {
		tagStatus.Stop()
		if ctx.Err() != nil || errors.Is(err, context.Canceled) {
			if createdTagObject != "" {
				if cleanupErr := service.cleanupTag(ctx, resolvedTag, createdTagObject); cleanupErr != nil {
					service.console.Warning("interrupted tag cleanup failed; local tag may remain: " + resolvedTag)
				}
			}
			return context.Canceled
		}
		return fmt.Errorf("create local tag %s: %w", resolvedTag, err)
	}
	tagStatus.Complete()
	if ctx.Err() != nil {
		if cleanupErr := service.cleanupTag(ctx, resolvedTag, createdTagObject); cleanupErr != nil {
			service.console.Warning("interrupted tag cleanup failed; local tag may remain: " + resolvedTag)
		}
		return ctx.Err()
	}
	pushStatus := service.startActivityStatus("pushing main and " + resolvedTag)
	if _, err := service.gitOutput(
		ctx,
		"push",
		"--atomic",
		"origin",
		targetSHA+":refs/heads/main",
		createdTagObject+":refs/tags/"+resolvedTag,
	); err != nil {
		pushStatus.Stop()
		cleanupErr := service.cleanupTag(ctx, resolvedTag, createdTagObject)
		if ctx.Err() != nil || errors.Is(err, context.Canceled) {
			if cleanupErr != nil {
				service.console.Warning("interrupted push cleanup failed; local tag may remain: " + resolvedTag)
			}
			return context.Canceled
		}
		if cleanupErr != nil {
			return fmt.Errorf("push failed and local tag cleanup failed for %s: %w", resolvedTag, cleanupErr)
		}
		return fmt.Errorf("push failed; removed the local tag created by this release attempt: %s", resolvedTag)
	}
	pushStatus.Complete()
	dispatchStatus := service.startActivityStatus("dispatching release workflow for " + resolvedTag)
	if err := service.DispatchRelease(
		ctx,
		repository,
		resolvedTag,
		targetSHA,
		createdTagObject,
	); err != nil {
		dispatchStatus.Stop()
		if ctx.Err() != nil || errors.Is(err, context.Canceled) {
			return fmt.Errorf(
				"tag %s was pushed, but release workflow dispatch was interrupted; recover with: %s",
				resolvedTag,
				releaseDispatchRecoveryCommand(
					repository,
					resolvedTag,
					targetSHA,
					createdTagObject,
				),
			)
		}
		return fmt.Errorf(
			"tag %s was pushed, but release workflow dispatch failed: %w; recover with: %s",
			resolvedTag,
			err,
			releaseDispatchRecoveryCommand(
				repository,
				resolvedTag,
				targetSHA,
				createdTagObject,
			),
		)
	}
	dispatchStatus.Complete()
	return nil
}

var errStoppedWithFailure = errors.New("release validation already reported")

func (service *Service) confirmDirty(ctx context.Context, options Options, dirty string) error {
	service.console.Section("Uncommitted changes")
	service.printLines(dirty)
	service.console.Info("This is a dry run; no tag or push will be created.")
	if options.AutoPush || service.Getenv("CI") != "" {
		return errors.New("dirty working tree requires interactive confirmation; aborting release")
	}
	confirmed, err := service.confirm(ctx, "Continue with dirty working tree?")
	if err != nil {
		return fmt.Errorf("no response; aborting release: %w", err)
	}
	if !confirmed {
		service.console.Info("Aborted. Commit or stash changes before releasing.")
		return errStopped
	}
	return nil
}

func (service *Service) syncTags(ctx context.Context) error {
	if !service.gitSucceeds(ctx, "remote", "get-url", "origin") {
		return nil
	}
	return service.gitRun(ctx, "fetch", "--tags", "--prune", "origin")
}

func (service *Service) publishedBase(ctx context.Context, options Options) (string, error) {
	if options.SinceSet {
		if _, err := ParseVersion(options.Since); err != nil {
			return "", errors.New("--since must be a semver tag like v1.2.3")
		}
		if !service.gitSucceeds(ctx, "rev-parse", "-q", "--verify", "refs/tags/"+options.Since) {
			return "", fmt.Errorf("--since tag does not exist locally: %s", options.Since)
		}
		return options.Since, nil
	}
	remote, err := service.gitOutput(ctx, "remote", "get-url", "origin")
	if err != nil {
		return "", errors.New("failed to read latest published GitHub release; use --since vX.Y.Z to set the release notes base explicitly")
	}
	repo, err := originRepoSlug(remote)
	if err != nil {
		return "", errors.New("failed to read latest published GitHub release; use --since vX.Y.Z to set the release notes base explicitly")
	}
	tag, err := service.latestTagFn(ctx, repo, service.Getenv("GITHUB_TOKEN"))
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, context.Canceled) {
			return "", context.Canceled
		}
		return "", errors.New("failed to read latest published GitHub release; use --since vX.Y.Z to set the release notes base explicitly")
	}
	if tag != "" {
		if _, err := ParseVersion(tag); err != nil {
			return "", errors.New("latest published GitHub release is not a strict semver tag; use --since vX.Y.Z to set the release notes base explicitly")
		}
	}
	if tag != "" && !service.gitSucceeds(ctx, "rev-parse", "-q", "--verify", "refs/tags/"+tag) {
		service.console.Warning("latest published release tag " + tag + " is not present locally; release notes will include all commits")
		return "", nil
	}
	return tag, nil
}

func (service *Service) latestStableTag(ctx context.Context) (string, error) {
	output, err := service.gitOutput(ctx, "tag", "--list", "v*", "--sort=-v:refname")
	if err != nil {
		return "", fmt.Errorf("list release tags: %w", err)
	}
	for _, candidate := range splitLines(output) {
		if _, err := ParseVersion(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", nil
}

func (service *Service) selectBump(
	ctx context.Context,
	baseTag string,
	releaseRange string,
	commits []string,
) (string, error) {
	version, err := ParseVersion(baseTag)
	if err != nil {
		return "", err
	}
	candidates := map[string]string{}
	for _, bump := range []string{"patch", "minor", "major"} {
		next, nextErr := version.Next(bump)
		if nextErr != nil {
			return "", nextErr
		}
		candidates[bump] = next.String()
	}
	subjectOutput, err := service.gitLog(ctx, releaseRange, "--format=%s")
	if err != nil {
		return "", fmt.Errorf("read commit subjects: %w", err)
	}
	messageOutput, err := service.gitLog(ctx, releaseRange, "--format=%s%n%b")
	if err != nil {
		return "", fmt.Errorf("read commit messages: %w", err)
	}
	recommended := recommendBump(splitLines(subjectOutput), messageOutput)
	reason := recommendationReason(recommended, commits, messageOutput)
	service.console.Section("Version bump")
	service.console.KV("Commits", fmt.Sprintf("%d", len(commits)))
	service.console.KV("Recommended", recommended)
	if reason != "" {
		service.console.KV("Reason", reason)
	}
	choices := make([]ui.Choice, 0, 3)
	for index, bump := range []string{"patch", "minor", "major"} {
		label := fmt.Sprintf("%-8s %s", bump, candidates[bump])
		if bump == recommended {
			label += "  recommended"
		}
		aliases := []string{fmt.Sprintf("%d", index+1)}
		switch bump {
		case "patch":
			aliases = append(aliases, "p")
		case "minor":
			aliases = append(aliases, "m")
		case "major":
			aliases = append(aliases, "M")
		}
		choices = append(choices, ui.Choice{
			Value:         bump,
			Label:         label,
			Aliases:       aliases,
			CaseSensitive: true,
		})
	}
	if input, ok := service.In.(*os.File); ok {
		return ui.PromptChoicesFileContext(
			ctx,
			input,
			service.reader,
			service.Out,
			"Select bump",
			recommended,
			choices,
		)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	//nolint:contextcheck // Non-file test readers cannot be interrupted safely; cancellation is checked on both sides.
	choice, err := ui.PromptChoices(
		service.reader,
		service.Out,
		"Select bump",
		recommended,
		choices,
	)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return "", ctxErr
	}
	return choice, err
}

func (service *Service) printReleaseBase(latestTag string) {
	service.console.Section("Release base")
	if latestTag == "" {
		service.console.Info("No previous release tag found. First semver tag starts from v0.0.0.")
		return
	}
	service.console.KV("Last tag", latestTag)
}

func (service *Service) printNotesBase(publishedTag, latestTag string) {
	service.console.Section("Release notes base")
	if publishedTag == "" {
		service.console.KV("Last published release", "none")
		return
	}
	service.console.KV("Last published release", publishedTag)
	if publishedTag != latestTag {
		service.console.KV("Latest tag", latestTag+" (unpublished; included below)")
	}
}

func (service *Service) commitLines(ctx context.Context, commitRange string) ([]string, error) {
	output, err := service.gitLog(ctx, commitRange, "--oneline", "--no-decorate")
	if err != nil {
		return nil, fmt.Errorf("read release commits: %w", err)
	}
	return splitLines(output), nil
}

func (service *Service) gitLog(ctx context.Context, commitRange string, args ...string) (string, error) {
	commandArgs := []string{"log"}
	commandArgs = append(commandArgs, args...)
	if commitRange != "" {
		commandArgs = append(commandArgs, commitRange)
	}
	return service.gitOutput(ctx, commandArgs...)
}

func (service *Service) confirmPush(ctx context.Context, tag string, options Options) (bool, error) {
	if options.AutoPush {
		return true, nil
	}
	if service.Getenv("CI") != "" {
		return false, nil
	}
	return service.confirm(ctx, "Push branch main and tag "+tag+" now?")
}

func (service *Service) confirm(ctx context.Context, label string) (bool, error) {
	if input, ok := service.In.(*os.File); ok {
		return ui.PromptConfirmFileContext(ctx, input, service.reader, service.Out, label)
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return false, ctxErr
	}
	confirmed, err := ui.PromptConfirmContext(ctx, service.reader, service.Out, label)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return false, ctxErr
	}
	return confirmed, err
}

func (service *Service) gitOutput(ctx context.Context, args ...string) (string, error) {
	return service.gitOutputInput(ctx, nil, args...)
}

func (service *Service) gitOutputInput(
	ctx context.Context,
	input io.Reader,
	args ...string,
) (string, error) {
	var stdout, stderr bytes.Buffer
	err := service.Runner.Run(ctx, runner.Command{
		Name:   "git",
		Args:   args,
		Dir:    service.Dir,
		Stdin:  input,
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			return "", err
		}
		return "", fmt.Errorf("%w: %s", err, detail)
	}
	return strings.TrimRight(stdout.String(), "\r\n"), nil
}

func (service *Service) createAnnotatedTag(
	ctx context.Context,
	tag, targetSHA, notes string,
) (string, error) {
	sign, err := service.annotatedTagSigningRequired(ctx)
	if err != nil {
		return "", err
	}
	if sign {
		return service.createSignedAnnotatedTag(ctx, tag, targetSHA, notes)
	}
	return service.createUnsignedAnnotatedTag(ctx, tag, targetSHA, notes)
}

func (service *Service) annotatedTagSigningRequired(ctx context.Context) (bool, error) {
	for _, policy := range []string{"tag.gpgSign", "tag.forceSignAnnotated"} {
		signTags, err := service.gitBool(ctx, policy)
		if err != nil {
			return false, fmt.Errorf("read annotated-tag signing policy %s: %w", policy, err)
		}
		if signTags {
			return true, nil
		}
	}
	return false, nil
}

func (service *Service) validateReleaseCommitSigning(ctx context.Context, targetSHA string) error {
	signCommits, err := service.gitBool(ctx, "commit.gpgSign")
	if err != nil {
		return fmt.Errorf("read commit signing policy: %w", err)
	}
	if !signCommits {
		return nil
	}
	commit, err := service.gitOutput(ctx, "cat-file", "commit", targetSHA)
	if err != nil {
		return fmt.Errorf("read release target commit signature: %w", err)
	}
	headers, _, _ := strings.Cut(commit, "\n\n")
	for _, line := range strings.Split(headers, "\n") {
		if strings.HasPrefix(line, "gpgsig ") ||
			strings.HasPrefix(line, "gpgsig-sha256 ") {
			return nil
		}
	}
	return fmt.Errorf(
		"release target %s is unsigned while commit.gpgSign=true",
		targetSHA,
	)
}

func (service *Service) gitBool(ctx context.Context, key string) (bool, error) {
	value, err := service.gitOutput(ctx, "config", "--type=bool", "--default=false", key)
	if err != nil {
		return false, err
	}
	return value == "true", nil
}

func (service *Service) createSignedAnnotatedTag(
	ctx context.Context,
	tag, targetSHA, notes string,
) (string, error) {
	_, err := service.gitOutputInput(
		ctx,
		strings.NewReader(notes),
		"tag",
		"--sign",
		"--cleanup=verbatim",
		"--file=-",
		tag,
		targetSHA,
	)
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, context.Canceled) {
			return service.resolveTagObjectForCleanup(ctx, tag), err
		}
		return "", fmt.Errorf("sign annotated tag: %w", err)
	}
	object, err := service.gitOutput(ctx, "rev-parse", "--verify", "refs/tags/"+tag+"^{tag}")
	if err != nil {
		return service.resolveTagObjectForCleanup(ctx, tag), fmt.Errorf("resolve signed tag object: %w", err)
	}
	if !validGitObjectID(object) {
		return object, fmt.Errorf("git rev-parse returned invalid signed tag object ID %q", object)
	}
	return object, nil
}

func (service *Service) createUnsignedAnnotatedTag(
	ctx context.Context,
	tag, targetSHA, notes string,
) (string, error) {
	identity, err := service.gitOutput(ctx, "var", "GIT_COMMITTER_IDENT")
	if err != nil {
		return "", fmt.Errorf("read tagger identity: %w", err)
	}
	var payload strings.Builder
	fmt.Fprintf(&payload, "object %s\ntype commit\ntag %s\ntagger %s\n\n%s\n", targetSHA, tag, identity, notes)
	object, err := service.gitOutputInput(ctx, strings.NewReader(payload.String()), "mktag")
	if err != nil {
		return "", fmt.Errorf("create annotated tag object: %w", err)
	}
	if !validGitObjectID(object) {
		return "", fmt.Errorf("git mktag returned invalid object ID %q", object)
	}
	err = service.gitRun(
		ctx,
		"update-ref",
		"refs/tags/"+tag,
		object,
		strings.Repeat("0", len(object)),
	)
	if err != nil {
		return object, fmt.Errorf("install annotated tag ref: %w", err)
	}
	return object, nil
}

func validGitObjectID(object string) bool {
	return object != "" && strings.Trim(object, "0123456789abcdef") == ""
}

func (service *Service) resolveTagObjectForCleanup(ctx context.Context, tag string) string {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	object, err := service.gitOutput(
		cleanupCtx,
		"rev-parse",
		"--verify",
		"refs/tags/"+tag+"^{tag}",
	)
	if err != nil || !validGitObjectID(object) {
		return ""
	}
	return object
}

func (service *Service) gitRun(ctx context.Context, args ...string) error {
	return service.Runner.Run(ctx, runner.Command{
		Name:   "git",
		Args:   args,
		Dir:    service.Dir,
		Stdout: service.Out,
		Stderr: service.Err,
	})
}

func (service *Service) gitQuiet(ctx context.Context, args ...string) error {
	return service.Runner.Run(ctx, runner.Command{
		Name: "git",
		Args: args,
		Dir:  service.Dir,
	})
}

func (service *Service) gitSucceeds(ctx context.Context, args ...string) bool {
	return service.gitQuiet(ctx, args...) == nil
}

func (service *Service) cleanupTag(ctx context.Context, tag, expectedObject string) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return service.gitQuiet(
		cleanupCtx,
		"update-ref",
		"-d",
		"refs/tags/"+tag,
		expectedObject,
	)
}

func (service *Service) printItems(lines []string) {
	for _, line := range lines {
		service.console.Item(line)
	}
}

func (service *Service) printLines(value string) {
	service.printItems(splitLines(value))
}

func (service *Service) startActivityStatus(label string) *ui.ActivityStatus {
	return ui.StartActivityStatus(service.Out, label, ui.ActivityOptions{
		Enabled: ui.ActivityEnabled(service.Out, false),
	})
}

func splitLines(value string) []string {
	value = strings.TrimRight(value, "\r\n")
	if value == "" {
		return nil
	}
	raw := strings.Split(value, "\n")
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func commitRange(base string) string {
	if base == "" {
		return ""
	}
	return base + "..HEAD"
}

func resolveTag(baseTag, input string) (string, error) {
	if isBump(input) {
		version, err := ParseVersion(baseTag)
		if err != nil {
			return "", err
		}
		next, err := version.Next(input)
		if err != nil {
			return "", err
		}
		return next.String(), nil
	}
	if _, err := ParseVersion(input); err != nil {
		return "", errors.New("tag must be vX.Y.Z or one of: patch, minor, major")
	}
	return input, nil
}

func releaseNotes(tag string, commits []string) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "Release %s\n\n", tag)
	for index, commit := range commits {
		if index > 0 {
			builder.WriteByte('\n')
		}
		builder.WriteString("- ")
		builder.WriteString(commit)
	}
	return builder.String()
}

package cirelease

import (
	"context"
	"fmt"
	"strings"
)

func (service *Service) waitForCI(ctx context.Context, sha, requestID string) error {
	if !commitSHAPattern.MatchString(sha) {
		return usage("usage: gate-dev ci wait-for-ci <40-character commit SHA> [request-id]")
	}
	if requestID != "" && !ciRequestIDPattern.MatchString(requestID) {
		return usage("CI request ID must contain only letters, digits, dot, underscore, and hyphen")
	}
	repository := service.Getenv("GH_REPO")
	if repository == "" {
		repository = service.Getenv("GITHUB_REPOSITORY")
	}
	if !repoSlugPattern.MatchString(repository) {
		return usage("GH_REPO or GITHUB_REPOSITORY must be an owner/repository slug")
	}
	workflow := service.Getenv("CI_WORKFLOW_FILE")
	if workflow == "" {
		workflow = "ci.yml"
	}
	if !workflowPattern.MatchString(workflow) {
		return usage("CI_WORKFLOW_FILE must be a workflow filename")
	}
	timeout, err := positiveSeconds(
		service.Getenv("CI_WAIT_TIMEOUT_SECONDS"),
		"1800",
		"CI wait timeout",
	)
	if err != nil {
		return err
	}
	poll, err := positiveSeconds(
		service.Getenv("CI_WAIT_POLL_SECONDS"),
		"10",
		"CI wait poll interval",
	)
	if err != nil {
		return err
	}

	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	timedOut := func() error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("timed out waiting for successful CI for %s", sha)
	}
	deadline := service.Now().Add(timeout)
	lastReported := ""
	selector := fmt.Sprintf(
		`.workflow_runs | map(select(.event == "push" and .head_sha == "%s")) | sort_by(.created_at) | last | select(. != null) | [.id, .status, (.conclusion // ""), .html_url] | @tsv`,
		sha,
	)
	if requestID != "" {
		selector = fmt.Sprintf(
			`.workflow_runs | map(select(.event == "workflow_dispatch" and .display_title == "CI %s %s")) | sort_by(.created_at) | last | select(. != null) | [.id, .status, (.conclusion // ""), .html_url] | @tsv`,
			sha,
			requestID,
		)
	}
	for !service.Now().After(deadline) {
		run, queryErr := service.output(
			waitCtx,
			"gh",
			"api",
			"--method",
			"GET",
			"-H",
			"Accept: application/vnd.github+json",
			"repos/"+repository+"/actions/workflows/"+workflow+"/runs",
			"-f",
			"per_page=100",
			"--jq",
			selector,
		)
		if queryErr != nil {
			if waitCtx.Err() != nil {
				return timedOut()
			}
			return queryErr
		}
		report := "waiting for CI run for " + sha
		if strings.TrimSpace(run) != "" {
			fields := strings.Split(strings.TrimSpace(run), "\t")
			if len(fields) != 4 {
				return fmt.Errorf("invalid CI run response %q", run)
			}
			runID, status, conclusion, runURL := fields[0], fields[1], fields[2], fields[3]
			report = "CI run " + runID + ": " + status
			if conclusion != "" {
				report += "/" + conclusion
			}
			switch status {
			case "completed":
				if conclusion == "success" {
					fmt.Fprintf(service.Out, "%s (%s)\n", report, runURL)
					return nil
				}
				return fmt.Errorf("%s (%s)", report, runURL)
			case "queued", "in_progress", "pending", "requested", "waiting":
			default:
				return fmt.Errorf("unexpected CI run status %s: %s", status, runURL)
			}
		}
		if report != lastReported {
			fmt.Fprintln(service.Out, report)
			lastReported = report
		}
		if !service.Now().Before(deadline) {
			break
		}
		if err := service.Sleep(waitCtx, poll); err != nil {
			if waitCtx.Err() != nil {
				return timedOut()
			}
			return err
		}
	}
	return timedOut()
}

func (service *Service) dispatchCI(ctx context.Context, sha, requestID string) error {
	if !commitSHAPattern.MatchString(sha) {
		return usage("usage: gate-dev ci dispatch-ci <40-character commit SHA> <request-id>")
	}
	if !ciRequestIDPattern.MatchString(requestID) {
		return usage("CI request ID must contain only letters, digits, dot, underscore, and hyphen")
	}
	repository := service.Getenv("GH_REPO")
	if repository == "" {
		repository = service.Getenv("GITHUB_REPOSITORY")
	}
	if !repoSlugPattern.MatchString(repository) {
		return usage("GH_REPO or GITHUB_REPOSITORY must be an owner/repository slug")
	}
	workflow := service.Getenv("CI_WORKFLOW_FILE")
	if workflow == "" {
		workflow = "ci.yml"
	}
	if !workflowPattern.MatchString(workflow) {
		return usage("CI_WORKFLOW_FILE must be a workflow filename")
	}
	workflowRef := service.Getenv("CI_WORKFLOW_REF")
	if workflowRef == "" {
		workflowRef = "main"
	}
	if !safeGitRef(workflowRef) {
		return usage("CI_WORKFLOW_REF must be a safe branch or tag name")
	}
	if err := service.stream(
		ctx,
		"gh",
		"workflow",
		"run",
		workflow,
		"--repo",
		repository,
		"--ref",
		workflowRef,
		"-f",
		"checkout_ref="+sha,
		"-f",
		"request_id="+requestID,
	); err != nil {
		return fmt.Errorf("dispatch exact-SHA CI: %w", err)
	}
	fmt.Fprintln(service.Out, "dispatched CI for "+sha)
	return nil
}

func safeGitRef(value string) bool {
	if value == "" ||
		strings.HasPrefix(value, "-") ||
		strings.Contains(value, "..") ||
		strings.ContainsAny(value, " \t\r\n~^:?*[\\") {
		return false
	}
	return true
}

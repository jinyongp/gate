package cirelease

import (
	"context"
	"fmt"
	"strings"
)

func (service *Service) waitForCI(ctx context.Context, sha string) error {
	if !commitSHAPattern.MatchString(sha) {
		return usage("usage: gate-dev ci wait-for-ci <40-character commit SHA>")
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

	deadline := service.Now().Add(timeout)
	lastReported := ""
	for !service.Now().After(deadline) {
		run, queryErr := service.output(
			ctx,
			"gh",
			"api",
			"--method",
			"GET",
			"-H",
			"Accept: application/vnd.github+json",
			"repos/"+repository+"/actions/workflows/"+workflow+"/runs",
			"-f",
			"head_sha="+sha,
			"-f",
			"event=push",
			"-f",
			"per_page=100",
			"--jq",
			`.workflow_runs | sort_by(.created_at) | last | select(. != null) | [.id, .status, (.conclusion // ""), .html_url] | @tsv`,
		)
		if queryErr != nil {
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
		if err := service.Sleep(ctx, poll); err != nil {
			return err
		}
	}
	return fmt.Errorf("timed out waiting for successful CI for %s", sha)
}

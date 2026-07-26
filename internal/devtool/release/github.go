package release

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"gate/internal/devtool/runner"
)

var repoSlugPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

func originRepoSlug(remote string) (string, error) {
	var slug string
	switch {
	case strings.HasPrefix(remote, "https://github.com/"):
		slug = strings.TrimPrefix(remote, "https://github.com/")
	case strings.HasPrefix(remote, "git@github.com:"):
		slug = strings.TrimPrefix(remote, "git@github.com:")
	case strings.HasPrefix(remote, "ssh://git@github.com/"):
		slug = strings.TrimPrefix(remote, "ssh://git@github.com/")
	default:
		return "", fmt.Errorf("origin is not a supported GitHub remote")
	}
	slug = strings.TrimSuffix(slug, ".git")
	if !repoSlugPattern.MatchString(slug) {
		return "", fmt.Errorf("origin does not contain an owner/repository slug")
	}
	return slug, nil
}

func (service *Service) prepareReleaseDispatch(ctx context.Context) (string, error) {
	remote, err := service.gitOutput(ctx, "remote", "get-url", "origin")
	if err != nil {
		return "", fmt.Errorf("read origin remote: %w", err)
	}
	repo, err := originRepoSlug(remote)
	if err != nil {
		return "", err
	}
	var permission, stderr bytes.Buffer
	if err := service.Runner.Run(ctx, runner.Command{
		Name: "gh",
		Args: []string{
			"api",
			"repos/" + repo,
			"--jq",
			".permissions.push",
		},
		Dir:    service.Dir,
		Env:    []string{"GH_PROMPT_DISABLED=1"},
		Stdout: &permission,
		Stderr: &stderr,
	}); err != nil {
		if detail := strings.TrimSpace(stderr.String()); detail != "" {
			return "", fmt.Errorf("verify GitHub CLI access to %s: %w: %s", repo, err, detail)
		}
		return "", fmt.Errorf("verify GitHub CLI access to %s: %w", repo, err)
	}
	if strings.TrimSpace(permission.String()) != "true" {
		return "", fmt.Errorf("authenticated GitHub account lacks push access to %s", repo)
	}
	return repo, nil
}

func (service *Service) dispatchRelease(
	ctx context.Context,
	repo,
	tag,
	targetSHA,
	tagObject string,
) error {
	var stderr bytes.Buffer
	err := service.Runner.Run(ctx, runner.Command{
		Name: "gh",
		Args: []string{
			"api",
			"--silent",
			"--method",
			"POST",
			"repos/" + repo + "/dispatches",
			"-f",
			"event_type=release",
			"-F",
			"client_payload[tag]=" + tag,
			"-F",
			"client_payload[target_sha]=" + targetSHA,
			"-F",
			"client_payload[tag_object]=" + tagObject,
		},
		Dir:    service.Dir,
		Env:    []string{"GH_PROMPT_DISABLED=1"},
		Stderr: &stderr,
	})
	if err != nil {
		if detail := strings.TrimSpace(stderr.String()); detail != "" {
			return fmt.Errorf("%w: %s", err, detail)
		}
		return err
	}
	return nil
}

func releaseDispatchRecoveryCommand(repo, tag, targetSHA, tagObject string) string {
	return "gh api --method POST repos/" + repo +
		"/dispatches -f event_type=release" +
		" -F 'client_payload[tag]=" + tag + "'" +
		" -F 'client_payload[target_sha]=" + targetSHA + "'" +
		" -F 'client_payload[tag_object]=" + tagObject + "'"
}

func latestReleaseTag(
	ctx context.Context,
	client *http.Client,
	apiBase string,
	repo string,
	token string,
) (string, error) {
	url := strings.TrimRight(apiBase, "/") + "/repos/" + repo + "/releases/latest"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("create latest release request: %w", err)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("read latest GitHub release: %w", err)
	}
	defer func() {
		_ = response.Body.Close()
	}()
	if response.StatusCode == http.StatusNotFound {
		_, _ = io.Copy(io.Discard, response.Body)
		return "", nil
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, response.Body)
		return "", fmt.Errorf("read latest GitHub release: unexpected HTTP status %s", response.Status)
	}
	var payload struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&payload); err != nil {
		return "", fmt.Errorf("decode latest GitHub release: %w", err)
	}
	return payload.TagName, nil
}

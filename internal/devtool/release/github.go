package release

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
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

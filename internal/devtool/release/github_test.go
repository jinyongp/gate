package release

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOriginRepoSlug(t *testing.T) {
	for remote, want := range map[string]string{
		"https://github.com/acme/gate.git":   "acme/gate",
		"git@github.com:acme/gate.git":       "acme/gate",
		"ssh://git@github.com/acme/gate.git": "acme/gate",
	} {
		got, err := originRepoSlug(remote)
		if err != nil {
			t.Fatalf("originRepoSlug(%q): %v", remote, err)
		}
		if got != want {
			t.Fatalf("originRepoSlug(%q) = %q, want %q", remote, got, want)
		}
	}
	if _, err := originRepoSlug("file:///tmp/gate.git"); err == nil {
		t.Fatal("file remote unexpectedly accepted")
	}
}

func TestLatestReleaseTag(t *testing.T) {
	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		authorization = request.Header.Get("Authorization")
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"tag_name":"v2.3.4"}`))
	}))
	t.Cleanup(server.Close)

	got, err := latestReleaseTag(context.Background(), server.Client(), server.URL, "acme/gate", "secret")
	if err != nil {
		t.Fatal(err)
	}
	if got != "v2.3.4" {
		t.Fatalf("tag = %q", got)
	}
	if authorization != "Bearer secret" {
		t.Fatalf("authorization = %q", authorization)
	}
}

func TestLatestReleaseTagTreatsNotFoundAsNoRelease(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(server.Close)
	got, err := latestReleaseTag(context.Background(), server.Client(), server.URL, "acme/gate", "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("tag = %q", got)
	}
}

func TestLatestReleaseTagRejectsServerFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, "failed", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	if _, err := latestReleaseTag(context.Background(), server.Client(), server.URL, "acme/gate", ""); err == nil {
		t.Fatal("server failure unexpectedly accepted")
	}
}

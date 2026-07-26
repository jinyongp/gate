package cirelease

import (
	"context"
	"strings"
	"testing"
	"time"

	"gate/internal/devtool/runner"
)

func TestWaitForCIReportsTransitionsAndRequiresSuccess(t *testing.T) {
	responses := []string{
		"",
		"123\tin_progress\t\thttps://example.test/run/123",
		"123\tcompleted\tsuccess\thttps://example.test/run/123",
	}
	call := 0
	fake := &fakeRunner{run: func(_ context.Context, command runner.Command) error {
		if command.Name != "gh" {
			t.Fatalf("unexpected command: %s", commandLine(command))
		}
		writeCommandOutput(command, responses[call])
		call++
		return nil
	}}
	service, out, _ := newTestService(t, fake)
	now := time.Unix(100, 0)
	service.Now = func() time.Time { return now }
	service.Sleep = func(_ context.Context, delay time.Duration) error {
		now = now.Add(delay)
		return nil
	}
	service.Getenv = environment(map[string]string{
		"GH_REPO":                     "jinyongp/gate",
		"CI_WAIT_TIMEOUT_SECONDS":     "30",
		"CI_WAIT_POLL_SECONDS":        "1",
		"GATE_UNUSED_SECRET_SENTINEL": "must-not-appear",
	})

	if code := service.Run(context.Background(), []string{"wait-for-ci", testSHA}); code != 0 {
		t.Fatalf("Run = %d", code)
	}
	requireContains(
		t,
		out.String(),
		"waiting for CI run for "+testSHA,
		"CI run 123: in_progress",
		"CI run 123: completed/success (https://example.test/run/123)",
	)
	if call != 3 {
		t.Fatalf("gh calls = %d", call)
	}
	for _, line := range fake.commandLines() {
		requireContains(
			t,
			line,
			`.event == "push"`,
			`.head_sha == "`+testSHA+`"`,
		)
		if strings.Contains(line, "workflow_dispatch") {
			t.Fatalf("push CI wait accepted an unrelated dispatch run: %s", line)
		}
	}
}

func TestWaitForCIFailsOnCompletedFailure(t *testing.T) {
	fake := &fakeRunner{run: func(_ context.Context, command runner.Command) error {
		writeCommandOutput(command, "456\tcompleted\tfailure\thttps://example.test/run/456")
		return nil
	}}
	service, _, errOut := newTestService(t, fake)
	service.Getenv = environment(map[string]string{"GITHUB_REPOSITORY": "jinyongp/gate"})
	if code := service.Run(context.Background(), []string{"wait-for-ci", testSHA}); code != 1 {
		t.Fatalf("Run = %d", code)
	}
	requireContains(t, errOut.String(), "completed/failure", "run/456")
}

func TestDispatchCIStartsExactSHAValidationForReleaseRecovery(t *testing.T) {
	requestID := "release-123-2"
	dispatch := 0
	fake := &fakeRunner{run: func(_ context.Context, command runner.Command) error {
		line := commandLine(command)
		if line == "gh workflow run ci.yml --repo jinyongp/gate --ref main -f checkout_ref="+testSHA+" -f request_id="+requestID {
			dispatch++
			return nil
		}
		t.Fatalf("unexpected command: %s", line)
		return nil
	}}
	service, out, _ := newTestService(t, fake)
	service.Getenv = environment(map[string]string{
		"GH_REPO": "jinyongp/gate",
	})

	if code := service.Run(context.Background(), []string{"dispatch-ci", testSHA, requestID}); code != 0 {
		t.Fatalf("Run = %d", code)
	}
	if dispatch != 1 {
		t.Fatalf("dispatch calls = %d", dispatch)
	}
	requireContains(t, out.String(), "dispatched CI for "+testSHA)
}

func TestWaitForCISelectsOnlyTheRequestedRecoveryRun(t *testing.T) {
	requestID := "release-123-2"
	fake := &fakeRunner{run: func(_ context.Context, command runner.Command) error {
		writeCommandOutput(command, "789\tcompleted\tsuccess\thttps://example.test/run/789")
		return nil
	}}
	service, _, _ := newTestService(t, fake)
	service.Getenv = environment(map[string]string{"GH_REPO": "jinyongp/gate"})

	if code := service.Run(
		context.Background(),
		[]string{"wait-for-ci", testSHA, requestID},
	); code != 0 {
		t.Fatalf("Run = %d", code)
	}
	line := fake.commandLines()[0]
	requireContains(
		t,
		line,
		`.event == "workflow_dispatch"`,
		`.display_title == "CI `+testSHA+` `+requestID+`"`,
	)
	if strings.Contains(line, `.event == "push"`) {
		t.Fatalf("recovery wait accepted a historical push run: %s", line)
	}
}

func TestWaitForCITimesOutAndValidatesInputs(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		fake := &fakeRunner{run: func(_ context.Context, command runner.Command) error {
			writeCommandOutput(command, "")
			return nil
		}}
		service, _, errOut := newTestService(t, fake)
		now := time.Unix(100, 0)
		service.Now = func() time.Time { return now }
		service.Sleep = func(_ context.Context, delay time.Duration) error {
			now = now.Add(delay)
			return nil
		}
		service.Getenv = environment(map[string]string{
			"GH_REPO":                 "jinyongp/gate",
			"CI_WAIT_TIMEOUT_SECONDS": "2",
			"CI_WAIT_POLL_SECONDS":    "1",
		})
		if code := service.Run(context.Background(), []string{"wait-for-ci", testSHA}); code != 1 {
			t.Fatalf("Run = %d", code)
		}
		requireContains(t, errOut.String(), "timed out waiting for successful CI")
	})

	t.Run("stalled API command", func(t *testing.T) {
		fake := &fakeRunner{run: func(ctx context.Context, _ runner.Command) error {
			<-ctx.Done()
			return ctx.Err()
		}}
		service, _, errOut := newTestService(t, fake)
		service.Getenv = environment(map[string]string{
			"GH_REPO":                 "jinyongp/gate",
			"CI_WAIT_TIMEOUT_SECONDS": "1",
		})
		started := time.Now()
		if code := service.Run(context.Background(), []string{"wait-for-ci", testSHA}); code != 1 {
			t.Fatalf("Run = %d", code)
		}
		if elapsed := time.Since(started); elapsed > 3*time.Second {
			t.Fatalf("stalled API command exceeded deadline: %s", elapsed)
		}
		requireContains(t, errOut.String(), "timed out waiting for successful CI")
	})

	t.Run("invalid", func(t *testing.T) {
		service, _, errOut := newTestService(t, &fakeRunner{})
		service.Getenv = environment(map[string]string{"GH_REPO": "unsafe repo"})
		if code := service.Run(context.Background(), []string{"wait-for-ci", "short"}); code != 2 {
			t.Fatalf("Run = %d", code)
		}
		requireContains(t, errOut.String(), "40-character commit SHA")
	})
}

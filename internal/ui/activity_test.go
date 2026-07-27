package ui

import (
	"bytes"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func resetActivityHooks(t *testing.T) {
	t.Helper()
	oldTerminal := activityIsTerminal
	oldGetenv := activityGetenv
	t.Cleanup(func() {
		activityIsTerminal = oldTerminal
		activityGetenv = oldGetenv
	})
}

func TestActivityEnabledGatesOutput(t *testing.T) {
	resetActivityHooks(t)
	var buf bytes.Buffer
	if ActivityEnabled(&buf, false) {
		t.Fatal("buffer writer should disable activity")
	}

	activityIsTerminal = func(*os.File) bool { return true }
	env := map[string]string{"LANG": "en_US.UTF-8"}
	activityGetenv = func(key string) string { return env[key] }
	if !ActivityEnabled(os.Stderr, false) {
		t.Fatal("terminal stderr should enable activity")
	}
	if ActivityEnabled(os.Stderr, true) {
		t.Fatal("json output should disable activity")
	}
}

func TestStartActivityNoopWritesNothing(t *testing.T) {
	var buf bytes.Buffer
	a := StartActivity(&buf, "work", ActivityOptions{Enabled: false})
	a.Stop()
	a.Stop()
	if buf.Len() != 0 {
		t.Fatalf("disabled activity wrote %q", buf.String())
	}
}

func TestStartActivityStoppedBeforeDelayRestoresCursor(t *testing.T) {
	var buf bytes.Buffer
	a := StartActivity(&buf, "work", ActivityOptions{
		Enabled:  true,
		Delay:    50 * time.Millisecond,
		Interval: time.Millisecond,
		Renderer: ActivityASCII,
	})
	a.Stop()
	if got := buf.String(); got != "\x1b[?25l\x1b[?25h" {
		t.Fatalf("cursor lifecycle output = %q", got)
	}
}

func TestStartActivityWritesAndClearsLine(t *testing.T) {
	var buf lockedBuffer
	a := StartActivity(&buf, "work", ActivityOptions{
		Enabled:  true,
		Delay:    time.Nanosecond,
		Interval: time.Millisecond,
		Renderer: ActivityASCII,
	})
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) && !strings.Contains(buf.String(), "work") {
		time.Sleep(time.Millisecond)
	}
	a.Stop()
	a.Stop()
	got := buf.String()
	if !strings.Contains(got, "work") {
		t.Fatalf("activity did not render label: %q", got)
	}
	if !strings.Contains(got, "\r\033[K") {
		t.Fatalf("activity did not clear line: %q", got)
	}
	if !strings.HasPrefix(got, "\x1b[?25l") || !strings.HasSuffix(got, "\x1b[?25h") {
		t.Fatalf("cursor lifecycle output = %q", got)
	}
}

func TestStartActivityCompleteKeepsDoneLine(t *testing.T) {
	var buf lockedBuffer
	a := StartActivity(&buf, "work", ActivityOptions{
		Enabled:  true,
		Delay:    time.Nanosecond,
		Interval: time.Millisecond,
		Renderer: ActivityASCII,
	})
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) && !strings.Contains(buf.String(), "work") {
		time.Sleep(time.Millisecond)
	}
	a.Complete("worked")
	a.Complete("ignored")
	got := buf.String()
	if !strings.Contains(got, "\r\033[Kok worked ") ||
		!strings.HasSuffix(got, "\n\x1b[?25h") {
		t.Fatalf("activity did not leave completion line: %q", got)
	}
}

func TestActivityStatusFallsBackToStableCompletionLine(t *testing.T) {
	var buf bytes.Buffer
	status := StartActivityStatus(&buf, "checking repository", ActivityOptions{
		Enabled: false,
		Now:     activityTestClock(1200 * time.Millisecond),
	})
	status.Complete("checked repository")
	status.Complete("ignored")
	if got := buf.String(); got != "ok: checked repository 1.2s\n" {
		t.Fatalf("output = %q", got)
	}
}

func TestActivityStatusShowsFastTerminalCompletion(t *testing.T) {
	var buf bytes.Buffer
	status := StartActivityStatus(&buf, "checking repository", ActivityOptions{
		Enabled:  true,
		Delay:    time.Hour,
		Renderer: ActivityASCII,
		Now:      activityTestClock(1200 * time.Millisecond),
	})
	status.Complete("checked repository")
	if got := buf.String(); got != "\x1b[?25lok checked repository 1.2s\n\x1b[?25h" {
		t.Fatalf("output = %q", got)
	}
}

func TestActivityStatusStopSuppressesFallback(t *testing.T) {
	var buf bytes.Buffer
	status := StartActivityStatus(&buf, "checking repository", ActivityOptions{Enabled: false})
	status.Stop()
	status.Complete("checked repository")
	if buf.Len() != 0 {
		t.Fatalf("output = %q", buf.String())
	}
}

func TestActivityShowsElapsedTimeWhileRunning(t *testing.T) {
	var buf lockedBuffer
	a := StartActivity(&buf, "checking repository", ActivityOptions{
		Enabled:  true,
		Delay:    time.Nanosecond,
		Interval: time.Millisecond,
		Renderer: ActivityASCII,
		Now:      activityTestClock(1200 * time.Millisecond),
	})
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) && !strings.Contains(buf.String(), "checking repository 1.2s") {
		time.Sleep(time.Millisecond)
	}
	a.Stop()
	if got := buf.String(); !strings.Contains(got, "checking repository 1.2s") {
		t.Fatalf("activity did not show elapsed time: %q", got)
	}
}

func TestActivityClearsEachFrameBeforeRendering(t *testing.T) {
	var buf bytes.Buffer
	a := &Activity{w: &buf}
	a.render("|", "checking repository", 990*time.Millisecond)
	a.render("/", "checking repository", time.Second)
	want := "\r\033[K| checking repository 990ms\r\033[K/ checking repository 1.0s"
	if got := buf.String(); got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestFormatActivityDuration(t *testing.T) {
	tests := []struct {
		elapsed time.Duration
		want    string
	}{
		{elapsed: 0, want: "0ms"},
		{elapsed: 44 * time.Millisecond, want: "40ms"},
		{elapsed: 1250 * time.Millisecond, want: "1.3s"},
		{elapsed: 5 * time.Second, want: "5.0s"},
		{elapsed: 62*time.Second + 340*time.Millisecond, want: "1m 2.3s"},
		{elapsed: 2 * time.Minute, want: "2m 0.0s"},
		{elapsed: time.Hour + 2*time.Minute + 3400*time.Millisecond, want: "1h 2m 3.4s"},
	}
	for _, test := range tests {
		if got := formatActivityDuration(test.elapsed); got != test.want {
			t.Errorf("formatActivityDuration(%s) = %q, want %q", test.elapsed, got, test.want)
		}
	}
}

func TestActivityFrameSelection(t *testing.T) {
	resetActivityHooks(t)
	activityGetenv = func(key string) string {
		if key == "LANG" {
			return "en_US.UTF-8"
		}
		return ""
	}
	if got := activityFrames(ActivityDotStack); len(got) == 0 || got[0] != "⠁" || got[len(got)-1] != "⠀" {
		t.Fatalf("unexpected dot-stack frames: %#v", got)
	}

	activityGetenv = func(string) string { return "" }
	if got := activityFrames(ActivityDotStack); len(got) == 0 || got[0] != "|" {
		t.Fatalf("expected ascii fallback frames: %#v", got)
	}

	if got := activityFrames(ActivityASCII); len(got) != 4 || got[0] != "|" || got[3] != "\\" {
		t.Fatalf("unexpected ascii frames: %#v", got)
	}
	if got := activityFrames(ActivityNone); got != nil {
		t.Fatalf("none renderer should have no frames: %#v", got)
	}
}

func TestDotStackFramesFallFillAndRelease(t *testing.T) {
	frames := buildDotStackFrames()
	wantPrefix := []string{
		brailleCell(1),
		brailleCell(2),
		brailleCell(3),
		brailleCell(7),
		brailleCell(7, 4),
		brailleCell(7, 5),
		brailleCell(7, 6),
		brailleCell(7, 8),
	}
	if len(frames) < len(wantPrefix) {
		t.Fatalf("frames too short: %#v", frames)
	}
	for i, want := range wantPrefix {
		if frames[i] != want {
			t.Fatalf("frame %d = %q, want %q; frames=%#v", i, frames[i], want, frames[:len(wantPrefix)])
		}
	}

	full := brailleCell(1, 2, 3, 4, 5, 6, 7, 8)
	if !containsFrame(frames, full) {
		t.Fatalf("frames never reach full two-column cell: %#v", frames)
	}

	wantRelease := []string{
		brailleCell(1, 2, 3, 4, 5, 6, 8),
		brailleCell(1, 2, 3, 4, 5, 6),
	}
	if !containsSubsequence(frames, wantRelease) {
		t.Fatalf("frames do not contain bottom-up release prefix %#v; frames=%#v", wantRelease, frames)
	}

	wantFallingRelease := []string{
		brailleCell(1, 2, 3, 4, 5, 6),
		brailleCell(1, 2, 4, 5, 6, 7),
		brailleCell(1, 2, 4, 5, 6),
	}
	if !containsSubsequence(frames, wantFallingRelease) {
		t.Fatalf("frames do not contain falling release %#v; frames=%#v", wantFallingRelease, frames)
	}
	if frames[len(frames)-1] != brailleCell() {
		t.Fatalf("last frame = %q, want blank", frames[len(frames)-1])
	}
}

func activityTestClock(elapsed time.Duration) func() time.Time {
	var mu sync.Mutex
	calls := 0
	started := time.Unix(100, 0)
	return func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if calls == 1 {
			return started
		}
		return started.Add(elapsed)
	}
}

func containsFrame(frames []string, want string) bool {
	for _, frame := range frames {
		if frame == want {
			return true
		}
	}
	return false
}

func containsSubsequence(frames, want []string) bool {
	for i := 0; i+len(want) <= len(frames); i++ {
		ok := true
		for j, frame := range want {
			if frames[i+j] != frame {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

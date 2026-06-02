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
	for _, key := range []string{"NO_COLOR", "CI", "GATE_NO_INDICATOR", "GATE_NO_SPINNER"} {
		env[key] = "1"
		if ActivityEnabled(os.Stderr, false) {
			t.Fatalf("%s should disable activity", key)
		}
		delete(env, key)
	}
	env["CI"] = "false"
	if !ActivityEnabled(os.Stderr, false) {
		t.Fatal("CI=false should not disable activity")
	}
	env["CI"] = "0"
	if !ActivityEnabled(os.Stderr, false) {
		t.Fatal("CI=0 should not disable activity")
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

func TestStartActivityStoppedBeforeDelayWritesNothing(t *testing.T) {
	var buf bytes.Buffer
	a := StartActivity(&buf, "work", ActivityOptions{
		Enabled:  true,
		Delay:    50 * time.Millisecond,
		Interval: time.Millisecond,
		Renderer: ActivityASCII,
	})
	a.Stop()
	if buf.Len() != 0 {
		t.Fatalf("activity wrote before delay: %q", buf.String())
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
	if frames[len(frames)-1] != brailleCell() {
		t.Fatalf("last frame = %q, want blank", frames[len(frames)-1])
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

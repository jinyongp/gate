package ui

import (
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"
)

type ActivityRenderer string

const (
	ActivityDotStack ActivityRenderer = "dot-stack"
	ActivityASCII    ActivityRenderer = "ascii"
	ActivityNone     ActivityRenderer = "none"
)

type ActivityOptions struct {
	Enabled  bool
	Delay    time.Duration
	Interval time.Duration
	Renderer ActivityRenderer
}

type Activity struct {
	w        io.Writer
	stopc    chan struct{}
	donec    chan struct{}
	stopOnce sync.Once
	mu       sync.Mutex
	wrote    bool
	enabled  bool
}

var (
	activityIsTerminal = func(f *os.File) bool { return term.IsTerminal(int(f.Fd())) }
	activityGetenv     = os.Getenv
)

var dotStackFrames = buildDotStackFrames()

var asciiActivityFrames = []string{"|", "/", "-", "\\"}

func ActivityEnabled(w io.Writer, jsonOut bool) bool {
	if jsonOut {
		return false
	}
	if activityGetenv("NO_COLOR") != "" || activityGetenv("GATE_NO_INDICATOR") != "" || activityGetenv("GATE_NO_SPINNER") != "" {
		return false
	}
	if ci := strings.ToLower(strings.TrimSpace(activityGetenv("CI"))); ci != "" && ci != "false" && ci != "0" {
		return false
	}
	f, ok := w.(*os.File)
	return ok && activityIsTerminal(f)
}

func StartActivity(w io.Writer, label string, opts ActivityOptions) *Activity {
	a := &Activity{w: w}
	if !opts.Enabled || w == nil {
		return a
	}

	frames := activityFrames(opts.Renderer)
	if len(frames) == 0 {
		return a
	}
	delay := opts.Delay
	if delay == 0 {
		delay = 150 * time.Millisecond
	}
	interval := opts.Interval
	if interval == 0 {
		interval = 80 * time.Millisecond
	}

	a.enabled = true
	a.stopc = make(chan struct{})
	a.donec = make(chan struct{})
	go a.run(label, frames, delay, interval)
	return a
}

func (a *Activity) Stop() {
	if a == nil || !a.enabled {
		return
	}
	a.stopOnce.Do(func() {
		close(a.stopc)
		<-a.donec
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.wrote {
			_, _ = io.WriteString(a.w, "\r\033[K")
		}
	})
}

func (a *Activity) run(label string, frames []string, delay, interval time.Duration) {
	defer close(a.donec)
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-a.stopc:
		return
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	i := 0
	for {
		a.render(frames[i%len(frames)], label)
		i++
		select {
		case <-a.stopc:
			return
		case <-ticker.C:
		}
	}
}

func (a *Activity) render(frame, label string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	_, _ = io.WriteString(a.w, "\r"+frame+" "+label)
	a.wrote = true
}

func activityFrames(renderer ActivityRenderer) []string {
	switch renderer {
	case "", ActivityDotStack:
		if activityBrailleSupported() {
			return dotStackFrames
		}
		return asciiActivityFrames
	case ActivityASCII:
		return asciiActivityFrames
	case ActivityNone:
		return nil
	default:
		return nil
	}
}

func activityBrailleSupported() bool {
	for _, key := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		value := strings.ToLower(activityGetenv(key))
		if strings.Contains(value, "utf-8") || strings.Contains(value, "utf8") {
			return true
		}
	}
	return false
}

func buildDotStackFrames() []string {
	fillOrder := []int{7, 8, 3, 6, 2, 5, 1, 4}
	var frames []string
	settled := rune(0)
	for _, target := range fillOrder {
		for _, dot := range fallingPathTo(target) {
			frames = append(frames, brailleCellFromMask(settled|brailleDotMask(dot)))
		}
		settled |= brailleDotMask(target)
	}
	frames = append(frames, brailleCellFromMask(settled))

	for _, target := range fillOrder {
		withoutTarget := settled &^ brailleDotMask(target)
		for _, dot := range fallingPathFrom(target) {
			frames = append(frames, brailleCellFromMask(withoutTarget|brailleDotMask(dot)))
		}
		settled = withoutTarget
		frames = append(frames, brailleCellFromMask(settled))
	}
	return frames
}

func fallingPathTo(target int) []int {
	switch target {
	case 1, 2, 3, 7:
		return []int{1, 2, 3, 7}[:leftPathLen(target)]
	case 4, 5, 6, 8:
		return []int{4, 5, 6, 8}[:rightPathLen(target)]
	default:
		return nil
	}
}

func fallingPathFrom(target int) []int {
	path := fallingPathTo(target)
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return path
}

func leftPathLen(target int) int {
	switch target {
	case 1:
		return 1
	case 2:
		return 2
	case 3:
		return 3
	default:
		return 4
	}
}

func rightPathLen(target int) int {
	switch target {
	case 4:
		return 1
	case 5:
		return 2
	case 6:
		return 3
	default:
		return 4
	}
}

func brailleDotMask(dot int) rune {
	switch dot {
	case 1:
		return 0x01
	case 2:
		return 0x02
	case 3:
		return 0x04
	case 4:
		return 0x08
	case 5:
		return 0x10
	case 6:
		return 0x20
	case 7:
		return 0x40
	case 8:
		return 0x80
	default:
		return 0
	}
}

func brailleCell(dots ...int) string {
	mask := rune(0)
	for _, dot := range dots {
		mask |= brailleDotMask(dot)
	}
	return brailleCellFromMask(mask)
}

func brailleCellFromMask(mask rune) string {
	return string(0x2800 + mask)
}

package ui

import "io"

func hideCursor(w io.Writer) {
	_, _ = io.WriteString(w, "\x1b[?25l")
}

func showCursor(w io.Writer) {
	_, _ = io.WriteString(w, "\x1b[?25h")
}

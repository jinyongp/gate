package logx

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTextHandlerPlainOnBuffer(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, FormatText, slog.LevelInfo)
	log.Info("hello", "domain", "app.localhost", "port", 4310)
	out := buf.String()
	for _, want := range []string{"INFO", "hello", "domain=app.localhost", "port=4310"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %q", want, out)
		}
	}
	if strings.Contains(out, "\x1b[") {
		t.Fatalf("colour leaked to non-TTY: %q", out)
	}
}

func TestTextHandlerRespectsLevel(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, FormatText, slog.LevelWarn)
	log.Info("suppressed")
	log.Warn("shown")
	if strings.Contains(buf.String(), "suppressed") {
		t.Fatal("info logged below threshold")
	}
	if !strings.Contains(buf.String(), "shown") {
		t.Fatal("warn not logged")
	}
}

func TestJSONHandler(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, FormatJSON, slog.LevelInfo)
	log.Info("hi", "k", "v")
	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, buf.String())
	}
	if m["msg"] != "hi" || m["k"] != "v" {
		t.Fatalf("unexpected json: %v", m)
	}
}

func TestAccessLog(t *testing.T) {
	var buf bytes.Buffer
	backend := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = io.WriteString(w, "body")
	})
	srv := httptest.NewServer(AccessLog(backend, &buf))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/x")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	var e AccessEntry
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &e); err != nil {
		t.Fatalf("access entry not JSON: %v\n%s", err, buf.String())
	}
	if e.Method != http.MethodGet || e.Status != http.StatusTeapot || e.Path != "/api/x" || e.Bytes != 4 {
		t.Fatalf("unexpected entry: %+v", e)
	}
}

func TestRotatingFileRotates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prx.log")
	rf, err := NewRotatingFile(path, 10, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rf.Close() }()

	_, _ = rf.Write([]byte("0123456789")) // size 10, no rotate yet
	_, _ = rf.Write([]byte("X"))          // 10+1 > 10 -> rotate, then write X

	cur, _ := os.ReadFile(path)
	if string(cur) != "X" {
		t.Fatalf("current log = %q, want X", cur)
	}
	rotated, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatalf("rotated file missing: %v", err)
	}
	if string(rotated) != "0123456789" {
		t.Fatalf("rotated content = %q", rotated)
	}
}

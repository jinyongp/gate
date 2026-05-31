package logx

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

// AccessEntry is one JSONL access-log record.
type AccessEntry struct {
	Time   string `json:"ts"`
	Host   string `json:"host"`
	Method string `json:"method"`
	Path   string `json:"path"`
	Status int    `json:"status"`
	DurMs  int64  `json:"dur_ms"`
	Bytes  int    `json:"bytes"`
	Proto  string `json:"proto"`
}

// AccessLog wraps next, writing one JSONL line per request to w. It is opt-in
// (prx enables it only with --access-log) to avoid dev-time noise.
func AccessLog(next http.Handler, w io.Writer) http.Handler {
	var mu sync.Mutex
	enc := json.NewEncoder(w)
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &recorder{ResponseWriter: rw, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		entry := AccessEntry{
			Time:   start.UTC().Format(time.RFC3339),
			Host:   hostOnly(r.Host),
			Method: r.Method,
			Path:   r.URL.Path,
			Status: rec.status,
			DurMs:  time.Since(start).Milliseconds(),
			Bytes:  rec.bytes,
			Proto:  r.Proto,
		}
		mu.Lock()
		defer mu.Unlock()
		_ = enc.Encode(entry)
	})
}

type recorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *recorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *recorder) Write(b []byte) (int, error) {
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n
	return n, err
}

func (r *recorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (r *recorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return h.Hijack()
}

func (r *recorder) Push(target string, opts *http.PushOptions) error {
	p, ok := r.ResponseWriter.(http.Pusher)
	if !ok {
		return http.ErrNotSupported
	}
	return p.Push(target, opts)
}

func (r *recorder) ReadFrom(src io.Reader) (int64, error) {
	if rf, ok := r.ResponseWriter.(io.ReaderFrom); ok {
		n, err := rf.ReadFrom(src)
		r.bytes += int(n)
		return n, err
	}
	n, err := io.Copy(r.ResponseWriter, src)
	r.bytes += int(n)
	return n, err
}

func (r *recorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

func hostOnly(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return h
	}
	return hostport
}

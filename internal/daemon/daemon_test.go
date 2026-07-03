package daemon

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gate/internal/proxy"
)

func TestAdminStatusAndRoutes(t *testing.T) {
	srv := proxy.New(nil, nil)
	socket := testSocketPath(t)
	stop, err := ServeAdmin(context.Background(), socket, srv)
	if err != nil {
		t.Fatalf("ServeAdmin: %v", err)
	}
	defer stop()

	c := NewClient(socket)

	// The accept goroutine starts immediately; retry briefly to avoid flakiness.
	var st Status
	for i := 0; i < 50; i++ {
		if st, err = c.Status(); err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.Running || st.Status != "running" || !st.PIDAlive || st.Routes != 0 {
		t.Fatalf("status = %+v", st)
	}

	if err := c.SetRoutes([]proxy.Route{{Domain: "a.localhost", Upstream: "127.0.0.1:4300", Auth: "user:pass"}}); err != nil {
		t.Fatalf("SetRoutes: %v", err)
	}
	if srv.RouteCount() != 1 {
		t.Fatalf("RouteCount = %d, want 1 (hot reload not applied)", srv.RouteCount())
	}
	routes, err := c.Routes()
	if err != nil {
		t.Fatalf("Routes: %v", err)
	}
	if len(routes) != 1 || routes[0].Domain != "a.localhost" || !routes[0].Auth {
		t.Fatalf("routes = %+v", routes)
	}
	resp, err := c.http.Get("http://unix/routes")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if strings.Contains(string(body), "user:pass") {
		t.Fatalf("routes response leaked auth secret: %s", body)
	}
	st, _ = c.Status()
	if st.Routes != 1 {
		t.Fatalf("status routes = %d, want 1", st.Routes)
	}
}

func TestAdminRejectsInvalidRoutesWithoutSwapping(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer backend.Close()

	srv := proxy.New(nil, nil)
	socket := testSocketPath(t)
	stop, err := ServeAdmin(context.Background(), socket, srv)
	if err != nil {
		t.Fatalf("ServeAdmin: %v", err)
	}
	defer stop()

	c := NewClient(socket)
	valid := []proxy.Route{{Domain: "a.localhost", Upstream: backend.Listener.Addr().String()}}
	if err := c.SetRoutes(valid); err != nil {
		t.Fatalf("SetRoutes valid: %v", err)
	}
	assertProxyBody(t, srv, "a.localhost", "ok")

	cases := map[string][]proxy.Route{
		"invalid domain": {{Domain: "bad domain", Upstream: "127.0.0.1:4300"}},
		"duplicate domain": {
			{Domain: "a.localhost", Upstream: "127.0.0.1:4300"},
			{Domain: "A.Localhost.", Upstream: "127.0.0.1:4301"},
		},
		"non-loopback upstream": {{Domain: "a.localhost", Upstream: "192.0.2.1:4300"}},
		"invalid port":          {{Domain: "a.localhost", Upstream: "127.0.0.1:0"}},
		"invalid auth":          {{Domain: "a.localhost", Upstream: "127.0.0.1:4300", Auth: "admin"}},
	}
	for name, routes := range cases {
		t.Run(name, func(t *testing.T) {
			if err := c.SetRoutes(routes); err == nil {
				t.Fatal("SetRoutes invalid succeeded")
			}
			assertProxyBody(t, srv, "a.localhost", "ok")
		})
	}
}

func TestAdminRejectsOversizedRoutesBodyWithoutSwapping(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer backend.Close()

	srv := proxy.New(nil, nil)
	socket := testSocketPath(t)
	stop, err := ServeAdmin(context.Background(), socket, srv)
	if err != nil {
		t.Fatalf("ServeAdmin: %v", err)
	}
	defer stop()

	c := NewClient(socket)
	if err := c.SetRoutes([]proxy.Route{{Domain: "a.localhost", Upstream: backend.Listener.Addr().String()}}); err != nil {
		t.Fatalf("SetRoutes valid: %v", err)
	}

	req, err := http.NewRequest(http.MethodPut, "http://unix/routes", strings.NewReader(strings.Repeat("x", maxRoutesBodyBytes+1)))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertProxyBody(t, srv, "a.localhost", "ok")
}

func TestServeAdminRefusesLiveSocket(t *testing.T) {
	srv := proxy.New(nil, nil)
	socket := testSocketPath(t)
	stop, err := ServeAdmin(context.Background(), socket, srv)
	if err != nil {
		t.Fatalf("ServeAdmin: %v", err)
	}
	defer stop()

	if _, err := NewClient(socket).Status(); err != nil {
		t.Fatalf("first daemon not reachable: %v", err)
	}
	if stop2, err := ServeAdmin(context.Background(), socket, proxy.New(nil, nil)); err == nil {
		stop2()
		t.Fatal("second ServeAdmin succeeded on live socket")
	}
	if _, err := NewClient(socket).Status(); err != nil {
		t.Fatalf("live socket was removed: %v", err)
	}
}

func TestClientNotRunning(t *testing.T) {
	c := NewClient(filepath.Join(t.TempDir(), "absent.sock"))
	if c.IsRunning() {
		t.Fatal("IsRunning = true for missing socket")
	}
}

func assertProxyBody(t *testing.T, srv *proxy.Server, host, want string) {
	t.Helper()
	fe := httptest.NewServer(srv.HTTPSHandler())
	defer fe.Close()
	req, err := http.NewRequest(http.MethodGet, fe.URL+"/", bytes.NewReader(nil))
	if err != nil {
		t.Fatal(err)
	}
	req.Host = host
	resp, err := fe.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != want {
		t.Fatalf("proxy status=%d body=%q, want 200 %q", resp.StatusCode, body, want)
	}
}

func testSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "gate-daemon-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "gate.sock")
}

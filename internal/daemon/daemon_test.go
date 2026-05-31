package daemon

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"prx/internal/proxy"
)

func TestAdminStatusAndRoutes(t *testing.T) {
	srv := proxy.New(nil, nil)
	socket := filepath.Join(t.TempDir(), "prx.sock")
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
	if !st.Running || st.Routes != 0 {
		t.Fatalf("status = %+v", st)
	}

	if err := c.SetRoutes([]proxy.Route{{Domain: "a.localhost", Upstream: "127.0.0.1:4300"}}); err != nil {
		t.Fatalf("SetRoutes: %v", err)
	}
	if srv.RouteCount() != 1 {
		t.Fatalf("RouteCount = %d, want 1 (hot reload not applied)", srv.RouteCount())
	}
	st, _ = c.Status()
	if st.Routes != 1 {
		t.Fatalf("status routes = %d, want 1", st.Routes)
	}
}

func TestServeAdminRefusesLiveSocket(t *testing.T) {
	srv := proxy.New(nil, nil)
	socket := filepath.Join(t.TempDir(), "prx.sock")
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

package daemon

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"gate/internal/proxy"
)

// Daemon binds the control socket and the proxy planes together.
type Daemon struct {
	Proxy     *proxy.Server
	Socket    string
	HTTPSAddr string
	HTTPAddr  string
}

// Run serves the control socket and both proxy planes until ctx is cancelled.
func (d *Daemon) Run(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var stop func()
	err := d.Proxy.RunReady(runCtx, d.HTTPSAddr, d.HTTPAddr, func(httpsAddr, httpAddr string) error {
		var err error
		stop, err = serveAdmin(runCtx, d.Socket, d.Proxy, httpsAddr, httpAddr, cancel)
		return err
	})
	if stop != nil {
		stop()
	}
	return err
}

// ServeAdmin starts the control-socket HTTP server for srv and returns a stop
// function. The socket is created with 0600 permissions; a stale socket is
// removed first. ctx is the base for the shutdown grace period (detached so a
// cancelled parent still allows graceful drain).
func ServeAdmin(ctx context.Context, socket string, srv *proxy.Server) (func(), error) {
	return serveAdmin(ctx, socket, srv, "", "", nil)
}

// ServeAdminWithShutdown starts a test/helper admin server whose shutdown
// endpoint invokes shutdown after acknowledging the request.
func ServeAdminWithShutdown(ctx context.Context, socket string, srv *proxy.Server, shutdown context.CancelFunc) (func(), error) {
	return serveAdmin(ctx, socket, srv, "", "", shutdown)
}

func serveAdmin(ctx context.Context, socket string, srv *proxy.Server, httpsAddr, httpAddr string, shutdown context.CancelFunc) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		return nil, err
	}
	if _, err := os.Stat(socket); err == nil {
		if NewClient(socket).IsRunning() {
			return nil, errors.New("daemon already running")
		}
		_ = os.Remove(socket)
	}
	ln, err := net.Listen("unix", socket)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(socket, 0o600); err != nil {
		_ = ln.Close()
		_ = os.Remove(socket)
		return nil, err
	}

	httpd := &http.Server{
		Handler:           adminHandlerWithListen(srv, time.Now(), httpsAddr, httpAddr, shutdown),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() { _ = httpd.Serve(ln) }()

	return func() {
		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_ = httpd.Shutdown(sctx)
		_ = os.Remove(socket)
	}, nil
}

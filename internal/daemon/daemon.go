package daemon

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"prx/internal/proxy"
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
	var stop func()
	err := d.Proxy.RunReady(ctx, d.HTTPSAddr, d.HTTPAddr, func(httpsAddr, httpAddr string) error {
		var err error
		stop, err = serveAdmin(ctx, d.Socket, d.Proxy, httpsAddr, httpAddr)
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
	return serveAdmin(ctx, socket, srv, "", "")
}

func serveAdmin(ctx context.Context, socket string, srv *proxy.Server, httpsAddr, httpAddr string) (func(), error) {
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
	_ = os.Chmod(socket, 0o600)

	httpd := &http.Server{
		Handler:           adminHandlerWithListen(srv, time.Now(), httpsAddr, httpAddr),
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

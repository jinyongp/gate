package proxy

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"time"
)

// Run serves the HTTPS plane on httpsAddr and the HTTP→HTTPS redirector on
// httpAddr until ctx is cancelled, then drains in-flight requests gracefully.
func (s *Server) Run(ctx context.Context, httpsAddr, httpAddr string) error {
	return s.RunReady(ctx, httpsAddr, httpAddr, nil)
}

// RunReady is Run plus a readiness hook called after both listeners are bound
// and before serving begins.
func (s *Server) RunReady(ctx context.Context, httpsAddr, httpAddr string, ready func(httpsAddr, httpAddr string) error) error {
	httpsSrv := &http.Server{
		Addr:              httpsAddr,
		Handler:           s.HTTPSHandler(),
		ReadHeaderTimeout: 10 * time.Second,
		TLSConfig: &tls.Config{
			MinVersion:     tls.VersionTLS12,
			GetCertificate: s.getCert,
		},
	}
	httpSrv := &http.Server{
		Addr:              httpAddr,
		Handler:           s.HTTPHandler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	httpsLn, err := net.Listen("tcp", httpsAddr)
	if err != nil {
		return err
	}
	httpLn, err := net.Listen("tcp", httpAddr)
	if err != nil {
		_ = httpsLn.Close()
		return err
	}
	actualHTTPSAddr := httpsLn.Addr().String()
	actualHTTPAddr := httpLn.Addr().String()
	s.SetHTTPSAddr(actualHTTPSAddr)
	if ready != nil {
		if err := ready(actualHTTPSAddr, actualHTTPAddr); err != nil {
			_ = httpsLn.Close()
			_ = httpLn.Close()
			return err
		}
	}

	errc := make(chan error, 2)
	go func() { errc <- httpsSrv.ServeTLS(httpsLn, "", "") }()
	go func() { errc <- httpSrv.Serve(httpLn) }()

	select {
	case <-ctx.Done():
		shutdown(ctx, httpsSrv)
		shutdown(ctx, httpSrv)
		return ctx.Err()
	case err := <-errc:
		shutdown(ctx, httpsSrv)
		shutdown(ctx, httpSrv)
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// shutdown drains srv. The parent ctx is typically already cancelled, so the
// grace deadline runs on a detached copy (WithoutCancel) to allow in-flight
// requests to finish.
func shutdown(ctx context.Context, srv *http.Server) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

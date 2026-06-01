// Package proxy is prx's data plane: a host-routing reverse proxy that
// terminates TLS, forwards to local dev servers, and reloads its route table
// without dropping connections.
package proxy

import (
	"context"
	"crypto/subtle"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

// Route maps an incoming host to a local upstream.
type Route struct {
	Domain   string `json:"domain"`
	Upstream string `json:"upstream"`       // host:port, e.g. 127.0.0.1:4310
	Exposed  bool   `json:"exposed"`        // if false, non-loopback clients are refused
	Auth     string `json:"auth,omitempty"` // optional "user:pass"; enforced before proxying
}

// LiveFunc reports whether an upstream (host:port) is accepting connections.
type LiveFunc func(upstream string) bool

// Server holds the atomically-swappable route table and serves both planes.
type Server struct {
	routes        atomic.Pointer[map[string]*Route]
	getCert       func(*tls.ClientHelloInfo) (*tls.Certificate, error)
	live          LiveFunc
	rp            *httputil.ReverseProxy
	httpsHostPort atomic.Value
}

type ctxKey int

const upstreamKey ctxKey = iota

// New returns a Server. getCert supplies leaf certificates by SNI; live (if nil)
// defaults to a short TCP dial.
func New(getCert func(*tls.ClientHelloInfo) (*tls.Certificate, error), live LiveFunc) *Server {
	if live == nil {
		live = dialLive
	}
	s := &Server{getCert: getCert, live: live}
	s.SetRoutes(nil)
	s.rp = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			target, _ := pr.In.Context().Value(upstreamKey).(*url.URL)
			pr.SetURL(target)
			pr.SetXForwarded()
			pr.Out.Host = pr.In.Host
		},
		FlushInterval: -1, // flush immediately for SSE/streaming
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(w, "502 Bad Gateway", http.StatusBadGateway)
		},
		Transport: transport(),
	}
	return s
}

// SetRoutes atomically replaces the route table. In-flight requests keep using
// the previous table; new requests see the new one. This is the hot reload.
func (s *Server) SetRoutes(routes []Route) {
	m := make(map[string]*Route, len(routes))
	for i := range routes {
		r := routes[i]
		r.Domain = canonicalDomain(r.Domain)
		m[r.Domain] = &r
	}
	s.routes.Store(&m)
}

func (s *Server) lookup(host string) *Route {
	m := s.routes.Load()
	if m == nil {
		return nil
	}
	return (*m)[canonicalDomain(host)]
}

// RouteCount returns the number of active routes.
func (s *Server) RouteCount() int {
	m := s.routes.Load()
	if m == nil {
		return 0
	}
	return len(*m)
}

// SetHTTPSAddr records the HTTPS listener so plaintext redirects can include a
// non-default port in no-sudo mode.
func (s *Server) SetHTTPSAddr(addr string) {
	s.httpsHostPort.Store(redirectHostPort(addr))
}

// HTTPSHandler routes a decrypted request to its upstream.
func (s *Server) HTTPSHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := hostOnly(r.Host)
		route := s.lookup(host)
		if route == nil {
			http.NotFound(w, r)
			return
		}
		if !remoteAllowed(r.RemoteAddr, route.Exposed) {
			http.Error(w, "403 Forbidden", http.StatusForbidden)
			return
		}
		if route.Auth != "" && !basicAuthOK(r, route.Auth) {
			w.Header().Set("WWW-Authenticate", `Basic realm="prx"`)
			http.Error(w, "401 Unauthorized", http.StatusUnauthorized)
			return
		}
		if !s.live(route.Upstream) {
			writeNotRunning(w, host)
			return
		}
		target := &url.URL{Scheme: "http", Host: route.Upstream}
		ctx := context.WithValue(r.Context(), upstreamKey, target)
		s.rp.ServeHTTP(w, r.WithContext(ctx))
	})
}

// HTTPHandler redirects plaintext :80 traffic to HTTPS on the same host.
func (s *Server) HTTPHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := hostOnly(r.Host)
		if hostPort, ok := s.httpsHostPort.Load().(string); ok && hostPort != "" {
			host = net.JoinHostPort(host, hostPort)
		}
		http.Redirect(w, r, "https://"+host+r.URL.RequestURI(), http.StatusMovedPermanently)
	})
}

func writeNotRunning(w http.ResponseWriter, host string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusBadGateway)
	fmt.Fprintf(w, "502 Bad Gateway\n\nprx: no dev server running for %s\n", host)
}

// remoteAllowed enforces the non-loopback block: only loopback clients may reach
// a route that has not been explicitly exposed.
func remoteAllowed(remoteAddr string, exposed bool) bool {
	if exposed {
		return true
	}
	return isLoopback(remoteAddr)
}

// basicAuthOK validates request credentials against userpass ("user:pass")
// using constant-time comparison.
func basicAuthOK(r *http.Request, userpass string) bool {
	wantUser, wantPass, _ := strings.Cut(userpass, ":")
	user, pass, ok := r.BasicAuth()
	if !ok {
		return false
	}
	userEq := subtle.ConstantTimeCompare([]byte(user), []byte(wantUser)) == 1
	passEq := subtle.ConstantTimeCompare([]byte(pass), []byte(wantPass)) == 1
	return userEq && passEq
}

func isLoopback(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func hostOnly(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return canonicalDomain(h)
	}
	return canonicalDomain(hostport)
}

func redirectHostPort(addr string) string {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return ""
	}
	if port == "" || port == "443" {
		return ""
	}
	return port
}

func canonicalDomain(domain string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")
}

func dialLive(upstream string) bool {
	conn, err := net.DialTimeout("tcp", upstream, 300*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func transport() *http.Transport {
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
		ForceAttemptHTTP2:     true,
	}
}

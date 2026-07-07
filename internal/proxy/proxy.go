// Package proxy is gate's data plane: a host-routing reverse proxy that
// terminates TLS, forwards to local dev servers, and reloads its route table
// without dropping connections.
package proxy

import (
	"context"
	"crypto/subtle"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// Route maps an incoming host to a local upstream.
type Route struct {
	Domain      string `json:"domain"`
	Upstream    string `json:"upstream"`               // host:port, e.g. 127.0.0.1:4310
	Exposed     bool   `json:"exposed"`                // if false, non-loopback clients are refused
	Auth        string `json:"auth,omitempty"`         // optional "user:pass"; enforced before proxying
	ForwardHost string `json:"forward_host,omitempty"` // optional Host override for provider URL aliases
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

const (
	upstreamKey ctxKey = iota
	routeKey
)

// New returns a Server. getCert supplies leaf certificates by SNI; live (if nil)
// defaults to a short TCP dial used to classify upstream proxy failures.
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
			route, _ := pr.In.Context().Value(routeKey).(*Route)
			if route != nil && route.ForwardHost != "" {
				pr.Out.Host = route.ForwardHost
			} else {
				pr.Out.Host = pr.In.Host
			}
		},
		FlushInterval: -1, // flush immediately for SSE/streaming
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, _ error) {
			host := hostOnly(r.Host)
			route := s.lookup(host)
			if route != nil && !s.live(route.Upstream) {
				writeNotRunning(w, host)
				return
			}
			writeBadGateway(w)
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

func (s *Server) routeCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	name := canonicalDomain(hello.ServerName)
	if name == "" {
		return nil, errors.New("proxy: missing SNI server name")
	}
	if s.lookup(name) == nil {
		return nil, fmt.Errorf("proxy: no active route for SNI %q", name)
	}
	if s.getCert == nil {
		return nil, errors.New("proxy: certificate provider not configured")
	}
	return s.getCert(hello)
}

// RouteCount returns the number of active routes.
func (s *Server) RouteCount() int {
	m := s.routes.Load()
	if m == nil {
		return 0
	}
	return len(*m)
}

// Routes returns a snapshot copy of the active route table.
func (s *Server) Routes() []Route {
	m := s.routes.Load()
	if m == nil {
		return nil
	}
	routes := make([]Route, 0, len(*m))
	for _, route := range *m {
		routes = append(routes, *route)
	}
	return routes
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
			w.Header().Set("WWW-Authenticate", `Basic realm="gate"`)
			http.Error(w, "401 Unauthorized", http.StatusUnauthorized)
			return
		}
		target := &url.URL{Scheme: "http", Host: route.Upstream}
		ctx := context.WithValue(r.Context(), upstreamKey, target)
		ctx = context.WithValue(ctx, routeKey, route)
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
	fmt.Fprintf(w, "502 Bad Gateway\n\ngate: no dev server running for %s\n", host)
}

func writeBadGateway(w http.ResponseWriter) {
	http.Error(w, "502 Bad Gateway", http.StatusBadGateway)
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
	normalized, err := NormalizeBasicAuth(userpass)
	if err != nil {
		return false
	}
	wantUser, wantPass, _ := strings.Cut(normalized, ":")
	user, pass, ok := r.BasicAuth()
	if !ok {
		return false
	}
	userEq := subtle.ConstantTimeCompare([]byte(user), []byte(wantUser)) == 1
	passEq := subtle.ConstantTimeCompare([]byte(pass), []byte(wantPass)) == 1
	return userEq && passEq
}

// NormalizeBasicAuth validates userpass as "user:pass" and returns the
// canonical stored form. Usernames are trimmed; passwords are preserved.
func NormalizeBasicAuth(userpass string) (string, error) {
	user, pass, ok := strings.Cut(userpass, ":")
	if !ok {
		return "", errors.New("auth must be user:pass")
	}
	user = strings.TrimSpace(user)
	if user == "" {
		return "", errors.New("auth user must not be empty")
	}
	if pass == "" {
		return "", errors.New("auth password must not be empty")
	}
	return user + ":" + pass, nil
}

// ValidateRoute checks the daemon route input contract before a route is
// installed in the live proxy table.
func ValidateRoute(route Route) error {
	domain := canonicalDomain(route.Domain)
	if err := validateDomain(domain); err != nil {
		return err
	}
	host, port, err := net.SplitHostPort(route.Upstream)
	if err != nil {
		return fmt.Errorf("invalid upstream %q", route.Upstream)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("upstream must be loopback, got %q", route.Upstream)
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 1 || p > 65535 {
		return fmt.Errorf("invalid upstream port %q", route.Upstream)
	}
	if route.Auth != "" {
		if _, err := NormalizeBasicAuth(route.Auth); err != nil {
			return err
		}
	}
	return nil
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

func validateDomain(domain string) error {
	if domain == "" || len(domain) > 253 {
		return fmt.Errorf("invalid domain %q", domain)
	}
	for _, label := range strings.Split(domain, ".") {
		if !validDomainLabel(label) {
			return fmt.Errorf("invalid domain %q", domain)
		}
	}
	return nil
}

func validDomainLabel(label string) bool {
	if label == "" || len(label) > 63 {
		return false
	}
	for i, r := range label {
		valid := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-'
		if !valid {
			return false
		}
		if (i == 0 || i == len(label)-1) && r == '-' {
			return false
		}
	}
	return true
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

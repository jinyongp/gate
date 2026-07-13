// Package daemon runs the resident gate proxy and its control plane. The control
// plane is a small HTTP API over a unix-domain socket; the CLI uses it to push
// routes (hot reload) and query status. Only one process owns the proxy listen
// ports at a time.
package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"gate/internal/proxy"
)

const maxRoutesBodyBytes = 1 << 20

// Status is the daemon's reported state.
type Status struct {
	Scope     string `json:"scope,omitempty"`
	ScopeKey  string `json:"-"`
	Status    string `json:"status,omitempty"`
	Listener  string `json:"listener,omitempty"`
	Socket    string `json:"socket_path,omitempty"`
	PIDPath   string `json:"pid_path,omitempty"`
	PIDAlive  bool   `json:"pid_alive"`
	Running   bool   `json:"running"`
	PID       int    `json:"pid"`
	Routes    int    `json:"routes"`
	UptimeSec int64  `json:"uptime_sec"`
	HTTPSAddr string `json:"https_addr,omitempty"`
	HTTPAddr  string `json:"http_addr,omitempty"`
}

// RouteStatus is the daemon's redacted route view for CLI status reporting.
type RouteStatus struct {
	Domain   string `json:"domain"`
	Upstream string `json:"upstream"`
	Exposed  bool   `json:"exposed"`
	Auth     bool   `json:"auth"`
}

func adminHandlerWithListen(srv *proxy.Server, started time.Time, httpsAddr, httpAddr string, shutdown func()) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /status", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, Status{
			Status:    "running",
			Running:   true,
			PIDAlive:  true,
			PID:       os.Getpid(),
			Routes:    srv.RouteCount(),
			UptimeSec: int64(time.Since(started).Seconds()),
			HTTPSAddr: httpsAddr,
			HTTPAddr:  httpAddr,
		})
	})

	mux.HandleFunc("GET /routes", func(w http.ResponseWriter, _ *http.Request) {
		routes := srv.Routes()
		statuses := make([]RouteStatus, 0, len(routes))
		for _, route := range routes {
			statuses = append(statuses, RouteStatus{
				Domain:   route.Domain,
				Upstream: route.Upstream,
				Exposed:  route.Exposed,
				Auth:     route.Auth != "",
			})
		}
		writeJSON(w, map[string]any{"routes": statuses})
	})

	mux.HandleFunc("PUT /routes", func(w http.ResponseWriter, r *http.Request) {
		var routes []proxy.Route
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRoutesBodyBytes)).Decode(&routes); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := validateRoutes(routes); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		srv.SetRoutes(routes)
		writeJSON(w, map[string]any{"reloaded": true, "routes": len(routes)})
	})

	mux.HandleFunc("POST /reload", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"reloaded": true})
	})

	if shutdown != nil {
		mux.HandleFunc("POST /shutdown", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, map[string]any{"stopping": true})
			go shutdown()
		})
	}

	return mux
}

func validateRoutes(routes []proxy.Route) error {
	seen := map[string]bool{}
	for _, route := range routes {
		if err := proxy.ValidateRoute(route); err != nil {
			return err
		}
		domain := canonicalDomain(route.Domain)
		if seen[domain] {
			return fmt.Errorf("duplicate route domain %q", domain)
		}
		seen[domain] = true
	}
	return nil
}

func canonicalDomain(domain string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

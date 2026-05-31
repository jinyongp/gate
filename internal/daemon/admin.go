// Package daemon runs the resident prx proxy and its control plane. The control
// plane is a small HTTP API over a unix-domain socket; the CLI uses it to push
// routes (hot reload) and query status. Only one process owns :443 at a time.
package daemon

import (
	"encoding/json"
	"net/http"
	"os"
	"time"

	"prx/internal/proxy"
)

// Status is the daemon's reported state.
type Status struct {
	Running   bool  `json:"running"`
	PID       int   `json:"pid"`
	Routes    int   `json:"routes"`
	UptimeSec int64 `json:"uptime_sec"`
}

// adminHandler serves the control API backed by a proxy.Server.
func adminHandler(srv *proxy.Server, started time.Time) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /status", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, Status{
			Running:   true,
			PID:       os.Getpid(),
			Routes:    srv.RouteCount(),
			UptimeSec: int64(time.Since(started).Seconds()),
		})
	})

	mux.HandleFunc("PUT /routes", func(w http.ResponseWriter, r *http.Request) {
		var routes []proxy.Route
		if err := json.NewDecoder(r.Body).Decode(&routes); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		srv.SetRoutes(routes)
		writeJSON(w, map[string]any{"reloaded": true, "routes": len(routes)})
	})

	mux.HandleFunc("POST /reload", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"reloaded": true})
	})

	return mux
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

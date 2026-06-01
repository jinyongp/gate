package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"
)

func main() {
	port := port()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintf(w, "hello from prx sample\nhost: %s\ntime: %s\n", r.Host, time.Now().Format(time.RFC3339))
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintln(w, "ok")
	})

	addr := "127.0.0.1:" + port
	log.Println("listening on sample HTTP server")
	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	if err := server.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

func port() string {
	raw := os.Getenv("PORT")
	if raw == "" {
		return "4300"
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > 65535 {
		log.Fatal("invalid PORT")
	}
	return raw
}

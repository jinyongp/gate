package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"

	"prx/internal/proxy"
)

// Client talks to a running daemon over its unix-domain control socket.
type Client struct {
	socket string
	http   *http.Client
}

// NewClient returns a control-socket client for the socket at path.
func NewClient(socket string) *Client {
	return &Client{
		socket: socket,
		http: &http.Client{
			Timeout: 3 * time.Second,
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, "unix", socket)
				},
			},
		},
	}
}

// IsRunning reports whether a daemon is answering on the socket.
func (c *Client) IsRunning() bool {
	_, err := c.Status()
	return err == nil
}

// Status fetches the daemon status.
func (c *Client) Status() (Status, error) {
	var s Status
	resp, err := c.http.Get("http://unix/status")
	if err != nil {
		return s, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return s, fmt.Errorf("daemon status: %s", resp.Status)
	}
	return s, json.NewDecoder(resp.Body).Decode(&s)
}

// SetRoutes pushes the route table to the daemon (triggering a hot reload).
func (c *Client) SetRoutes(routes []proxy.Route) error {
	body, err := json.Marshal(routes)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPut, "http://unix/routes", bytes.NewReader(body))
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("daemon set routes: %s", resp.Status)
	}
	return nil
}

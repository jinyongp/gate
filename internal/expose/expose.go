// Package expose abstracts how a local route is reached from beyond the host:
// only this machine (local), the LAN (mDNS), or a public URL (cloudflared,
// tailscale). Providers are pluggable behind a common interface.
package expose

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"
)

// Provider names.
const (
	ProviderLocal       = "local"
	ProviderLAN         = "lan"
	ProviderCloudflared = "cloudflared"
	ProviderTailscale   = "tailscale"
)

// Opts configures an exposure.
type Opts struct {
	// Auth, if non-empty as "user:pass", requires basic auth at the proxy.
	Auth string
}

const (
	StatusLive       = "live"
	StatusDown       = "down"
	StatusUnverified = "unverified"
)

type Result struct {
	URL     string
	PID     int
	Command string
}

type StopOpts struct {
	Force bool
}

// Provider establishes external reachability for a domain and manages the
// provider-owned state referenced by persisted exposure records.
type Provider interface {
	Expose(ctx context.Context, domain string, opts Opts) (Result, error)
	Status(ctx context.Context, record Record) (string, error)
	Stop(ctx context.Context, record Record, opts StopOpts) error
	Close() error
}

// For returns the named provider.
func For(name string) (Provider, error) {
	switch name {
	case ProviderLocal, "":
		return Local{}, nil
	case ProviderLAN:
		return LAN{}, nil
	case ProviderCloudflared:
		return &Cloudflared{}, nil
	case ProviderTailscale:
		return &Tailscale{}, nil
	default:
		return nil, fmt.Errorf("expose: unknown provider %q", name)
	}
}

// Local exposes nothing beyond this machine; the URL is the local HTTPS address.
type Local struct{}

// Expose returns the local HTTPS URL.
func (Local) Expose(_ context.Context, domain string, _ Opts) (Result, error) {
	return Result{URL: "https://" + domain}, nil
}

func (Local) Status(_ context.Context, record Record) (string, error) {
	return localStatus(record.Target), nil
}

func (Local) Stop(_ context.Context, _ Record, _ StopOpts) error {
	return nil
}

// Close is a no-op.
func (Local) Close() error { return nil }

// LAN advertises over mDNS, which requires a ".local" domain.
type LAN struct{}

// Expose validates the mDNS constraint and returns the LAN URL. Other devices
// must install the gate root CA (gate ca export) to trust it.
func (LAN) Expose(_ context.Context, domain string, _ Opts) (Result, error) {
	if !strings.HasSuffix(domain, ".local") {
		return Result{}, fmt.Errorf("expose: lan requires a .local domain, got %q", domain)
	}
	return Result{URL: "https://" + domain}, nil
}

func (LAN) Status(_ context.Context, record Record) (string, error) {
	return localStatus(record.Target), nil
}

func (LAN) Stop(_ context.Context, _ Record, _ StopOpts) error {
	return nil
}

// Close is a no-op.
func (LAN) Close() error { return nil }

func localStatus(domain string) string {
	host := strings.TrimSpace(domain)
	if host == "" {
		return StatusDown
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, "443"), 150*time.Millisecond)
	if err != nil {
		return StatusDown
	}
	_ = conn.Close()
	return StatusLive
}

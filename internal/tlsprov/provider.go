// Package tlsprov abstracts certificate provisioning so services can choose
// between the local CA (internal) and real ACME-issued certificates per domain.
package tlsprov

import (
	"context"
	"crypto/tls"

	"prx/internal/ca"
)

// Provider supplies certificates for the TLS server.
type Provider interface {
	// GetCertificate is the tls.Config.GetCertificate callback (SNI).
	GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error)
	// Ensure prepares a certificate for domain ahead of first use.
	Ensure(ctx context.Context, domain string) error
}

// Internal issues certificates from the local CA. Leaves are minted lazily on
// SNI, so Ensure is a no-op.
type Internal struct {
	ca *ca.CA
}

// NewInternal wraps a loaded CA as a Provider.
func NewInternal(authority *ca.CA) *Internal {
	return &Internal{ca: authority}
}

// GetCertificate returns a leaf for the SNI server name.
func (p *Internal) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	return p.ca.GetCertificate(hello)
}

// Ensure is a no-op: internal leaves are issued on demand.
func (p *Internal) Ensure(context.Context, string) error { return nil }

package tlsprov

import (
	"crypto/x509"
	"time"
)

// RenewWindow is how long before expiry a certificate should be renewed.
const RenewWindow = 30 * 24 * time.Hour

// NeedsRenewal reports whether cert expires within the given window (or already
// has). A nil certificate always needs renewal.
func NeedsRenewal(cert *x509.Certificate, within time.Duration) bool {
	if cert == nil {
		return true
	}
	return time.Until(cert.NotAfter) < within
}

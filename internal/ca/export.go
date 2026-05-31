package ca

import (
	"crypto/sha256"
	"fmt"
	"strings"

	"prx/internal/fsutil"
)

// Fingerprint returns the SHA-256 fingerprint of the root certificate as an
// upper-case, colon-separated hex string (for verifying installs on other
// devices).
func (c *CA) Fingerprint() string {
	sum := sha256.Sum256(c.der)
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02X", b)
	}
	return strings.Join(parts, ":")
}

// Export writes the PEM-encoded root certificate to outPath and returns its
// fingerprint. Used to install the CA on other machines (phones, LAN devices).
func (c *CA) Export(outPath string) (string, error) {
	if err := fsutil.WriteAtomic(outPath, c.CertPEM(), 0o644); err != nil {
		return "", err
	}
	return c.Fingerprint(), nil
}

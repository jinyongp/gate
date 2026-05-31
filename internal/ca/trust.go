package ca

import (
	"prx/internal/truststore"
)

// Trust installs the root CA into the OS trust store and the Firefox/NSS store
// so browsers stop warning. Extra options (e.g. truststore.WithLogger) may be
// passed through. This is the one-time `prx trust` step.
func (c *CA) Trust(opts ...truststore.Option) error {
	base := []truststore.Option{
		truststore.WithPrefix("prx"),
		truststore.WithFirefox(),
	}
	return truststore.Install(c.cert, append(base, opts...)...)
}

// Untrust removes the root CA from the OS and Firefox/NSS trust stores.
func (c *CA) Untrust(opts ...truststore.Option) error {
	base := []truststore.Option{
		truststore.WithPrefix("prx"),
		truststore.WithFirefox(),
	}
	return truststore.Uninstall(c.cert, append(base, opts...)...)
}

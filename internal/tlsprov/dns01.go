package tlsprov

import "context"

// DNSProvider publishes and removes the TXT record used to prove control of a
// domain during the ACME DNS-01 challenge. Providers are pluggable (cloudflare
// ships first); each talks to its own DNS API.
type DNSProvider interface {
	SetTXT(ctx context.Context, fqdn, value string) error
	ClearTXT(ctx context.Context, fqdn, value string) error
}

// ChallengeFQDN returns the DNS name a DNS-01 TXT record must be published at
// for the given domain.
func ChallengeFQDN(domain string) string {
	return "_acme-challenge." + domain
}

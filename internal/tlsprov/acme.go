package tlsprov

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/crypto/acme"
)

// ACME directory URLs.
const (
	letsEncryptProd    = "https://acme-v02.api.letsencrypt.org/directory"
	letsEncryptStaging = "https://acme-staging-v02.api.letsencrypt.org/directory"
)

// ACME provisions real certificates via the ACME DNS-01 challenge. It satisfies
// Provider: certificates are obtained by Ensure and served by GetCertificate.
type ACME struct {
	dir     string
	dns     DNSProvider
	staging bool
	account *ecdsa.PrivateKey

	mu    sync.RWMutex
	cache map[string]*tls.Certificate
}

// NewACME returns an ACME provider storing state under dir, using dns to answer
// challenges. When staging is true it uses the Let's Encrypt staging endpoint.
func NewACME(dir string, dns DNSProvider, staging bool) (*ACME, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	acct, err := loadOrGenKey(filepath.Join(dir, "account.key"))
	if err != nil {
		return nil, err
	}
	return &ACME{dir: dir, dns: dns, staging: staging, account: acct, cache: map[string]*tls.Certificate{}}, nil
}

// GetCertificate returns a previously-obtained certificate for the SNI name.
func (a *ACME) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	cert, ok := a.cache[hello.ServerName]
	if !ok {
		return nil, fmt.Errorf("acme: no certificate for %q (run prx up)", hello.ServerName)
	}
	return cert, nil
}

// Ensure obtains a certificate for domain if one is not cached or it is within
// the renewal window.
func (a *ACME) Ensure(ctx context.Context, domain string) error {
	a.mu.RLock()
	cert := a.cache[domain]
	a.mu.RUnlock()
	if cert != nil && !NeedsRenewal(cert.Leaf, RenewWindow) {
		return nil
	}
	obtained, err := a.obtain(ctx, domain)
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.cache[domain] = obtained
	a.mu.Unlock()
	return nil
}

func (a *ACME) directoryURL() string {
	if a.staging {
		return letsEncryptStaging
	}
	return letsEncryptProd
}

// obtain runs the full DNS-01 order: authorize, publish TXT, finalize.
func (a *ACME) obtain(ctx context.Context, domain string) (*tls.Certificate, error) {
	client := &acme.Client{Key: a.account, DirectoryURL: a.directoryURL()}
	if _, err := client.Register(ctx, &acme.Account{}, acme.AcceptTOS); err != nil && !errors.Is(err, acme.ErrAccountAlreadyExists) {
		return nil, err
	}

	order, err := client.AuthorizeOrder(ctx, acme.DomainIDs(domain))
	if err != nil {
		return nil, err
	}
	for _, authzURL := range order.AuthzURLs {
		if err := a.solveDNS01(ctx, client, domain, authzURL); err != nil {
			return nil, err
		}
	}

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: domain},
		DNSNames: []string{domain},
	}, leafKey)
	if err != nil {
		return nil, err
	}
	ders, _, err := client.CreateOrderCert(ctx, order.FinalizeURL, csr, true)
	if err != nil {
		return nil, err
	}
	cert := &tls.Certificate{PrivateKey: leafKey}
	cert.Certificate = append(cert.Certificate, ders...)
	if leaf, perr := x509.ParseCertificate(ders[0]); perr == nil {
		cert.Leaf = leaf
	}
	return cert, nil
}

func (a *ACME) solveDNS01(ctx context.Context, client *acme.Client, domain, authzURL string) error {
	authz, err := client.GetAuthorization(ctx, authzURL)
	if err != nil {
		return err
	}
	var chal *acme.Challenge
	for _, c := range authz.Challenges {
		if c.Type == "dns-01" {
			chal = c
			break
		}
	}
	if chal == nil {
		return fmt.Errorf("acme: no dns-01 challenge for %s", domain)
	}
	rec, err := client.DNS01ChallengeRecord(chal.Token)
	if err != nil {
		return err
	}
	fqdn := ChallengeFQDN(domain)
	if err := a.dns.SetTXT(ctx, fqdn, rec); err != nil {
		return err
	}
	defer func() { _ = a.dns.ClearTXT(ctx, fqdn, rec) }()

	if _, err := client.Accept(ctx, chal); err != nil {
		return err
	}
	_, err = client.WaitAuthorization(ctx, authzURL)
	return err
}

func loadOrGenKey(path string) (*ecdsa.PrivateKey, error) {
	if b, err := os.ReadFile(path); err == nil {
		blk, _ := pem.Decode(b)
		if blk == nil {
			return nil, fmt.Errorf("acme: bad key PEM in %s", path)
		}
		return x509.ParseECPrivateKey(blk.Bytes)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

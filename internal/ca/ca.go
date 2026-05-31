// Package ca implements prx's local certificate authority: a self-signed root
// CA plus per-domain leaf certificates issued on demand for SNI. The root is
// persisted (its private key 0600); leaves are cached in memory and reissued on
// restart.
package ca

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	rootValidity = 10 * 365 * 24 * time.Hour
	leafValidity = 90 * 24 * time.Hour
	rootCN       = "prx local CA"
)

// CA is the loaded root authority and its in-memory leaf cache.
type CA struct {
	dir  string // the "ca" directory holding root.crt / root.key
	cert *x509.Certificate
	der  []byte
	key  *ecdsa.PrivateKey

	mu    sync.RWMutex
	cache map[string]*tls.Certificate
}

// Load loads the root CA from baseDir/ca, generating and persisting it on first
// use.
func Load(baseDir string) (*CA, error) {
	dir := filepath.Join(baseDir, "ca")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	ca := &CA{dir: dir, cache: map[string]*tls.Certificate{}}

	crtPath, keyPath := ca.paths()
	if fileExists(crtPath) && fileExists(keyPath) {
		if err := ca.load(crtPath, keyPath); err != nil {
			return nil, err
		}
		return ca, nil
	}
	if err := ca.generate(); err != nil {
		return nil, err
	}
	return ca, nil
}

// CertPEM returns the PEM-encoded root certificate.
func (c *CA) CertPEM() []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.der})
}

// Certificate returns the parsed root certificate.
func (c *CA) Certificate() *x509.Certificate { return c.cert }

// GetCertificate is a tls.Config.GetCertificate callback: it returns a leaf for
// the SNI server name, issuing and caching one on first request.
func (c *CA) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	name := hello.ServerName
	if name == "" {
		return nil, errors.New("ca: missing SNI server name")
	}
	name = canonicalDomain(name)
	c.mu.RLock()
	if cert, ok := c.cache[name]; ok {
		c.mu.RUnlock()
		return cert, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()
	if cert, ok := c.cache[name]; ok { // re-check after lock upgrade
		return cert, nil
	}
	cert, err := c.issueLeaf(name)
	if err != nil {
		return nil, err
	}
	c.cache[name] = cert
	return cert, nil
}

func (c *CA) issueLeaf(domain string) (*tls.Certificate, error) {
	domain = canonicalDomain(domain)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: domain},
		DNSNames:     []string{domain},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(leafValidity),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.cert, &key.PublicKey, c.key)
	if err != nil {
		return nil, err
	}
	return &tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  key,
		Leaf:        mustParse(der),
	}, nil
}

func canonicalDomain(domain string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")
}

func (c *CA) generate() error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := randomSerial()
	if err != nil {
		return err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: rootCN},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(rootValidity),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	c.der = der
	c.cert = mustParse(der)
	c.key = key
	return c.save()
}

func (c *CA) save() error {
	crtPath, keyPath := c.paths()
	crtPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.der})
	if err := os.WriteFile(crtPath, crtPEM, 0o644); err != nil { //nolint:gosec // G306: root cert is public.
		return err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(c.key)
	if err != nil {
		return err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	return os.WriteFile(keyPath, keyPEM, 0o600)
}

func (c *CA) load(crtPath, keyPath string) error {
	crtPEM, err := os.ReadFile(crtPath)
	if err != nil {
		return err
	}
	blk, _ := pem.Decode(crtPEM)
	if blk == nil {
		return fmt.Errorf("ca: bad root cert PEM in %s", crtPath)
	}
	cert, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		return err
	}
	if info, err := os.Stat(keyPath); err != nil {
		return err
	} else if info.Mode().Perm()&0o077 != 0 {
		if err := os.Chmod(keyPath, 0o600); err != nil {
			return fmt.Errorf("ca: root key %s permissions too broad and chmod failed: %w", keyPath, err)
		}
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return err
	}
	kblk, _ := pem.Decode(keyPEM)
	if kblk == nil {
		return fmt.Errorf("ca: bad root key PEM in %s", keyPath)
	}
	key, err := x509.ParsePKCS8PrivateKey(kblk.Bytes)
	if err != nil {
		return err
	}
	ecKey, ok := key.(*ecdsa.PrivateKey)
	if !ok {
		return errors.New("ca: root key is not ECDSA")
	}
	c.cert, c.der, c.key = cert, cert.Raw, ecKey
	return nil
}

func (c *CA) paths() (crt, key string) {
	return filepath.Join(c.dir, "root.crt"), filepath.Join(c.dir, "root.key")
}

func randomSerial() (*big.Int, error) {
	return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
}

func mustParse(der []byte) *x509.Certificate {
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		panic(err) // we just created this DER; parse cannot fail
	}
	return cert
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.Mode().IsRegular()
}

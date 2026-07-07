// Package ca implements gate's local certificate authority: a self-signed root
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
	rootCN       = "gate local CA"
)

// ErrNotFound means no persisted gate root CA exists at the requested location.
var ErrNotFound = errors.New("ca: root CA not found")

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
	ca, err := loadExistingDir(dir)
	if err == nil {
		return ca, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	ca = &CA{dir: dir, cache: map[string]*tls.Certificate{}}
	if err := ca.generate(); err != nil {
		return nil, err
	}
	return ca, nil
}

// LoadExisting loads the root CA from baseDir/ca without generating a new one.
func LoadExisting(baseDir string) (*CA, error) {
	return loadExistingDir(filepath.Join(baseDir, "ca"))
}

// LoadCertificate loads only the persisted root certificate without requiring
// the private key. This is for trust-store cleanup paths that must keep working
// even when key material has already been removed.
func LoadCertificate(baseDir string) (*CA, error) {
	dir := filepath.Join(baseDir, "ca")
	ca := &CA{dir: dir, cache: map[string]*tls.Certificate{}}
	crtPath, _ := ca.paths()
	if !fileExists(crtPath) {
		return nil, ErrNotFound
	}
	crtPEM, err := os.ReadFile(crtPath)
	if err != nil {
		return nil, err
	}
	blk, _ := pem.Decode(crtPEM)
	if blk == nil {
		return nil, fmt.Errorf("ca: bad root cert PEM in %s", crtPath)
	}
	cert, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		return nil, err
	}
	ca.cert, ca.der = cert, cert.Raw
	return ca, nil
}

func loadExistingDir(dir string) (*CA, error) {
	ca := &CA{dir: dir, cache: map[string]*tls.Certificate{}}
	crtPath, keyPath := ca.paths()
	if !fileExists(crtPath) || !fileExists(keyPath) {
		return nil, ErrNotFound
	}
	if err := ca.load(crtPath, keyPath); err != nil {
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
	if err := validateDomain(name); err != nil {
		return nil, err
	}
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

func validateDomain(domain string) error {
	if domain == "" || len(domain) > 253 {
		return fmt.Errorf("ca: invalid SNI server name %q", domain)
	}
	for _, label := range strings.Split(domain, ".") {
		if !validDomainLabel(label) {
			return fmt.Errorf("ca: invalid SNI server name %q", domain)
		}
	}
	return nil
}

func validDomainLabel(label string) bool {
	if label == "" || len(label) > 63 {
		return false
	}
	for i, r := range label {
		valid := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-'
		if !valid {
			return false
		}
		if (i == 0 || i == len(label)-1) && r == '-' {
			return false
		}
	}
	return true
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
	info, err := os.Stat(keyPath)
	if err != nil {
		return err
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
	if err := validateRootMaterial(cert, ecKey); err != nil {
		return err
	}
	if info.Mode().Perm()&0o077 != 0 {
		if err := os.Chmod(keyPath, 0o600); err != nil {
			return fmt.Errorf("ca: root key %s permissions too broad and chmod failed: %w", keyPath, err)
		}
	}
	c.cert, c.der, c.key = cert, cert.Raw, ecKey
	return nil
}

func validateRootMaterial(cert *x509.Certificate, key *ecdsa.PrivateKey) error {
	if !cert.IsCA {
		return errors.New("ca: root certificate is not a CA")
	}
	if !cert.BasicConstraintsValid {
		return errors.New("ca: root certificate missing valid basic constraints")
	}
	if cert.KeyUsage&x509.KeyUsageCertSign == 0 {
		return errors.New("ca: root certificate cannot sign certificates")
	}
	now := time.Now()
	if now.Before(cert.NotBefore) {
		return errors.New("ca: root certificate is not yet valid")
	}
	if now.After(cert.NotAfter) {
		return errors.New("ca: root certificate is expired")
	}
	pub, ok := cert.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		return errors.New("ca: root certificate public key is not ECDSA")
	}
	if !pub.Equal(&key.PublicKey) {
		return errors.New("ca: root certificate does not match root key")
	}
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

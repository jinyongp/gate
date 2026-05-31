package tlsprov

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"prx/internal/ca"
)

func TestInternalProvider(t *testing.T) {
	authority, err := ca.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := NewInternal(authority)
	if err := p.Ensure(context.Background(), "app.localhost"); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	cert, err := p.GetCertificate(&tls.ClientHelloInfo{ServerName: "app.localhost"})
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
	if cert.Leaf.Subject.CommonName != "app.localhost" {
		t.Fatalf("leaf CN = %q", cert.Leaf.Subject.CommonName)
	}
}

func TestNeedsRenewal(t *testing.T) {
	now := time.Now()
	soon := &x509.Certificate{NotAfter: now.Add(10 * 24 * time.Hour)}
	later := &x509.Certificate{NotAfter: now.Add(60 * 24 * time.Hour)}
	if !NeedsRenewal(soon, RenewWindow) {
		t.Fatal("cert expiring in 10d should need renewal")
	}
	if NeedsRenewal(later, RenewWindow) {
		t.Fatal("cert expiring in 60d should not need renewal")
	}
	if !NeedsRenewal(nil, RenewWindow) {
		t.Fatal("nil cert should need renewal")
	}
}

func TestChallengeFQDN(t *testing.T) {
	if got := ChallengeFQDN("app.example.com"); got != "_acme-challenge.app.example.com" {
		t.Fatalf("ChallengeFQDN = %q", got)
	}
}

func TestACMELoadsPersistedCertificate(t *testing.T) {
	dir := t.TempDir()
	a, err := NewACME(dir, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	cert := testCert(t, "app.example.com")
	if err := a.saveCert("app.example.com", cert); err != nil {
		t.Fatalf("saveCert: %v", err)
	}
	reloaded, err := NewACME(dir, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reloaded.GetCertificate(&tls.ClientHelloInfo{ServerName: "App.Example.Com."})
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
	if got.Leaf == nil || got.Leaf.Subject.CommonName != "app.example.com" {
		t.Fatalf("leaf = %+v", got.Leaf)
	}
}

func TestACMEEnsureConcurrentObtainsOnce(t *testing.T) {
	a, err := NewACME(t.TempDir(), nil, true)
	if err != nil {
		t.Fatal(err)
	}
	cert := testCert(t, "app.example.com")
	var calls int32
	a.obtainFunc = func(context.Context, string) (*tls.Certificate, error) {
		atomic.AddInt32(&calls, 1)
		time.Sleep(10 * time.Millisecond)
		return cert, nil
	}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := a.Ensure(context.Background(), "App.Example.Com."); err != nil {
				t.Errorf("Ensure: %v", err)
			}
		}()
	}
	wg.Wait()
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("obtain calls = %d, want 1", got)
	}
}

func testCert(t *testing.T, domain string) *tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: domain},
		DNSNames:     []string{domain},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(60 * 24 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: tmpl}
}

func TestCloudflareSetAndClear(t *testing.T) {
	var sawAuth string
	var deleted bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodPost:
			_ = json.NewEncoder(w).Encode(cfResponse{Success: true})
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(cfResponse{Success: true, Result: []cfRecord{{ID: "rec1"}}})
		case http.MethodDelete:
			deleted = true
			_ = json.NewEncoder(w).Encode(cfResponse{Success: true})
		}
	}))
	defer srv.Close()

	cf := NewCloudflare("tok", "ZONE")
	cf.BaseURL = srv.URL

	ctx := context.Background()
	if err := cf.SetTXT(ctx, "_acme-challenge.app.example.com", "val"); err != nil {
		t.Fatalf("SetTXT: %v", err)
	}
	if sawAuth != "Bearer tok" {
		t.Fatalf("auth header = %q", sawAuth)
	}
	if err := cf.ClearTXT(ctx, "_acme-challenge.app.example.com", "val"); err != nil {
		t.Fatalf("ClearTXT: %v", err)
	}
	if !deleted {
		t.Fatal("ClearTXT did not delete the matched record")
	}
}

func TestCloudflareAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(cfResponse{Success: false, Errors: []cfError{{Message: "bad token"}}})
	}))
	defer srv.Close()
	cf := NewCloudflare("tok", "ZONE")
	cf.BaseURL = srv.URL
	err := cf.SetTXT(context.Background(), "x", "y")
	if err == nil || !strings.Contains(err.Error(), "bad token") {
		t.Fatalf("err = %v, want bad token", err)
	}
}

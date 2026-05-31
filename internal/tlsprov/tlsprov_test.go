package tlsprov

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

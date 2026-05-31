package tlsprov

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// cloudflareAPI is the default Cloudflare API v4 base URL.
const cloudflareAPI = "https://api.cloudflare.com/client/v4"

// Cloudflare is a DNSProvider backed by the Cloudflare API v4.
type Cloudflare struct {
	Token   string // API token with DNS edit scope
	ZoneID  string // target zone
	BaseURL string // override for testing; defaults to the public API
	HTTP    *http.Client
}

// NewCloudflare returns a Cloudflare provider for a zone.
func NewCloudflare(token, zoneID string) *Cloudflare {
	return &Cloudflare{
		Token:   token,
		ZoneID:  zoneID,
		BaseURL: cloudflareAPI,
		HTTP:    &http.Client{Timeout: 15 * time.Second},
	}
}

type cfRecord struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
}

type cfResponse struct {
	Success bool       `json:"success"`
	Errors  []cfError  `json:"errors"`
	Result  []cfRecord `json:"result"`
}

type cfError struct {
	Message string `json:"message"`
}

// SetTXT creates a TXT record fqdn=value.
func (c *Cloudflare) SetTXT(ctx context.Context, fqdn, value string) error {
	body, err := json.Marshal(cfRecord{Type: "TXT", Name: fqdn, Content: value, TTL: 60})
	if err != nil {
		return err
	}
	path := fmt.Sprintf("/zones/%s/dns_records", c.ZoneID)
	_, err = c.do(ctx, http.MethodPost, path, bytes.NewReader(body))
	return err
}

// ClearTXT deletes the TXT record(s) matching fqdn and value.
func (c *Cloudflare) ClearTXT(ctx context.Context, fqdn, value string) error {
	q := url.Values{"type": {"TXT"}, "name": {fqdn}, "content": {value}}
	listPath := fmt.Sprintf("/zones/%s/dns_records?%s", c.ZoneID, q.Encode())
	resp, err := c.do(ctx, http.MethodGet, listPath, nil)
	if err != nil {
		return err
	}
	for _, rec := range resp.Result {
		delPath := fmt.Sprintf("/zones/%s/dns_records/%s", c.ZoneID, rec.ID)
		if _, err := c.do(ctx, http.MethodDelete, delPath, nil); err != nil {
			return err
		}
	}
	return nil
}

func (c *Cloudflare) do(ctx context.Context, method, path string, body io.Reader) (*cfResponse, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.base()+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	var out cfResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if !out.Success {
		msg := "cloudflare API error"
		if len(out.Errors) > 0 {
			msg = out.Errors[0].Message
		}
		return &out, fmt.Errorf("cloudflare: %s (%s)", msg, resp.Status)
	}
	return &out, nil
}

func (c *Cloudflare) base() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return cloudflareAPI
}

func (c *Cloudflare) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

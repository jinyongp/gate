// Package listener identifies the front proxy listener pair owned by a daemon.
package listener

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"strconv"
	"strings"
	"unicode"
)

const (
	DefaultHTTPSAddr = ":443"
	DefaultHTTPAddr  = ":80"
)

// Pair is the HTTPS/HTTP bind address pair for one front proxy listener.
type Pair struct {
	HTTPSAddr string
	HTTPAddr  string
}

// Key is a stable, file-safe listener identifier.
type Key string

// DefaultPair returns gate's standard local HTTPS listener.
func DefaultPair() Pair {
	return Pair{HTTPSAddr: DefaultHTTPSAddr, HTTPAddr: DefaultHTTPAddr}
}

// FromFlags applies defaults and normalizes a pair from CLI flag values.
func FromFlags(httpsAddr, httpAddr string) Pair {
	if strings.TrimSpace(httpsAddr) == "" {
		httpsAddr = DefaultHTTPSAddr
	}
	if strings.TrimSpace(httpAddr) == "" {
		httpAddr = DefaultHTTPAddr
	}
	return Normalize(Pair{HTTPSAddr: httpsAddr, HTTPAddr: httpAddr})
}

// Normalize returns canonical bind address strings. Equivalent wildcard binds
// collapse to port-only addresses, while loopback and interface-specific binds
// remain explicit.
func Normalize(pair Pair) Pair {
	return Pair{
		HTTPSAddr: normalizeAddr(pair.HTTPSAddr, DefaultHTTPSAddr),
		HTTPAddr:  normalizeAddr(pair.HTTPAddr, DefaultHTTPAddr),
	}
}

// Equivalent reports whether two listener pairs identify the same listener.
func Equivalent(a, b Pair) bool {
	na, nb := Normalize(a), Normalize(b)
	return na.HTTPSAddr == nb.HTTPSAddr && na.HTTPAddr == nb.HTTPAddr
}

// Validate checks that both bind addresses are syntactically usable TCP
// endpoints. allowZero permits ephemeral listener ports for callers that can
// persist the concrete bound address returned by the daemon.
func Validate(pair Pair, allowZero bool) error {
	pair = Normalize(pair)
	for _, item := range []struct {
		name string
		addr string
	}{
		{name: "HTTPS", addr: pair.HTTPSAddr},
		{name: "HTTP", addr: pair.HTTPAddr},
	} {
		host, port, err := split(item.addr)
		if err != nil {
			return fmt.Errorf("invalid %s listener address %q", item.name, item.addr)
		}
		p, err := strconv.Atoi(port)
		if err != nil || p < 0 || p > 65535 || (!allowZero && p == 0) {
			return fmt.Errorf("invalid %s listener port in %q", item.name, item.addr)
		}
		if strings.IndexFunc(host, unicode.IsSpace) >= 0 || strings.IndexFunc(host, unicode.IsControl) >= 0 {
			return fmt.Errorf("invalid %s listener host in %q", item.name, item.addr)
		}
		if !validBindHost(host) {
			return fmt.Errorf("invalid %s listener host in %q", item.name, item.addr)
		}
	}
	_, httpsPort, _ := split(pair.HTTPSAddr)
	if pair.HTTPSAddr == pair.HTTPAddr && httpsPort != "0" {
		return fmt.Errorf("HTTPS and HTTP listener addresses must differ")
	}
	return nil
}

func validBindHost(host string) bool {
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if host == "" {
		return true
	}
	ipHost := host
	if strings.Contains(host, "%") {
		if strings.Count(host, "%") != 1 {
			return false
		}
		before, zone, _ := strings.Cut(host, "%")
		ip := net.ParseIP(before)
		if ip == nil || ip.To4() != nil || zone == "" || !validZone(zone) {
			return false
		}
		return true
	}
	if net.ParseIP(ipHost) != nil {
		return true
	}
	if len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' {
				return false
			}
		}
	}
	return true
}

func validZone(zone string) bool {
	for _, r := range zone {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' && r != '_' && r != '.' {
			return false
		}
	}
	return true
}

// KeyFor returns a stable, file-safe key for pair.
func KeyFor(pair Pair) Key {
	pair = Normalize(pair)
	return Key("https-" + keyAddr(pair.HTTPSAddr) + "-http-" + keyAddr(pair.HTTPAddr))
}

func normalizeAddr(addr, def string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		addr = def
	}
	host, port, err := split(addr)
	if err != nil {
		return addr
	}
	host = normalizeHost(host)
	if isWildcard(host) {
		return ":" + port
	}
	return net.JoinHostPort(host, port)
}

func split(addr string) (host, port string, err error) {
	host, port, err = net.SplitHostPort(addr)
	if err == nil {
		return host, port, nil
	}
	if strings.HasPrefix(addr, ":") {
		return "", strings.TrimPrefix(addr, ":"), nil
	}
	if p, perr := strconv.Atoi(addr); perr == nil && p > 0 && p <= 65535 {
		return "", addr, nil
	}
	return "", "", err
}

func normalizeHost(host string) string {
	host = strings.TrimSpace(strings.Trim(host, "[]"))
	if strings.EqualFold(host, "0.0.0.0") || host == "::" {
		return ""
	}
	return strings.ToLower(host)
}

func isWildcard(host string) bool {
	return host == ""
}

func keyAddr(addr string) string {
	host, port, err := split(addr)
	if err != nil {
		return "x" + shortHash(addr)
	}
	host = normalizeHost(host)
	if isWildcard(host) {
		return port
	}
	return "h" + shortHash(host) + "-" + port
}

func shortHash(s string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(s))))
	return hex.EncodeToString(sum[:4])
}

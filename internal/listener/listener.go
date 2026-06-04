// Package listener identifies the front proxy listener pair owned by a daemon.
package listener

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"strconv"
	"strings"
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

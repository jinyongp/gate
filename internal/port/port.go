// Package port allocates and probes local development ports. Allocation is
// best-effort: it avoids both registry-reserved ports and ports the OS reports
// as currently bound, but reservations are a prx-internal promise, not an OS
// lock.
package port

import (
	"errors"
	"net"
	"strconv"
	"time"
)

// Pool is an inclusive port range to allocate from.
type Pool struct {
	Min int
	Max int
}

// DefaultPool avoids common dev ports (3000, 5173, 8080) and the OS ephemeral
// range (49152+).
var DefaultPool = Pool{Min: 4300, Max: 4999}

// ErrPoolExhausted is returned when no free port remains in the pool.
var ErrPoolExhausted = errors.New("no free port in pool")

// liveTimeout bounds the liveness probe dial.
const liveTimeout = 300 * time.Millisecond

// Allocate returns the lowest port in pool that is neither reserved nor
// currently bound by the OS.
func Allocate(pool Pool, reserved map[int]bool) (int, error) {
	for p := pool.Min; p <= pool.Max; p++ {
		if reserved[p] {
			continue
		}
		if IsBound(p) {
			continue
		}
		return p, nil
	}
	return 0, ErrPoolExhausted
}

// IsBound reports whether the OS already has something bound to the port on
// loopback (best-effort: it briefly attempts to listen, then releases).
func IsBound(p int) bool {
	ln, err := net.Listen("tcp", addr(p))
	if err != nil {
		return true
	}
	_ = ln.Close()
	return false
}

// IsLive reports whether a backend is accepting connections on the port. prx
// cannot observe the dev-server process, so liveness is determined by dialling.
func IsLive(p int) bool {
	conn, err := net.DialTimeout("tcp", addr(p), liveTimeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func addr(p int) string {
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(p))
}

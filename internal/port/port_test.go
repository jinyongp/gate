package port

import (
	"errors"
	"net"
	"testing"
)

// freePort returns a port that was free at call time (the listener is closed).
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return p
}

func TestAllocateSkipsBound(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	bound := ln.Addr().(*net.TCPAddr).Port

	// A pool containing only the bound port must be exhausted.
	if _, err := Allocate(Pool{Min: bound, Max: bound}, nil); !errors.Is(err, ErrPoolExhausted) {
		t.Fatalf("err = %v, want ErrPoolExhausted", err)
	}
}

func TestAllocateSkipsReserved(t *testing.T) {
	p := freePort(t)
	if _, err := Allocate(Pool{Min: p, Max: p}, map[int]bool{p: true}); !errors.Is(err, ErrPoolExhausted) {
		t.Fatalf("err = %v, want ErrPoolExhausted (reserved)", err)
	}
}

func TestAllocateReturnsFree(t *testing.T) {
	p := freePort(t)
	got, err := Allocate(Pool{Min: p, Max: p}, nil)
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if got != p {
		t.Fatalf("Allocate = %d, want %d", got, p)
	}
}

func TestIsLive(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := ln.Addr().(*net.TCPAddr).Port
	if !IsLive(p) {
		t.Fatal("IsLive = false for open listener")
	}
	_ = ln.Close()
	if IsLive(p) {
		t.Fatal("IsLive = true after close")
	}
}

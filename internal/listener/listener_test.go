package listener

import "testing"

func TestDefaultPair(t *testing.T) {
	got := DefaultPair()
	if got.HTTPSAddr != ":443" || got.HTTPAddr != ":80" {
		t.Fatalf("DefaultPair = %+v", got)
	}
}

func TestNormalizeCollapsesWildcardBinds(t *testing.T) {
	cases := []Pair{
		{HTTPSAddr: ":443", HTTPAddr: ":80"},
		{HTTPSAddr: "[::]:443", HTTPAddr: "[::]:80"},
		{HTTPSAddr: "0.0.0.0:443", HTTPAddr: "0.0.0.0:80"},
	}
	for _, c := range cases {
		if got, want := Normalize(c), DefaultPair(); got != want {
			t.Fatalf("Normalize(%+v) = %+v, want %+v", c, got, want)
		}
	}
}

func TestNormalizeKeepsLoopbackAndInterfaceSpecific(t *testing.T) {
	defaultPair := DefaultPair()
	loopback4 := Normalize(Pair{HTTPSAddr: "127.0.0.1:443", HTTPAddr: "127.0.0.1:80"})
	loopback6 := Normalize(Pair{HTTPSAddr: "[::1]:443", HTTPAddr: "[::1]:80"})
	if loopback4 == defaultPair {
		t.Fatal("IPv4 loopback collapsed to wildcard")
	}
	if loopback6 == defaultPair {
		t.Fatal("IPv6 loopback collapsed to wildcard")
	}
	lan := Normalize(Pair{HTTPSAddr: "192.168.1.10:443", HTTPAddr: "192.168.1.10:80"})
	if lan == defaultPair {
		t.Fatal("interface-specific bind collapsed to wildcard")
	}
}

func TestEquivalent(t *testing.T) {
	if !Equivalent(Pair{HTTPSAddr: ":443", HTTPAddr: ":80"}, Pair{HTTPSAddr: "[::]:443", HTTPAddr: "0.0.0.0:80"}) {
		t.Fatal("wildcard binds should be equivalent")
	}
	if Equivalent(Pair{HTTPSAddr: ":443", HTTPAddr: ":80"}, Pair{HTTPSAddr: "127.0.0.1:443", HTTPAddr: ":80"}) {
		t.Fatal("loopback bind should not equal wildcard")
	}
}

func TestKeyFor(t *testing.T) {
	if got, want := KeyFor(DefaultPair()), Key("https-443-http-80"); got != want {
		t.Fatalf("KeyFor(default) = %q, want %q", got, want)
	}
	if KeyFor(DefaultPair()) != KeyFor(Pair{HTTPSAddr: "[::]:443", HTTPAddr: "[::]:80"}) {
		t.Fatal("equivalent wildcard pairs produced different keys")
	}
	if KeyFor(DefaultPair()) == KeyFor(Pair{HTTPSAddr: "127.0.0.1:443", HTTPAddr: "127.0.0.1:80"}) {
		t.Fatal("loopback pair produced default key")
	}
}

func TestKeyForAvoidsHostPunctuationCollisions(t *testing.T) {
	cases := []Pair{
		{HTTPSAddr: "a.b:443", HTTPAddr: "a.b:80"},
		{HTTPSAddr: "a-b:443", HTTPAddr: "a-b:80"},
		{HTTPSAddr: "127.0.0.1:443", HTTPAddr: "127.0.0.1:80"},
		{HTTPSAddr: "127-0-0-1:443", HTTPAddr: "127-0-0-1:80"},
		{HTTPSAddr: "[::1]:443", HTTPAddr: "[::1]:80"},
		{HTTPSAddr: "1:443", HTTPAddr: "1:80"},
	}
	seen := map[Key]Pair{}
	for _, pair := range cases {
		key := KeyFor(pair)
		if previous, ok := seen[key]; ok {
			t.Fatalf("KeyFor collision: %+v and %+v -> %s", previous, pair, key)
		}
		seen[key] = pair
	}
}

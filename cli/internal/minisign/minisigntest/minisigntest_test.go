package minisigntest

import (
	"strings"
	"testing"

	"makeit/internal/minisign"
)

func TestSignVerifies(t *testing.T) {
	k, other := NewKey(), NewKey()
	data := []byte("0123  makeit-1.1.0-linux-x64\n")
	sig := k.Sign(data, "makeit 1.1.0 checksums.txt")
	if c, err := minisign.Verify([]string{k.Public()}, sig, data); err != nil || c != "makeit 1.1.0 checksums.txt" {
		t.Fatalf("valid: %q %v", c, err)
	}
	edited := strings.Replace(sig, "trusted comment: makeit 1.1.0", "trusted comment: makeit 1.2.0", 1)
	for name, c := range map[string]struct {
		keys []string
		sig  string
		data []byte
	}{
		"changed data":            {[]string{k.Public()}, sig, []byte(string(data) + "x")},
		"another key":             {[]string{other.Public()}, sig, data},
		"changed trusted comment": {[]string{k.Public()}, edited, data},
	} {
		if _, err := minisign.Verify(c.keys, c.sig, c.data); err == nil {
			t.Errorf("%s: verified", name)
		}
	}
}

func TestKeyFromSeedDeterministic(t *testing.T) {
	seed := [32]byte{1, 2, 3}
	if a, b := KeyFromSeed(seed).Public(), KeyFromSeed(seed).Public(); a != b {
		t.Errorf("same seed, keys %q and %q", a, b)
	}
	if KeyFromSeed(seed).Public() == KeyFromSeed([32]byte{4}).Public() {
		t.Error("two seeds gave the same key")
	}
}

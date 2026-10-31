package minisigntest

import (
	"os"
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

// The release key (minisign.pub at the repository root, the owner's) parses, and a build that
// carries it refuses a throwaway key's signature; a build with that throwaway key accepts it. No key
// accepts the other's signatures.
func TestReleaseKeyAcceptsOnlyItsOwn(t *testing.T) {
	b, err := os.ReadFile("../../../../minisign.pub")
	if err != nil {
		t.Fatalf("the release key file: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 2 {
		t.Fatalf("minisign.pub: want a comment line and a key line, got %q", b)
	}
	release := strings.TrimSpace(lines[1])
	if _, err := minisign.ParsePublicKey(release); err != nil {
		t.Fatalf("release key %q: %v", release, err)
	}
	throwaway := NewKey()
	data := []byte("deadbeef  makeit-1.0.0-linux-x64\n")
	sig := throwaway.Sign(data, "makeit 1.0.0 checksums.txt")
	if _, err := minisign.Verify([]string{release}, sig, data); err == nil {
		t.Error("a build with the release key accepted a throwaway key's signature")
	}
	if _, err := minisign.Verify([]string{throwaway.Public()}, sig, data); err != nil {
		t.Errorf("a build with the throwaway key refused its own signature: %v", err)
	}
}

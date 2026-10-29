package minisign

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testdata comes from the minisign 0.12 CLI: scripts/gen-minisign-vectors.sh.
func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestVerify(t *testing.T) {
	pub, other := fixture(t, "pub.key"), fixture(t, "other.pub")
	data := []byte(fixture(t, "checksums.txt"))
	good := fixture(t, "checksums.txt.minisig")
	if c, err := Verify([]string{pub}, good, data); err != nil || c != "claude-stack 1.2.3 checksums.txt" {
		t.Fatalf("valid: %q %v", c, err)
	}
	bareLine := strings.TrimSpace(strings.Split(pub, "\n")[1])
	if _, err := Verify([]string{other, bareLine}, good, data); err != nil {
		t.Fatalf("second of two keys, bare line: %v", err)
	}
	cases := []struct {
		name, sig string
		data      []byte
		keys      []string
		want      string
	}{
		{"tampered file", good, append([]byte(string(data)), 'x'), []string{pub}, "file does not match"},
		{"edited trusted comment", strings.Replace(good, "1.2.3", "9.9.9", 1), data, []string{pub}, "trusted comment was altered"},
		{"legacy Ed", fixture(t, "legacy.minisig"), data, []string{pub}, "only prehashed ED"},
		{"untrusted key", fixture(t, "other.minisig"), data, []string{pub}, "does not trust"},
		{"truncated", "untrusted comment: x\n", data, []string{pub}, "missing"},
		{"no keys", good, data, nil, "no release keys"},
		{"bad key", good, data, []string{"bm90IGEga2V5"}, "not an Ed25519"},
		{"bad base64", strings.Replace(good, "RUS", "!!!", 1), data, []string{pub}, "wrong length"},
	}
	for _, c := range cases {
		if _, err := Verify(c.keys, c.sig, c.data); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want %q, got %v", c.name, c.want, err)
		}
	}
}

package latest

import "testing"

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		have, v string
		want    bool
	}{
		{"18.1.3", "19.0.0", true},
		{"19.0.0", "18.1.3", false},
		{"18.1.3", "18.1.3", false},
		{"19.0.0-rc.1", "19.0.0", true},
		{"18.1.3", "19.0.0-rc.1", false},
		{"18.1.3", "latest", false},
		{"", "1.0.0", true},
	} {
		if got := Newer(c.have, c.v); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.have, c.v, got, c.want)
		}
	}
}

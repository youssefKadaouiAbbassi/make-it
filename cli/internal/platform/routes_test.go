package platform

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestViaCmdExpandsHome(t *testing.T) {
	v := Via{Bin: "go", Argv: []string{"go", "install", "x@latest"}, Env: []string{"GOBIN=~/.local/bin"}}
	c := v.Cmd("/h")
	if !slices.Equal(c.Argv, v.Argv) || !slices.Equal(c.Env, []string{"GOBIN=/h/.local/bin"}) {
		t.Errorf("cmd %+v", c)
	}
	n := Via{Bin: "npm", Argv: []string{"npm", "install", "-g", "--prefix", "~/.local", "pyright"}}
	if c := n.Cmd("/h"); c.Argv[4] != "/h/.local" || len(c.Env) != 0 {
		t.Errorf("cmd %+v", c)
	}
}

func TestReleaseURL(t *testing.T) {
	r := Release{Repo: "j178/prek"}
	if got := r.URL("https://github.com", "0.5.9", "x.tar.gz"); got != "https://github.com/j178/prek/releases/download/v0.5.9/x.tar.gz" {
		t.Errorf("url %q", got)
	}
}

// The asset names each project publishes, for any version.
func TestReleaseAssetNames(t *testing.T) {
	for _, tc := range []struct{ id, goos, goarch, asset, sums string }{
		{"prek", "linux", "amd64", "prek-x86_64-unknown-linux-musl.tar.gz", "prek-x86_64-unknown-linux-musl.tar.gz.sha256"},
		{"prek", "linux", "arm64", "prek-aarch64-unknown-linux-musl.tar.gz", "prek-aarch64-unknown-linux-musl.tar.gz.sha256"},
		{"prek", "darwin", "arm64", "prek-aarch64-apple-darwin.tar.gz", "prek-aarch64-apple-darwin.tar.gz.sha256"},
		{"prek", "windows", "amd64", "prek-x86_64-pc-windows-msvc.zip", "prek-x86_64-pc-windows-msvc.zip.sha256"},
		{"deja-vu", "linux", "amd64", "deja-vu_1.2.3_linux_amd64.tar.gz", "checksums.txt"},
		{"deja-vu", "darwin", "arm64", "deja-vu_1.2.3_darwin_arm64.tar.gz", "checksums.txt"},
		{"deja-vu", "windows", "arm64", "deja-vu_1.2.3_windows_arm64.zip", "checksums.txt"},
		{"grype", "linux", "arm64", "grype_1.2.3_linux_arm64.tar.gz", "grype_1.2.3_checksums.txt"},
		{"grype", "darwin", "amd64", "grype_1.2.3_darwin_amd64.tar.gz", "grype_1.2.3_checksums.txt"},
		{"grype", "windows", "amd64", "grype_1.2.3_windows_amd64.zip", "grype_1.2.3_checksums.txt"},
		{"grype", "windows", "arm64", "", ""},
		{"prek", "linux", "riscv64", "", ""},
	} {
		r := Find(tc.id).Release
		if r == nil {
			t.Fatalf("%s has no release route", tc.id)
		}
		asset, sums := r.Asset("1.2.3", tc.goos, tc.goarch)
		if asset != tc.asset || sums != tc.sums {
			t.Errorf("%s %s/%s: %q %q, want %q %q", tc.id, tc.goos, tc.goarch, asset, sums, tc.asset, tc.sums)
		}
	}
}

func TestChromeRoutes(t *testing.T) {
	c := Find("chrome")
	if c.PkgFile[APT] != "https://dl.google.com/linux/direct/google-chrome-stable_current_amd64.deb" ||
		c.PkgFile[DNF] != "https://dl.google.com/linux/direct/google-chrome-stable_current_x86_64.rpm" {
		t.Errorf("package files %v", c.PkgFile)
	}
	if !strings.Contains(c.Manual, "https://www.google.com/chrome/") || strings.Contains(c.Manual, "chromium") {
		t.Errorf("manual %q", c.Manual)
	}
}

// The CA bundle check reads the files: x509.SystemCertPool would load Go's pool once for the
// whole process, and a probe before ca-certificates is installed would cache an empty pool for
// every download the same run makes afterward.
func TestCABundleDetection(t *testing.T) {
	dir := t.TempDir()
	full := filepath.Join(dir, "full.crt")
	empty := filepath.Join(dir, "empty.crt")
	if err := os.WriteFile(full, []byte("-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		paths []string
		want  bool
	}{
		{[]string{filepath.Join(dir, "missing.crt"), full}, true},
		{[]string{empty}, false},
		{[]string{filepath.Join(dir, "missing.crt")}, false},
		{nil, false},
	} {
		if got := caBundleIn(tc.paths); got != tc.want {
			t.Errorf("caBundleIn(%v) = %v, want %v", tc.paths, got, tc.want)
		}
	}
}

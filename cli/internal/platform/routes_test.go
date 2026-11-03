package platform

import (
	"os"
	"regexp"
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
	r := Release{Repo: "j178/prek", Version: "0.5.9"}
	if got := r.URL("https://github.com", "x.tar.gz"); got != "https://github.com/j178/prek/releases/download/v0.5.9/x.tar.gz" {
		t.Errorf("url %q", got)
	}
}

// The names the projects published on 2026-11-03.
func TestReleaseAssetNames(t *testing.T) {
	for _, tc := range []struct{ id, goos, goarch, asset, sums string }{
		{"prek", "linux", "amd64", "prek-x86_64-unknown-linux-musl.tar.gz", "prek-x86_64-unknown-linux-musl.tar.gz.sha256"},
		{"prek", "linux", "arm64", "prek-aarch64-unknown-linux-musl.tar.gz", "prek-aarch64-unknown-linux-musl.tar.gz.sha256"},
		{"prek", "darwin", "arm64", "prek-aarch64-apple-darwin.tar.gz", "prek-aarch64-apple-darwin.tar.gz.sha256"},
		{"prek", "windows", "amd64", "prek-x86_64-pc-windows-msvc.zip", "prek-x86_64-pc-windows-msvc.zip.sha256"},
		{"deja-vu", "linux", "amd64", "deja-vu_0.23.5_linux_amd64.tar.gz", "checksums.txt"},
		{"deja-vu", "darwin", "arm64", "deja-vu_0.23.5_darwin_arm64.tar.gz", "checksums.txt"},
		{"deja-vu", "windows", "arm64", "deja-vu_0.23.5_windows_arm64.zip", "checksums.txt"},
		{"grype", "linux", "arm64", "grype_0.121.1_linux_arm64.tar.gz", "grype_0.121.1_checksums.txt"},
		{"grype", "darwin", "amd64", "grype_0.121.1_darwin_amd64.tar.gz", "grype_0.121.1_checksums.txt"},
		{"grype", "windows", "amd64", "grype_0.121.1_windows_amd64.zip", "grype_0.121.1_checksums.txt"},
		{"grype", "windows", "arm64", "", ""},
		{"prek", "linux", "riscv64", "", ""},
	} {
		r := Find(tc.id).Release
		if r == nil {
			t.Fatalf("%s has no release route", tc.id)
		}
		asset, sums := r.Asset(r.Version, tc.goos, tc.goarch)
		if asset != tc.asset || sums != tc.sums {
			t.Errorf("%s %s/%s: %q %q, want %q %q", tc.id, tc.goos, tc.goarch, asset, sums, tc.asset, tc.sums)
		}
	}
}

// Every pinned release version carries the marker Renovate's manager matches.
func TestReleasePinsHaveRenovateMarkers(t *testing.T) {
	b, err := os.ReadFile("platform.go")
	if err != nil {
		t.Fatal(err)
	}
	marked := regexp.MustCompile(`Version:\s*"[^"]+",?\s*// renovate: github-releases=\S+`).FindAllString(string(b), -1)
	releases := 0
	for _, p := range Prereqs {
		if p.Release != nil {
			releases++
		}
	}
	if releases == 0 || len(marked) != releases {
		t.Errorf("%d release rows, %d renovate markers", releases, len(marked))
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

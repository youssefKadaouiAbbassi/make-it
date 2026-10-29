// Package buildinfo holds the values the release build sets with -ldflags -X.
package buildinfo

import "runtime"

// Set at release time: -X makeit/internal/buildinfo.Version=1.2.3 and so on.
var (
	Version = "dev"
	Commit  = ""
	// Repo is owner/name of the GitHub repository that publishes releases. Empty in dev builds,
	// which therefore never check for or install updates.
	Repo = ""
	// KeyCurrent and KeyNext are the minisign public keys (the base64 line) that release
	// signatures must come from. Two keys allow a rotation without breaking self update.
	KeyCurrent = ""
	KeyNext    = ""
)

// Target is the release asset target name of this binary, such as linux-x64.
func Target() string { return TargetFor(runtime.GOOS, runtime.GOARCH) }

// TargetFor maps GOOS/GOARCH to the asset names the install scripts use.
func TargetFor(goos, goarch string) string {
	arch := goarch
	if goarch == "amd64" {
		arch = "x64"
	}
	return goos + "-" + arch
}

// Keys returns the trusted release keys that are set.
func Keys() []string {
	var out []string
	for _, k := range []string{KeyCurrent, KeyNext} {
		if k != "" {
			out = append(out, k)
		}
	}
	return out
}

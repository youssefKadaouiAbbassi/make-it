package claudecli

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"makeit/internal/execx"
)

// Install is one claude binary found on this machine. Source is where it was found: path,
// native, brew or npm.
type Install struct {
	Path   string
	Source string
}

// Probe is what Locate reads from the machine.
type Probe struct {
	GOOS string
	Home string
	// LookPath finds a program on PATH.
	LookPath func(string) (string, bool)
	// Exists reports whether a regular file is at the path.
	Exists func(string) bool
	// NpmPrefix is the output of `npm prefix -g`, or "" when npm is missing.
	NpmPrefix func() string
}

// Locate lists the claude binaries on this machine, the one PATH runs first. The others are
// where each install route puts it (setup.md, docs.brew.sh/FAQ, docs.npmjs.com folders), so
// setup finds a fresh install whose directory is not on PATH yet and doctor can report a second
// installation.
func Locate(p Probe) []Install {
	var found []Install
	add := func(path, source string) {
		path = filepath.Clean(path)
		for _, f := range found {
			if f.Path == path {
				return
			}
		}
		found = append(found, Install{path, source})
	}
	if path, ok := p.LookPath("claude"); ok {
		add(path, "path")
	}
	exe, shim, bin := "claude", "claude", "bin"
	if p.GOOS == "windows" {
		exe, shim, bin = "claude.exe", "claude.cmd", ""
	}
	if f := filepath.Join(p.Home, ".local", "bin", exe); p.Exists(f) {
		add(f, "native")
	}
	var brew []string
	switch p.GOOS {
	case "darwin":
		brew = []string{"/opt/homebrew", "/usr/local"}
	case "linux":
		brew = []string{"/home/linuxbrew/.linuxbrew"}
	}
	for _, prefix := range brew {
		if f := filepath.Join(prefix, "bin", "claude"); p.Exists(f) {
			add(f, "brew")
		}
	}
	if prefix := p.NpmPrefix(); prefix != "" {
		if f := filepath.Join(prefix, bin, shim); p.Exists(f) {
			add(f, "npm")
		}
	}
	return found
}

// MachineProbe reads this machine; npm is asked for its prefix only when it is on PATH.
func MachineProbe(ctx context.Context, home string) Probe {
	return Probe{
		GOOS:     runtime.GOOS,
		Home:     home,
		LookPath: execx.LookPath,
		Exists: func(path string) bool {
			st, err := os.Stat(path)
			return err == nil && st.Mode().IsRegular()
		},
		NpmPrefix: func() string {
			npm, ok := execx.LookPath("npm")
			if !ok {
				return ""
			}
			r, err := execx.Run(ctx, execx.Cmd{Argv: []string{npm, "prefix", "-g"}, Timeout: 10 * time.Second})
			if err != nil || r.Code != 0 {
				return ""
			}
			return strings.TrimSpace(r.Stdout)
		},
	}
}

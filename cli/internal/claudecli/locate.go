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

// Install is one claude binary found on this machine. Source is the install route whose
// location it is (native, brew or npm), or path for a claude on PATH at none of them. OnPath
// marks the one PATH runs.
type Install struct {
	Path   string
	Source string
	OnPath bool
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
		for i, f := range found {
			if f.Path == path {
				// PATH's claude at a route's location takes that route's name, for its upgrade hint.
				if f.Source == "path" {
					found[i].Source = source
				}
				return
			}
		}
		found = append(found, Install{Path: path, Source: source})
	}
	if path, ok := p.LookPath("claude"); ok {
		add(path, "path")
		found[0].OnPath = true
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
			// Without these npm writes a debug log under ~/.npm/_logs on every run and may check
			// the registry for a newer npm (docs.npmjs.com config: logs-max, update-notifier).
			r, err := execx.Run(ctx, execx.Cmd{Argv: []string{npm, "prefix", "-g", "--logs-max=0", "--no-update-notifier"},
				Timeout: 10 * time.Second})
			if err != nil || r.Code != 0 {
				return ""
			}
			return strings.TrimSpace(r.Stdout)
		},
	}
}

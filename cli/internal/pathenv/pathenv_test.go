package pathenv

import (
	"path/filepath"
	"slices"
	"testing"
)

func exists(paths ...string) func(string) bool {
	return func(p string) bool { return slices.Contains(paths, p) }
}

func env(kv map[string]string) func(string) string { return func(k string) string { return kv[k] } }

func TestDirs(t *testing.T) {
	h := "/h"
	got := Dirs(h, env(map[string]string{"HOME": h}), exists("/h/go/bin"))
	if want := []string{"/h/.local/bin", "/h/go/bin"}; !slices.Equal(got, want) {
		t.Errorf("dirs %v, want %v", got, want)
	}
	got = Dirs(h, env(map[string]string{"HOME": h, "GOBIN": "/opt/gobin", "CARGO_HOME": "/opt/cargo"}), exists("/opt/gobin", "/opt/cargo/bin"))
	if want := []string{"/h/.local/bin", "/opt/gobin", "/opt/cargo/bin"}; !slices.Equal(got, want) {
		t.Errorf("dirs %v, want %v", got, want)
	}
}

func TestEnvFile(t *testing.T) {
	got := string(EnvFile([]string{"/h/.local/bin", "/h/go/bin", "/opt/cargo/bin"}, "/h"))
	want := "# written by makeit: folders where it installs programs\n" +
		`case ":$PATH:" in *":$HOME/.local/bin:"*) ;; *) PATH="$HOME/.local/bin:$PATH";; esac` + "\n" +
		`case ":$PATH:" in *":$HOME/go/bin:"*) ;; *) PATH="$HOME/go/bin:$PATH";; esac` + "\n" +
		`case ":$PATH:" in *":/opt/cargo/bin:"*) ;; *) PATH="/opt/cargo/bin:$PATH";; esac` + "\n" +
		"export PATH\n"
	if got != want {
		t.Errorf("env file:\n%s\nwant:\n%s", got, want)
	}
}

func TestProfiles(t *testing.T) {
	h := "/h"
	for _, tc := range []struct {
		name, shell string
		have        []string
		getenv      map[string]string
		want        []string
	}{
		{"bash, nothing yet", "/bin/bash", nil, nil, []string{"/h/.profile"}},
		{"bash with bashrc", "/bin/bash", []string{"/h/.bashrc"}, nil, []string{"/h/.profile", "/h/.bashrc"}},
		{"zsh", "/usr/bin/zsh", nil, nil, []string{"/h/.profile", "/h/.zshenv"}},
		{"zsh with ZDOTDIR", "/usr/bin/zsh", nil, map[string]string{"ZDOTDIR": "/h/.config/zsh"}, []string{"/h/.profile", "/h/.config/zsh/.zshenv"}},
		{"bash with an existing zshenv", "/bin/bash", []string{"/h/.zshenv"}, nil, []string{"/h/.profile", "/h/.zshenv"}},
		{"fish installed", "/usr/bin/fish", []string{"/h/.config/fish"}, nil, []string{"/h/.profile", "/h/.config/fish/conf.d/makeit.fish"}},
		// bash reads .bash_profile instead of .profile at login when it exists.
		{"bash_profile", "/bin/bash", []string{"/h/.bash_profile"}, nil, []string{"/h/.profile", "/h/.bash_profile"}},
	} {
		var paths []string
		for _, p := range Profiles(h, filepath.Join(h, ".makeit", "env"), tc.shell, env(tc.getenv), exists(tc.have...)) {
			paths = append(paths, p.Path)
		}
		if !slices.Equal(paths, tc.want) {
			t.Errorf("%s: %v, want %v", tc.name, paths, tc.want)
		}
	}
	ps := Profiles(h, "/h/.makeit/env", "/usr/bin/fish", env(nil), exists("/h/.config/fish"))
	// Guarded: a profile must never fail (dash would exit a login shell) once the env file is gone.
	if sh := ps[0].Lines; !slices.Equal(sh, []string{"# >>> makeit >>>", `[ -r "$HOME/.makeit/env" ] && . "$HOME/.makeit/env"`, "# <<< makeit <<<"}) {
		t.Errorf("sh lines %v", sh)
	}
	// The fish file is a block between the same markers, the folders quoted inside.
	if want := []string{"# >>> makeit >>>", `fish_add_path -g "/h/.local/bin"`, "# <<< makeit <<<"}; !slices.Equal(ps[1].Lines, want) {
		t.Errorf("fish lines %v, want %v", ps[1].Lines, want)
	}
}

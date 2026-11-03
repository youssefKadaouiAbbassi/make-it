// Package pathenv is how what makeit installs gets on PATH: one env file listing the folders,
// loaded from the shell profile by one managed block (rustup's way).
package pathenv

import (
	"path/filepath"
	"strings"

	"makeit/internal/platform"
)

// Dirs are the folders the env file puts on PATH: ~/.local/bin always, and the go and cargo
// binary folders when they exist (tools the user installed with those toolchains).
func Dirs(home string, getenv func(string) string, exists func(string) bool) []string {
	dirs := []string{filepath.Join(home, ".local", "bin")}
	for _, v := range []platform.Via{{Bin: "go"}, {Bin: "rustup"}} {
		d := v.Into(getenv)
		if d == "" {
			d = filepath.Join(home, map[string]string{"go": "go", "rustup": ".cargo"}[v.Bin], "bin")
		}
		if exists(d) {
			dirs = append(dirs, d)
		}
	}
	return dirs
}

// HomeRef writes p with $HOME for the home directory, so the file reads the same on every machine.
func HomeRef(p, home string) string {
	if rel, err := filepath.Rel(home, p); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "$HOME/" + filepath.ToSlash(rel)
	}
	return filepath.ToSlash(p)
}

// EnvFile is the POSIX sh file that prepends each of dirs to PATH once.
func EnvFile(dirs []string, home string) []byte {
	var b strings.Builder
	b.WriteString("# written by makeit: folders where it installs programs\n")
	for _, d := range dirs {
		ref := HomeRef(d, home)
		b.WriteString(`case ":$PATH:" in *":` + ref + `:"*) ;; *) PATH="` + ref + `:$PATH";; esac` + "\n")
	}
	b.WriteString("export PATH\n")
	return []byte(b.String())
}

// Profile is one shell file and the lines it gets.
type Profile struct {
	Path  string
	Lines []string
	Fish  bool
}

// Block is the managed block for an sh-style profile: it loads the env file at envPath when it
// is there. Guarded, because a profile must never fail: dash exits a login shell whose `.` fails,
// and the env file can be gone (a rollback, a deleted ~/.makeit) while the block stays.
func Block(envPath, home string) []string {
	ref := HomeRef(envPath, home)
	return []string{"# >>> makeit >>>", `[ -r "` + ref + `" ] && . "` + ref + `"`, "# <<< makeit <<<"}
}

// Profiles are the shell files to load the env file from: ~/.profile always; ~/.bashrc when it
// exists; .zshenv (under $ZDOTDIR when set) when zsh is the login shell or the file exists; a
// fish conf.d file when fish is set up, since fish cannot read the sh file.
func Profiles(home, envPath, shell string, getenv func(string) string, exists func(string) bool) []Profile {
	block := Block(envPath, home)
	out := []Profile{{Path: filepath.Join(home, ".profile"), Lines: block}}
	for _, name := range []string{".bashrc", ".bash_profile"} { // bash reads .bash_profile at login instead of .profile
		if p := filepath.Join(home, name); exists(p) {
			out = append(out, Profile{Path: p, Lines: block})
		}
	}
	zdot := getenv("ZDOTDIR")
	if zdot == "" {
		zdot = home
	}
	if zshenv := filepath.Join(zdot, ".zshenv"); filepath.Base(shell) == "zsh" || exists(zshenv) {
		out = append(out, Profile{Path: zshenv, Lines: block})
	}
	if fish := filepath.Join(home, ".config", "fish"); exists(fish) {
		lines := []string{"# written by makeit: folders where it installs programs"}
		for _, d := range Dirs(home, getenv, exists) {
			lines = append(lines, `fish_add_path -g "`+d+`"`)
		}
		out = append(out, Profile{Path: filepath.Join(fish, "conf.d", "makeit.fish"), Lines: lines, Fish: true})
	}
	return out
}

package catsrc

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	dir := t.TempDir()
	sha := strings.Repeat("ab", 20)
	cases := []struct {
		arg         string
		allowBranch bool
		want        Spec
		err         error // ErrCredentials, or errAny for any other error
	}{
		{"embedded", false, Spec{Kind: "embedded"}, nil},
		{"owner/repo", false, Spec{Kind: "git", Location: "https://github.com/owner/repo.git"}, nil},
		// owner/repo.git that is no local directory is GitHub, with the guess kept for the plan line.
		{"owner/repo.git", false, Spec{Kind: "git", Location: "https://github.com/owner/repo.git", Guessed: "owner/repo.git"}, nil},
		{"owner/repo#v1.0.0", false, Spec{Kind: "git", Location: "https://github.com/owner/repo.git", Ref: "v1.0.0"}, nil},
		{"owner/repo@" + sha, false, Spec{Kind: "git", Location: "https://github.com/owner/repo.git", Ref: sha, Commit: true}, nil},
		{"https://h/x.git#main", true, Spec{Kind: "git", Location: "https://h/x.git", Ref: "main", AllowBranch: true}, nil},
		{"git@h:o/r.git#v1", false, Spec{Kind: "git", Location: "git@h:o/r.git", Ref: "v1"}, nil},
		{"ssh://git@h/o/r.git", false, Spec{Kind: "git", Location: "ssh://git@h/o/r.git"}, nil},
		{dir, false, Spec{Kind: "directory", Location: dir}, nil},
		{"http://h/x.git", false, Spec{}, errAny},
		{"https://u:t@h/x.git", false, Spec{}, ErrCredentials},
		{"https://t@h/x.git", false, Spec{}, ErrCredentials},
		{"https://h/x.git?token=1", false, Spec{}, ErrCredentials},
		{"ssh://u:pw@h/x.git", false, Spec{}, ErrCredentials},
		{"/no/such/dir", false, Spec{}, errAny},
		{"--upload-pack=touch x;.git", false, Spec{}, errAny},
		{"owner/repo@abc", false, Spec{}, errAny},
		{"owner/repo#", false, Spec{}, errAny},
		{"embedded", true, Spec{}, errAny},
		{"owner/repo", true, Spec{}, errAny},
	}
	for _, c := range cases {
		got, err := Parse(c.arg, c.allowBranch)
		switch {
		case c.err == nil && (err != nil || got != c.want):
			t.Errorf("%q: %+v %v, want %+v", c.arg, got, err, c.want)
		case c.err == ErrCredentials && !errors.Is(err, ErrCredentials):
			t.Errorf("%q: %v, want ErrCredentials", c.arg, err)
		case c.err == errAny && err == nil:
			t.Errorf("%q: parsed as %+v, want an error", c.arg, got)
		}
	}
	// A local path git could only read through file://, which makeit's git refuses: say what is wrong
	// with the path, not that a credential is missing.
	file := filepath.Join(t.TempDir(), "x.git")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for arg, want := range map[string]string{"../no-such/cat.git": "no such directory: ../no-such/cat.git",
		"host.example.com/repo.git": "no such directory: host.example.com/repo.git", file: "not a directory: " + file} {
		if _, err := Parse(arg, false); err == nil || err.Error() != want {
			t.Errorf("%q: %v, want %q", arg, err, want)
		}
	}
	if _, err := Parse("https://u:t@h/x.git", false); err == nil || !strings.Contains(err.Error(), "credential helper") {
		t.Errorf("credentials message: %v", err)
	}
}

var errAny = errors.New("any error")

// A relative directory shaped like owner/repo is a directory, never GitHub.
func TestParseDirectoryBeforeGitHub(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.MkdirAll("catalogs/mine", 0o755); err != nil {
		t.Fatal(err)
	}
	if s, err := Parse("catalogs/mine", false); err != nil || s.Kind != "directory" {
		t.Errorf("%+v %v, want a directory", s, err)
	}
}

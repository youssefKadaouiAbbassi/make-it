// Package catsrc is where a catalog comes from (cli/CATALOG-GIT.md): the --catalog value, the
// remote's refs and the ref rules, a hardened fetch of one commit read as data, the identity
// checks, and the catalog a run plans with.
package catsrc

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Spec is a parsed --catalog value.
type Spec struct {
	// Kind is embedded, directory or git.
	Kind string
	// Location is the git URL or the absolute directory.
	Location string
	// Ref is the #tag or #branch, or the @commit; "" means the newest release tag.
	Ref         string
	Commit      bool
	AllowBranch bool
	// Guessed is the argument when it was read as GitHub's owner/repo for lack of a local path.
	Guessed string
}

// ErrCredentials is a URL that carries a secret, which state.json would record.
var ErrCredentials = errors.New("remove the credentials from the URL: makeit records it in ~/.makeit/state.json; use a git credential helper or an SSH key")

var (
	// A GitHub owner has no dot (so no "..", "." or host name); a repo is more than dots.
	ownerRepo = regexp.MustCompile(`^[\w-]+/[\w.-]*[\w-][\w.-]*$`)
	commitRef = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)
)

// Parse reads a --catalog value: embedded, an existing directory, owner/repo (GitHub), or an
// https or ssh git URL, a git source taking #<tag or branch> or @<commit>.
func Parse(arg string, allowBranch bool) (Spec, error) {
	if arg == "embedded" {
		if allowBranch {
			return Spec{}, errors.New("--allow-branch is for a git source's #<branch>")
		}
		return Spec{Kind: "embedded"}, nil
	}
	if fi, err := os.Stat(arg); err == nil && fi.IsDir() {
		abs, err := filepath.Abs(arg)
		if err != nil {
			return Spec{}, err
		}
		if allowBranch {
			return Spec{}, errors.New("--allow-branch is for a git source's #<branch>")
		}
		return Spec{Kind: "directory", Location: abs}, nil
	}
	s := Spec{Kind: "git", AllowBranch: allowBranch}
	loc := arg
	if i := strings.LastIndex(loc, "@"); i >= 0 && commitRef.MatchString(loc[i+1:]) {
		loc, s.Ref, s.Commit = loc[:i], strings.ToLower(loc[i+1:]), true
	} else if i := strings.LastIndex(loc, "#"); i >= 0 {
		if loc, s.Ref = loc[:i], loc[i+1:]; s.Ref == "" {
			return Spec{}, fmt.Errorf("%s: no ref after #", arg)
		}
	}
	switch {
	case strings.HasPrefix(loc, "-"):
		return Spec{}, fmt.Errorf("%s is not a plugin set source", arg)
	case strings.HasPrefix(loc, "http://"):
		return Spec{}, fmt.Errorf("%s: a git source needs https or ssh, not http", arg)
	case strings.HasPrefix(loc, "https://"), strings.HasPrefix(loc, "ssh://"):
		u, err := url.Parse(loc)
		if err != nil || u.Host == "" {
			return Spec{}, fmt.Errorf("%s is not a git URL", arg)
		}
		if _, hasPassword := u.User.Password(); u.RawQuery != "" || hasPassword || (u.Scheme == "https" && u.User != nil) {
			return Spec{}, ErrCredentials
		}
		s.Location = loc
	case strings.HasSuffix(loc, ".git") && ownerRepo.MatchString(loc):
		// Not an existing directory (checked above): GitHub, named in full before the fetch.
		s.Location, s.Guessed = "https://github.com/"+loc, loc
	case strings.HasSuffix(loc, ".git") && !strings.HasPrefix(loc, "git@") && !strings.Contains(loc, "://") && !scpLike(loc):
		// A local path: makeit's git reads only https and ssh, so name what is wrong with the path.
		if fi, err := os.Stat(loc); err == nil && !fi.IsDir() {
			return Spec{}, fmt.Errorf("not a directory: %s", loc)
		}
		return Spec{}, fmt.Errorf("no such directory: %s", loc)
	case strings.HasPrefix(loc, "git@"), strings.HasSuffix(loc, ".git"):
		if user, _, ok := strings.Cut(loc, "@"); ok && strings.Contains(user, ":") {
			return Spec{}, ErrCredentials
		}
		s.Location = loc
	case ownerRepo.MatchString(loc):
		s.Location = "https://github.com/" + loc + ".git"
	case strings.Contains(arg, "@") && ownerRepo.MatchString(arg[:strings.LastIndex(arg, "@")]):
		return Spec{}, fmt.Errorf("%s: pin a commit with @ and its 40 hex characters", arg)
	default:
		return Spec{}, fmt.Errorf("%s is not a directory, owner/repo or git URL", arg)
	}
	if s.AllowBranch && (s.Ref == "" || s.Commit) {
		return Spec{}, errors.New("--allow-branch needs #<branch>")
	}
	return s, nil
}

// scpLike is git's host:path form (a colon before the first slash), an ssh source.
func scpLike(loc string) bool {
	i := strings.Index(loc, ":")
	return i > 0 && !strings.Contains(loc[:i], "/")
}

package catsrc

import (
	"errors"
	"fmt"
	"regexp"

	"makeit/internal/claudecli"
	"makeit/internal/state"
)

// Refs are a remote's tags (peeled to their commits) and branch heads.
type Refs struct {
	Tags, Heads map[string]string
}

// Choice is the commit a run takes from a git catalog, and what it records.
type Choice struct {
	Commit string
	// Requested is "latest", the tag, the branch or the commit; RefKind is latest, tag, branch or
	// commit; Tag is the tag the commit came from.
	Requested, RefKind, Tag string
	// Hold keeps the recorded commit: its tag now points elsewhere.
	Hold bool
}

var releaseTag = regexp.MustCompile(`^v?\d+\.\d+\.\d+$`)

// newest is the highest release tag (vX.Y.Z or X.Y.Z, no prerelease), or "".
func newest(tags map[string]string) string {
	var best string
	for name := range tags {
		if !releaseTag.MatchString(name) {
			continue
		}
		if n, err := claudecli.Compare(name, best); best == "" || err == nil && n > 0 {
			best = name
		}
	}
	return best
}

// Choose applies the ref rules of cli/CATALOG-GIT.md. A fresh resolution (explicit --catalog, no
// record, or another location) takes what the remote says now. A plain update keeps a pin, moves
// "latest" to a newer tag and a branch to its head, and holds a recorded tag that now points
// elsewhere. The notices are lines for update's notice.
func Choose(s Spec, refs Refs, rec *state.Catalog, explicit bool) (Choice, []string, error) {
	if explicit || rec == nil || rec.Kind != "git" || rec.Location != s.Location {
		return fresh(s, refs)
	}
	accept := "makeit update --catalog " + s.Location
	cur := Choice{Commit: rec.Resolved, Requested: deref(rec.Requested), RefKind: rec.RefKind, Tag: rec.Tag}
	top := newest(refs.Tags)
	switch rec.RefKind {
	case "latest":
		if top == "" {
			return Choice{}, nil, fmt.Errorf("%s has no release tag left", s.Location)
		}
		if n, err := claudecli.Compare(top, rec.Tag); err == nil && n > 0 {
			return Choice{Commit: refs.Tags[top], Requested: "latest", RefKind: "latest", Tag: top}, nil, nil
		} else if err == nil && n < 0 {
			return Choice{}, nil, fmt.Errorf("catalog tag %s is gone from %s; the newest is %s", rec.Tag, s.Location, top)
		}
		if refs.Tags[top] != rec.Resolved {
			cur.Hold = true
			return cur, []string{moved(rec.Tag, refs.Tags[top], rec.Resolved, accept)}, nil
		}
		return cur, nil, nil
	case "tag":
		at, ok := refs.Tags[rec.Tag]
		if !ok {
			return Choice{}, nil, fmt.Errorf("catalog tag %s is gone from %s", rec.Tag, s.Location)
		}
		var notices []string
		if at != rec.Resolved {
			cur.Hold = true
			notices = append(notices, moved(rec.Tag, at, rec.Resolved, accept+"#"+rec.Tag))
		}
		if n, err := claudecli.Compare(top, rec.Tag); top != "" && err == nil && n > 0 {
			notices = append(notices, newer(top, rec.Tag, accept))
		}
		return cur, notices, nil
	case "commit":
		if top != "" && refs.Tags[top] != rec.Resolved {
			return cur, []string{newer(top, rec.Resolved[:min(12, len(rec.Resolved))], accept)}, nil
		}
		return cur, nil, nil
	case "branch":
		head, ok := refs.Heads[cur.Requested]
		if !ok {
			return Choice{}, nil, fmt.Errorf("catalog branch %s is gone from %s", cur.Requested, s.Location)
		}
		cur.Commit = head
		return cur, nil, nil
	}
	return fresh(s, refs)
}

func fresh(s Spec, refs Refs) (Choice, []string, error) {
	switch {
	case s.Commit:
		return Choice{Commit: s.Ref, Requested: s.Ref, RefKind: "commit"}, nil, nil
	case s.Ref == "":
		top := newest(refs.Tags)
		if top == "" {
			return Choice{}, nil, fmt.Errorf("%s has no release tag (vX.Y.Z): pin a ref with #<tag>, or follow a branch with #<branch> --allow-branch", s.Location)
		}
		return Choice{Commit: refs.Tags[top], Requested: "latest", RefKind: "latest", Tag: top}, nil, nil
	}
	if at, ok := refs.Tags[s.Ref]; ok {
		return Choice{Commit: at, Requested: s.Ref, RefKind: "tag", Tag: s.Ref}, nil, nil
	}
	if head, ok := refs.Heads[s.Ref]; ok {
		if !s.AllowBranch {
			return Choice{}, nil, fmt.Errorf("%s is a branch of %s: makeit follows a branch only with --allow-branch", s.Ref, s.Location)
		}
		return Choice{Commit: head, Requested: s.Ref, RefKind: "branch"}, nil, nil
	}
	return Choice{}, nil, fmt.Errorf("%s is neither a tag nor a branch of %s", s.Ref, s.Location)
}

func moved(tag, now, recorded, command string) string {
	return fmt.Sprintf("catalog tag %s now points to %s, not %s: `%s` accepts it", tag, now, recorded, command)
}

func newer(top, recorded, command string) string {
	return fmt.Sprintf("catalog %s is newer than %s: `%s` takes it (and follows newer tags)", top, recorded, command)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

var errFetchByID = errors.New("the server does not allow fetching a commit by its id: pin a tag instead (`#<tag>`)")

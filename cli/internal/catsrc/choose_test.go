package catsrc

import (
	"strings"
	"testing"

	"makeit/internal/state"
)

func TestChoose(t *testing.T) {
	c := func(n string) string { return strings.Repeat(n, 40) }
	const url = "https://h/cat.git"
	refs := Refs{
		Tags:  map[string]string{"v1.0.0": c("a"), "v1.2.0": c("b"), "v1.10.0": c("d"), "v2.0.0-rc1": c("e"), "light": c("f")},
		Heads: map[string]string{"main": c("9")},
	}
	git := func(ref string, commit, allow bool) Spec {
		return Spec{Kind: "git", Location: url, Ref: ref, Commit: commit, AllowBranch: allow}
	}
	rec := func(requested, kind, tag, resolved string) *state.Catalog {
		return &state.Catalog{Kind: "git", Location: url, Requested: &requested, RefKind: kind, Tag: tag, Resolved: resolved}
	}
	cases := []struct {
		name     string
		spec     Spec
		refs     Refs
		rec      *state.Catalog
		explicit bool
		want     Choice
		err      string
		notice   []string // substrings, one per notice
	}{
		{"fresh latest", git("", false, false), refs, nil, true, Choice{Commit: c("d"), Requested: "latest", RefKind: "latest", Tag: "v1.10.0"}, "", nil},
		{"no release tags", git("", false, false), Refs{Tags: map[string]string{"light": c("f")}}, nil, true, Choice{}, "--allow-branch", nil},
		{"a tag", git("v1.0.0", false, false), refs, nil, true, Choice{Commit: c("a"), Requested: "v1.0.0", RefKind: "tag", Tag: "v1.0.0"}, "", nil},
		{"a branch without --allow-branch", git("main", false, false), refs, nil, true, Choice{}, "--allow-branch", nil},
		{"a branch", git("main", false, true), refs, nil, true, Choice{Commit: c("9"), Requested: "main", RefKind: "branch"}, "", nil},
		{"a commit", git(c("7"), true, false), refs, nil, true, Choice{Commit: c("7"), Requested: c("7"), RefKind: "commit"}, "", nil},
		{"neither tag nor branch", git("nope", false, false), refs, nil, true, Choice{}, "neither a tag nor a branch", nil},
		{"latest moves to a newer tag", git("", false, false), refs, rec("latest", "latest", "v1.2.0", c("b")), false,
			Choice{Commit: c("d"), Requested: "latest", RefKind: "latest", Tag: "v1.10.0"}, "", nil},
		{"latest whose tag moved holds", git("", false, false),
			Refs{Tags: map[string]string{"v1.2.0": c("c")}}, rec("latest", "latest", "v1.2.0", c("b")), false,
			Choice{Commit: c("b"), Requested: "latest", RefKind: "latest", Tag: "v1.2.0", Hold: true}, "",
			[]string{"catalog tag v1.2.0 now points to " + c("c") + ", not " + c("b") + ": `makeit update --catalog " + url + "` accepts it"}},
		{"a pinned tag that moved holds", git("v1.0.0", false, false),
			Refs{Tags: map[string]string{"v1.0.0": c("c")}}, rec("v1.0.0", "tag", "v1.0.0", c("a")), false,
			Choice{Commit: c("a"), Requested: "v1.0.0", RefKind: "tag", Tag: "v1.0.0", Hold: true}, "",
			[]string{"`makeit update --catalog " + url + "#v1.0.0` accepts it"}},
		{"a pinned tag with a newer one stays", git("v1.0.0", false, false), refs, rec("v1.0.0", "tag", "v1.0.0", c("a")), false,
			Choice{Commit: c("a"), Requested: "v1.0.0", RefKind: "tag", Tag: "v1.0.0"}, "",
			[]string{"catalog v1.10.0 is newer than v1.0.0: `makeit update --catalog " + url + "` takes it (and follows newer tags)"}},
		// An untagged pin may be ahead of the newest tag: ls-remote holds no history, so no claim.
		{"an untagged commit pin", git(c("7"), true, false), refs, rec(c("7"), "commit", "", c("7")), false,
			Choice{Commit: c("7"), Requested: c("7"), RefKind: "commit"}, "", nil},
		{"a commit pin at an older release", git(c("a"), true, false), refs, rec(c("a"), "commit", "", c("a")), false,
			Choice{Commit: c("a"), Requested: c("a"), RefKind: "commit"}, "", []string{"catalog v1.10.0 is newer than v1.0.0"}},
		{"a branch follows its head", git("main", false, true), refs, rec("main", "branch", "", c("8")), false,
			Choice{Commit: c("9"), Requested: "main", RefKind: "branch"}, "", nil},
		{"latest whose tag is gone for an older one", git("", false, false),
			Refs{Tags: map[string]string{"v1.0.0": c("a")}}, rec("latest", "latest", "v1.2.0", c("b")), false, Choice{}, "gone", nil},
		{"explicit re-resolves the same record", git("v1.0.0", false, false),
			Refs{Tags: map[string]string{"v1.0.0": c("c")}}, rec("v1.0.0", "tag", "v1.0.0", c("a")), true,
			Choice{Commit: c("c"), Requested: "v1.0.0", RefKind: "tag", Tag: "v1.0.0"}, "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, notices, err := Choose(tc.spec, tc.refs, tc.rec, tc.explicit)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("err %v, want one saying %q", err, tc.err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("%+v %v, want %+v", got, err, tc.want)
			}
			if len(notices) != len(tc.notice) {
				t.Fatalf("notices %q, want %d", notices, len(tc.notice))
			}
			for i, n := range tc.notice {
				if !strings.Contains(notices[i], n) {
					t.Errorf("notice %q does not say %q", notices[i], n)
				}
			}
		})
	}
}

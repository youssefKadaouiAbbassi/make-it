package initrepo

import (
	"slices"

	"makeit/internal/catalog"
)

// MergeSeed is one file init merges into rather than writes: Apply returns theirs with what the
// stack adds, leaving out the entries in skip (added by an earlier run and since removed).
// Template names what the additions come from.
type MergeSeed struct {
	Path, Template string
	Apply          func(theirs []byte, skip map[string]bool) (Merged, error)
}

// SeedReport is what Seeds could not do itself: Manual lines for the agent step (exact text to
// add to files makeit does not edit) and Notices for the user.
type SeedReport struct {
	Manual  []string
	Notices []string
}

// checkStubs are the check tasks a project gets when it has none; each fails and says what to
// wire, so a red check:full is honest until the agent step wires the real tools.
var checkStubs = [][2]string{
	{"check:fast", `echo "check:fast: wire the formatter, linter and type checker" && exit 1`},
	{"check:task", `echo "check:task: wire check:fast and the related tests" && exit 1`},
	{"check:full", `echo "check:full: wire the linter, type checker and tests" && exit 1`},
}

// envLines keep real environment files out of git.
var envLines = []string{".env", ".env.*", "!.env.example"}

// Seeds are the files init writes or merges for p beyond the docs (cli/INIT.md, CLI items 4-9).
func Seeds(p Project, src catalog.Source, req catalog.Requires) ([]Seed, []MergeSeed, SeedReport) {
	var seeds []Seed
	var merges []MergeSeed
	var rep SeedReport
	if slices.Contains(p.Types, "node") {
		merges = append(merges, MergeSeed{Path: "package.json", Template: "makeit:check-stubs",
			Apply: func(theirs []byte, skip map[string]bool) (Merged, error) {
				return PackageScripts(theirs, checkStubs, skip)
			}})
	}
	merges = append(merges, MergeSeed{Path: ".gitignore", Template: "makeit:env-lines",
		Apply: func(theirs []byte, skip map[string]bool) (Merged, error) {
			return MergeLines(theirs, envLines, skip), nil
		}})
	return seeds, merges, rep
}

package initrepo

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"

	"makeit/internal/catalog"
	"makeit/internal/latest"
)

// ChangesPath is the list of init changes per version in the plugin set.
const ChangesPath = "templates/project/init-changes.json"

// SkillPath is the init skill, whose steps the list also follows.
const SkillPath = "plugins/makeit/skills/init/SKILL.md"

// Change is one change to how init sets a project up: the end state /makeit:init brings an older
// project to, so applying it twice is harmless.
type Change struct {
	Version string   `json:"version"`
	ID      string   `json:"id"`
	Files   []string `json:"files"`
	Prompt  string   `json:"prompt"`
}

// LoadChanges reads the list: the hash of the templates and the skill it was written against,
// and the changes in version order.
func LoadChanges(src catalog.Source) (sources string, all []Change, err error) {
	b, err := src.ReadFile(ChangesPath)
	if err != nil {
		return "", nil, fmt.Errorf("read %s: %w", ChangesPath, err)
	}
	var f struct {
		Sources string   `json:"sources"`
		Changes []Change `json:"changes"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return "", nil, fmt.Errorf("parse %s: %w", ChangesPath, err)
	}
	return f.Sources, f.Changes, nil
}

// Pending are the changes newer than the version a project was brought up to; all of them when
// it has none (set up before the list).
func Pending(all []Change, initVersion string) []Change {
	out := []Change{}
	for _, c := range all {
		if initVersion == "" || latest.Newer(initVersion, c.Version) {
			out = append(out, c)
		}
	}
	return out
}

// Newest is the version of the last change, or "" for none.
func Newest(all []Change) string {
	if len(all) == 0 {
		return ""
	}
	return all[len(all)-1].Version
}

// SourcesHash hashes the files the list follows (path, length and content, in path order); the list
// itself is left out.
func SourcesHash(files map[string][]byte) string {
	paths := make([]string, 0, len(files))
	for p := range files {
		if p != ChangesPath {
			paths = append(paths, p)
		}
	}
	slices.Sort(paths)
	h := sha256.New()
	for _, p := range paths {
		fmt.Fprintf(h, "%s\x00%d\x00", p, len(files[p]))
		h.Write(files[p])
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

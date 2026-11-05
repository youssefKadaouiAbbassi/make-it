package setup

import (
	"bytes"
	"context"
	"path/filepath"
	"slices"
	"strings"

	"makeit/internal/catalog"
	"makeit/internal/fsx"
	"makeit/internal/plan"
	"makeit/internal/state"
)

// globalSteps write the catalog's global files (templates/global/) into Claude Code's
// configuration directory under the same names. A file already there is the user's: adopted,
// recorded and never changed.
func (e *env) globalSteps() []plan.Step {
	// The files come from the catalog in use: a git or directory catalog may hold fewer or more.
	files, err := catalog.List(e.Catalog, "templates/global/")
	if err != nil {
		return []plan.Step{{ID: "global", Title: "Write the global files",
			Already: func(context.Context) (bool, error) { return false, err }}}
	}
	var steps []plan.Step
	for _, src := range files {
		rel := strings.TrimPrefix(src, "templates/global/")
		path := filepath.Join(e.Roots.Claude, filepath.FromSlash(rel))
		key := e.Roots.Portable(path)
		data, rerr := e.Catalog.ReadFile(src)
		detail := ""
		if rerr == nil {
			detail = "+ " + strings.ReplaceAll(strings.TrimRight(string(data), "\n"), "\n", "\n+ ")
		}
		steps = append(steps, plan.Step{ID: "global." + rel, Title: "Write " + path, Detail: detail,
			Already: func(context.Context) (bool, error) {
				cur, found, err := fsx.ReadOptional(path)
				if err != nil || !found {
					return false, err
				}
				e.st.Files = initMap(e.st.Files)
				rec := e.st.Files[key]
				if rec == nil {
					rec = &state.File{Class: "owned", Source: src, PreExisted: true}
					e.st.Files[key] = rec
				}
				if handBack(rec, cur, data, rerr) {
					e.recordChanged = true
				}
				return true, nil
			},
			Run: func(context.Context) (string, error) {
				if rerr != nil {
					return "", rerr
				}
				// Owner-only: the mode is set exactly, not filtered by umask (see landing).
				if err := e.write("global."+rel, path, data, 0o600); err != nil {
					return "", err
				}
				e.st.Files = initMap(e.st.Files)
				e.st.Files[key] = &state.File{Class: "owned", Source: src, Sha256: fsx.Sha256(data), Generation: e.st.Generation}
				return "", nil
			}})
	}
	return steps
}

//go:generate go run ../../tools/globalhistory -repo ../../.. -out globalhistory_gen.go

// handBack lets makeit's updates reach a global file that was there before makeit once it holds
// makeit's text: the running version, or any earlier one (globalHistory). Its sha is recorded, so
// update sees the file unchanged and writes the current version. PreExisted stays: uninstall still
// leaves the file, which the user had before makeit. A file with any other text stays the user's.
// It reports whether the record changed, so a run with nothing else to do still saves it.
func handBack(rec *state.File, cur, want []byte, wantErr error) bool {
	if !rec.PreExisted || wantErr != nil {
		return false
	}
	sha := fsx.Sha256(cur)
	if rec.Sha256 == sha || (!bytes.Equal(cur, want) && !slices.Contains(globalHistory[rec.Source], sha)) {
		return false
	}
	rec.Sha256 = sha
	return true
}

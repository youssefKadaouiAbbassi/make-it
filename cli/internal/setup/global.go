package setup

import (
	"bytes"
	"context"
	"path/filepath"
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
	m, err := catalog.EmbeddedManifest()
	if err != nil {
		return []plan.Step{{ID: "global", Title: "Write the global files",
			Already: func(context.Context) (bool, error) { return false, err }}}
	}
	var steps []plan.Step
	for _, f := range m.Files {
		rel, ok := strings.CutPrefix(f.Path, "templates/global/")
		if !ok {
			continue
		}
		src := f.Path
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
				handBack(rec, cur, data, rerr)
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

// handBack lets makeit's updates reach a global file that was there before makeit once it is
// byte-identical to makeit's version: the sha is recorded, so the next release sees the file
// unchanged and writes the new version. PreExisted stays: uninstall still leaves the file, which
// the user had before makeit. A file with any difference stays the user's.
func handBack(rec *state.File, cur, want []byte, wantErr error) {
	if rec.PreExisted && rec.Sha256 == "" && wantErr == nil && bytes.Equal(cur, want) {
		rec.Sha256 = fsx.Sha256(want)
	}
}

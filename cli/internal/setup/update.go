package setup

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"makeit/internal/catalog"
	"makeit/internal/fsx"
	"makeit/internal/plan"
	"makeit/internal/state"
)

// Update brings the machine to the stack this binary carries (cli/UPDATE-UNINSTALL.md): setup's
// steps with update's rules for what makeit owns, then the plugin phase, which keeps going past a
// failed plugin and never undoes the earlier steps.
func Update(ctx context.Context, e Env, mode plan.Mode, confirm func([]plan.Outcome) (bool, error), argv []string) (Report, error) {
	rep, _, err := execute(ctx, e, "update", mode, confirm, argv, func(en *env) []phase {
		return []phase{{steps: en.setupSteps(en.updateGlobalSteps())}, {steps: en.pluginSteps(), keepGoing: true}}
	})
	return rep, err
}

// updateGlobalSteps are update's global-file steps. A new catalog file is written. An owned file
// nobody changed is replaced by a newer catalog version, or removed when the catalog dropped it.
// A file the user changed, removed or had before makeit is kept and reported.
func (e *env) updateGlobalSteps() []plan.Step {
	m, err := catalog.EmbeddedManifest()
	if err != nil {
		return []plan.Step{{ID: "global", Title: "Update the global files",
			Already: func(context.Context) (bool, error) { return false, err }}}
	}
	var steps []plan.Step
	inCatalog := map[string]bool{}
	for _, f := range m.Files {
		rel, ok := strings.CutPrefix(f.Path, "templates/global/")
		if !ok {
			continue
		}
		src := f.Path
		path := filepath.Join(e.Roots.Claude, filepath.FromSlash(rel))
		key := e.Roots.Portable(path)
		inCatalog[key] = true
		want, rerr := e.Catalog.ReadFile(src)
		steps = append(steps, plan.Step{ID: "global." + rel, Title: "Update " + path,
			Detail: "+ " + strings.ReplaceAll(strings.TrimRight(string(want), "\n"), "\n", "\n+ "),
			Already: func(context.Context) (bool, error) {
				if rerr != nil {
					return false, rerr
				}
				cur, found, err := fsx.ReadOptional(path)
				if err != nil {
					return false, err
				}
				rec := e.st.Files[key]
				switch {
				case !found && rec == nil:
					return false, nil
				case !found:
					e.keep(key, removedByYou)
					return true, nil
				case rec == nil:
					// There before makeit: adopted, as setup does.
					e.st.Files = initMap(e.st.Files)
					rec = &state.File{Class: "owned", Source: src, PreExisted: true}
					e.st.Files[key] = rec
				}
				switch {
				case rec.PreExisted:
					if !bytes.Equal(cur, want) {
						e.keep(key, "yours; the catalog's version differs")
					}
					return true, nil
				case bytes.Equal(cur, want):
					rec.Sha256 = fsx.Sha256(want)
					return true, nil
				case fsx.Sha256(cur) != rec.Sha256:
					e.keep(key, "changed since makeit wrote it")
					return true, nil
				}
				return false, nil
			},
			Run: func(context.Context) (string, error) {
				// Owner-only: the mode is set exactly, not filtered by umask (see landing).
				if err := e.write("global."+rel, path, want, 0o600); err != nil {
					return "", err
				}
				e.st.Files = initMap(e.st.Files)
				e.st.Files[key] = &state.File{Class: "owned", Source: src, Sha256: fsx.Sha256(want), Generation: e.st.Generation}
				return "", nil
			}})
	}
	keys := make([]string, 0, len(e.st.Files))
	for key, rec := range e.st.Files {
		if !inCatalog[key] && !rec.PreExisted {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	for _, key := range keys {
		rec := e.st.Files[key]
		path := e.Roots.Abs(key)
		rel := strings.TrimPrefix(key, "claude:")
		steps = append(steps, plan.Step{ID: "global.drop." + rel, Title: "Remove " + path + " (no longer in the catalog)",
			Already: func(context.Context) (bool, error) {
				if fi, err := os.Lstat(path); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
					e.keep(key, "a link, so it is yours")
					return true, nil
				}
				cur, found, err := fsx.ReadOptional(path)
				if err != nil {
					return false, err
				}
				if !found {
					delete(e.st.Files, key)
					return true, nil
				}
				if fsx.Sha256(cur) != rec.Sha256 {
					e.keep(key, "no longer in the catalog; changed since makeit wrote it")
					return true, nil
				}
				return false, nil
			},
			Run: func(context.Context) (string, error) {
				if err := e.remove("global.drop."+rel, path); err != nil {
					return "", err
				}
				delete(e.st.Files, key)
				return "", nil
			}})
	}
	return steps
}

// pluginSteps are update's plugin phase.
func (e *env) pluginSteps() []plan.Step { return nil }

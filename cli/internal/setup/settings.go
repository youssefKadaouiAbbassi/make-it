package setup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"makeit/internal/fsx"
	"makeit/internal/jsonx"
	"makeit/internal/plan"
	"makeit/internal/settings"
)

// settingsStep merges the catalog's baseline into Claude Code's settings.json: missing stack
// values are added, the user's values are kept, and the merge base is recorded for later
// updates. Claude Code validates the candidate before the write and the file after it; a file
// it rejects after the write is put back from the pre-image.
func (e *env) settingsStep() plan.Step {
	path := filepath.Join(e.Roots.Claude, "settings.json")
	detail := ""
	if res, _, err := e.mergeSettings(path); err == nil {
		detail = diff(res.Changes)
	}
	return plan.Step{ID: "settings", Title: "Merge the stack's settings into " + path, Detail: detail,
		Already: func(context.Context) (bool, error) {
			res, _, err := e.mergeSettings(path)
			e.keepHeld(res.Changes)
			return err == nil && !res.Changed() && len(e.st.Settings.Paths) > 0, err
		},
		Run: func(ctx context.Context) (string, error) {
			// Merged again now, not at planning time: a running Claude Code may have written the file.
			res, old, err := e.mergeSettings(path)
			if err != nil {
				return "", err
			}
			e.keepHeld(res.Changes)
			record := func() {
				e.st.Settings.Paths = res.State
				e.st.Settings.File = e.Roots.Portable(path)
			}
			if !res.Changed() {
				record()
				return "merge base recorded; nothing to change", nil
			}
			if e.claude == nil {
				return "", errors.New("settings.json cannot be validated: Claude Code is not usable")
			}
			data, err := jsonx.Marshal(res.Settings)
			if err != nil {
				return "", err
			}
			if err := e.validate(ctx, data, "the merged settings"); err != nil {
				return "", err
			}
			if err := e.write("settings", path, data, 0o600); err != nil {
				return "", err
			}
			written, _, err := fsx.ReadOptional(path)
			if err == nil {
				err = e.validate(ctx, written, "settings.json after the write")
			}
			if err != nil {
				if rerr := e.restore(path, old); rerr != nil {
					return "", errors.Join(err, rerr)
				}
				return "", fmt.Errorf("%w; the previous settings.json was put back", err)
			}
			record()
			if old == nil {
				e.st.Settings.CreatedByUs = true
			}
			return diff(res.Changes), nil
		}}
}

// mergeSettings reads the live file (absent is empty) and merges the desired baseline into it.
func (e *env) mergeSettings(path string) (settings.Result, []byte, error) {
	raw, err := e.Catalog.ReadFile("settings.baseline.json")
	if err != nil {
		return settings.Result{}, nil, fmt.Errorf("the plugin set's settings.baseline.json: %w", err)
	}
	baseline, err := jsonx.DecodeObject(raw)
	if err != nil {
		return settings.Result{}, nil, fmt.Errorf("the plugin set's settings.baseline.json: %w", err)
	}
	old, found, err := fsx.ReadOptional(path)
	if err != nil {
		return settings.Result{}, nil, err
	}
	live := jsonx.NewObject()
	if found {
		if live, err = jsonx.DecodeObject(old); err != nil {
			return settings.Result{}, nil, fmt.Errorf("%s is not valid JSON, fix it first: %w", path, err)
		}
	}
	return settings.Merge(live, settings.Desired(baseline, e.req.Marketplace), e.st.Settings.Paths, settings.DefaultOptions()), old, nil
}

func (e *env) validate(ctx context.Context, data []byte, what string) error {
	problems, err := e.claude.ValidateSettings(ctx, data)
	if err != nil {
		return err
	}
	if len(problems) > 0 {
		var lines []string
		for _, p := range problems {
			lines = append(lines, p.String())
		}
		return fmt.Errorf("%s: rejected by Claude Code: %s", what, strings.Join(lines, "; "))
	}
	return nil
}

// restore puts back the bytes the file had before the write; old is nil when there was none.
func (e *env) restore(path string, old []byte) error {
	dst, perm, err := landing(path, 0o600)
	if err != nil {
		return err
	}
	if old == nil {
		return os.Remove(dst)
	}
	return fsx.WriteAtomic(dst, old, perm)
}

// keepHeld reports each path where the merge kept the user's value over the stack's.
func (e *env) keepHeld(changes []settings.Change) {
	for _, c := range changes {
		if c.Action == settings.Held || c.Action == settings.KeptTheirs {
			e.keep(c.Path, "yours (held)")
		}
	}
}

// diff renders merge changes one line per JSON pointer, secrets hidden.
func diff(changes []settings.Change) string {
	var lines []string
	for _, c := range changes {
		switch c.Action {
		case settings.Set, settings.AddItems, settings.ReAsserted:
			lines = append(lines, "+ "+c.Path+": "+settings.Show(c.Path, c.After))
		case settings.KeptTheirs, settings.Held:
			lines = append(lines, "= "+c.Path+": kept (yours)")
		}
	}
	return strings.Join(lines, "\n")
}

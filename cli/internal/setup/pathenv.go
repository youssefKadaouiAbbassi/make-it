package setup

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"makeit/internal/execx"
	"makeit/internal/fsx"
	"makeit/internal/mergefile"
	"makeit/internal/pathenv"
	"makeit/internal/plan"
	"makeit/internal/state"
)

func (e *env) getenv(k string) string {
	if e.Getenv == nil {
		return ""
	}
	return e.Getenv(k)
}

func (e *env) exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func (e *env) envPath() string { return filepath.Join(e.Roots.Stack, "env") }

// pathSteps write the env file and, except in the daily run, load it from the shell profile.
func (e *env) pathSteps() []plan.Step {
	steps := []plan.Step{e.envStep()}
	if !e.Background {
		steps = append(steps, e.profileStep())
	}
	return steps
}

// envStep writes ~/.makeit/env, makeit's own file, listing the folders that exist now.
func (e *env) envStep() plan.Step {
	path := e.envPath()
	key := e.Roots.Portable(path)
	data := pathenv.EnvFile(pathenv.Dirs(e.Roots.Home, e.getenv, e.exists), e.Roots.Home)
	record := func() {
		e.st.Files = initMap(e.st.Files)
		e.st.Files[key] = &state.File{Class: "owned", Sha256: fsx.Sha256(data), Generation: e.st.Generation}
	}
	return plan.Step{ID: "path.env", Title: "Write " + path, Detail: strings.TrimSpace(string(data)),
		Already: func(context.Context) (bool, error) {
			cur, found, err := fsx.ReadOptional(path)
			if err != nil || !found || !bytes.Equal(cur, data) {
				return false, err
			}
			if e.st.Files[key] == nil {
				record()
			}
			return true, nil
		},
		Run: func(context.Context) (string, error) {
			if err := e.write("path.env", path, data, 0o644); err != nil {
				return "", err
			}
			record()
			return "", nil
		}}
}

// profileStep adds the managed block to each shell profile that lacks it. On Windows the user
// PATH in the registry gets ~/.local/bin instead.
func (e *env) profileStep() plan.Step {
	home := e.Roots.Home
	if e.Platform.OS == "windows" {
		dir := filepath.Join(home, ".local", "bin")
		q := strings.ReplaceAll(dir, "'", "''") // a quote inside a single-quoted PowerShell string
		ps := fmt.Sprintf(`$p=[Environment]::GetEnvironmentVariable('Path','User'); if (($p -split ';') -notcontains '%s') { [Environment]::SetEnvironmentVariable('Path', (($p.TrimEnd(';') + ';%s').TrimStart(';')), 'User') }`, q, q)
		cmd := execx.Cmd{Argv: []string{"powershell", "-NoProfile", "-Command", ps}}
		return plan.Step{ID: "path.profile", Title: "Add " + dir + " to your PATH", Command: cmd.Argv,
			Already: func(context.Context) (bool, error) {
				return slices.Contains(filepath.SplitList(e.getenv("PATH")), dir), nil
			},
			Run: func(ctx context.Context) (string, error) { return "", e.runAll(ctx, []execx.Cmd{cmd}) }}
	}
	profiles := pathenv.Profiles(home, e.envPath(), e.getenv("SHELL"), e.getenv, e.exists)
	var names []string
	for _, p := range profiles {
		names = append(names, p.Path)
	}
	return plan.Step{ID: "path.profile", Title: "Load " + e.envPath() + " from the shell profile", Detail: strings.Join(names, ", "),
		Already: func(context.Context) (bool, error) {
			for _, p := range profiles {
				cur, _, err := fsx.ReadOptional(p.Path)
				if err != nil {
					return false, err
				}
				if mergefile.MergeLines(cur, p.Lines, nil).Changed {
					return false, nil
				}
			}
			return true, nil
		},
		Run: func(context.Context) (string, error) {
			for _, p := range profiles {
				cur, found, err := fsx.ReadOptional(p.Path)
				if err != nil {
					return "", err
				}
				m := mergefile.MergeLines(cur, p.Lines, nil)
				if !m.Changed {
					continue
				}
				if err := e.write("path.profile", p.Path, m.Body, 0o644); err != nil {
					return "", err
				}
				key := e.Roots.Portable(p.Path)
				e.st.Files = initMap(e.st.Files)
				rec := e.st.Files[key]
				if rec == nil {
					rec = &state.File{Class: "merge", PreExisted: found}
					e.st.Files[key] = rec
				}
				for _, l := range m.Added {
					if !slices.Contains(rec.Added, l) {
						rec.Added = append(rec.Added, l)
					}
				}
				rec.Sha256, rec.Generation = fsx.Sha256(m.Body), e.st.Generation
			}
			return "", nil
		}}
}

// unMergeStep removes exactly the lines makeit added to a file the user owns, and the file
// itself when nothing is left and makeit created it.
func (e *env) unMergeStep(key string) plan.Step {
	rec := e.st.Files[key]
	path := e.Roots.Abs(key)
	return plan.Step{ID: "file." + strings.TrimPrefix(key, "home:"), Title: "Remove makeit's lines from " + path,
		Already: func(context.Context) (bool, error) {
			cur, found, err := fsx.ReadOptional(path)
			if err != nil {
				return false, err
			}
			if _, changed := mergefile.RemoveLines(cur, rec.Added); !found || !changed {
				delete(e.st.Files, key)
				return true, nil
			}
			return false, nil
		},
		Run: func(context.Context) (string, error) {
			cur, _, err := fsx.ReadOptional(path)
			if err != nil {
				return "", err
			}
			body, _ := mergefile.RemoveLines(cur, rec.Added)
			if len(bytes.TrimSpace(body)) == 0 && !rec.PreExisted {
				err = e.remove("file."+key, path)
			} else {
				err = e.write("file."+key, path, body, 0o644)
			}
			if err != nil {
				return "", err
			}
			delete(e.st.Files, key)
			return "", nil
		}}
}

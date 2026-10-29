package catsrc

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"makeit/internal/catalog"
	"makeit/internal/state"
)

// synced writes the fixture's v1.0.0 files to <stack>/catalog, as a previous run would have.
func synced(t *testing.T, stack string, version string) {
	t.Helper()
	files := map[string][]byte{}
	for p, body := range catalogFiles(version) {
		files[p] = []byte(body)
	}
	tr, err := catalog.TreeOf(files, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.SyncTree(filepath.Join(stack, "catalog"), tr); err != nil {
		t.Fatal(err)
	}
}

func gitRecord(url, requested, kind, tag, commit string) *state.Catalog {
	return &state.Catalog{Kind: "git", Location: url, Requested: &requested, RefKind: kind, Tag: tag, Resolved: commit, MarketplaceName: "makeit"}
}

func params(stack string, st *state.State) Params {
	return Params{Command: "update", State: st, Stack: stack, Git: local, Prereq: func(string) bool { return true }}
}

func stateWith(rec *state.Catalog) *state.State {
	st := &state.State{Marketplaces: map[string]*state.Marketplace{}}
	if rec != nil {
		st.Catalog = *rec
	}
	return st
}

func TestPrepare(t *testing.T) {
	ctx := context.Background()
	t.Run("adopted marketplace refuses --catalog", func(t *testing.T) {
		st := stateWith(nil)
		st.Marketplaces["makeit"] = &state.Marketplace{AddedBy: "user"}
		p := params(t.TempDir(), st)
		p.Arg = "embedded"
		if _, err := Prepare(ctx, p); !errors.Is(err, ErrAdopted) || !strings.Contains(err.Error(), "nothing was changed") {
			t.Errorf("err %v", err)
		}
	})
	t.Run("fresh setup without git", func(t *testing.T) {
		p := params(t.TempDir(), stateWith(nil))
		p.Command, p.Arg = "setup", "https://h/cat.git"
		p.Prereq = func(id string) bool { return id != "git" }
		_, err := Prepare(ctx, p)
		if !errors.Is(err, ErrFresh) || !strings.Contains(err.Error(), "`makeit setup --yes`, then `makeit update --catalog https://h/cat.git --yes`") {
			t.Errorf("err %v", err)
		}
	})
	for _, c := range []struct{ name, key, value string }{
		{"renamed marketplace", "makeit.json", `{"schemaVersion": 1, "marketplace": "other", "bundle": "makeit-bundle@makeit"}`},
		{"another bundle", "makeit.json", `{"schemaVersion": 1, "marketplace": "makeit", "bundle": "other@makeit"}`},
		{"newer schema", "makeit.json", `{"schemaVersion": 2, "marketplace": "makeit", "bundle": "makeit-bundle@makeit"}`},
		{"marketplace.json renamed", ".claude-plugin/marketplace.json", `{"name": "other", "owner": {"name": "t"}, "plugins": []}`},
	} {
		t.Run(c.name+" is refused", func(t *testing.T) {
			f := newFixture(t)
			f.commit(t, "change", map[string]string{c.key: c.value})
			sh(t, f.work, "tag", "-a", "v2.0.0", "-m", "v2.0.0")
			f.push(t)
			p := params(t.TempDir(), stateWith(nil))
			p.Arg = f.url + "#v2.0.0"
			if _, err := Prepare(ctx, p); err == nil {
				t.Error("taken")
			}
		})
	}
	t.Run("offline", func(t *testing.T) {
		stack := t.TempDir()
		synced(t, stack, "1.0.0")
		p := params(stack, stateWith(gitRecord("https://h/cat.git", "latest", "latest", "v1.0.0", strings.Repeat("a", 40))))
		p.Offline = true
		got, err := Prepare(ctx, p)
		if err != nil || len(got.Notices) != 1 || got.Record.Resolved != strings.Repeat("a", 40) {
			t.Errorf("plain: %+v %v", got.Notices, err)
		}
		p.Arg = "https://h/cat.git"
		if _, err := Prepare(ctx, p); err == nil || !strings.Contains(err.Error(), "network disabled") {
			t.Errorf("explicit: %v", err)
		}
	})
	t.Run("unreachable (Review Focus 1)", func(t *testing.T) {
		stack := t.TempDir()
		synced(t, stack, "1.0.0")
		gone := "file:///no/such/cat.git"
		got, err := Prepare(ctx, params(stack, stateWith(gitRecord(gone, "latest", "latest", "v1.0.0", strings.Repeat("a", 40)))))
		if err != nil || len(got.Notices) != 1 || !strings.Contains(got.Notices[0], "could not refresh the catalog") || got.Tree == nil {
			t.Errorf("plain: %+v %v", got.Notices, err)
		}
		p := params(stack, stateWith(nil))
		p.Arg = gone
		if _, err := Prepare(ctx, p); err == nil {
			t.Error("explicit: taken")
		}
	})
	t.Run("deleted tag (Review Focus 3)", func(t *testing.T) {
		f := newFixture(t)
		stack := t.TempDir()
		synced(t, stack, "1.0.0")
		got, err := Prepare(ctx, params(stack, stateWith(gitRecord(f.url, "v9.9.9", "tag", "v9.9.9", strings.Repeat("a", 40)))))
		if err != nil || len(got.Notices) != 1 || !strings.Contains(got.Notices[0], "gone") {
			t.Errorf("%+v %v", got.Notices, err)
		}
	})
	t.Run("latest moves to a newer tag", func(t *testing.T) {
		f := newFixture(t)
		a := sh(t, f.work, "rev-parse", "HEAD")
		b := f.commit(t, "1.1.0", map[string]string{"templates/a.md": "a 1.1.0\n"})
		sh(t, f.work, "tag", "-a", "v1.1.0", "-m", "v1.1.0")
		f.push(t)
		stack := t.TempDir()
		synced(t, stack, "1.0.0")
		got, err := Prepare(ctx, params(stack, stateWith(gitRecord(f.url, "latest", "latest", "v1.0.0", a))))
		if err != nil || got.Record.Resolved != b || got.Record.Tag != "v1.1.0" || *got.Record.Requested != "latest" {
			t.Fatalf("%+v %v", got.Record, err)
		}
		if body, _ := got.Tree.ReadFile("templates/a.md"); string(body) != "a 1.1.0\n" {
			t.Errorf("tree has %q", body)
		}
	})
	t.Run("a pin gets the newer-tag notice", func(t *testing.T) {
		f := newFixture(t)
		a := sh(t, f.work, "rev-parse", "HEAD")
		f.commit(t, "1.1.0", map[string]string{"templates/a.md": "a 1.1.0\n"})
		sh(t, f.work, "tag", "-a", "v1.1.0", "-m", "v1.1.0")
		f.push(t)
		stack := t.TempDir()
		synced(t, stack, "1.0.0")
		got, err := Prepare(ctx, params(stack, stateWith(gitRecord(f.url, "v1.0.0", "tag", "v1.0.0", a))))
		if err != nil || got.Record.Resolved != a || len(got.Notices) != 1 || !strings.Contains(got.Notices[0], "is newer than v1.0.0") {
			t.Errorf("%+v %+v %v", got.Record, got.Notices, err)
		}
	})
	t.Run("a moved tag holds", func(t *testing.T) {
		f := newFixture(t)
		a := sh(t, f.work, "rev-parse", "HEAD")
		f.commit(t, "moved", map[string]string{"templates/a.md": "moved\n"})
		sh(t, f.work, "tag", "-f", "-a", "v1.0.0", "-m", "moved")
		f.push(t)
		stack := t.TempDir()
		synced(t, stack, "1.0.0")
		got, err := Prepare(ctx, params(stack, stateWith(gitRecord(f.url, "v1.0.0", "tag", "v1.0.0", a))))
		if err != nil || got.Record.Resolved != a || len(got.Notices) != 1 || !strings.Contains(got.Notices[0], "now points to") {
			t.Errorf("%+v %+v %v", got.Record, got.Notices, err)
		}
	})
	t.Run("a directory source", func(t *testing.T) {
		dir := t.TempDir()
		synced(t, dir, "1.0.0")
		p := params(t.TempDir(), stateWith(nil))
		p.Arg = filepath.Join(dir, "catalog")
		got, err := Prepare(ctx, p)
		if err != nil || got.Record.Kind != "directory" || got.Record.Location != p.Arg || !strings.HasPrefix(got.Record.Resolved, "sha256:") {
			t.Errorf("%+v %v", got.Record, err)
		}
	})
	t.Run("embedded", func(t *testing.T) {
		got, err := Prepare(ctx, params(t.TempDir(), stateWith(nil)))
		emb, _ := catalog.EmbeddedTree()
		if err != nil || got.Record.Kind != "embedded" || got.Record.Resolved != catalog.Digest(emb.Manifest) {
			t.Errorf("%+v %v", got.Record, err)
		}
	})
}

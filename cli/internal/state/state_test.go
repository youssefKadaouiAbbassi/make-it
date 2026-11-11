package state

import (
	"bytes"
	"encoding/json/v2"
	"errors"
	"go/version"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"makeit/internal/buildinfo"
	"makeit/internal/jsonx"
	"makeit/internal/settings"
)

func write(t *testing.T, path, s string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestStateRoundTripKeepsUnknownMembers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	write(t, path, `{"schemaVersion":1,"revision":7,"cliVersion":"9.9.9","generation":3,
		"futureTop":{"q":[1,2]},
		"catalog":{"kind":"git","requested":"v1.2.0","resolved":"0123456789abcdef0123456789abcdef01234567"},
		"settings":{"paths":{"/model":{"kind":"value","base":"m1","original":{"value":null},"held":true,"pinnedBy":"later"}}},
		"plugins":{"x@makeit":{"installedBy":"makeit","scope":"user","generation":1,"observed":{},"hasUserConfig":true}},
		"files":{"claude:rules/makeit-go.md":{"class":"owned","sha256":"sha256:aa","preExisted":false,"generation":2}}}`)
	s, info, err := LoadState(path)
	if err != nil || !info.Found || info.PreMigration != nil {
		t.Fatalf("load: %v %+v", err, info)
	}
	if s.Generation != 3 || *s.Catalog.Requested != "v1.2.0" || !s.Settings.Paths["/model"].Held {
		t.Fatalf("decoded wrong: %+v", s)
	}
	if o := s.Settings.Paths["/model"].Original; o == nil || o.Value.V != nil {
		t.Fatalf("original null lost: %+v", o)
	}
	if err := SaveState(path, s); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), `"futureTop": {`) || !strings.Contains(string(b), `"q": [`) ||
		!strings.Contains(string(b), `"hasUserConfig": true`) || !strings.Contains(string(b), `"pinnedBy": "later"`) {
		t.Fatalf("unknown member lost:\n%s", b)
	}
	if s.Revision != 7 {
		t.Fatalf("revision lowered to %d", s.Revision)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode().Perm())
	}
}

func TestEncodeIsStable(t *testing.T) {
	s := &State{SchemaVersion: 1, Files: map[string]*File{}, Settings: Settings{Paths: settings.State{}}}
	for i := range 20 {
		s.Files["claude:f"+strconv.Itoa(i)] = &File{Class: "owned"}
		s.Settings.Paths["/p"+strconv.Itoa(i)] = &settings.Record{Kind: "value", Base: jsonx.Value{V: num(t, i)}}
	}
	a, err := Encode(s)
	if err != nil {
		t.Fatal(err)
	}
	for range 5 {
		b, _ := Encode(s)
		if !bytes.Equal(a, b) {
			t.Fatal("two encodings of the same state differ")
		}
	}
	if !bytes.HasSuffix(a, []byte("}\n")) || !bytes.Contains(a, []byte("\n  \"")) {
		t.Fatalf("not indented with a trailing newline:\n%s", a)
	}
}

func TestMissingFileGivesFreshDocuments(t *testing.T) {
	dir := t.TempDir()
	s, info, err := LoadState(filepath.Join(dir, "state.json"))
	if err != nil || info.Found || s.SchemaVersion != SchemaVersion {
		t.Fatalf("%v %+v %+v", err, info, s)
	}
	s.Files["claude:x"] = &File{}
	s.Settings.Paths["/x"] = &settings.Record{}
	s.Steps["x"] = &StepRecord{}
	r, info, err := LoadReceipt(filepath.Join(dir, "receipt.json"))
	if err != nil || info.Found || r != nil {
		t.Fatalf("%v %+v %+v", err, info, r)
	}
	repo, _, err := LoadRepo(filepath.Join(dir, ".makeit.json"))
	if err != nil || repo.Comment != RepoComment || repo.SchemaVersion != SchemaVersion {
		t.Fatalf("%v %+v", err, repo)
	}
}

func TestRefusals(t *testing.T) {
	cases := []struct {
		name, body, want string
		have             int
	}{
		{"draft without schemaVersion", `{"cliVersion":"0.1.0","catalog":"x","installedAt":"2026-10-01","globalFiles":["/h/.claude/CLAUDE.md"]}`, "move it aside", 0},
		{"newer schema", `{"schemaVersion":3,"cliVersion":"5.1.0","settings":{"paths":"changed shape"}}`, "format 3 written by makeit 5.1.0", 3},
		{"newer schema with a non-string cli member", `{"schemaVersion":2,"cli":{"v":2},"version":"6.0.0"}`, "format 2 written by makeit 6.0.0", 2},
		{"older than kept migrations", `{"schemaVersion":-1}`, "older than this makeit can migrate", -1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.json")
			write(t, path, c.body)
			_, _, err := LoadState(path)
			var fe *FormatError
			if !errors.As(err, &fe) || fe.Have != c.have || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v", err)
			}
			if b, _ := os.ReadFile(path); string(b) != c.body {
				t.Fatal("refused file was rewritten")
			}
		})
	}
	if err := SaveState(filepath.Join(t.TempDir(), "s.json"), &State{SchemaVersion: 2}); err == nil {
		t.Fatal("saved a document of another schema")
	}
	if _, _, err := LoadState(writeTmp(t, "{not json")); err == nil {
		t.Fatal("accepted invalid JSON")
	}
}

func writeTmp(t *testing.T, s string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "f.json")
	write(t, p, s)
	return p
}

// synthetic is a v0 -> v1 migration of the draft layout, used only to test the runner.
var synthetic = []Migration{{ID: "0001-global-files", From: 0, To: 1, RemoveBy: "99.0.0",
	Up: func(doc map[string]any) error {
		old, ok := doc["globalFiles"].([]any)
		if !ok {
			return nil
		}
		files, _ := doc["files"].(map[string]any)
		if files == nil {
			files = map[string]any{}
		}
		for _, f := range old {
			// Unknown hash: uninstall treats the file as modified and keeps it.
			files[f.(string)] = map[string]any{"class": "owned", "sha256": "", "preExisted": false, "generation": 0}
		}
		doc["files"] = files
		delete(doc, "globalFiles")
		return nil
	}}}

func TestMigrationRunner(t *testing.T) {
	path := writeTmp(t, `{"cliVersion":"0.1.0","globalFiles":["claude:CLAUDE.md"],"keepMe":true}`)
	saved := stateMigrations
	stateMigrations = synthetic
	t.Cleanup(func() { stateMigrations = saved })

	s, info, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	pre, _ := os.ReadFile(path)
	if !bytes.Equal(info.PreMigration, pre) {
		t.Fatal("pre-migration bytes not returned")
	}
	if s.SchemaVersion != 1 || s.Files["claude:CLAUDE.md"] == nil || s.Files["claude:CLAUDE.md"].Sha256 != "" {
		t.Fatalf("not migrated: %+v", s)
	}
	if len(s.Migrations) != 1 || s.Migrations[0].ID != "0001-global-files" || s.Migrations[0].At.IsZero() {
		t.Fatalf("run not recorded: %+v", s.Migrations)
	}
	if !strings.Contains(string(s.Extra), "keepMe") || strings.Contains(string(s.Extra), "globalFiles") {
		t.Fatalf("extra: %s", s.Extra)
	}

	// Idempotent: Up on an already migrated document changes nothing.
	var doc map[string]any
	b, _ := Encode(s)
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	before, _ := Encode(doc)
	if err := synthetic[0].Up(doc); err != nil {
		t.Fatal(err)
	}
	if after, _ := Encode(doc); !bytes.Equal(before, after) {
		t.Fatalf("second Up changed the document:\n%s\n%s", before, after)
	}
}

func TestMigrationMustAdvance(t *testing.T) {
	saved := stateMigrations
	stateMigrations = []Migration{{ID: "0001-typo", From: 0, To: 0, Up: func(map[string]any) error { return nil }}}
	t.Cleanup(func() { stateMigrations = saved })
	done := make(chan error, 1)
	go func() { _, _, err := LoadState(writeTmp(t, `{}`)); done <- err }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "does not advance") {
			t.Fatalf("got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("migration loop did not stop")
	}
}

// Old migrations are deleted on schedule: this fails once a release reaches RemoveBy. A plain go
// test sees Version "dev" and skips; release.yml runs it with the tag's version (ci/test.sh checks).
func TestMigrationsRemovedOnSchedule(t *testing.T) {
	if buildinfo.Version == "dev" {
		t.Skip("dev build has no version to compare")
	}
	for _, list := range [][]Migration{stateMigrations, receiptMigrations, repoMigrations} {
		for _, m := range list {
			if version.Compare("go"+buildinfo.Version, "go"+m.RemoveBy) >= 0 {
				t.Errorf("migration %s is due for removal in %s (this is %s)", m.ID, m.RemoveBy, buildinfo.Version)
			}
		}
	}
}

func TestReceiptAndRepo(t *testing.T) {
	dir := t.TempDir()
	rp := filepath.Join(dir, "receipt.json")
	if err := SaveReceipt(rp, &Receipt{Method: "script", BinPath: "home:.local/bin/makeit", Version: "1.0.0", Target: "linux-x64"}); err != nil {
		t.Fatal(err)
	}
	r, info, err := LoadReceipt(rp)
	if err != nil || !info.Found || r.SchemaVersion != 1 || r.Previous != nil || r.BinPath != "home:.local/bin/makeit" {
		t.Fatalf("%v %+v", err, r)
	}
	if b, _ := os.ReadFile(rp); !strings.Contains(string(b), `"previous": null`) {
		t.Fatalf("previous not null:\n%s", b)
	}

	pp := filepath.Join(dir, ".makeit.json")
	write(t, pp, `{"schemaVersion":1,"cli":"1.0.0","catalog":{"kind":"embedded"},"answers":{"types":["go"]},"later":1}`)
	repo, _, err := LoadRepo(pp)
	if err != nil {
		t.Fatal(err)
	}
	repo.Files = map[string]*RepoFile{"CLAUDE.md": {Class: "seeded", Template: "templates/project/CLAUDE.md", WrittenSha256: "sha256:bb"}}
	if err := SaveRepo(pp, repo); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(pp)
	if !strings.HasPrefix(string(b), "{\n  \"$comment\": ") || !strings.Contains(string(b), `"later": 1`) {
		t.Fatalf("repo record:\n%s", b)
	}
	if fi, _ := os.Stat(pp); fi.Mode().Perm() != 0o644 {
		t.Fatalf("mode %v", fi.Mode().Perm())
	}
}

func TestGenerations(t *testing.T) {
	stack := t.TempDir()
	for n := 1; n <= 7; n++ {
		g, err := NewGeneration(stack, n-1, "setup", []string{"setup", "--yes"})
		if err != nil || g.Manifest.Generation != n {
			t.Fatalf("%v %+v", err, g)
		}
		rel, err := g.PreImage([]byte("old settings"))
		if err != nil || rel != "files/0001" {
			t.Fatalf("%v %s", err, rel)
		}
		pre := "sha256:aa"
		if err := g.Record(Op{ID: "settings", Kind: "write", Path: "claude:settings.json", PreSha256: &pre, PreImage: rel}); err != nil {
			t.Fatal(err)
		}
		if n == 7 {
			if err := g.KeepStateBackup([]byte(`{"old":true}`)); err != nil {
				t.Fatal(err)
			}
		}
	}
	b, err := os.ReadFile(filepath.Join(GenerationsDir(stack), "7", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m.Generation != 7 || len(m.Ops) != 1 || m.Ops[0].PostSha256 != nil || m.StateBackup == nil || m.StartedAt.After(time.Now()) {
		t.Fatalf("manifest: %+v", m)
	}
	if err := Prune(stack); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(GenerationsDir(stack))
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if strings.Join(names, ",") != "3,4,5,6,7" {
		t.Fatalf("kept %v", names)
	}
	// state.json reset to generation 0 while folders 3-7 remain: the new run is 8 and survives a prune.
	g, err := NewGeneration(stack, 0, "setup", nil)
	if err != nil || g.Manifest.Generation != 8 {
		t.Fatalf("%v %+v", err, g)
	}
	if err := Prune(stack); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(g.Dir); err != nil {
		t.Fatalf("prune removed the newest run: %v", err)
	}
	if err := Prune(t.TempDir()); err != nil {
		t.Fatalf("prune without generations: %v", err)
	}
}

func num(t *testing.T, i int) any {
	t.Helper()
	v, err := jsonx.Decode([]byte(strconv.Itoa(i)))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestToolLatestRoundTrip(t *testing.T) {
	for _, c := range []struct {
		name, doc string
		want      map[string]string
	}{
		{"without", `{"schemaVersion":1,"generation":2}`, nil},
		{"with", `{"schemaVersion":1,"generation":2,"toolLatest":{"prek":"0.6.0"}}`, map[string]string{"prek": "0.6.0"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.json")
			write(t, path, c.doc)
			s, _, err := LoadState(path)
			if err != nil || s.ToolLatest["prek"] != c.want["prek"] {
				t.Fatalf("load: %v %v", err, s.ToolLatest)
			}
			if err := SaveState(path, s); err != nil {
				t.Fatal(err)
			}
			b, _ := os.ReadFile(path)
			if has := strings.Contains(string(b), `"toolLatest"`); has != (c.want != nil) {
				t.Errorf("toolLatest in file = %v:\n%s", has, b)
			}
			s2, _, err := LoadState(path)
			if err != nil || s2.Generation != 2 || s2.ToolLatest["prek"] != c.want["prek"] {
				t.Errorf("reload: %v %+v", err, s2)
			}
		})
	}
}

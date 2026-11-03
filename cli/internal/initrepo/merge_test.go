package initrepo

import (
	"slices"
	"strings"
	"testing"

	"makeit/internal/mergefile"
)

var checkScripts = [][2]string{
	{"check:fast", "echo \"check:fast: wire the formatter, linter and type checker\" && exit 1"},
	{"check:task", "echo \"check:task: wire check:fast and the related tests\" && exit 1"},
	{"check:full", "echo \"check:full: wire the linter, type checker and tests\" && exit 1"},
}

// Review Focus 2: a 4-space CRLF package.json keeps its style; only the new scripts are added.
func TestPackageScriptsKeepStyle(t *testing.T) {
	in := strings.ReplaceAll(`{
    "name": "app",
    "scripts": {
        "build": "tsc",
        "check:full": "vitest run"
    },
    "dependencies": {
        "zod": "^4.1.12"
    }
}
`, "\n", "\r\n")
	m, err := PackageScripts([]byte(in), checkScripts, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.ReplaceAll(`{
    "name": "app",
    "scripts": {
        "build": "tsc",
        "check:full": "vitest run",
        "check:fast": "echo \"check:fast: wire the formatter, linter and type checker\" && exit 1",
        "check:task": "echo \"check:task: wire check:fast and the related tests\" && exit 1"
    },
    "dependencies": {
        "zod": "^4.1.12"
    }
}
`, "\n", "\r\n")
	if string(m.Body) != want {
		t.Errorf("body\n%q\nwant\n%q", m.Body, want)
	}
	if !m.Changed || !slices.Equal(m.Added, []string{"/scripts/check:fast", "/scripts/check:task"}) || !slices.Equal(m.Conflicts, []string{"/scripts/check:full"}) {
		t.Errorf("added %v conflicts %v changed %v", m.Added, m.Conflicts, m.Changed)
	}
}

func TestPackageScriptsStyles(t *testing.T) {
	for _, c := range []struct{ name, in, want string }{
		{"tab, no final newline", "{\n\t\"name\": \"a\"\n}", "{\n\t\"name\": \"a\",\n\t\"scripts\": {\n\t\t\"check:fast\": \"x\"\n\t}\n}"},
		{"two spaces", "{\n  \"name\": \"a\",\n  \"scripts\": {}\n}\n", "{\n  \"name\": \"a\",\n  \"scripts\": {\n    \"check:fast\": \"x\"\n  }\n}\n"},
	} {
		m, err := PackageScripts([]byte(c.in), [][2]string{{"check:fast", "x"}}, nil)
		if err != nil || string(m.Body) != c.want {
			t.Errorf("%s: %q %v\nwant %q", c.name, m.Body, err, c.want)
		}
	}
}

func TestMergeJSONNoChange(t *testing.T) {
	in := "{\n    \"a\": {\"b\": 1},\n    \"c\": [1,2]\n}"
	m, err := MergeJSON([]byte(in), []byte(`{"a": {"b": 1}, "c": [1, 2]}`), nil, nil)
	if err != nil || m.Changed || string(m.Body) != in || len(m.Added)+len(m.Conflicts) != 0 {
		t.Errorf("%+v %v", m, err)
	}
}

func TestMergeJSONDeep(t *testing.T) {
	theirs := `{"enabledPlugins": {"theirs@x": true}, "env": {"A": "1"}}`
	ours := `{"enabledPlugins": {"makeit-ui@makeit": true}, "env": {"A": "2", "B": "3"}, "permissions": {"ask": ["Bash(git push:*)"]}}`
	m, err := MergeJSON([]byte(theirs), []byte(ours), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"enabledPlugins\": {\n    \"theirs@x\": true,\n    \"makeit-ui@makeit\": true\n  },\n  \"env\": {\n    \"A\": \"1\",\n    \"B\": \"3\"\n  },\n  \"permissions\": {\n    \"ask\": [\n      \"Bash(git push:*)\"\n    ]\n  }\n}"
	if string(m.Body) != want {
		t.Errorf("body\n%s", m.Body)
	}
	if !slices.Equal(m.Added, []string{"/enabledPlugins/makeit-ui@makeit", "/env/B", "/permissions/ask"}) || !slices.Equal(m.Conflicts, []string{"/env/A"}) {
		t.Errorf("added %v conflicts %v", m.Added, m.Conflicts)
	}
}

func TestMergeRenovate(t *testing.T) {
	theirs := `{"extends": ["config:recommended"], "packageRules": [{"description": "group eslint", "groupName": "theirs"}]}`
	ours := `{"extends": ["config:best-practices"], "packageRules": [{"description": "group eslint", "groupName": "ours"}, {"description": "pin actions", "pinDigests": true}], "customManagers": [{"description": "catalog pins", "customType": "regex"}]}`
	m, err := MergeJSON([]byte(theirs), []byte(ours), map[string]string{"/packageRules": "description", "/customManagers": "description"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"groupName": "theirs"`, `"description": "pin actions"`, `"description": "catalog pins"`, `"config:recommended"`} {
		if !strings.Contains(string(m.Body), want) {
			t.Errorf("body lacks %s:\n%s", want, m.Body)
		}
	}
	if strings.Contains(string(m.Body), `"groupName": "ours"`) || strings.Contains(string(m.Body), "best-practices") {
		t.Errorf("a value of theirs was replaced:\n%s", m.Body)
	}
	if !slices.Equal(m.Added, []string{"/packageRules/pin actions", "/customManagers"}) || !slices.Equal(m.Conflicts, []string{"/extends", "/packageRules/group eslint"}) {
		t.Errorf("added %v conflicts %v", m.Added, m.Conflicts)
	}
}

func TestMergeJSONRefusesJSON5(t *testing.T) {
	if _, err := MergeJSON([]byte("{\n  // comment\n  \"a\": 1,\n}"), []byte(`{"b": 2}`), nil, nil); err == nil || !strings.Contains(err.Error(), "not plain JSON") {
		t.Errorf("err %v", err)
	}
	if _, err := MergeJSON([]byte(`[1]`), []byte(`{"b": 2}`), nil, nil); err == nil {
		t.Error("a JSON array was taken as an object")
	}
}

// What a merge adds is recorded leaf by leaf, and a leaf in skip (added before, since removed by
// the user) is not added again, even inside an object that is missing as a whole.
func TestMergeSkipsRemovedLeaves(t *testing.T) {
	ours := `{"scripts": {"check:fast": "a", "check:task": "b", "check:api": "c"}}`
	m, err := MergeJSON([]byte(`{"name": "x"}`), []byte(ours), nil, map[string]bool{"/scripts/check:task": true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(m.Body), "check:task") || !slices.Equal(m.Added, []string{"/scripts/check:fast", "/scripts/check:api"}) {
		t.Errorf("added %v\n%s", m.Added, m.Body)
	}
	m, _ = MergeJSON([]byte(`{}`), []byte(`{"a": {"b": 1}}`), nil, map[string]bool{"/a/b": true})
	if m.Changed || strings.Contains(string(m.Body), `"a"`) {
		t.Errorf("an object with only skipped leaves was added: %s", m.Body)
	}
	m, _ = MergeJSON([]byte(`{"rules": []}`), []byte(`{"rules": [{"description": "x"}, {"description": "y"}]}`), map[string]string{"/rules": "description"}, map[string]bool{"/rules/x": true})
	if !slices.Equal(m.Added, []string{"/rules/y"}) {
		t.Errorf("matched array: %v", m.Added)
	}
	if l := mergefile.MergeLines([]byte("a\n"), []string{".env", ".env.*"}, map[string]bool{".env": true}); !slices.Equal(l.Added, []string{".env.*"}) {
		t.Errorf("lines: %v", l.Added)
	}
}

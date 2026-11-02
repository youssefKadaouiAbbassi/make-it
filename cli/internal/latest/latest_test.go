package latest

import (
	"context"
	"errors"
	"strings"
	"testing"

	"makeit/internal/catalog"
	"makeit/internal/execx"
)

func TestPrefix(t *testing.T) {
	for ref, want := range map[string]string{
		"v7.0.1":                      "v",
		"skill-v4.8.0":                "skill-v",
		"chrome-devtools-mcp-v1.12.0": "chrome-devtools-mcp-v",
		"0.23.1":                      "",
	} {
		if got, ok := Prefix(ref); !ok || got != want {
			t.Errorf("Prefix(%q) = %q, %v; want %q", ref, got, ok, want)
		}
	}
	if _, ok := Prefix("main"); ok {
		t.Error("a branch has no release prefix")
	}
}

func tags(kv ...string) map[string]string {
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return m
}

func TestPickHighestStable(t *testing.T) {
	tag, sha, ok := Pick(tags("v7.0.1", "a", "v7.1.0", "b", "v7.2.0-rc.1", "c", "v6.9.0", "d", "v7.10.0", "e"), "v", "v7.0.1", nil)
	if !ok || tag != "v7.10.0" || sha != "e" {
		t.Errorf("Pick = %q %q %v; want v7.10.0 (numeric order, no pre-release)", tag, sha, ok)
	}
}

func TestPickFloor(t *testing.T) {
	if tag, _, ok := Pick(tags("v6.9.0", "d", "v7.0.1", "a"), "v", "v7.0.1", nil); !ok || tag != "v7.0.1" {
		t.Errorf("Pick = %q, %v; want the floor v7.0.1", tag, ok)
	}
}

func TestPickSkip(t *testing.T) {
	if tag, _, _ := Pick(tags("v7.0.1", "a", "v7.1.0", "b"), "v", "v7.0.1", []string{"v7.1.0"}); tag != "v7.0.1" {
		t.Errorf("Pick = %q; want the skipped v7.1.0 passed over", tag)
	}
}

func TestPickPrefixFamily(t *testing.T) {
	if tag, _, _ := Pick(tags("skill-v4.8.0", "a", "skill-v4.9.0", "b", "cli-v9.0.0", "c", "v10.0.0", "d"), "skill-v", "skill-v4.8.0", nil); tag != "skill-v4.9.0" {
		t.Errorf("Pick = %q; want skill-v4.9.0 only from its own family", tag)
	}
}

func TestParseLsRemotePrefersPeeled(t *testing.T) {
	out := "1111\trefs/tags/v1.0.0\n2222\trefs/tags/v1.0.0^{}\n3333\trefs/tags/v1.1.0\n"
	got := parseLsRemote(out)
	if got["v1.0.0"] != "2222" || got["v1.1.0"] != "3333" {
		t.Errorf("tags %v; want the peeled commit for annotated tags", got)
	}
}

const market = `{"name":"makeit","plugins":[
{"name":"superpowers","source":{"source":"github","repo":"obra/superpowers","ref":"v7.0.1","sha":"aaa"}},
{"name":"impeccable","source":{"source":"git-subdir","url":"https://github.com/pbakaus/impeccable.git","path":"plugin","ref":"skill-v4.8.0","sha":"bbb"}},
{"name":"security-audit","source":{"source":"github","repo":"cloudflare/security-audit-skill","ref":"main","sha":"ccc"}},
{"name":"makeit","source":"./plugins/makeit"}]}`

const mcp = `{"mcpServers":{"docs":{"command":"npx","args":["-y","@arabold/docs-mcp-server@3.4.1","--read-only"]}}}`

func tree(t *testing.T) *catalog.Tree {
	t.Helper()
	tr, err := catalog.TreeOf(map[string][]byte{".claude-plugin/marketplace.json": []byte(market), "plugins/makeit/.mcp.json": []byte(mcp)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

func fakeRun(fail map[string]bool) execx.Runner {
	return func(_ context.Context, c execx.Cmd) (execx.Result, error) {
		cmd := strings.Join(c.Argv, " ")
		for k := range fail {
			if strings.Contains(cmd, k) {
				return execx.Result{Code: 128, Stderr: "fatal: unable to access"}, errors.New("exit 128")
			}
		}
		switch {
		case strings.Contains(cmd, "obra/superpowers"):
			return execx.Result{Stdout: "aaa\trefs/tags/v7.0.1\nnew1\trefs/tags/v7.1.0\n"}, nil
		case strings.Contains(cmd, "impeccable"):
			return execx.Result{Stdout: "bbb\trefs/tags/skill-v4.8.0\nnew2\trefs/tags/skill-v4.9.0\nx\trefs/tags/cli-v9.0.0\n"}, nil
		case strings.Contains(cmd, "npm view @arabold/docs-mcp-server version"):
			return execx.Result{Stdout: "3.5.0\n"}, nil
		}
		return execx.Result{Code: 1}, errors.New("unexpected " + cmd)
	}
}

func TestApplyMovesToLatest(t *testing.T) {
	got, moves, notes := Apply(context.Background(), fakeRun(nil), tree(t), tree(t), nil)
	b, _ := got.ReadFile(".claude-plugin/marketplace.json")
	s := string(b)
	if !strings.Contains(s, `"ref":"v7.1.0"`) || !strings.Contains(s, `"sha":"new1"`) || !strings.Contains(s, `"ref":"skill-v4.9.0"`) {
		t.Errorf("marketplace %s", s)
	}
	if !strings.Contains(s, `"ref":"main"`) {
		t.Errorf("a branch pin moved: %s", s)
	}
	if len(moves) != 2 || moves[0].ID != "superpowers@makeit" || moves[0].FromTag != "v7.0.1" || moves[0].ToTag != "v7.1.0" {
		t.Errorf("moves %+v", moves)
	}
	m, _ := got.ReadFile("plugins/makeit/.mcp.json")
	if !strings.Contains(string(m), "@arabold/docs-mcp-server@3.5.0") {
		t.Errorf(".mcp.json %s, want the server's latest npm version", m)
	}
	if len(notes) != 0 {
		t.Errorf("notes %v", notes)
	}
}

func TestApplyOfflineKeepsPins(t *testing.T) {
	got, moves, notes := Apply(context.Background(), fakeRun(map[string]bool{"obra/superpowers": true}), tree(t), tree(t), nil)
	b, _ := got.ReadFile(".claude-plugin/marketplace.json")
	if !strings.Contains(string(b), `"ref":"v7.0.1"`) {
		t.Errorf("an unreachable plugin moved: %s", b)
	}
	if len(moves) != 1 || moves[0].ID != "impeccable@makeit" {
		t.Errorf("moves %+v, want only impeccable", moves)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "superpowers") {
		t.Errorf("notes %v, want one naming superpowers", notes)
	}
}

func TestApplySkips(t *testing.T) {
	_, moves, _ := Apply(context.Background(), fakeRun(nil), tree(t), tree(t), map[string][]string{"superpowers@makeit": {"v7.1.0"}})
	for _, m := range moves {
		if m.ID == "superpowers@makeit" {
			t.Errorf("a skipped release came back: %+v", m)
		}
	}
}

// ahead is the plugin set on a machine whose daily runs already moved superpowers to 7.1.0,
// impeccable to 4.9.0 and the docs server to 3.5.0.
func aheadTree(t *testing.T) *catalog.Tree {
	t.Helper()
	m := strings.NewReplacer(`"ref":"v7.0.1","sha":"aaa"`, `"ref":"v7.1.0","sha":"new1"`, `"ref":"skill-v4.8.0","sha":"bbb"`, `"ref":"skill-v4.9.0","sha":"new2"`).Replace(market)
	tr, err := catalog.TreeOf(map[string][]byte{".claude-plugin/marketplace.json": []byte(m), "plugins/makeit/.mcp.json": []byte(strings.Replace(mcp, "3.4.1", "3.5.0", 1))}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

// A plugin that cannot be checked keeps the pin it has on disk, never the older one makeit ships.
func TestApplyUnreachableKeepsDiskPin(t *testing.T) {
	got, moves, _ := Apply(context.Background(), fakeRun(map[string]bool{"obra/superpowers": true, "impeccable": true}), tree(t), aheadTree(t), nil)
	b, _ := got.ReadFile(".claude-plugin/marketplace.json")
	if !strings.Contains(string(b), `"ref":"v7.1.0"`) || !strings.Contains(string(b), `"ref":"skill-v4.9.0"`) {
		t.Errorf("went back to the shipped pins: %s", b)
	}
	if len(moves) != 0 {
		t.Errorf("moves %+v, want none", moves)
	}
}

// Offline (no runner): nothing is fetched and every disk pin ahead of the shipped one stays.
func TestApplyOfflineCarriesForward(t *testing.T) {
	got, moves, notes := Apply(context.Background(), nil, tree(t), aheadTree(t), nil)
	b, _ := got.ReadFile(".claude-plugin/marketplace.json")
	m, _ := got.ReadFile("plugins/makeit/.mcp.json")
	if !strings.Contains(string(b), `"ref":"v7.1.0"`) || !strings.Contains(string(m), "docs-mcp-server@3.5.0") {
		t.Errorf("offline went back to the shipped pins:\n%s\n%s", b, m)
	}
	if len(moves) != 0 || len(notes) != 0 {
		t.Errorf("moves %+v notes %v", moves, notes)
	}
}

// A move starts from the pin on disk, so rollback puts back what the machine had.
func TestApplyMoveFromDiskPin(t *testing.T) {
	run := func(_ context.Context, c execx.Cmd) (execx.Result, error) {
		if strings.Contains(strings.Join(c.Argv, " "), "obra/superpowers") {
			return execx.Result{Stdout: "new1\trefs/tags/v7.1.0\nnew3\trefs/tags/v7.2.0\n"}, nil
		}
		return execx.Result{Code: 1}, errors.New("offline")
	}
	_, moves, _ := Apply(context.Background(), run, tree(t), aheadTree(t), nil)
	if len(moves) != 1 || moves[0].FromTag != "v7.1.0" || moves[0].FromSHA != "new1" || moves[0].ToTag != "v7.2.0" {
		t.Errorf("moves %+v, want v7.1.0 -> v7.2.0", moves)
	}
}

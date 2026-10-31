package docs

import (
	"maps"
	"strings"
	"testing"
	"time"
)

func TestRecordRoundTrip(t *testing.T) {
	stack, dir := t.TempDir(), t.TempDir()
	if r := LoadRecord(stack, dir); len(r.Manifests) != 0 {
		t.Fatalf("fresh record %+v", r)
	}
	want := Record{Dir: dir, Manifests: map[string]string{"package.json": "sha256:aa"}, SyncedAt: now}
	if err := SaveRecord(stack, dir, want); err != nil {
		t.Fatal(err)
	}
	got := LoadRecord(stack, dir)
	if got.Dir != dir || !maps.Equal(got.Manifests, want.Manifests) || !got.SyncedAt.Equal(now) {
		t.Errorf("got %+v", got)
	}
	if other := LoadRecord(stack, t.TempDir()); len(other.Manifests) != 0 {
		t.Error("records are not per directory")
	}
	_ = time.Second
}

func TestServerArgv(t *testing.T) {
	got, err := ServerArgv([]byte(`{"mcpServers": {"docs": {"command": "npx", "args": ["-y", "@arabold/docs-mcp-server@3.4.1", "--read-only"]}}}`))
	if err != nil || strings.Join(got, " ") != "npx -y @arabold/docs-mcp-server@3.4.1" {
		t.Errorf("%q %v", got, err)
	}
	if _, err := ServerArgv([]byte(`{"mcpServers": {}}`)); err == nil {
		t.Error("no docs server taken")
	}
}

package mcplocal

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPinAndInstalled(t *testing.T) {
	if got := Pin([]byte(`{"args":["-y","chrome-devtools-mcp@1.12.0"]}`)); got != "1.12.0" {
		t.Errorf("pin %q", got)
	}
	if got := Pin([]byte(`{"args":["-y","other@1.0.0"]}`)); got != "" {
		t.Errorf("pin of an absent package %q", got)
	}
	stack := t.TempDir()
	if got := Installed(stack); got != "" {
		t.Errorf("installed with no copy: %q", got)
	}
	dir := filepath.Join(Dir(stack), "node_modules", Package)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(dir, "package.json"), `{"version":"1.11.0","engines":{"node":"^20.19.0 || ^22.12.0 || >=23"}}`)
	if got := Installed(stack); got != "1.11.0" {
		t.Errorf("installed %q", got)
	}
	if got := Engines(stack); got != "^20.19.0 || ^22.12.0 || >=23" {
		t.Errorf("engines %q", got)
	}
	put(t, filepath.Join(dir, "package.json"), `not json`)
	if got := Installed(stack); got != "" {
		t.Errorf("installed with a broken file: %q", got)
	}
}

func put(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSatisfies(t *testing.T) {
	const r = "^20.19.0 || ^22.12.0 || >=23"
	for v, want := range map[string]bool{"v18.19.1": false, "v20.18.9": false, "v20.19.0": true, "v21.7.0": false,
		"v22.11.0": false, "v22.12.0": true, "v22.20.1": true, "v23.0.0": true, "v24.1.0": true, "garbage": true} {
		if got := Satisfies(v, r); got != want {
			t.Errorf("Satisfies(%q) = %v, want %v", v, got, want)
		}
	}
	if !Satisfies("v18.0.0", "") {
		t.Error("no range must accept any node")
	}
}

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
	os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"version":"1.11.0"}`), 0o644)
	if got := Installed(stack); got != "1.11.0" {
		t.Errorf("installed %q", got)
	}
	os.WriteFile(filepath.Join(dir, "package.json"), []byte(`not json`), 0o644)
	if got := Installed(stack); got != "" {
		t.Errorf("installed with a broken file: %q", got)
	}
}

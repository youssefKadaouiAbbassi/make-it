package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReceiptSkippedRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receipt.json")
	r := &Receipt{Method: "script", BinPath: "home:.local/bin/makeit", Version: "1.0.0", Target: "linux-x64", Skipped: "1.1.0"}
	if err := SaveReceipt(path, r); err != nil {
		t.Fatal(err)
	}
	got, _, err := LoadReceipt(path)
	if err != nil || got.Skipped != "1.1.0" {
		t.Fatalf("loaded %+v, %v", got, err)
	}
	r.Skipped = ""
	if err := SaveReceipt(path, r); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); strings.Contains(string(b), "skipped") {
		t.Errorf("an empty skip was written: %s", b)
	}
}

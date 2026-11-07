package platform

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The script's temp GnuPG home and key file go away however it ends: here the download fails, the
// first step that touches them.
func TestRepoScriptRemovesItsTempFiles(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	bin, rec := t.TempDir(), filepath.Join(t.TempDir(), "paths")
	wget := "#!/bin/sh\nprintf '%s\\n%s\\n' \"$GNUPGHOME\" \"$2\" >" + rec + "\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "wget"), []byte(wget), 0o755); err != nil {
		t.Fatal(err)
	}
	script, _ := RepoScript(APK)
	cmd := exec.Command("sh", "-c", script)
	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "TMPDIR="+t.TempDir())
	if err := cmd.Run(); err == nil {
		t.Fatal("the script went on after a failed download")
	}
	b, err := os.ReadFile(rec)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range strings.Fields(string(b)) {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s is still there", p)
		}
	}
}

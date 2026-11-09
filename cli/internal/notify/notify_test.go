package notify

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"makeit/internal/jsonx"
)

func fixture(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestEnableNtfyTopic(t *testing.T) {
	p := fixture(t, "{\n  \"model\": \"opus\"\n}\n")
	topic, err := EnableNtfy(p, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^makeit-[a-z2-7]{20}$`).MatchString(topic) {
		t.Fatalf("topic %q", topic)
	}
	if got := read(t, p); !strings.Contains(got, `"NTFY_TOPIC": "`+topic+`"`) || !strings.Contains(got, `"model": "opus"`) {
		t.Errorf("settings %s", got)
	}
	again, err := EnableNtfy(p, bytes.NewReader(bytes.Repeat([]byte{0xff}, 13)))
	if err != nil || again != topic {
		t.Errorf("second call gave %q, %v; want the existing %q", again, err, topic)
	}
}

func TestEnableNtfyMissingFileAndShortRand(t *testing.T) {
	p := filepath.Join(t.TempDir(), "new", "settings.json")
	if _, err := EnableNtfy(p, rand.Reader); err != nil {
		t.Fatalf("missing file: %v", err)
	}
	if _, err := EnableNtfy(p+"2", bytes.NewReader([]byte{1})); err == nil {
		t.Error("a short random source was accepted")
	}
	if _, err := os.Stat(p + "2"); err == nil {
		t.Error("file written without a topic")
	}
}

func TestEnableNtfyRefusesBrokenJSON(t *testing.T) {
	p := fixture(t, "{ not json")
	if _, err := EnableNtfy(p, rand.Reader); err == nil {
		t.Fatal("broken settings.json accepted")
	}
	if read(t, p) != "{ not json" {
		t.Error("broken file was changed")
	}
}

// 30 other keys, one of the three set to false: only the three keys change.
func TestEnablePush(t *testing.T) {
	var b strings.Builder
	b.WriteString("{\n  \"oauthAccount\": {\"emailAddress\": \"a@b.c\"},\n  \"inputNeededNotifEnabled\": false")
	for i := range 30 {
		fmt.Fprintf(&b, ",\n  \"key%02d\": {\"n\": %d}", i, i)
	}
	b.WriteString("\n}\n")
	orig := b.String()
	p := fixture(t, orig)
	if err := EnablePush(p); err != nil {
		t.Fatal(err)
	}
	if got := read(t, p+".makeit-bak"); got != orig {
		t.Errorf("backup differs from the original: %q", got)
	}
	after, err := jsonx.DecodeObject([]byte(read(t, p)))
	if err != nil {
		t.Fatal(err)
	}
	before, _ := jsonx.DecodeObject([]byte(orig))
	for _, k := range []string{"remoteControlAtStartup", "inputNeededNotifEnabled", "agentPushNotifEnabled"} {
		if v, _ := after.Get(k); v != true {
			t.Errorf("%s = %v", k, v)
		}
		after.Delete(k)
		before.Delete(k)
	}
	if jsonx.Canonical(after) != jsonx.Canonical(before) || after.Len() != 31 {
		t.Errorf("another key changed: %d keys left", after.Len())
	}
	if k := after.Keys(); k[0] != "oauthAccount" || k[30] != "key29" {
		t.Errorf("key order changed: %v", k)
	}
}

func TestEnablePushRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real.json")
	if err := os.WriteFile(real, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "settings.json")
	if err := os.Symlink(real, link); err != nil {
		t.Skip("no symlinks here")
	}
	if err := EnablePush(link); err == nil {
		t.Fatal("symlink accepted")
	}
	if read(t, real) != "{}\n" {
		t.Error("target was changed")
	}
	if _, err := os.Stat(link + ".makeit-bak"); err == nil {
		t.Error("backup written for a refused symlink")
	}
}

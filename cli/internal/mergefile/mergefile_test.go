package mergefile

import (
	"slices"
	"testing"
)

func TestMergeLines(t *testing.T) {
	lines := []string{".env", ".env.*", "!.env.example"}
	m := MergeLines([]byte("node_modules\n.env\n"), lines, nil)
	if string(m.Body) != "node_modules\n.env\n.env.*\n!.env.example\n" || !slices.Equal(m.Added, []string{".env.*", "!.env.example"}) {
		t.Errorf("%q %v", m.Body, m.Added)
	}
	m = MergeLines([]byte("dist"), lines, nil)
	if string(m.Body) != "dist\n.env\n.env.*\n!.env.example\n" {
		t.Errorf("no final newline: %q", m.Body)
	}
	m = MergeLines([]byte("dist\r\n.env\r\n"), lines, nil)
	if string(m.Body) != "dist\r\n.env\r\n.env.*\r\n!.env.example\r\n" {
		t.Errorf("crlf: %q", m.Body)
	}
	m = MergeLines([]byte("  .env  \n.env.*\n!.env.example\n"), lines, nil)
	if m.Changed {
		t.Errorf("present lines added again: %q", m.Body)
	}
	if m = MergeLines(nil, lines, nil); string(m.Body) != ".env\n.env.*\n!.env.example\n" || !m.Changed {
		t.Errorf("new file: %q", m.Body)
	}
}

func TestRemoveLines(t *testing.T) {
	body, changed := RemoveLines([]byte("export A=1\n# >>> makeit >>>\n. \"$HOME/.makeit/env\"\n# <<< makeit <<<\nexport B=2\n"),
		[]string{"# >>> makeit >>>", `. "$HOME/.makeit/env"`, "# <<< makeit <<<"})
	if string(body) != "export A=1\nexport B=2\n" || !changed {
		t.Errorf("%q %v", body, changed)
	}
	if body, changed := RemoveLines([]byte("export A=1\n"), []string{"x"}); changed || string(body) != "export A=1\n" {
		t.Errorf("nothing to remove: %q %v", body, changed)
	}
	if body, changed := RemoveLines([]byte("a\r\nx\r\nb\r\n"), []string{"x"}); !changed || string(body) != "a\r\nb\r\n" {
		t.Errorf("crlf: %q", body)
	}
}

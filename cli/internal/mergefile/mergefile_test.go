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

func TestMergeBlock(t *testing.T) {
	block := []string{"# >>> makeit >>>", `. "$HOME/.makeit/env"`, "# <<< makeit <<<"}
	// A copy of the loader line someone wrote by hand is not the block: the block is appended whole.
	m := MergeBlock([]byte(". \"$HOME/.makeit/env\"\n"), block)
	if !m.Changed || string(m.Body) != ". \"$HOME/.makeit/env\"\n# >>> makeit >>>\n. \"$HOME/.makeit/env\"\n# <<< makeit <<<\n" || len(m.Added) != 3 {
		t.Errorf("hand line: %q %v", m.Body, m.Added)
	}
	if m := MergeBlock(m.Body, block); m.Changed {
		t.Errorf("present: changed to %q", m.Body)
	}
	// A block edited inside is repaired in place, not added a second time.
	m = MergeBlock([]byte("a\n# >>> makeit >>>\n# broken\n# <<< makeit <<<\nb\n"), block)
	if !m.Changed || string(m.Body) != "a\n# >>> makeit >>>\n. \"$HOME/.makeit/env\"\n# <<< makeit <<<\nb\n" {
		t.Errorf("repair: %q", m.Body)
	}
	if m := MergeBlock([]byte("a\r\n"), block); string(m.Body) != "a\r\n# >>> makeit >>>\r\n. \"$HOME/.makeit/env\"\r\n# <<< makeit <<<\r\n" {
		t.Errorf("crlf: %q", m.Body)
	}
}

func TestRemoveBlockKeepsHandWrittenLine(t *testing.T) {
	block := []string{"# >>> makeit >>>", `. "$HOME/.makeit/env"`, "# <<< makeit <<<"}
	body, changed := RemoveBlock([]byte(". \"$HOME/.makeit/env\"\n# >>> makeit >>>\n. \"$HOME/.makeit/env\"\n# <<< makeit <<<\nb\n"), block)
	if !changed || string(body) != ". \"$HOME/.makeit/env\"\nb\n" {
		t.Errorf("%q %v", body, changed)
	}
	// Markers alone (an older run found the line by hand and added only them) go too.
	if body, changed := RemoveBlock([]byte("x\n# >>> makeit >>>\n# <<< makeit <<<\n"), []string{"# >>> makeit >>>", "# <<< makeit <<<"}); !changed || string(body) != "x\n" {
		t.Errorf("markers only: %q %v", body, changed)
	}
	if body, changed := RemoveBlock([]byte("x\n"), block); changed || string(body) != "x\n" {
		t.Errorf("absent: %q %v", body, changed)
	}
}

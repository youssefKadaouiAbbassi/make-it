package cli

import (
	"strings"
	"testing"
)

var setup = Command{Name: "setup", Summary: "Set the machine up.", Flags: []Flag{
	{Name: "catalog", Value: "source", Help: "where"},
	{Name: "force", Help: "take ours"},
}}

func TestParseFlagsAnywhere(t *testing.T) {
	p, err := Parse(setup, []string{"extra", "--catalog", "org/repo", "-y", "--dry-run", "--color=never", "--", "--literal"})
	if err != nil {
		t.Fatal(err)
	}
	if p.String["catalog"] != "org/repo" || !p.Bool["yes"] || !p.Bool["dry-run"] || p.String["color"] != "never" {
		t.Fatalf("%+v", p)
	}
	if strings.Join(p.Positionals, ",") != "extra,--literal" {
		t.Fatalf("positionals %v", p.Positionals)
	}
}

func TestDefaultsAndGroupedShorts(t *testing.T) {
	p, err := Parse(setup, []string{"-yq"})
	if err != nil {
		t.Fatal(err)
	}
	if !p.Bool["yes"] || !p.Bool["quiet"] || p.Bool["force"] {
		t.Fatalf("%+v", p.Bool)
	}
	if _, ok := p.String["catalog"]; ok {
		t.Fatal("unset string flag present")
	}
}

func TestParseErrors(t *testing.T) {
	for _, args := range [][]string{{"--nope"}, {"--catalog"}, {"--yes=1"}, {"-yz"}, {"-yc"}} {
		if _, err := Parse(setup, args); err == nil {
			t.Errorf("%v accepted", args)
		}
	}
	_, err := Parse(setup, []string{"--forc"})
	if err == nil || !strings.Contains(err.Error(), "did you mean --force") {
		t.Fatalf("suggestion: %v", err)
	}
}

func TestUsage(t *testing.T) {
	u := Usage("makeit", "1.0.0", []Command{setup})
	if !strings.Contains(u, "setup") || !strings.Contains(u, "--yes, -y") {
		t.Fatal(u)
	}
	c := CommandUsage("makeit", setup)
	if !strings.Contains(c, "--catalog <source>") || !strings.Contains(c, "--dry-run, -n") {
		t.Fatal(c)
	}
}

// A value flag given twice is refused: the first value would be dropped without a word.
func TestParseRepeatedValueFlag(t *testing.T) {
	spec := Command{Name: "init", Flags: []Flag{{Name: "keep", Value: "line"}}}
	if _, err := Parse(spec, []string{"--keep", "a", "--keep=b"}); err == nil || !strings.Contains(err.Error(), "--keep given twice") {
		t.Errorf("err %v", err)
	}
}

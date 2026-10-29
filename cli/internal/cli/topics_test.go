package cli

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestSuggest(t *testing.T) {
	names := []string{"doctor", "help", "version"}
	for in, want := range map[string]string{"docter": "doctor", "verison": "version", "hlep": "help", "xyz": "", "doctorr": "doctor", "": ""} {
		if got := Suggest(in, names); got != want {
			t.Errorf("Suggest(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSuggestPrefersTheClosest(t *testing.T) {
	// "updat" is 1 from "update" and 2 from "upgrade"; table order alone would pick upgrade.
	if got := Suggest("updat", []string{"upgrade", "update"}); got != "update" {
		t.Errorf("Suggest = %q, want update", got)
	}
}

// TestEnvironmentListsEveryVariable fails when code in the internal packages reads a variable
// that `help environment` does not list.
func TestEnvironmentListsEveryVariable(t *testing.T) {
	read := regexp.MustCompile(`(?i)(?:getenv|lookupenv)\("([A-Z_]+)"\)`)
	var listed []string
	for _, e := range Environment {
		listed = append(listed, e.Name)
	}
	found := 0
	err := filepath.WalkDir("..", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range read.FindAllStringSubmatch(string(b), -1) {
			found++
			if !slices.Contains(listed, m[1]) {
				t.Errorf("%s reads %s, which help environment does not list", path, m[1])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if found == 0 {
		t.Fatal("found no environment reads: the walk is looking in the wrong place")
	}
}

func TestTopic(t *testing.T) {
	codes, ok := Topic("makeit", "exit-codes")
	if !ok || !strings.Contains(codes, "130") || !strings.Contains(codes, "143") {
		t.Errorf("exit-codes = %q, %v", codes, ok)
	}
	env, ok := Topic("makeit", "environment")
	if !ok || !strings.Contains(env, "MAKEIT_DIR") || !strings.Contains(env, "CLAUDE_CONFIG_DIR") {
		t.Errorf("environment = %q, %v", env, ok)
	}
	if _, ok := Topic("makeit", "nope"); ok {
		t.Error("an unknown topic is reported as found")
	}
}

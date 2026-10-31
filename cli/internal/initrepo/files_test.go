package initrepo

import (
	"strings"
	"testing"

	"makeit/internal/catalog"
)

func TestRender(t *testing.T) {
	tpl := []byte("# <project name>\n<One sentence: what this is.>\n`<runner> check:full` with `<pm>`, reviewed <YYYY-MM-DD>.\n")
	got := string(Render(tpl, Values{ProjectName: "shop", Runner: "pnpm", PM: "pnpm", Date: "2026-10-31"}))
	want := "# shop\n<One sentence: what this is.>\n`pnpm check:full` with `pnpm`, reviewed 2026-10-31.\n"
	if got != want {
		t.Errorf("got %q", got)
	}
	if got := string(Render([]byte("<runner> x"), Values{})); got != "<runner> x" {
		t.Errorf("an empty value replaced its placeholder: %q", got)
	}
}

func docPaths(seeds []Seed) []string {
	var out []string
	for _, s := range seeds {
		out = append(out, s.Path)
	}
	return out
}

func TestDocFiles(t *testing.T) {
	v := Values{ProjectName: "shop", Runner: "npm run", PM: "npm", Date: "2026-10-31"}
	seeds, err := DocFiles(Project{Types: []string{"node"}}, catalog.Embedded, v)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(docPaths(seeds), " "); got != "CLAUDE.md CONSTRAINTS.md HANDOFF.md" {
		t.Errorf("node: %s", got)
	}
	for _, s := range seeds {
		if s.Class != "doc" || s.Template != "templates/project/"+s.Path || strings.Contains(string(s.Body), "<project name>") {
			t.Errorf("%s: class %q template %q", s.Path, s.Class, s.Template)
		}
	}
	claude := string(seeds[0].Body)
	if strings.Contains(claude, "chrome-devtools") || strings.Contains(claude, "## Mobile") {
		t.Error("node got the web or mobile section")
	}

	seeds, _ = DocFiles(Project{Types: []string{"node", "web", "model", "mobile"}}, catalog.Embedded, v)
	if got := strings.Join(docPaths(seeds), " "); got != "CLAUDE.md CONSTRAINTS.md HANDOFF.md TYPED-DECISIONS.md" {
		t.Errorf("model: %s", got)
	}
	claude = string(seeds[0].Body)
	verify := strings.Index(claude, "## How to verify a change")
	web := strings.Index(claude, "chrome-devtools")
	mobile := strings.Index(claude, "## Mobile")
	gotchas := strings.Index(claude, "## Gotchas")
	if !(verify >= 0 && verify < web && web < mobile && mobile < gotchas) {
		t.Errorf("sections out of place: verify %d web %d mobile %d gotchas %d", verify, web, mobile, gotchas)
	}
	for _, want := range []string{"1280 px and 375 px", "agent-device snapshot -i", "agent-device press", "maestro test .maestro/", "Java 17"} {
		if !strings.Contains(claude, want) {
			t.Errorf("CLAUDE.md lacks %q", want)
		}
	}
}

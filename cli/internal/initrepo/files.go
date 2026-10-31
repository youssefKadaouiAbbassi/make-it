package initrepo

import (
	"bytes"
	"slices"

	"makeit/internal/catalog"
)

// Values fill the mechanical placeholders of the project templates; the rest is the agent's.
type Values struct{ ProjectName, Runner, PM, Date string }

// Seed is one file init writes whole: its path in the repository, the catalog template it comes
// from, its class (doc, settings or ci) and its content.
type Seed struct {
	Path, Template, Class string
	Body                  []byte
}

// Render replaces exactly <project name>, <runner>, <pm> and <YYYY-MM-DD>; an empty value keeps
// its placeholder, and every other <...> stays for the agent.
func Render(tpl []byte, v Values) []byte {
	out := tpl
	for _, r := range [][2]string{{"<project name>", v.ProjectName}, {"<runner>", v.Runner}, {"<pm>", v.PM}, {"<YYYY-MM-DD>", v.Date}} {
		if r[1] != "" {
			out = bytes.ReplaceAll(out, []byte(r[0]), []byte(r[1]))
		}
	}
	return out
}

// DocFiles are the project docs for p: CLAUDE.md, CONSTRAINTS.md and HANDOFF.md, plus
// TYPED-DECISIONS.md when the project calls a model. A web project gets the browser check as the
// last item of CLAUDE.md's "How to verify a change"; a mobile one gets the Mobile section.
func DocFiles(p Project, src catalog.Source, v Values) ([]Seed, error) {
	names := []string{"CLAUDE.md", "CONSTRAINTS.md", "HANDOFF.md"}
	if slices.Contains(p.Types, "model") {
		names = append(names, "TYPED-DECISIONS.md")
	}
	var out []Seed
	for _, n := range names {
		tpl := "templates/project/" + n
		b, err := src.ReadFile(tpl)
		if err != nil {
			return nil, err
		}
		if n == "CLAUDE.md" {
			if b, err = withSections(b, p, src); err != nil {
				return nil, err
			}
		}
		out = append(out, Seed{Path: n, Template: tpl, Class: "doc", Body: Render(b, v)})
	}
	return out, nil
}

// withSections inserts the per-type sections into CLAUDE.md.
func withSections(b []byte, p Project, src catalog.Source) ([]byte, error) {
	if slices.Contains(p.Types, "web") {
		web, err := src.ReadFile("templates/project/sections/web-verify.md")
		if err != nil {
			return nil, err
		}
		b = insertBefore(b, sectionEnd(b, "## How to verify a change"), web)
	}
	if slices.Contains(p.Types, "mobile") {
		mobile, err := src.ReadFile("templates/project/sections/mobile.md")
		if err != nil {
			return nil, err
		}
		b = insertBefore(b, bytes.Index(b, []byte("## Gotchas")), append(mobile, '\n'))
	}
	return b, nil
}

// sectionEnd is where the text of the section under heading ends: before the blank line that
// precedes the next heading, or the end of the file.
func sectionEnd(b []byte, heading string) int {
	start := bytes.Index(b, []byte(heading))
	if start < 0 {
		return len(b)
	}
	next := bytes.Index(b[start+len(heading):], []byte("\n## "))
	if next < 0 {
		return len(b)
	}
	end := start + len(heading) + next
	for end > start && b[end-1] == '\n' {
		end--
	}
	return end + 1
}

func insertBefore(b []byte, at int, text []byte) []byte {
	if at < 0 || at > len(b) {
		at = len(b)
	}
	out := append([]byte{}, b[:at]...)
	out = append(out, text...)
	return append(out, b[at:]...)
}

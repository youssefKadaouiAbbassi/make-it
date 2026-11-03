// Package mergefile adds lines to, and removes them from, files the user owns, keeping what is
// there: init's entries in .gitignore, setup's block in the shell profile.
package mergefile

import (
	"bytes"
	"strings"
)

// Merged is the result of an additive merge. Added and Conflicts are JSON pointers (or lines);
// a conflict is a value the file has that differs from ours, and theirs is kept.
type Merged struct {
	Body      []byte
	Added     []string
	Conflicts []string
	Changed   bool
}

// MergeLines appends the lines theirs lacks (compared without surrounding spaces) and skip does
// not name, keeping its line endings and ending with a newline.
func MergeLines(theirs []byte, lines []string, skip map[string]bool) Merged {
	m := Merged{Body: theirs}
	eol := "\n"
	if bytes.Contains(theirs, []byte("\r\n")) {
		eol = "\r\n"
	}
	have := map[string]bool{}
	for _, l := range strings.Split(strings.ReplaceAll(string(theirs), "\r\n", "\n"), "\n") {
		have[strings.TrimSpace(l)] = true
	}
	var add []string
	for _, l := range lines {
		if !have[l] && !skip[l] {
			add, have[l] = append(add, l), true
		}
	}
	if len(add) == 0 {
		return m
	}
	out := string(theirs)
	if out != "" && !strings.HasSuffix(out, "\n") {
		out += eol
	}
	m.Body, m.Added, m.Changed = []byte(out+strings.Join(add, eol)+eol), add, true
	return m
}

// RemoveLines drops every line of theirs that equals one of lines (compared without surrounding
// spaces), keeping the rest as it is. changed is false when none was there.
func RemoveLines(theirs []byte, lines []string) (body []byte, changed bool) {
	eol := "\n"
	if bytes.Contains(theirs, []byte("\r\n")) {
		eol = "\r\n"
	}
	drop := map[string]bool{}
	for _, l := range lines {
		drop[strings.TrimSpace(l)] = true
	}
	var kept []string
	for _, l := range strings.Split(string(theirs), eol) {
		if drop[strings.TrimSpace(l)] {
			changed = true
			continue
		}
		kept = append(kept, l)
	}
	if !changed {
		return theirs, false
	}
	return []byte(strings.Join(kept, eol)), true
}

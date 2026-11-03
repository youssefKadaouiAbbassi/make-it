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

// span is where block sits in lines: the index of its first line and of its last line after
// that, compared without surrounding spaces; -1, -1 when the file has no such block.
func span(lines, block []string) (open, end int) {
	first, last := strings.TrimSpace(block[0]), strings.TrimSpace(block[len(block)-1])
	open = -1
	for i, l := range lines {
		switch {
		case open < 0 && strings.TrimSpace(l) == first:
			open = i
		case open >= 0 && strings.TrimSpace(l) == last:
			return open, i
		}
	}
	return -1, -1
}

// MergeBlock adds block, whose first and last lines are its markers, to theirs: appended when the
// file has no block, rewritten in place when the block there differs, untouched when it is the
// same. A copy of an inner line elsewhere in the file is the user's, not the block.
func MergeBlock(theirs []byte, block []string) Merged {
	m := Merged{Body: theirs}
	eol := "\n"
	if bytes.Contains(theirs, []byte("\r\n")) {
		eol = "\r\n"
	}
	lines := strings.Split(string(theirs), eol)
	if open, end := span(lines, block); open >= 0 {
		same := end-open+1 == len(block)
		for i := 0; same && i < len(block); i++ {
			same = strings.TrimSpace(lines[open+i]) == strings.TrimSpace(block[i])
		}
		if same {
			return m
		}
		kept := append(append(append([]string{}, lines[:open]...), block...), lines[end+1:]...)
		m.Body, m.Added, m.Changed = []byte(strings.Join(kept, eol)), block, true
		return m
	}
	out := string(theirs)
	if out != "" && !strings.HasSuffix(out, "\n") {
		out += eol
	}
	m.Body, m.Added, m.Changed = []byte(out+strings.Join(block, eol)+eol), block, true
	return m
}

// RemoveBlock drops the block between its markers from theirs and nothing else; changed is false
// when there is none. A block of one line has no span and is removed as a line.
func RemoveBlock(theirs []byte, block []string) (body []byte, changed bool) {
	if len(block) < 2 {
		return RemoveLines(theirs, block)
	}
	eol := "\n"
	if bytes.Contains(theirs, []byte("\r\n")) {
		eol = "\r\n"
	}
	lines := strings.Split(string(theirs), eol)
	open, end := span(lines, block)
	if open < 0 {
		return theirs, false
	}
	kept := append(append([]string{}, lines[:open]...), lines[end+1:]...)
	return []byte(strings.Join(kept, eol)), true
}

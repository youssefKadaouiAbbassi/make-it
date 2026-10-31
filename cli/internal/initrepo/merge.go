package initrepo

import (
	"bytes"
	"fmt"
	"strings"

	"makeit/internal/jsonx"
)

// Merged is the result of an additive merge. Added and Conflicts are JSON pointers (or lines);
// a conflict is a value the repository has that differs from ours, and theirs is kept.
type Merged struct {
	Body      []byte
	Added     []string
	Conflicts []string
	Changed   bool
}

// MergeJSON adds what ours has and theirs lacks: object keys at any depth, and, for the arrays
// named in match (pointer -> identity field), the elements whose identity theirs does not have.
// It never removes anything or changes a value theirs has. The result keeps theirs' key order,
// indentation, line endings and final newline; when nothing is added, Body is theirs unchanged.
// Empty theirs is an empty object.
func MergeJSON(theirs, ours []byte, match map[string]string) (Merged, error) {
	m := Merged{Body: theirs}
	style := detectStyle(theirs)
	dst, err := jsonx.DecodeObject(bytes.TrimPrefix(theirs, bom))
	if err != nil {
		return m, fmt.Errorf("not plain JSON (comments, trailing commas or JSON5 cannot be merged): %w", err)
	}
	src, err := jsonx.DecodeObject(ours)
	if err != nil {
		return m, err
	}
	addMissing(dst, src, "", match, &m)
	if len(m.Added) == 0 {
		return m, nil
	}
	b, err := jsonx.MarshalIndent(dst, style.indent)
	if err != nil {
		return m, err
	}
	if !style.finalNewline {
		b = bytes.TrimSuffix(b, []byte("\n"))
	}
	if style.crlf {
		b = bytes.ReplaceAll(b, []byte("\n"), []byte("\r\n"))
	}
	if style.bom {
		b = append(append([]byte{}, bom...), b...)
	}
	m.Body, m.Changed = b, true
	return m, nil
}

// PackageScripts adds the scripts package.json lacks, in the order given.
func PackageScripts(theirs []byte, scripts [][2]string) (Merged, error) {
	ours := jsonx.NewObject()
	s := jsonx.NewObject()
	for _, kv := range scripts {
		s.Set(kv[0], kv[1])
	}
	ours.Set("scripts", s)
	b, err := jsonx.Compact(ours)
	if err != nil {
		return Merged{}, err
	}
	return MergeJSON(theirs, b, nil)
}

// MergeLines appends the lines theirs lacks (compared without surrounding spaces), keeping its
// line endings and ending with a newline.
func MergeLines(theirs []byte, lines []string) Merged {
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
		if !have[l] {
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

var bom = []byte("\xef\xbb\xbf")

type jsonStyle struct {
	indent             string
	crlf, finalNewline bool
	bom                bool
}

// detectStyle reads the indentation of the first indented line (two spaces when there is none),
// the line endings and the final newline.
func detectStyle(b []byte) jsonStyle {
	s := jsonStyle{indent: "  ", crlf: bytes.Contains(b, []byte("\r\n")), bom: bytes.HasPrefix(b, bom),
		finalNewline: len(bytes.TrimSpace(b)) == 0 || bytes.HasSuffix(b, []byte("\n"))}
	for _, line := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")[1:] {
		ws := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
		if ws == "" || strings.TrimSpace(line) == "" {
			continue
		}
		if ws[0] == '\t' {
			s.indent = "\t"
		} else {
			s.indent = strings.Repeat(" ", len(ws))
		}
		break
	}
	return s
}

// addMissing copies into dst what src has and dst lacks, recording pointers in m.
func addMissing(dst, src *jsonx.Object, ptr string, match map[string]string, m *Merged) {
	for _, k := range src.Keys() {
		sv, _ := src.Get(k)
		p := ptr + "/" + escapePointer(k)
		dv, ok := dst.Get(k)
		if !ok {
			dst.Set(k, jsonx.Clone(sv))
			m.Added = append(m.Added, p)
			continue
		}
		do, dObj := dv.(*jsonx.Object)
		so, sObj := sv.(*jsonx.Object)
		if dObj && sObj {
			addMissing(do, so, p, match, m)
			continue
		}
		da, dArr := dv.([]any)
		sa, sArr := sv.([]any)
		if id := match[p]; id != "" && dArr && sArr {
			dst.Set(k, appendMissing(da, sa, id, p, m))
			continue
		}
		if !jsonx.Equal(dv, sv) {
			m.Conflicts = append(m.Conflicts, p)
		}
	}
}

// appendMissing appends the objects of ours whose id field no element of theirs has.
func appendMissing(theirs, ours []any, id, ptr string, m *Merged) []any {
	have := map[string]any{}
	for _, e := range theirs {
		if v, ok := idOf(e, id); ok {
			have[v] = e
		}
	}
	out := theirs
	for _, e := range ours {
		v, ok := idOf(e, id)
		if !ok {
			continue
		}
		if prev, dup := have[v]; dup {
			if !jsonx.Equal(prev, e) {
				m.Conflicts = append(m.Conflicts, ptr+"/"+escapePointer(v))
			}
			continue
		}
		out, have[v] = append(out, jsonx.Clone(e)), e
		m.Added = append(m.Added, ptr+"/"+escapePointer(v))
	}
	return out
}

func idOf(e any, field string) (string, bool) {
	o, ok := e.(*jsonx.Object)
	if !ok {
		return "", false
	}
	v, ok := o.Get(field)
	s, isStr := v.(string)
	return s, ok && isStr && s != ""
}

// escapePointer escapes one JSON pointer token (RFC 6901).
func escapePointer(k string) string {
	return strings.ReplaceAll(strings.ReplaceAll(k, "~", "~0"), "/", "~1")
}

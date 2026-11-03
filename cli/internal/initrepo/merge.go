package initrepo

import (
	"bytes"
	"fmt"
	"strings"

	"makeit/internal/jsonx"
	"makeit/internal/mergefile"
)

// Merged is mergefile.Merged: the result of an additive merge.
type Merged = mergefile.Merged

// MergeJSON adds what ours has and theirs lacks: object keys at any depth, and, for the arrays
// named in match (pointer -> identity field), the elements whose identity theirs does not have.
// It never removes anything or changes a value theirs has. Added lists leaf pointers (an added
// object is listed by its members, an array whole or by element identity), and a leaf in skip is
// never added: it was added before and the user removed it. The result keeps theirs' key order,
// indentation, line endings and final newline; when nothing is added, Body is theirs unchanged.
// Empty theirs is an empty object.
func MergeJSON(theirs, ours []byte, match map[string]string, skip map[string]bool) (Merged, error) {
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
	addMissing(dst, src, "", match, skip, &m)
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

// PackageScripts adds the scripts package.json lacks, in the order given, except those in skip.
func PackageScripts(theirs []byte, scripts [][2]string, skip map[string]bool) (Merged, error) {
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
	return MergeJSON(theirs, b, nil, skip)
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

// addMissing copies into dst what src has and dst lacks, recording leaf pointers in m.
func addMissing(dst, src *jsonx.Object, ptr string, match map[string]string, skip map[string]bool, m *Merged) {
	for _, k := range src.Keys() {
		sv, _ := src.Get(k)
		p := ptr + "/" + escapePointer(k)
		if skip[p] {
			continue
		}
		dv, ok := dst.Get(k)
		if !ok {
			if v, leaves := withoutSkipped(sv, p, skip); len(leaves) > 0 {
				dst.Set(k, v)
				m.Added = append(m.Added, leaves...)
			}
			continue
		}
		do, dObj := dv.(*jsonx.Object)
		so, sObj := sv.(*jsonx.Object)
		if dObj && sObj {
			addMissing(do, so, p, match, skip, m)
			continue
		}
		da, dArr := dv.([]any)
		sa, sArr := sv.([]any)
		if id := match[p]; id != "" && dArr && sArr {
			dst.Set(k, appendMissing(da, sa, id, p, skip, m))
			continue
		}
		if !jsonx.Equal(dv, sv) {
			m.Conflicts = append(m.Conflicts, p)
		}
	}
}

// withoutSkipped is a copy of v at pointer p without the leaves skip names, and the leaves kept.
// An object is a set of leaves; anything else is one leaf.
func withoutSkipped(v any, p string, skip map[string]bool) (any, []string) {
	o, ok := v.(*jsonx.Object)
	if !ok || o.Len() == 0 {
		if skip[p] {
			return nil, nil
		}
		return jsonx.Clone(v), []string{p}
	}
	out := jsonx.NewObject()
	var leaves []string
	for _, k := range o.Keys() {
		cv, _ := o.Get(k)
		if c, l := withoutSkipped(cv, p+"/"+escapePointer(k), skip); len(l) > 0 {
			out.Set(k, c)
			leaves = append(leaves, l...)
		}
	}
	return out, leaves
}

// appendMissing appends the objects of ours whose id field no element of theirs has.
func appendMissing(theirs, ours []any, id, ptr string, skip map[string]bool, m *Merged) []any {
	have := map[string]any{}
	for _, e := range theirs {
		if v, ok := idOf(e, id); ok {
			have[v] = e
		}
	}
	out := theirs
	for _, e := range ours {
		v, ok := idOf(e, id)
		if !ok || skip[ptr+"/"+escapePointer(v)] {
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

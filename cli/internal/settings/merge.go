// Package settings merges makeit's settings diff into Claude Code's settings.json.
//
// It is a three-way merge: the base is what makeit intended at its last run (kept per path
// in state), ours is the diff wanted now, theirs is the file on disk. A path the user changed is
// held and reported once instead of being overwritten, a list item the user deleted is never
// re-added, and uninstall undoes only what makeit wrote.
package settings

import (
	"encoding/json/jsontext"
	"regexp"
	"sort"
	"strings"

	"makeit/internal/jsonx"
)

// Record is what state keeps for one managed path.
type Record struct {
	Kind string `json:"kind"` // "value" or "set"
	// Base is what makeit intended at the last run. For a secret it holds the hash only.
	Base jsonx.Value `json:"base"`
	// Original is the value before makeit first managed the path; nil when it was absent.
	Original *Original `json:"original,omitempty"`
	// Held: the user keeps their own value; stack changes are reported, never applied.
	Held bool `json:"held,omitempty"`
	// Notified is the canonical stack value last reported while held, so each is reported once.
	Notified string `json:"notified,omitempty"`
	// Secret: Base and Original hold hashes, never the value.
	Secret bool `json:"secret,omitempty"`
	// Inserted are the canonical list items makeit added; only these are ever removed.
	Inserted []string `json:"inserted,omitempty"`
	// RemovedByUser are canonical list items the user deleted; never re-added.
	RemovedByUser []string `json:"removedByUser,omitempty"`
	// Extra holds members written by a newer makeit, so they survive a round trip.
	Extra jsontext.Value `json:",embed"`
}

// Original wraps the pre-existing value so that a JSON null is distinct from "absent".
type Original struct {
	Value jsonx.Value `json:"value"`
}

// State maps an RFC 6901 pointer to its record.
type State map[string]*Record

// Action names what the merge did at one path.
type Action string

// Actions reported in a Change.
const (
	Set         Action = "set"
	Restore     Action = "restore"
	Delete      Action = "delete"
	AddItems    Action = "add-items"
	RemoveItems Action = "remove-items"
	KeptTheirs  Action = "kept-theirs"
	Held        Action = "held"
	Conflict    Action = "conflict"
	ReAsserted  Action = "re-asserted"
	Noop        Action = "noop"
)

// Change is one line of the merge report.
type Change struct {
	Path   string `json:"path"`
	Action Action `json:"action"`
	Before any    `json:"-"`
	After  any    `json:"-"`
	Note   string `json:"note,omitempty"`
}

// Options configure which paths merge item by item and which are re-asserted.
type Options struct {
	// SetPaths are arrays merged element by element; every other array is one value.
	SetPaths map[string]bool
	// Enforced paths are re-asserted every run when makeit introduced the value, because a known
	// writer strips them (claude plugin marketplace add drops autoUpdate). A value the user had
	// before makeit managed the path is kept, like any other.
	Enforced map[string]bool
	// External path patterns are left to the claude CLI and never merged.
	External []*regexp.Regexp
	// Force takes the stack value on held and conflicting paths.
	Force bool
}

// DefaultOptions is the merge configuration for user settings.json.
func DefaultOptions() Options {
	return Options{
		SetPaths: map[string]bool{
			"/permissions/allow": true, "/permissions/ask": true,
			"/permissions/deny": true, "/permissions/additionalDirectories": true,
		},
		Enforced: map[string]bool{"/extraKnownMarketplaces/claude-plugins-official/autoUpdate": true},
		// claude plugin install writes enabledPlugins and claude plugin marketplace add writes the
		// makeit entry; claude-plugins-official is declared here because nothing else adds it.
		External: []*regexp.Regexp{
			regexp.MustCompile(`^/enabledPlugins/`),
			regexp.MustCompile(`^/extraKnownMarketplaces/makeit/source(/|$)`),
		},
	}
}

var secretName = regexp.MustCompile(`(?i)(TOKEN|SECRET|KEY|PASSWORD|TOPIC)`)

// IsSecret reports whether a path holds a value that must never be stored or printed.
func IsSecret(key string) bool {
	return strings.HasPrefix(key, "/env/") && secretName.MatchString(key[len("/env/"):])
}

// Pointer joins path segments into an RFC 6901 pointer.
func Pointer(path []string) string {
	var b strings.Builder
	for _, s := range path {
		b.WriteByte('/')
		b.WriteString(strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1"))
	}
	return b.String()
}

// Split parses an RFC 6901 pointer into segments.
func Split(ptr string) []string {
	if ptr == "" {
		return nil
	}
	parts := strings.Split(strings.TrimPrefix(ptr, "/"), "/")
	for i, p := range parts {
		parts[i] = strings.ReplaceAll(strings.ReplaceAll(p, "~1", "/"), "~0", "~")
	}
	return parts
}

type leaf struct {
	path  []string
	isSet bool
	value any
}

// leaves lists the leaf paths of the desired diff. Keys starting with $ are comments.
func leaves(o *jsonx.Object, opts Options, prefix []string, out map[string]leaf) {
	for _, k := range o.Keys() {
		if strings.HasPrefix(k, "$") {
			continue
		}
		v, _ := o.Get(k)
		p := append(append([]string(nil), prefix...), k)
		key := Pointer(p)
		if opts.external(key) {
			continue
		}
		if arr, ok := v.([]any); ok && opts.SetPaths[key] {
			out[key] = leaf{path: p, isSet: true, value: arr}
		} else if sub, ok := v.(*jsonx.Object); ok && sub.Len() > 0 {
			leaves(sub, opts, p, out)
		} else {
			out[key] = leaf{path: p, value: v}
		}
	}
}

func (o Options) external(key string) bool {
	for _, re := range o.External {
		if re.MatchString(key) {
			return true
		}
	}
	return false
}

func get(o *jsonx.Object, p []string) (any, bool) {
	var cur any = o
	for _, seg := range p {
		obj, ok := cur.(*jsonx.Object)
		if !ok {
			return nil, false
		}
		if cur, ok = obj.Get(seg); !ok {
			return nil, false
		}
	}
	return cur, true
}

func set(o *jsonx.Object, p []string, v any) {
	cur := o
	for _, seg := range p[:len(p)-1] {
		next, ok := cur.Get(seg)
		obj, isObj := next.(*jsonx.Object)
		if !ok || !isObj {
			obj = jsonx.NewObject()
			cur.Set(seg, obj)
		}
		cur = obj
	}
	cur.Set(p[len(p)-1], v)
}

// del removes the leaf and every parent object the removal left empty.
func del(o *jsonx.Object, p []string) {
	parents := []*jsonx.Object{o}
	cur := o
	for _, seg := range p[:len(p)-1] {
		next, _ := cur.Get(seg)
		obj, ok := next.(*jsonx.Object)
		if !ok {
			return
		}
		parents = append(parents, obj)
		cur = obj
	}
	cur.Delete(p[len(p)-1])
	for i := len(parents) - 1; i > 0; i-- {
		if parents[i].Len() == 0 {
			parents[i-1].Delete(p[i-1])
		}
	}
}

// matches compares the file's value with a recorded value, hashing for secrets.
func (r *Record) matches(present bool, t any, recorded any) bool {
	if !present {
		return false
	}
	if r.Secret {
		s, _ := recorded.(string)
		return jsonx.Hash(t) == s
	}
	return jsonx.Equal(t, recorded)
}

func newValueRecord(key string, want any, present bool, t any) *Record {
	r := &Record{Kind: "value", Base: jsonx.Value{V: want}, Secret: IsSecret(key)}
	if present {
		r.Original = &Original{Value: jsonx.Value{V: t}}
	}
	if r.Secret {
		r.Base.V = jsonx.Hash(want)
		if r.Original != nil {
			r.Original.Value.V = jsonx.Hash(t)
		}
	}
	return r
}

func (r *Record) setBase(want any) {
	if r.Secret {
		r.Base.V = jsonx.Hash(want)
	} else {
		r.Base.V = want
	}
}

func notice(want any, secret bool) string {
	if secret {
		return jsonx.Hash(want)
	}
	return jsonx.Canonical(want)
}

// restore puts the original value back, or deletes the path when there was none.
// A secret original is a hash, so it cannot be put back: the value is left alone.
func restore(o *jsonx.Object, p []string, r *Record) Action {
	if r.Original == nil {
		del(o, p)
		return Delete
	}
	if r.Secret {
		return KeptTheirs
	}
	if t, ok := get(o, p); ok && jsonx.Equal(t, r.Original.Value.V) {
		return Noop
	}
	set(o, p, jsonx.Clone(r.Original.Value.V))
	return Restore
}

// Result is the merged file, the report and the state to store.
type Result struct {
	Settings *jsonx.Object
	Changes  []Change
	State    State
}

// Changed reports whether the merge would write anything.
func (r Result) Changed() bool {
	for _, c := range r.Changes {
		switch c.Action {
		case Set, Restore, Delete, AddItems, RemoveItems, ReAsserted:
			return true
		}
	}
	return false
}

// Merge applies desired onto theirs given the previous state (nil on the first run).
func Merge(theirs, desired *jsonx.Object, prev State, opts Options) Result {
	result := jsonx.Clone(theirs).(*jsonx.Object)
	want := map[string]leaf{}
	leaves(desired, opts, nil, want)
	keys := map[string]bool{}
	for k := range want {
		keys[k] = true
	}
	for k := range prev {
		keys[k] = true
	}
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	m := merger{result: result, next: State{}, opts: opts}
	for _, key := range sorted {
		d, hasD := want[key]
		r := prev[key]
		var dp *leaf
		if hasD {
			dp = &d
		}
		isSet := (hasD && d.isSet) || (!hasD && r != nil && r.Kind == "set")
		if isSet {
			if r != nil && r.Kind != "set" {
				r = nil
			}
			m.mergeSet(key, Split(key), dp, r)
		} else {
			if r != nil && r.Kind != "value" {
				r = nil
			}
			m.mergeValue(key, Split(key), dp, r)
		}
	}
	return Result{Settings: result, Changes: m.changes, State: m.next}
}

type merger struct {
	result  *jsonx.Object
	changes []Change
	next    State
	opts    Options
}

func (m *merger) add(c Change) { m.changes = append(m.changes, c) }

func (m *merger) mergeSet(key string, path []string, d *leaf, r *Record) {
	t, present := get(m.result, path)
	var dArr []any
	if d != nil {
		dArr = d.value.([]any)
	}
	base := map[string]bool{}
	inserted := map[string]bool{}
	removedByUser := map[string]bool{}
	if r != nil {
		if arr, ok := r.Base.V.([]any); ok {
			for _, x := range arr {
				base[jsonx.Canonical(x)] = true
			}
		}
		for _, c := range r.Inserted {
			inserted[c] = true
		}
		for _, c := range r.RemovedByUser {
			removedByUser[c] = true
		}
	}
	tArr, isArr := t.([]any)
	if present && !isArr {
		m.add(Change{Path: key, Action: Conflict, Before: t, Note: "expected a list; left as is"})
		if r != nil {
			m.next[key] = r
		}
		return
	}
	arr := append([]any(nil), tArr...)
	have := map[string]bool{}
	for _, x := range arr {
		have[jsonx.Canonical(x)] = true
	}
	var added, removed []any
	for _, item := range dArr {
		c := jsonx.Canonical(item)
		switch {
		case !base[c]:
			if !have[c] {
				arr = append(arr, item)
				have[c] = true
				inserted[c] = true
				added = append(added, item)
			}
		case !have[c]:
			delete(inserted, c)
			if m.opts.Force {
				arr = append(arr, item)
				have[c] = true
				inserted[c] = true
				added = append(added, item)
				delete(removedByUser, c)
			} else if !removedByUser[c] {
				removedByUser[c] = true
				m.add(Change{Path: key, Action: KeptTheirs, Before: item, Note: "item removed outside makeit; not re-added"})
			}
		default:
			delete(removedByUser, c)
		}
	}
	wantItems := map[string]bool{}
	for _, x := range dArr {
		wantItems[jsonx.Canonical(x)] = true
	}
	for c := range base {
		if wantItems[c] {
			continue
		}
		if have[c] && inserted[c] {
			for i, x := range arr {
				if jsonx.Canonical(x) == c {
					removed = append(removed, x)
					arr = append(arr[:i], arr[i+1:]...)
					break
				}
			}
		}
		delete(inserted, c)
		delete(removedByUser, c)
	}
	if len(added) > 0 {
		m.add(Change{Path: key, Action: AddItems, After: added})
	}
	if len(removed) > 0 {
		m.add(Change{Path: key, Action: RemoveItems, Before: removed})
	}
	if len(added) > 0 || len(removed) > 0 {
		if len(arr) > 0 {
			set(m.result, path, arr)
		} else {
			del(m.result, path)
		}
	}
	if d != nil {
		m.next[key] = &Record{Kind: "set", Base: jsonx.Value{V: dArr}, Inserted: sortedKeys(inserted), RemovedByUser: sortedKeys(removedByUser)}
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (m *merger) mergeValue(key string, path []string, d *leaf, r *Record) {
	t, present := get(m.result, path)
	if r == nil {
		if d == nil {
			return
		}
		rec := newValueRecord(key, d.value, present, t)
		switch {
		case !present:
			set(m.result, path, jsonx.Clone(d.value))
			m.add(Change{Path: key, Action: Set, After: d.value})
		case jsonx.Equal(t, d.value):
			m.add(Change{Path: key, Action: Noop})
		case m.opts.Force:
			set(m.result, path, jsonx.Clone(d.value))
			m.add(Change{Path: key, Action: ReAsserted, Before: t, After: d.value})
		default:
			// Also on an enforced path: a value that was there before makeit is the user's.
			m.add(Change{Path: key, Action: KeptTheirs, Before: t, After: d.value, Note: "value existed before makeit managed it"})
			rec.Held = true
			rec.Notified = notice(d.value, rec.Secret)
		}
		m.next[key] = rec
		return
	}
	if d == nil {
		// The new diff no longer manages this path.
		if r.matches(present, t, r.Base.V) {
			m.add(Change{Path: key, Action: restore(m.result, path, r), Before: t})
		} else if present {
			m.add(Change{Path: key, Action: KeptTheirs, Before: t, Note: "no longer managed; value kept"})
		}
		return
	}
	want := d.value
	cur := *r
	next := &cur
	m.next[key] = next
	equalWant := present && jsonx.Equal(t, want)
	// Enforcement re-asserts only a value makeit introduced (no Original); Force takes any over.
	if m.opts.Force || (m.opts.Enforced[key] && r.Original == nil) {
		if !equalWant {
			set(m.result, path, jsonx.Clone(want))
			m.add(Change{Path: key, Action: ReAsserted, Before: t, After: want})
		} else {
			m.add(Change{Path: key, Action: Noop})
		}
		next.setBase(want)
		next.Held, next.Notified = false, ""
		return
	}
	if r.Held {
		switch {
		case equalWant:
			m.add(Change{Path: key, Action: Noop, Note: "your value now equals the stack value"})
			next.setBase(want)
			next.Held, next.Notified = false, ""
		case r.Notified != notice(want, r.Secret):
			m.add(Change{Path: key, Action: Held, Before: t, After: want, Note: "stack value changed; yours kept"})
			next.setBase(want)
			next.Notified = notice(want, r.Secret)
		default:
			m.add(Change{Path: key, Action: Noop, Note: "held"})
			next.setBase(want)
		}
		return
	}
	baseWant := (&Record{Secret: r.Secret}).matches(true, want, r.Base.V)
	switch {
	case r.matches(present, t, r.Base.V):
		if baseWant {
			m.add(Change{Path: key, Action: Noop})
		} else {
			set(m.result, path, jsonx.Clone(want))
			m.add(Change{Path: key, Action: Set, Before: t, After: want})
		}
		next.setBase(want)
	case equalWant:
		m.add(Change{Path: key, Action: Noop})
		next.setBase(want)
	case baseWant:
		m.add(Change{Path: key, Action: KeptTheirs, Before: t, After: want, Note: "changed outside makeit"})
		next.Held, next.Notified = true, notice(want, r.Secret)
	default:
		m.add(Change{Path: key, Action: Conflict, Before: t, After: want, Note: "both you and the stack changed it; yours kept"})
		next.setBase(want)
		next.Held, next.Notified = true, notice(want, r.Secret)
	}
}

// Unmerge undoes only what makeit wrote and nobody changed since, and restores values the
// user had before.
func Unmerge(theirs *jsonx.Object, st State) Result {
	result := jsonx.Clone(theirs).(*jsonx.Object)
	var changes []Change
	keys := make([]string, 0, len(st))
	for k := range st {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		r := st[key]
		path := Split(key)
		t, present := get(result, path)
		if r.Kind == "set" {
			arr, ok := t.([]any)
			if !ok {
				continue
			}
			drop := map[string]bool{}
			for _, c := range r.Inserted {
				drop[c] = true
			}
			var kept, gone []any
			for _, x := range arr {
				if drop[jsonx.Canonical(x)] {
					gone = append(gone, x)
				} else {
					kept = append(kept, x)
				}
			}
			if len(gone) > 0 {
				changes = append(changes, Change{Path: key, Action: RemoveItems, Before: gone})
				if len(kept) > 0 {
					set(result, path, kept)
				} else {
					del(result, path)
				}
			}
			continue
		}
		if r.matches(present, t, r.Base.V) {
			changes = append(changes, Change{Path: key, Action: restore(result, path, r), Before: t})
		} else if present {
			changes = append(changes, Change{Path: key, Action: KeptTheirs, Before: t})
		}
	}
	return Result{Settings: result, Changes: changes, State: State{}}
}

// HeldPaths lists the paths where the user's value is kept over the stack's.
func HeldPaths(st State) []string {
	var out []string
	for k, r := range st {
		if r.Held {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

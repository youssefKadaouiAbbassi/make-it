// Package latest moves the external plugins of a plugin set to their newest releases: for each
// plugin pinned to a release tag, the highest stable tag of the same family, read with
// git ls-remote (no token, no rate limit, any git host). It also moves the npx versions of the
// makeit plugin's MCP servers to their newest npm release. It never goes below the pins it is
// given and never takes a release a rollback skipped.
package latest

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"makeit/internal/catalog"
	"makeit/internal/execx"
	"makeit/internal/state"
)

const (
	marketFile = ".claude-plugin/marketplace.json"
	mcpFile    = "plugins/makeit/.mcp.json"
)

var (
	refRe     = regexp.MustCompile(`^(.*?)(\d+\.\d+\.\d+)$`)
	npmQuoted = regexp.MustCompile(`"(@?[a-z0-9][a-z0-9._/-]*)@(\d+\.\d+\.\d+)"`)
	npmArg    = regexp.MustCompile(`^(@?[a-z0-9][a-z0-9._/-]*)@(\d+\.\d+\.\d+)$`)
	numbers   = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
)

// Prefix is the tag family of a release ref ("skill-v4.8.0" → "skill-v"); ok is false for a ref
// that is not a release, such as a branch.
func Prefix(ref string) (prefix string, ok bool) {
	m := refRe.FindStringSubmatch(ref)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// semver is MAJOR.MINOR.PATCH as numbers; ok is false for anything else, pre-releases included.
func semver(v string) (n [3]int, ok bool) {
	if !numbers.MatchString(v) {
		return n, false
	}
	for i, part := range strings.Split(v, ".") {
		n[i], _ = strconv.Atoi(part)
	}
	return n, true
}

func less(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// Pick is the highest stable tag of the family that is at least floor and not skipped.
func Pick(tags map[string]string, prefix, floor string, skip []string) (tag, sha string, ok bool) {
	min, fok := semver(strings.TrimPrefix(floor, prefix))
	var best [3]int
	for t, s := range tags {
		if !strings.HasPrefix(t, prefix) {
			continue
		}
		v, vok := semver(strings.TrimPrefix(t, prefix))
		if !vok || (fok && less(v, min)) || contains(skip, t) {
			continue
		}
		if !ok || less(best, v) {
			best, tag, sha, ok = v, t, s, true
		}
	}
	return tag, sha, ok
}

func contains(xs []string, x string) bool {
	for _, s := range xs {
		if s == x {
			return true
		}
	}
	return false
}

// parseLsRemote reads `git ls-remote --tags`: tag → commit, the peeled commit of an annotated tag.
func parseLsRemote(out string) map[string]string {
	tags := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		sha, ref, found := strings.Cut(strings.TrimSpace(line), "\t")
		if !found || !strings.HasPrefix(ref, "refs/tags/") {
			continue
		}
		name := strings.TrimPrefix(ref, "refs/tags/")
		if peeled, ok := strings.CutSuffix(name, "^{}"); ok {
			tags[peeled] = sha
		} else if _, seen := tags[name]; !seen {
			tags[name] = sha
		}
	}
	return tags
}

func lsRemote(ctx context.Context, run execx.Runner, url string) (map[string]string, error) {
	res, err := run(ctx, execx.Cmd{Argv: []string{"git", "ls-remote", "--tags", url},
		Env: []string{"GIT_TERMINAL_PROMPT=0"}, Timeout: 30 * time.Second})
	// git exits 128 for an unreachable remote; execx keeps that as the code, not as an error.
	if err != nil || res.Code != 0 {
		why := res.Text()
		if err != nil {
			why += " " + err.Error()
		}
		return nil, fmt.Errorf("%s: %s", url, strings.TrimSpace(why))
	}
	return parseLsRemote(res.Stdout), nil
}

// Newest is the highest stable release of a GitHub repository, without its "v", read with git
// ls-remote (no token, no rate limit); a tag in skip is passed over.
func Newest(ctx context.Context, run execx.Runner, repo string, skip []string) (string, error) {
	tags, err := lsRemote(ctx, run, "https://github.com/"+repo+".git")
	if err != nil {
		return "", err
	}
	tag, _, ok := Pick(tags, "v", "", skip)
	if !ok {
		return "", fmt.Errorf("%s has no stable release tag", repo)
	}
	return strings.TrimPrefix(tag, "v"), nil
}

type pin struct{ ref, sha string }

// pins reads a plugin set's release pins by plugin name.
func pins(t *catalog.Tree) map[string]pin {
	out := map[string]pin{}
	if t == nil {
		return out
	}
	var m struct {
		Plugins []struct {
			Name   string         `json:"name"`
			Source jsontext.Value `json:"source"` // a string for the plugins inside the set
		} `json:"plugins"`
	}
	b, err := t.ReadFile(marketFile)
	if err != nil || json.Unmarshal(b, &m) != nil {
		return out
	}
	for _, p := range m.Plugins {
		var src struct {
			Ref string `json:"ref"`
			SHA string `json:"sha"`
		}
		if json.Unmarshal(p.Source, &src) == nil && src.Ref != "" {
			out[p.Name] = pin{src.Ref, src.SHA}
		}
	}
	return out
}

// Apply returns t with each external plugin at its newest release and the MCP servers at their
// newest npm versions, the moves it made (from the pins in current, the plugin set on disk, when
// it has one), and a note for each plugin it could not check. A plugin starts from the higher of
// its pin in t and in current, so a pin on disk that is ahead stays when nothing newer is found.
// A nil run is offline: the pins on disk carry forward and nothing is fetched.
func Apply(ctx context.Context, run execx.Runner, t, current *catalog.Tree, skips map[string][]string) (*catalog.Tree, []state.PinMove, []string) {
	files := map[string][]byte{}
	for k, v := range t.Files {
		files[k] = v
	}
	var moves []state.PinMove
	var notes []string
	changed := false
	// The git probes and the npm probes run as two batches at the same time; each batch writes
	// its own variables, and files is touched only once both are done.
	var wg sync.WaitGroup
	var mOut, cOut []byte
	var mChanged, cChanged bool
	marketB, hasMarket := files[marketFile]
	if hasMarket {
		cur := pins(current)
		wg.Add(1)
		go func() { defer wg.Done(); mOut, moves, notes, mChanged = applyMarket(ctx, run, marketB, cur, skips) }()
	}
	mcpB, hasMCP := files[mcpFile]
	if hasMCP {
		var cur []byte // nil on a fresh machine, which has no plugin set yet
		if current != nil {
			cur, _ = current.ReadFile(mcpFile)
		}
		wg.Add(1)
		go func() { defer wg.Done(); cOut, cChanged = applyMCP(ctx, run, mcpB, cur) }()
	}
	wg.Wait()
	if hasMarket {
		files[marketFile], changed = mOut, mChanged
	}
	if hasMCP {
		files[mcpFile], changed = cOut, changed || cChanged
	}
	if !changed {
		return t, nil, notes
	}
	exec := map[string]bool{}
	for _, e := range t.Manifest.Files {
		if e.Mode == "0755" {
			exec[e.Path] = true
		}
	}
	nt, err := catalog.TreeOf(files, exec)
	if err != nil {
		return t, nil, append(notes, "kept every plugin at its pin: "+err.Error())
	}
	return nt, moves, notes
}

func applyMarket(ctx context.Context, run execx.Runner, b []byte, current map[string]pin, skips map[string][]string) ([]byte, []state.PinMove, []string, bool) {
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return b, nil, []string{"could not read the plugin set: " + err.Error()}, false
	}
	market, _ := m["name"].(string)
	plugins, _ := m["plugins"].([]any)
	var moves []state.PinMove
	var notes []string
	rewritten := false
	var jobs []*tagJob
	for _, raw := range plugins {
		entry, _ := raw.(map[string]any)
		src, _ := entry["source"].(map[string]any)
		name, _ := entry["name"].(string)
		if src == nil {
			continue
		}
		ref, _ := src["ref"].(string)
		sha, _ := src["sha"].(string)
		prefix, ok := Prefix(ref)
		if !ok || sha == "" {
			continue
		}
		url, _ := src["url"].(string)
		if repo, _ := src["repo"].(string); repo != "" {
			url = "https://github.com/" + repo + ".git"
		}
		if url == "" {
			continue
		}
		id := name + "@" + market
		from := pin{ref, sha}
		if cur, ok := current[name]; ok && ahead(cur, from, prefix) {
			from = cur
			src["ref"], src["sha"] = cur.ref, cur.sha
			rewritten = true
		}
		if run == nil {
			continue
		}
		jobs = append(jobs, &tagJob{src: src, id: id, name: name, from: from, prefix: prefix, url: url})
	}
	// One probe per source, all at once: six sources took six times 0.4 s in a row.
	var wg sync.WaitGroup
	for _, j := range jobs {
		wg.Add(1)
		go func() { defer wg.Done(); j.tags, j.err = lsRemote(ctx, run, j.url) }()
	}
	wg.Wait()
	for _, j := range jobs {
		if j.err != nil {
			notes = append(notes, fmt.Sprintf("%s kept at %s: could not read its releases (%v)", j.name, j.from.ref, j.err))
			continue
		}
		tag, tagSHA, ok := Pick(j.tags, j.prefix, j.from.ref, skips[j.id])
		if !ok || tag == j.from.ref {
			continue
		}
		j.src["ref"], j.src["sha"] = tag, tagSHA
		rewritten = true
		moves = append(moves, state.PinMove{ID: j.id, FromTag: j.from.ref, FromSHA: j.from.sha, ToTag: tag, ToSHA: tagSHA})
	}
	if !rewritten {
		return b, nil, notes, false
	}
	out, err := json.Marshal(m, json.Deterministic(true))
	if err != nil {
		return b, nil, append(notes, "could not write the plugin set: "+err.Error()), false
	}
	return out, moves, notes, true
}

// tagJob is one plugin source to probe for a newer release, and what the probe found.
type tagJob struct {
	src      map[string]any
	id, name string
	from     pin
	prefix   string
	url      string
	tags     map[string]string
	err      error
}

// ahead reports a pin on disk of the same tag family as the shipped one and a newer release.
func ahead(cur, shipped pin, prefix string) bool {
	if cur.sha == "" || !strings.HasPrefix(cur.ref, prefix) {
		return false
	}
	cv, cok := semver(strings.TrimPrefix(cur.ref, prefix))
	sv, sok := semver(strings.TrimPrefix(shipped.ref, prefix))
	return cok && sok && less(sv, cv)
}

// npmPins reads the package@version arguments of an .mcp.json.
func npmPins(b []byte) map[string]string {
	out := map[string]string{}
	for _, mm := range npmQuoted.FindAllStringSubmatch(string(b), -1) {
		out[mm[1]] = mm[2]
	}
	return out
}

// MCPChanges are the MCP servers whose npm version moved between the plugin set on disk and a
// new one; nothing on disk (a fresh machine) is not a move.
func MCPChanges(current, next *catalog.Tree) []state.Change {
	if current == nil || next == nil {
		return nil
	}
	cur, _ := current.ReadFile(mcpFile)
	nxt, _ := next.ReadFile(mcpFile)
	was, now := npmPins(cur), npmPins(nxt)
	var out []state.Change
	for _, pkg := range slices.Sorted(maps.Keys(now)) {
		if from, ok := was[pkg]; ok && from != now[pkg] {
			out = append(out, state.Change{Kind: "mcp", Item: pkg, From: from, To: now[pkg]})
		}
	}
	return out
}

// npmJob is one npm package argument to probe for a newer version.
type npmJob struct {
	args   []any
	i      int
	pkg    string
	cv     [3]int
	latest string
}

func applyMCP(ctx context.Context, run execx.Runner, b, current []byte) ([]byte, bool) {
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		return b, false
	}
	onDisk := npmPins(current)
	servers, _ := m["mcpServers"].(map[string]any)
	changed := false
	var jobs []*npmJob
	for _, raw := range servers {
		srv, _ := raw.(map[string]any)
		args, _ := srv["args"].([]any)
		for i, a := range args {
			s, _ := a.(string)
			mm := npmArg.FindStringSubmatch(s)
			if mm == nil {
				continue
			}
			pkg, version := mm[1], mm[2]
			cv, _ := semver(version)
			if dv, ok := semver(onDisk[pkg]); ok && less(cv, dv) {
				version, cv = onDisk[pkg], dv
				args[i] = pkg + "@" + version
				changed = true
			}
			if run == nil {
				continue
			}
			jobs = append(jobs, &npmJob{args: args, i: i, pkg: pkg, cv: cv})
		}
	}
	var wg sync.WaitGroup
	for _, j := range jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := run(ctx, execx.Cmd{Argv: []string{"npm", "view", j.pkg, "version"}, Timeout: 30 * time.Second})
			if err == nil {
				j.latest = strings.TrimSpace(res.Stdout)
			}
		}()
	}
	wg.Wait()
	for _, j := range jobs {
		if lv, ok := semver(j.latest); ok && less(j.cv, lv) {
			j.args[j.i] = j.pkg + "@" + j.latest
			changed = true
		}
	}
	if !changed {
		return b, false
	}
	out, err := json.Marshal(m, json.Deterministic(true))
	if err != nil {
		return b, false
	}
	return out, true
}

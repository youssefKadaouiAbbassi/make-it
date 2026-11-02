// Package latest moves the external plugins of a plugin set to their newest releases: for each
// plugin pinned to a release tag, the highest stable tag of the same family, read with
// git ls-remote (no token, no rate limit, any git host). It also moves the npx versions of the
// makeit plugin's MCP servers to their newest npm release. It never goes below the pins it is
// given and never takes a release a rollback skipped.
package latest

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"regexp"
	"strconv"
	"strings"
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
	refRe   = regexp.MustCompile(`^(.*?)(\d+\.\d+\.\d+)$`)
	npmArg  = regexp.MustCompile(`^(@?[a-z0-9][a-z0-9._/-]*)@(\d+\.\d+\.\d+)$`)
	numbers = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
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
	if err != nil {
		return nil, fmt.Errorf("%s: %s", url, strings.TrimSpace(res.Text()+" "+err.Error()))
	}
	return parseLsRemote(res.Stdout), nil
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
			Source map[string]any `json:"source"`
		} `json:"plugins"`
	}
	b, err := t.ReadFile(marketFile)
	if err != nil || json.Unmarshal(b, &m) != nil {
		return out
	}
	for _, p := range m.Plugins {
		ref, _ := p.Source["ref"].(string)
		sha, _ := p.Source["sha"].(string)
		out[p.Name] = pin{ref, sha}
	}
	return out
}

// Apply returns t with each external plugin at its newest release and the MCP servers at their
// newest npm versions, the moves it made (from the pins in current, the plugin set on disk, when
// it has one), and a note for each plugin it could not check. The floor of a plugin is the higher
// of its pin in t and in current.
func Apply(ctx context.Context, run execx.Runner, t, current *catalog.Tree, skips map[string][]string) (*catalog.Tree, []state.PinMove, []string) {
	files := map[string][]byte{}
	for k, v := range t.Files {
		files[k] = v
	}
	var moves []state.PinMove
	var notes []string
	changed := false
	if b, ok := files[marketFile]; ok {
		out, mv, ns, c := applyMarket(ctx, run, b, pins(current), skips)
		files[marketFile], moves, notes, changed = out, mv, ns, c
	}
	if b, ok := files[mcpFile]; ok {
		out, c := applyMCP(ctx, run, b)
		files[mcpFile], changed = out, changed || c
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
		floor := ref
		from := pin{ref, sha}
		if cur, ok := current[name]; ok && cur.ref != "" {
			from = cur
			if cv, ok := semver(strings.TrimPrefix(cur.ref, prefix)); ok {
				if fv, ok := semver(strings.TrimPrefix(ref, prefix)); ok && less(fv, cv) {
					floor = cur.ref
				}
			}
		}
		tags, err := lsRemote(ctx, run, url)
		if err != nil {
			notes = append(notes, fmt.Sprintf("%s kept at %s: could not read its releases (%v)", name, from.ref, err))
			continue
		}
		tag, tagSHA, ok := Pick(tags, prefix, floor, skips[id])
		if !ok || tag == ref {
			continue
		}
		src["ref"], src["sha"] = tag, tagSHA
		rewritten = true
		if tag != from.ref {
			moves = append(moves, state.PinMove{ID: id, FromTag: from.ref, FromSHA: from.sha, ToTag: tag, ToSHA: tagSHA})
		}
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

func applyMCP(ctx context.Context, run execx.Runner, b []byte) ([]byte, bool) {
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		return b, false
	}
	servers, _ := m["mcpServers"].(map[string]any)
	changed := false
	for _, raw := range servers {
		srv, _ := raw.(map[string]any)
		args, _ := srv["args"].([]any)
		for i, a := range args {
			s, _ := a.(string)
			mm := npmArg.FindStringSubmatch(s)
			if mm == nil {
				continue
			}
			res, err := run(ctx, execx.Cmd{Argv: []string{"npm", "view", mm[1], "version"}, Timeout: 30 * time.Second})
			if err != nil {
				continue
			}
			latest := strings.TrimSpace(res.Stdout)
			lv, lok := semver(latest)
			cv, _ := semver(mm[2])
			if lok && less(cv, lv) {
				args[i] = mm[1] + "@" + latest
				changed = true
			}
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

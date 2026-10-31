package initrepo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"makeit/internal/execx"
	"makeit/internal/fsx"
	"makeit/internal/jsonx"
	"makeit/internal/plan"
	"makeit/internal/state"
)

// rulesetFiles are the catalog's GitHub rulesets, sent as desired state by name.
var rulesetFiles = []string{"ci/rulesets/no-force-push.json", "ci/rulesets/default-branch.json"}

// ghEnv keeps gh's telemetry off for every gh makeit runs (INIT.md item 10).
var ghEnv = []string{"GH_TELEMETRY=false"}

// errNoRemote refuses --rulesets in a repository GitHub does not host.
var errNoRemote = errors.New("--rulesets needs a GitHub remote (origin on github.com)")

// ghError is gh api's failure: the HTTP status and message GitHub returned.
type ghError struct {
	Status  string
	Message string
}

func (e *ghError) Error() string { return e.Message + " (HTTP " + e.Status + ")" }

// gh runs gh api with telemetry off. On an HTTP error gh exits 1 and prints GitHub's JSON error
// body on stdout, which becomes a *ghError.
func (r *run) gh(ctx context.Context, stdin []byte, args ...string) (string, error) {
	c := execx.Cmd{Argv: append([]string{"gh", "api"}, args...), Dir: r.Dir, Env: ghEnv}
	if stdin != nil {
		c.Stdin = bytes.NewReader(stdin)
	}
	res, err := r.Run(ctx, c)
	if err != nil {
		return "", err
	}
	if res.Code != 0 {
		var e ghError
		if json.Unmarshal([]byte(res.Stdout), &e) == nil && e.Message != "" {
			return "", &e
		}
		return "", fmt.Errorf("gh api %s: %s", strings.Join(args, " "), res.Text())
	}
	return res.Stdout, nil
}

// rulesetSteps send each catalog ruleset as desired state: created when the repository has none
// by that name, replaced when it differs, left alone when it matches. default-branch requires the
// gate's checks, so it waits until each has passed once on the default branch head; otherwise
// every merge would be blocked. Each step records what it sent in .makeit.json itself, after the
// record step, so a refused ruleset never stops the file record.
func (r *run) rulesetSteps(ctx context.Context) ([]plan.Step, error) {
	repo := "repos/" + r.p.GitHub.Owner + "/" + r.p.GitHub.Repo
	var steps []plan.Step
	for _, f := range rulesetFiles {
		body, err := r.Catalog.ReadFile(f)
		if err != nil {
			return nil, err
		}
		// jsonx keeps numbers and objects in the form its compare understands.
		desired, err := jsonx.DecodeObject(body)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		nv, _ := desired.Get("name")
		name, _ := nv.(string)
		blocked := ""
		if checks := requiredChecks(desired); len(checks) > 0 {
			missing, branch, err := r.missingChecks(ctx, repo, checks)
			if err != nil {
				return nil, err
			}
			if len(missing) > 0 {
				blocked = "not sent until these checks have passed once on " + branch + ": " + strings.Join(missing, ", ")
			}
		}
		steps = append(steps, r.rulesetStep(repo, name, body, desired, blocked))
	}
	return steps, nil
}

func (r *run) rulesetStep(repo, name string, body []byte, desired *jsonx.Object, blocked string) plan.Step {
	id := 0
	return plan.Step{ID: "ruleset:" + name, Title: "Send the GitHub ruleset " + name,
		Command: []string{"gh", "api", "-X", "POST|PUT", repo + "/rulesets", "--input", "-"},
		Already: func(ctx context.Context) (bool, error) {
			list, err := r.gh(ctx, nil, repo+"/rulesets?per_page=100&includes_parents=false")
			if err != nil {
				return false, err
			}
			var have []struct {
				ID         int    `json:"id"`
				Name       string `json:"name"`
				SourceType string `json:"source_type"`
			}
			if err := json.Unmarshal([]byte(list), &have); err != nil {
				return false, fmt.Errorf("gh api %s/rulesets: %w", repo, err)
			}
			id = 0
			if i := slices.IndexFunc(have, func(h struct {
				ID         int    `json:"id"`
				Name       string `json:"name"`
				SourceType string `json:"source_type"`
			}) bool {
				return h.Name == name && h.SourceType == "Repository"
			}); i >= 0 {
				id = have[i].ID
				server, err := r.gh(ctx, nil, repo+"/rulesets/"+strconv.Itoa(id))
				if err != nil {
					return false, err
				}
				got, err := jsonx.DecodeObject([]byte(server))
				if err != nil {
					return false, fmt.Errorf("gh api %s/rulesets/%d: %w", repo, id, err)
				}
				if sameRuleset(desired, got) {
					return true, nil
				}
			}
			if blocked != "" {
				r.keep("ruleset "+name, blocked)
				return true, nil
			}
			return false, nil
		},
		Run: func(ctx context.Context) (string, error) {
			args := []string{"-X", "POST", repo + "/rulesets", "--input", "-"}
			if id != 0 {
				args = []string{"-X", "PUT", repo + "/rulesets/" + strconv.Itoa(id), "--input", "-"}
			}
			out, err := r.gh(ctx, body, args...)
			var ge *ghError
			if errors.As(err, &ge) && ge.Status == "403" && strings.Contains(ge.Message, "Upgrade to GitHub Pro") {
				return "rulesets need a public repository, or GitHub Pro, Team or Enterprise for a private one", plan.ErrSkip
			}
			if err != nil {
				return "", err
			}
			if id == 0 {
				var created struct {
					ID int `json:"id"`
				}
				if err := json.Unmarshal([]byte(out), &created); err != nil || created.ID == 0 {
					return "", fmt.Errorf("gh api %s/rulesets: no id in the response", repo)
				}
				id = created.ID
			}
			return "", r.recordRuleset(name, &state.RepoRuleset{ID: int64(id), SentSha256: fsx.Sha256(body)})
		}}
}

// recordRuleset keeps what a ruleset step sent, in the record the record step wrote.
func (r *run) recordRuleset(name string, rs *state.RepoRuleset) error {
	path := filepath.Join(r.Dir, RecordName)
	if r.linked(RecordName) {
		return fmt.Errorf("%s is a symlink: the ruleset was sent but not recorded", RecordName)
	}
	rec, _, err := state.LoadRepo(path)
	if err != nil {
		return err
	}
	rec.Rulesets[name] = rs
	return state.SaveRepo(path, rec)
}

// requiredChecks are the contexts a ruleset's required_status_checks rule names.
func requiredChecks(rs *jsonx.Object) []string {
	var out []string
	for _, rule := range asList(get(rs, "rules")) {
		m, _ := rule.(*jsonx.Object)
		if get(m, "type") != "required_status_checks" {
			continue
		}
		params, _ := get(m, "parameters").(*jsonx.Object)
		for _, c := range asList(get(params, "required_status_checks")) {
			if cm, ok := c.(*jsonx.Object); ok {
				if s, ok := get(cm, "context").(string); ok {
					out = append(out, s)
				}
			}
		}
	}
	return out
}

// get is o's member k, or nil for a missing key or a nil object.
func get(o *jsonx.Object, k string) any {
	if o == nil {
		return nil
	}
	v, _ := o.Get(k)
	return v
}

// missingChecks are the contexts that have no completed, successful check run on the default
// branch head, and that branch.
func (r *run) missingChecks(ctx context.Context, repo string, contexts []string) ([]string, string, error) {
	out, err := r.gh(ctx, nil, repo, "--jq", ".default_branch")
	if err != nil {
		return nil, "", err
	}
	branch := strings.TrimSpace(out)
	if out, err = r.gh(ctx, nil, repo+"/commits/"+branch, "--jq", ".sha"); err != nil {
		var ge *ghError
		if errors.As(err, &ge) && (ge.Status == "404" || ge.Status == "409") {
			// An empty repository has no head yet: nothing has passed.
			return contexts, branch, nil
		}
		return nil, "", err
	}
	sha := strings.TrimSpace(out)
	out, err = r.gh(ctx, nil, "--paginate", repo+"/commits/"+sha+"/check-runs?per_page=100&filter=latest",
		"--jq", `.check_runs[] | [.name, .status, (.conclusion // "")] | @tsv`)
	if err != nil {
		return nil, "", err
	}
	passed := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, "\t")
		if len(f) == 3 && f[1] == "completed" && f[2] == "success" {
			passed[f[0]] = true
		}
	}
	var missing []string
	for _, c := range contexts {
		if !passed[c] {
			missing = append(missing, c)
		}
	}
	return missing, branch, nil
}

// serverKeys are what GitHub adds to a ruleset; a desired-state compare ignores them.
var serverKeys = map[string]bool{"id": true, "source": true, "source_type": true, "node_id": true, "_links": true,
	"created_at": true, "updated_at": true, "current_user_can_bypass": true}

// sameRuleset compares a desired ruleset with the server's copy as GitHub returns it: key order,
// the order of lists that are sets, keys only the server has, and an absent bypass_actors do not
// count; every value the desired file sets does.
func sameRuleset(desired, server *jsonx.Object) bool {
	for _, k := range desired.Keys() {
		if serverKeys[k] {
			continue
		}
		v := get(desired, k)
		sv, ok := server.Get(k)
		switch k {
		case "bypass_actors":
			if !ok {
				sv = []any{}
			}
			if !sameSet(asList(v), asList(sv)) {
				return false
			}
		case "conditions":
			if !sameConditions(v, sv) {
				return false
			}
		case "rules":
			if !sameRules(asList(v), asList(sv)) {
				return false
			}
		default:
			if !ok || !jsonx.Equal(v, sv) {
				return false
			}
		}
	}
	return true
}

func sameConditions(desired, server any) bool {
	d, _ := desired.(*jsonx.Object)
	s, _ := server.(*jsonx.Object)
	if d == nil || s == nil {
		return jsonx.Equal(desired, server)
	}
	for _, k := range d.Keys() {
		dm, dok := get(d, k).(*jsonx.Object)
		sm, sok := get(s, k).(*jsonx.Object)
		if !dok || !sok {
			if !jsonx.Equal(get(d, k), get(s, k)) {
				return false
			}
			continue
		}
		for _, kk := range dm.Keys() {
			if !sameSet(asList(get(dm, kk)), asList(get(sm, kk))) {
				return false
			}
		}
	}
	return true
}

// sameRules matches rules by type, both ways; within a rule every desired parameter must equal the
// server's, lists compared as sets; parameters only the server has are ignored.
func sameRules(desired, server []any) bool {
	byType := map[string]*jsonx.Object{}
	for _, r := range server {
		if m, ok := r.(*jsonx.Object); ok {
			t, _ := get(m, "type").(string)
			byType[t] = m
		}
	}
	if len(byType) != len(desired) {
		return false
	}
	for _, r := range desired {
		m, _ := r.(*jsonx.Object)
		t, _ := get(m, "type").(string)
		s, ok := byType[t]
		if !ok {
			return false
		}
		dp, _ := get(m, "parameters").(*jsonx.Object)
		sp, _ := get(s, "parameters").(*jsonx.Object)
		if dp == nil {
			continue
		}
		for _, k := range dp.Keys() {
			v := get(dp, k)
			if l, isList := v.([]any); isList {
				if !sameSet(l, asList(get(sp, k))) {
					return false
				}
			} else if !jsonx.Equal(v, get(sp, k)) {
				return false
			}
		}
	}
	return true
}

// sameSet compares two lists as sets of canonical JSON values.
func sameSet(a, b []any) bool {
	if len(a) != len(b) {
		return false
	}
	count := map[string]int{}
	for _, v := range a {
		count[jsonx.Canonical(v)]++
	}
	for _, v := range b {
		k := jsonx.Canonical(v)
		if count[k] == 0 {
			return false
		}
		count[k]--
	}
	return true
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

package initrepo

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"makeit/internal/catalog"
	"makeit/internal/fsx"
	"makeit/internal/plan"
	"makeit/internal/setup"
)

var gateContexts = []string{"gate / check:full", "gate / floor-guard", "gate / betterleaks", "gate / semgrep", "gate / zizmor", "gate / pinact check", "gate / syft + grype"}

// ghServer is a fake GitHub for o/r: rulesets by id, check runs on the default branch's head, and
// what POST and PUT received.
type ghServer struct {
	rulesets map[int]string // id -> server body (name read from it)
	passed   []string       // check runs that completed with success
	posted   []string
	put      map[string]string
	postBody string // what POST answers, default {"id": 42}
	postCode int
}

func (g *ghServer) handle(argv []string, stdin string) (string, int) {
	a := strings.Join(argv, " ")
	switch {
	case a == "gh api repos/o/r/rulesets?per_page=100&includes_parents=false":
		var list []string
		for id, body := range g.rulesets {
			name := strings.Split(strings.SplitN(body, `"name": "`, 2)[1], `"`)[0]
			list = append(list, `{"id": `+strconv.Itoa(id)+`, "name": "`+name+`", "source_type": "Repository", "source": "o/r"}`)
		}
		return "[" + strings.Join(list, ",") + "]", 0
	case strings.HasPrefix(a, "gh api repos/o/r/rulesets/") && len(argv) == 3:
		id := strings.TrimPrefix(argv[2], "repos/o/r/rulesets/")
		for k, body := range g.rulesets {
			if strconv.Itoa(k) == id {
				return body, 0
			}
		}
		return `{"message": "Not Found", "status": "404"}`, 1
	case a == "gh api repos/o/r --jq .default_branch":
		return "main\n", 0
	case a == "gh api repos/o/r/commits/main --jq .sha":
		return "abc123\n", 0
	case strings.HasPrefix(a, "gh api --paginate repos/o/r/commits/abc123/check-runs?per_page=100&filter=latest --jq "):
		var rows []string
		for _, c := range g.passed {
			rows = append(rows, c+"\tcompleted\tsuccess")
		}
		if !slices.Contains(g.passed, "gate / zizmor") {
			// filter=latest gives one run per name: here the latest zizmor run is still going.
			rows = append(rows, "gate / zizmor\tin_progress\t")
		}
		return strings.Join(rows, "\n") + "\n", 0
	case a == "gh api -X POST repos/o/r/rulesets --input -":
		g.posted = append(g.posted, stdin)
		if g.postCode != 0 {
			return g.postBody, g.postCode
		}
		return `{"id": 42, "name": "x"}`, 0
	case strings.HasPrefix(a, "gh api -X PUT repos/o/r/rulesets/"):
		g.put[strings.TrimSuffix(strings.TrimPrefix(a, "gh api -X PUT repos/o/r/rulesets/"), " --input -")] = stdin
		return `{"id": 0}`, 0
	}
	return `{"message": "unexpected call ` + a + `", "status": "500"}`, 1
}

func githubRepo(t *testing.T) string {
	return tree(t, map[string]string{".git/config": "[remote \"origin\"]\n\turl = https://github.com/o/r.git\n", "go.mod": "module x\n"})
}

func catalogFile(t *testing.T, p string) []byte {
	t.Helper()
	b, err := catalog.Embedded.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRulesetsNeedRemoteAndGh(t *testing.T) {
	e, _ := testEnv(t, tree(t, map[string]string{".git/": "", "go.mod": "module x\n"}))
	if _, err := Run(context.Background(), e, plan.Apply, nil, Options{Rulesets: true}); err == nil || err.Error() != "--rulesets needs a GitHub remote (origin on github.com)" {
		t.Errorf("no remote: %v", err)
	}
	e, f := testEnv(t, githubRepo(t))
	e.Present = func(name string) bool { return name != "gh" }
	rep, err := Run(context.Background(), e, plan.Apply, nil, Options{Rulesets: true})
	if err != nil || !slices.ContainsFunc(rep.Notices, func(n string) bool { return strings.Contains(n, "--rulesets needs gh") }) {
		t.Errorf("no gh: %v %q", err, rep.Notices)
	}
	if slices.ContainsFunc(f.take(), func(c string) bool { return strings.HasPrefix(c, "gh ") }) {
		t.Error("gh ran without being present")
	}
}

// Absent rulesets are created; default-branch waits until each required check has passed once on
// the default branch, and the record keeps what was sent.
func TestRulesetsCreate(t *testing.T) {
	dir := githubRepo(t)
	e, f := testEnv(t, dir)
	g := &ghServer{rulesets: map[int]string{}, passed: slices.DeleteFunc(slices.Clone(gateContexts), func(c string) bool { return c == "gate / semgrep" || c == "gate / zizmor" }), put: map[string]string{}}
	f.gh = g.handle
	rep, err := Run(context.Background(), e, plan.Apply, nil, Options{Rulesets: true})
	if err != nil || rep.Plan.Failed() {
		t.Fatalf("%v %s", err, rep.Plan.Summary())
	}
	nfp := catalogFile(t, "ci/rulesets/no-force-push.json")
	if len(g.posted) != 1 || g.posted[0] != string(nfp) {
		t.Fatalf("posted %q", g.posted)
	}
	want := setup.Kept{Item: "ruleset default-branch", Reason: "not sent until these checks have passed once on main: gate / semgrep, gate / zizmor"}
	if !slices.Contains(rep.Kept, want) {
		t.Errorf("kept %v", rep.Kept)
	}
	r := loadRepo(t, dir).Rulesets["no-force-push"]
	if r == nil || r.ID != 42 || r.SentSha256 != fsx.Sha256(nfp) {
		t.Errorf("record %+v", r)
	}
	if _, ok := loadRepo(t, dir).Rulesets["default-branch"]; ok {
		t.Error("default-branch recorded without being sent")
	}
	if !slices.ContainsFunc(rep.Plan.Outcomes, func(o plan.Outcome) bool { return o.ID == "ruleset:no-force-push" && o.Status == plan.Done }) {
		t.Errorf("outcomes %+v", rep.Plan.Outcomes)
	}
}

// What the server sends back has more keys, other orders and server-side defaults; none of that
// is a difference. A real difference is sent with PUT.
func TestRulesetsAlreadyAndUpdate(t *testing.T) {
	serverNFP := `{"id": 7, "name": "no-force-push", "node_id": "RRS_x", "source_type": "Repository", "source": "o/r", "enforcement": "active", "target": "branch",
		"conditions": {"ref_name": {"exclude": [], "include": ["~ALL"]}}, "rules": [{"type": "non_fast_forward"}],
		"created_at": "2026-10-31T00:00:00Z", "updated_at": "2026-10-31T00:00:00Z", "_links": {}, "current_user_can_bypass": "never"}`
	db := string(catalogFile(t, "ci/rulesets/default-branch.json"))
	// The server's copy: keys reordered by the encoding, merge methods and checks in another order,
	// extra pull_request parameters, and bypass_actors absent.
	serverDB := strings.NewReplacer(
		`"allowed_merge_methods": ["squash", "merge"]`, `"allowed_merge_methods": ["merge", "squash"], "required_reviewers": [], "dismissal_restriction": {"enabled": false, "allowed_actors": []}, "require_extra_approval_for_unattributed_changes": true`,
		`{ "context": "gate / check:full" },`, ``,
		`{ "context": "gate / syft + grype" }`, `{ "context": "gate / syft + grype" }, { "context": "gate / check:full" }`,
		`"bypass_actors": [],`, ``,
	).Replace(db)
	serverDB = strings.Replace(serverDB, `"name": "default-branch",`, `"id": 8, "source_type": "Repository", "name": "default-branch",`, 1)
	dir := githubRepo(t)
	e, f := testEnv(t, dir)
	g := &ghServer{rulesets: map[int]string{7: serverNFP, 8: serverDB}, passed: gateContexts, put: map[string]string{}}
	f.gh = g.handle
	rep, err := Run(context.Background(), e, plan.Apply, nil, Options{Rulesets: true})
	if err != nil || rep.Plan.Failed() {
		t.Fatalf("%v %s", err, rep.Plan.Summary())
	}
	for _, o := range rep.Plan.Outcomes {
		if strings.HasPrefix(o.ID, "ruleset:") && o.Status != plan.Already {
			t.Errorf("%s: %s", o.ID, o.Status)
		}
	}
	if len(g.posted)+len(g.put) != 0 {
		t.Fatalf("sent %q %q", g.posted, g.put)
	}
	g.rulesets[8] = strings.Replace(serverDB, `"required_approving_review_count": 0`, `"required_approving_review_count": 1`, 1)
	if _, err := Run(context.Background(), e, plan.Apply, nil, Options{Rulesets: true}); err != nil {
		t.Fatal(err)
	}
	if g.put["8"] != db {
		t.Errorf("PUT %q", g.put)
	}
	if r := loadRepo(t, dir).Rulesets["default-branch"]; r == nil || r.ID != 8 {
		t.Errorf("record %+v", r)
	}
	// A list that differs as a set is a difference too.
	g.rulesets[7] = strings.Replace(serverNFP, `"include": ["~ALL"]`, `"include": ["~DEFAULT_BRANCH"]`, 1)
	g.rulesets[8] = serverDB
	g.put = map[string]string{}
	if _, err := Run(context.Background(), e, plan.Apply, nil, Options{Rulesets: true}); err != nil {
		t.Fatal(err)
	}
	if g.put["7"] != string(catalogFile(t, "ci/rulesets/no-force-push.json")) || len(g.put) != 1 {
		t.Errorf("a changed include list was not sent: %q", g.put)
	}
}

func TestRulesetsRefused(t *testing.T) {
	dir := githubRepo(t)
	e, f := testEnv(t, dir)
	g := &ghServer{rulesets: map[int]string{}, put: map[string]string{}, postCode: 1,
		postBody: `{"message": "Upgrade to GitHub Pro or make this repository public to enable this feature.", "documentation_url": "x", "status": "403"}`}
	f.gh = g.handle
	rep, err := Run(context.Background(), e, plan.Apply, nil, Options{Rulesets: true})
	if err != nil || rep.Plan.Failed() {
		t.Fatalf("%v %s", err, rep.Plan.Summary())
	}
	i := slices.IndexFunc(rep.Plan.Outcomes, func(o plan.Outcome) bool { return o.ID == "ruleset:no-force-push" })
	if i < 0 || rep.Plan.Outcomes[i].Status != plan.Skipped || !strings.Contains(rep.Plan.Outcomes[i].Detail, "rulesets need a public repository, or GitHub Pro, Team or Enterprise for a private one") {
		t.Errorf("outcomes %+v", rep.Plan.Outcomes)
	}
	g.postBody = `{"message": "Not Found", "status": "404"}`
	rep, _ = Run(context.Background(), e, plan.Apply, nil, Options{Rulesets: true})
	i = slices.IndexFunc(rep.Plan.Outcomes, func(o plan.Outcome) bool { return o.ID == "ruleset:no-force-push" })
	if i < 0 || rep.Plan.Outcomes[i].Status != plan.Failed || !strings.Contains(rep.Plan.Outcomes[i].Detail, "Not Found (HTTP 404)") {
		t.Errorf("404: %+v", rep.Plan.Outcomes)
	}
	if _, err := os.Stat(filepath.Join(dir, RecordName)); err != nil {
		t.Errorf("a refused ruleset stopped the record: %v", err)
	}
}

// A gh read that fails, while the plan is built or in a step's Already, is a kept line: the
// ruleset is not checked and every file step still applies.
func TestRulesetsReadFailureKeepsFiles(t *testing.T) {
	const pro = `{"message": "Upgrade to GitHub Pro or make this repository public to enable this feature.", "status": "403"}`
	for _, c := range []struct {
		name string
		gh   func(argv []string, stdin string) (string, int)
		want string
	}{
		{"not logged in", func([]string, string) (string, int) { return "", 4 }, "not checked: gh api "},
		{"403 Pro on the list", func(argv []string, _ string) (string, int) {
			if strings.HasSuffix(argv[2], "/rulesets?per_page=100&includes_parents=false") {
				return pro, 1
			}
			return (&ghServer{rulesets: map[int]string{}, put: map[string]string{}}).handle(argv, "")
		}, "rulesets need a public repository, or GitHub Pro, Team or Enterprise for a private one"},
	} {
		dir := githubRepo(t)
		e, f := testEnv(t, dir)
		f.gh = c.gh
		rep, err := Run(context.Background(), e, plan.Apply, nil, Options{Rulesets: true})
		if err != nil || rep.Plan.Failed() {
			t.Errorf("%s: %v %s", c.name, err, rep.Plan.Summary())
			continue
		}
		for _, n := range []string{"CLAUDE.md", RecordName} {
			if _, err := os.Stat(filepath.Join(dir, n)); err != nil {
				t.Errorf("%s: %s not written: %v", c.name, n, err)
			}
		}
		for _, name := range []string{"ruleset no-force-push", "ruleset default-branch"} {
			if !slices.ContainsFunc(rep.Kept, func(k setup.Kept) bool { return k.Item == name && strings.HasPrefix(k.Reason, c.want) }) {
				t.Errorf("%s: no kept line for %s: %v", c.name, name, rep.Kept)
			}
		}
	}
}

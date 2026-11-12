package settings

import (
	"encoding/json"
	"testing"

	"makeit/internal/jsonx"
)

// Cases A-K are the prototype's merge3.test.ts cases, recovered from the design research.

func obj(t *testing.T, s string) *jsonx.Object {
	t.Helper()
	o, err := jsonx.DecodeObject([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return o
}

const v1 = `{
  "$comment": "ignored",
  "model": "model-v1",
  "skipDangerousModePermissionPrompt": true,
  "permissions": {"defaultMode": "bypassPermissions", "ask": ["Bash(git push -f *)", "Bash(gh pr comment *)", "Bash(eas update *)"]},
  "env": {"PONYTAIL_DEFAULT_MODE": "full"}
}`

const v2 = `{
  "model": "model-v2",
  "skipDangerousModePermissionPrompt": true,
  "permissions": {"defaultMode": "bypassPermissions", "ask": ["Bash(git push -f *)", "Bash(gh pr comment *)"]},
  "env": {"PONYTAIL_DEFAULT_MODE": "lite"}
}`

func at(t *testing.T, o *jsonx.Object, ptr string) any {
	t.Helper()
	v, _ := get(o, Split(ptr))
	return v
}

func str(t *testing.T, o *jsonx.Object, ptr string) string {
	t.Helper()
	return jsonx.Canonical(at(t, o, ptr))
}

func action(r Result, ptr string) Action {
	for _, c := range r.Changes {
		if c.Path == ptr {
			return c.Action
		}
	}
	return ""
}

func install(t *testing.T, onto string) Result {
	t.Helper()
	return Merge(obj(t, onto), obj(t, v1), nil, DefaultOptions())
}

func setPtr(t *testing.T, o *jsonx.Object, ptr, value string) {
	t.Helper()
	v, err := jsonx.Decode([]byte(value))
	if err != nil {
		t.Fatal(err)
	}
	set(o, Split(ptr), v)
}

func TestA_UserChangedOwnedScalar(t *testing.T) {
	first := install(t, `{}`)
	setPtr(t, first.Settings, "/permissions/defaultMode", `"plan"`)
	r := Merge(first.Settings, obj(t, v1), first.State, DefaultOptions())
	if got := str(t, r.Settings, "/permissions/defaultMode"); got != `"plan"` {
		t.Fatalf("defaultMode %s", got)
	}
	if action(r, "/permissions/defaultMode") != KeptTheirs || !r.State["/permissions/defaultMode"].Held {
		t.Fatalf("want kept-theirs and held, got %v", r.Changes)
	}
}

func TestB_UserRemovedAskRule(t *testing.T) {
	first := install(t, `{}`)
	setPtr(t, first.Settings, "/permissions/ask", `["Bash(git push -f *)", "Bash(eas update *)"]`)
	r := Merge(first.Settings, obj(t, v1), first.State, DefaultOptions())
	if got := str(t, r.Settings, "/permissions/ask"); got != `["Bash(git push -f *)","Bash(eas update *)"]` {
		t.Fatalf("ask %s", got)
	}
	if action(r, "/permissions/ask") != KeptTheirs {
		t.Fatalf("changes %v", r.Changes)
	}
	again := Merge(r.Settings, obj(t, v1), r.State, DefaultOptions())
	if action(again, "/permissions/ask") != "" {
		t.Fatalf("reported twice: %v", again.Changes)
	}
	if rb := again.State["/permissions/ask"].RemovedByUser; len(rb) != 1 || rb[0] != `"Bash(gh pr comment *)"` {
		t.Fatalf("removedByUser %v", rb)
	}
}

func TestC_StackDropsRuleItAdded(t *testing.T) {
	first := install(t, `{}`)
	r := Merge(first.Settings, obj(t, v2), first.State, DefaultOptions())
	if got := str(t, r.Settings, "/permissions/ask"); got != `["Bash(git push -f *)","Bash(gh pr comment *)"]` {
		t.Fatalf("ask %s", got)
	}
	if action(r, "/permissions/ask") != RemoveItems {
		t.Fatalf("changes %v", r.Changes)
	}
}

func TestC2_StackDropsRuleUserHadBefore(t *testing.T) {
	first := install(t, `{"permissions":{"ask":["Bash(eas update *)"]}}`)
	r := Merge(first.Settings, obj(t, v2), first.State, DefaultOptions())
	if got := str(t, r.Settings, "/permissions/ask"); got != `["Bash(eas update *)","Bash(git push -f *)","Bash(gh pr comment *)"]` {
		t.Fatalf("ask %s", got)
	}
}

func TestD_UpstreamChangeUserUntouched(t *testing.T) {
	first := install(t, `{}`)
	r := Merge(first.Settings, obj(t, v2), first.State, DefaultOptions())
	if got := str(t, r.Settings, "/env/PONYTAIL_DEFAULT_MODE"); got != `"lite"` {
		t.Fatalf("mode %s", got)
	}
	if action(r, "/env/PONYTAIL_DEFAULT_MODE") != Set {
		t.Fatalf("changes %v", r.Changes)
	}
}

func TestE_BothChangedSameValue(t *testing.T) {
	first := install(t, `{}`)
	setPtr(t, first.Settings, "/env/PONYTAIL_DEFAULT_MODE", `"ultra"`)
	r := Merge(first.Settings, obj(t, v2), first.State, DefaultOptions())
	if got := str(t, r.Settings, "/env/PONYTAIL_DEFAULT_MODE"); got != `"ultra"` {
		t.Fatalf("mode %s", got)
	}
	if action(r, "/env/PONYTAIL_DEFAULT_MODE") != Conflict || !r.State["/env/PONYTAIL_DEFAULT_MODE"].Held {
		t.Fatalf("changes %v", r.Changes)
	}
	forced := Merge(first.Settings, obj(t, v2), first.State, Options{SetPaths: DefaultOptions().SetPaths, Force: true})
	if got := str(t, forced.Settings, "/env/PONYTAIL_DEFAULT_MODE"); got != `"lite"` {
		t.Fatalf("--force kept %s", got)
	}
}

func TestF_UserValueBeforeFirstInstall(t *testing.T) {
	first := install(t, `{"model":"model-user"}`)
	if got := str(t, first.Settings, "/model"); got != `"model-user"` {
		t.Fatalf("model %s", got)
	}
	rec := first.State["/model"]
	if !rec.Held || rec.Original == nil || rec.Original.Value.V != "model-user" || rec.Base.V != "model-v1" {
		t.Fatalf("record %+v", rec)
	}
	later := Merge(first.Settings, obj(t, v2), first.State, DefaultOptions())
	if got := str(t, later.Settings, "/model"); got != `"model-user"` || action(later, "/model") != Held {
		t.Fatalf("model %s, changes %v", got, later.Changes)
	}
	quiet := Merge(later.Settings, obj(t, v2), later.State, DefaultOptions())
	if action(quiet, "/model") != Noop {
		t.Fatalf("held reported again: %v", quiet.Changes)
	}
}

func TestG_SecondRunChangesNothing(t *testing.T) {
	first := install(t, `{}`)
	second := Merge(first.Settings, obj(t, v1), first.State, DefaultOptions())
	for _, c := range second.Changes {
		if c.Action != Noop {
			t.Fatalf("second run: %v", c)
		}
	}
	if second.Changed() || jsonx.Hash(second.Settings) != jsonx.Hash(first.Settings) {
		t.Fatal("second run changed the file")
	}
}

func TestH_ReorderedFileIsNotDrift(t *testing.T) {
	first := install(t, `{}`)
	reordered := jsonx.NewObject()
	keys := first.Settings.Keys()
	for i := len(keys) - 1; i >= 0; i-- {
		v, _ := first.Settings.Get(keys[i])
		reordered.Set(keys[i], v)
	}
	a, _ := jsonx.Marshal(first.Settings)
	b, _ := jsonx.Marshal(reordered)
	if string(a) == string(b) {
		t.Fatal("fixture did not reorder")
	}
	r := Merge(reordered, obj(t, v1), first.State, DefaultOptions())
	if r.Changed() {
		t.Fatalf("reordering read as drift: %v", r.Changes)
	}
}

func TestI_UninstallKeepsRuleUserHadBefore(t *testing.T) {
	first := install(t, `{"permissions":{"ask":["Bash(gh pr comment *)","Bash(mine *)"]}}`)
	u := Unmerge(first.Settings, first.State)
	if got := str(t, u.Settings, "/permissions/ask"); got != `["Bash(gh pr comment *)","Bash(mine *)"]` {
		t.Fatalf("ask %s", got)
	}
}

func TestJ_UninstallKeepsValueUserHadBefore(t *testing.T) {
	first := install(t, `{"model":"model-v1"}`)
	u := Unmerge(first.Settings, first.State)
	if got := str(t, u.Settings, "/model"); got != `"model-v1"` {
		t.Fatalf("model %s", got)
	}
}

func TestJ2_UninstallRestoresAfterStackMovedValue(t *testing.T) {
	first := install(t, `{"model":"model-v1"}`)
	up := Merge(first.Settings, obj(t, v2), first.State, DefaultOptions())
	if got := str(t, up.Settings, "/model"); got != `"model-v2"` {
		t.Fatalf("upgrade model %s", got)
	}
	u := Unmerge(up.Settings, up.State)
	if got := str(t, u.Settings, "/model"); got != `"model-v1"` || action(u, "/model") != Restore {
		t.Fatalf("model %s, changes %v", got, u.Changes)
	}
}

func TestK_StrippedFieldIsReasserted(t *testing.T) {
	want := `{"extraKnownMarketplaces":{"makeit":{"source":{"source":"github","repo":"o/makeit"},"autoUpdate":true}}}`
	key := "/extraKnownMarketplaces/makeit/autoUpdate"
	plainOpts := Options{SetPaths: DefaultOptions().SetPaths}
	first := Merge(obj(t, `{}`), obj(t, want), nil, plainOpts)
	stripped := `{"extraKnownMarketplaces":{"makeit":{"source":{"source":"github","repo":"o/makeit"}}}}`
	plain := Merge(obj(t, stripped), obj(t, want), first.State, plainOpts)
	if action(plain, key) != KeptTheirs {
		t.Fatalf("plain: %v", plain.Changes)
	}
	enf := plainOpts
	enf.Enforced = map[string]bool{key: true}
	r := Merge(obj(t, stripped), obj(t, want), first.State, enf)
	if action(r, key) != ReAsserted || str(t, r.Settings, key) != "true" {
		t.Fatalf("enforced: %v", r.Changes)
	}
}

func TestUninstallAfterCleanInstallIsIdentity(t *testing.T) {
	before := `{"theme":"dark","permissions":{"allow":["Read"]},"env":{"MINE":"1"}}`
	first := install(t, before)
	u := Unmerge(first.Settings, first.State)
	if !jsonx.Equal(u.Settings, obj(t, before)) {
		c, _ := jsonx.Compact(u.Settings)
		t.Fatalf("not restored: %s", c)
	}
}

func TestExternalPathsAreLeftToClaude(t *testing.T) {
	desired := `{"enabledPlugins":{"makeit-bundle@makeit":true},"extraKnownMarketplaces":{
	  "claude-plugins-official":{"source":{"source":"github","repo":"anthropics/claude-plugins-official"},"autoUpdate":true},
	  "makeit":{"source":{"source":"github","repo":"o/makeit"},"autoUpdate":true}}}`
	r := Merge(obj(t, `{}`), obj(t, desired), nil, DefaultOptions())
	if at(t, r.Settings, "/enabledPlugins") != nil || at(t, r.Settings, "/extraKnownMarketplaces/makeit/source") != nil {
		c, _ := jsonx.Compact(r.Settings)
		t.Fatalf("external path written: %s", c)
	}
	// Nothing else declares the official marketplace, so its source must be written.
	if str(t, r.Settings, "/extraKnownMarketplaces/claude-plugins-official/source/repo") != `"anthropics/claude-plugins-official"` {
		t.Fatal("official marketplace written without a source")
	}
	if str(t, r.Settings, "/extraKnownMarketplaces/claude-plugins-official/autoUpdate") != "true" {
		t.Fatal("enforced autoUpdate missing")
	}
}

func TestSecretsAreStoredAsHashes(t *testing.T) {
	desired := `{"env":{"NTFY_TOPIC":"s3cret-topic"}}`
	first := Merge(obj(t, `{}`), obj(t, desired), nil, DefaultOptions())
	rec := first.State["/env/NTFY_TOPIC"]
	b, _ := json.Marshal(first.State)
	if !rec.Secret || string(b) == "" || contains(string(b), "s3cret-topic") {
		t.Fatalf("secret stored: %s", b)
	}
	again := Merge(first.Settings, obj(t, desired), first.State, DefaultOptions())
	if again.Changed() {
		t.Fatalf("secret re-applied: %v", again.Changes)
	}
	u := Unmerge(first.Settings, first.State)
	if at(t, u.Settings, "/env/NTFY_TOPIC") != nil {
		t.Fatal("secret not removed on uninstall")
	}
}

func TestStateSurvivesJSONRoundTrip(t *testing.T) {
	first := install(t, `{"model":"model-user","permissions":{"ask":["x"]}}`)
	b, err := json.Marshal(first.State)
	if err != nil {
		t.Fatal(err)
	}
	var back State
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	r := Merge(first.Settings, obj(t, v1), back, DefaultOptions())
	if r.Changed() {
		t.Fatalf("decoded state differs: %v", r.Changes)
	}
}

func TestNonListAtSetPathIsConflict(t *testing.T) {
	r := Merge(obj(t, `{"permissions":{"ask":"oops"}}`), obj(t, v1), nil, DefaultOptions())
	if action(r, "/permissions/ask") != Conflict || str(t, r.Settings, "/permissions/ask") != `"oops"` {
		t.Fatalf("changes %v", r.Changes)
	}
}

func TestPointerEscaping(t *testing.T) {
	p := []string{"a/b", "c~d", "e"}
	if got := Pointer(p); got != "/a~1b/c~0d/e" {
		t.Fatalf("pointer %s", got)
	}
	if got := Split(Pointer(p)); len(got) != 3 || got[0] != "a/b" || got[1] != "c~d" {
		t.Fatalf("split %v", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// A value that was there before makeit managed it stays the user's, also on an enforced path, on
// the first run and every run after; the enforcement covers only a value makeit introduced
// (TestK_StrippedFieldIsReasserted). Taking a value over is Force.
func TestEnforcedKeepsAPreexistingValue(t *testing.T) {
	key := "/extraKnownMarketplaces/claude-plugins-official/autoUpdate"
	desired := `{"extraKnownMarketplaces":{"claude-plugins-official":{"source":{"source":"github","repo":"anthropics/claude-plugins-official"},"autoUpdate":true}}}`
	live := `{"extraKnownMarketplaces":{"claude-plugins-official":{"source":{"source":"github","repo":"anthropics/claude-plugins-official"},"autoUpdate":false}}}`
	first := Merge(obj(t, live), obj(t, desired), nil, DefaultOptions())
	if str(t, first.Settings, key) != "false" || action(first, key) != KeptTheirs {
		t.Fatalf("first run: %s, %v", str(t, first.Settings, key), first.Changes)
	}
	second := Merge(first.Settings, obj(t, desired), first.State, DefaultOptions())
	if str(t, second.Settings, key) != "false" {
		t.Fatalf("second run overwrote the user's value: %v", second.Changes)
	}
	force := DefaultOptions()
	force.Force = true
	if taken := Merge(first.Settings, obj(t, desired), first.State, force); str(t, taken.Settings, key) != "true" {
		t.Fatalf("Force did not take it over: %v", taken.Changes)
	}
}

// An attribution value the user set before makeit managed the key is held and reported once,
// while the other attribution keys are added.
func TestMergeHoldsUserAttribution(t *testing.T) {
	const ours = `{"attribution": {"commit": "", "pr": "", "sessionUrl": false}}`
	first := Merge(obj(t, `{"attribution": {"commit": "Signed-off-by: me"}}`), obj(t, ours), nil, DefaultOptions())
	if got := str(t, first.Settings, "/attribution/commit"); got != `"Signed-off-by: me"` {
		t.Fatalf("commit overwritten: %s", got)
	}
	if got := str(t, first.Settings, "/attribution/pr"); got != `""` || action(first, "/attribution/pr") != Set {
		t.Fatalf("pr %s, changes %v", got, first.Changes)
	}
	if got := str(t, first.Settings, "/attribution/sessionUrl"); got != `false` || action(first, "/attribution/sessionUrl") != Set {
		t.Fatalf("sessionUrl %s, changes %v", got, first.Changes)
	}
	if action(first, "/attribution/commit") != KeptTheirs || !first.State["/attribution/commit"].Held {
		t.Fatalf("want kept-theirs and held, got %v", first.Changes)
	}
	second := Merge(first.Settings, obj(t, ours), first.State, DefaultOptions())
	if got := str(t, second.Settings, "/attribution/commit"); got != `"Signed-off-by: me"` || action(second, "/attribution/commit") != Noop {
		t.Fatalf("second run: commit %s, changes %v", got, second.Changes)
	}
}

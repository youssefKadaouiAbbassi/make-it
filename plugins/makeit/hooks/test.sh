#!/usr/bin/env bash
# Tests for the makeit hooks, with fake payloads and a fake curl.
# Needs bash, jq, git, realpath. Runs on the host or in the eval image. Nothing is installed.
set -uo pipefail
here=$(cd "$(dirname "$0")" && pwd)
fail=0
ok() { echo "ok   $1"; }
bad() { echo "FAIL $1"; fail=1; }
expect() { # name expected-substring actual
  case "$3" in *"$2"*) ok "$1" ;; *) bad "$1: expected [$2] in [$3]" ;; esac
}
expect_empty() { [ -z "$2" ] && ok "$1" || bad "$1: expected no output, got [$2]"; }

tmp=$(mktemp -d "${TMPDIR:-/tmp}/hooks-test.XXXXXX")
trap '/bin/rm -rf "$tmp"' EXIT

# --- ntfy.sh: fake curl records the call ---------------------------------------------------
unset NTFY_TOPIC NTFY_URL NTFY_TOKEN
export XDG_STATE_HOME="$tmp/state"
mkdir -p "$tmp/bin"
printf '#!/usr/bin/env bash\nprintf "%%s\\n" "$*" >>"%s/curl.log"\n' "$tmp" >"$tmp/bin/curl"; chmod +x "$tmp/bin/curl"
run_ntfy() { PATH="$tmp/bin:$PATH" NTFY_TOPIC="${NTFY_TOPIC-t0pic}" NTFY_URL="${NTFY_URL-https://ntfy.example}" NTFY_TOKEN="${NTFY_TOKEN-}" bash "$here/ntfy.sh"; }
: >"$tmp/curl.log"
printf '{"hook_event_name":"Stop","cwd":"/home/u/proj","session_id":"s"}' | run_ntfy; rc=$?
[ "$rc" = 0 ] && ok "ntfy Stop exits 0" || bad "ntfy Stop exit $rc"
expect "ntfy Stop posts done" "-d done https://ntfy.example/t0pic" "$(cat "$tmp/curl.log")"
expect "ntfy title is repo name" "Title: claude - proj" "$(cat "$tmp/curl.log")"
: >"$tmp/curl.log"
printf '{"hook_event_name":"PreToolUse","cwd":"/x/repo","tool_name":"AskUserQuestion","tool_input":{}}' | run_ntfy
expect "ntfy PreToolUse names the tool" "needs you: AskUserQuestion" "$(cat "$tmp/curl.log")"
: >"$tmp/curl.log"
printf '{"hook_event_name":"Notification","cwd":"/x/repo","message":"Claude is waiting for your input","notification_type":"idle_prompt"}' | run_ntfy
expect "ntfy Notification uses the message" "Claude is waiting for your input" "$(cat "$tmp/curl.log")"
: >"$tmp/curl.log"
NTFY_TOKEN=abc bash -c 'printf "{\"hook_event_name\":\"Stop\",\"cwd\":\"/x/repo\"}" | PATH="'"$tmp"'/bin:$PATH" NTFY_TOPIC=t NTFY_URL=https://n bash "'"$here"'/ntfy.sh"'
expect "ntfy sends bearer token when set" "Authorization: Bearer abc" "$(cat "$tmp/curl.log")"
: >"$tmp/curl.log"
out=$(printf '{"hook_event_name":"Stop","cwd":"/x/repo"}' | NTFY_TOPIC= run_ntfy)
[ ! -s "$tmp/curl.log" ] && ok "ntfy without topic sends nothing" || bad "ntfy without topic sent"
expect_empty "ntfy prints nothing to stdout" "$out"
mkdir -p "$tmp/nojq"; for b in bash cat basename head; do ln -s "$(command -v $b)" "$tmp/nojq/$b"; done
out=$(printf '{"hook_event_name":"Stop","cwd":"/x/repo"}' | PATH="$tmp/nojq" NTFY_TOPIC=t bash "$here/ntfy.sh" 2>&1; echo "rc=$?")
expect "ntfy without jq exits 0" "rc=0" "$out"

# --- handoff.sh -----------------------------------------------------------------------------
repo="$tmp/repo"; mkdir -p "$repo"; git -C "$repo" init -q; git -C "$repo" config user.email t@e; git -C "$repo" config user.name t
printf '# Handoff\n- **Goal:** build the thing\n' >"$repo/HANDOFF.md"; printf 'a\n' >"$repo/a.txt"
git -C "$repo" add -A; git -C "$repo" commit -qm init
payload() { printf '{"hook_event_name":"%s","cwd":"%s","session_id":"s"%s}' "$1" "$repo" "${2:-}"; }
out=$(payload SessionStart ',"source":"startup"' | bash "$here/handoff.sh")
expect "handoff SessionStart adds context" '"hookEventName":"SessionStart"' "$out"
expect "handoff SessionStart carries the file" 'build the thing' "$(printf '%s' "$out" | jq -r '.hookSpecificOutput.additionalContext')"
out=$(printf '{"hook_event_name":"SessionStart","cwd":"%s/nohandoff","source":"startup"}' "$tmp" | bash "$here/handoff.sh")
expect_empty "handoff SessionStart silent without the file" "$out"
out=$(payload PreCompact ',"trigger":"auto"' | bash "$here/handoff.sh")
expect "handoff PreCompact reminds" 'rewrite HANDOFF.md' "$out"
out=$(payload Stop ',"stop_hook_active":false' | bash "$here/handoff.sh")
expect_empty "handoff Stop clean tree: no block" "$out"
sleep 1; printf 'b\n' >>"$repo/a.txt"
out=$(payload Stop ',"stop_hook_active":false' | bash "$here/handoff.sh")
expect "handoff Stop stale HANDOFF blocks" '"decision":"block"' "$out"
out=$(payload Stop ',"stop_hook_active":true' | bash "$here/handoff.sh")
expect_empty "handoff Stop does not block twice" "$out"
sleep 1; printf '# Handoff\n- **Goal:** rewritten\n' >"$repo/HANDOFF.md"
out=$(payload Stop ',"stop_hook_active":false' | bash "$here/handoff.sh")
expect_empty "handoff Stop after rewrite: no block" "$out"
sleep 1; printf 'c\n' >"$repo/new.txt"
out=$(payload Stop ',"stop_hook_active":false' | bash "$here/handoff.sh")
expect "handoff Stop untracked newer file blocks" '"decision":"block"' "$out"
out=$(printf '{"hook_event_name":"Stop","cwd":"%s/notgit","stop_hook_active":false}' "$tmp" | bash "$here/handoff.sh")
expect_empty "handoff Stop outside git: silent" "$out"
# Edits made before this session started (another session, a copy) do not count.
late() { printf '{"hook_event_name":"%s","cwd":"%s","session_id":"late"%s}' "$1" "$repo" "${2:-}" | bash "$here/handoff.sh"; }
sleep 1; printf 'd\n' >>"$repo/a.txt"
sleep 1; late SessionStart ',"source":"startup"' >/dev/null
expect_empty "handoff Stop ignores edits from before the session" "$(late Stop ',"stop_hook_active":false')"
sleep 1; printf 'e\n' >>"$repo/a.txt"
expect "handoff Stop blocks on an edit in this session" '"decision":"block"' "$(late Stop ',"stop_hook_active":false')"
sleep 1; late SessionStart ',"source":"compact"' >/dev/null; printf '# Handoff\n- **Goal:** again\n' >"$repo/HANDOFF.md"
expect_empty "handoff compact keeps the start marker" "$(late Stop ',"stop_hook_active":false')"
out=$(printf '{"hook_event_name":"Stop","cwd":"%s","session_id":"nostart","stop_hook_active":false}' "$repo" | bash "$here/handoff.sh")
expect_empty "handoff Stop without a start marker: silent" "$out"

# --- variant-ask.sh ---------------------------------------------------------------------------
va() { printf '{"hook_event_name":"PreToolUse","tool_name":"Bash","cwd":"%s","tool_input":{"command":%s}}' "$repo" "$(printf '%s' "$1" | jq -Rs .)" | HOME="$tmp/home" CLAUDE_PROJECT_DIR="$repo" bash "$here/optional/variant-ask.sh"; }
asks() { expect "variant asks: $1" '"permissionDecision":"ask"' "$(va "$1")"; }
quiet() { expect_empty "variant quiet: $1" "$(va "$1")"; }
asks 'git -C /x/repo push origin main'
asks 'bash -c "git push origin HEAD:main"'
asks 'GIT_SSH_COMMAND=ssh /usr/bin/git push --force origin feature'
asks 'git push origin +feature'
asks 'git push origin feature --force-with-lease'
asks 'cd /x && git reset --hard HEAD~1'
asks 'gh api -X DELETE repos/o/r/git/refs/heads/x'
asks 'gh api repos/o/r/pulls/1/merge --method PUT'
asks 'rm -rf ~/'
asks 'rm -rf /'
asks 'rm -fr ../other'
asks 'rm -rf /etc/x'
quiet 'git push origin feature'
quiet 'git push -u origin feat/main-menu'
quiet 'git commit -m "push to main later"'
quiet 'gh pr view 1'
quiet 'gh api repos/o/r/pulls'
quiet 'rm -rf node_modules'
quiet 'rm -rf ./dist'
quiet 'ls -la'
expect_empty "variant ignores other tools" "$(printf '{"hook_event_name":"PreToolUse","tool_name":"Read","cwd":"%s","tool_input":{"file_path":"x"}}' "$repo" | bash "$here/optional/variant-ask.sh")"
# --- order-note.sh + order-ask.sh ------------------------------------------------------------
bash "$here/optional/order-test.sh" | tail -n 1 | grep -q "ORDER OK" && ok "order hooks (optional/order-test.sh)" || bad "order hooks: run optional/order-test.sh"


# --- skills.sh ----------------------------------------------------------------------------------
out=$(printf '{"hook_event_name":"UserPromptSubmit","cwd":"/x","prompt":"continue from HANDOFF.md"}' | bash "$here/skills.sh")
expect "skills adds prompt context" '"hookEventName":"UserPromptSubmit"' "$out"
expect "skills lists the usual matches" 'superpowers:executing-plans' "$(printf '%s' "$out" | jq -r '.hookSpecificOutput.additionalContext')"
expect "skills covers follow-ups" '"go for it"' "$(printf '%s' "$out" | jq -r '.hookSpecificOutput.additionalContext')"
expect "skills comes before a deja recall" 'including a deja recall' "$(printf '%s' "$out" | jq -r '.hookSpecificOutput.additionalContext')"
expect_empty "skills ignores other events" "$(printf '{"hook_event_name":"Stop","cwd":"/x"}' | bash "$here/skills.sh")"
out=$(printf '{"hook_event_name":"UserPromptSubmit"}' | PATH="$tmp/nojq" bash "$here/skills.sh" 2>&1; echo "rc=$?")
expect "skills without jq exits 0" "rc=0" "$out"


# --- docs.sh -------------------------------------------------------------------------------------
dp="$tmp/docsproj"; mkdir -p "$dp/cli" "$dp/web/node_modules/x"
printf 'module m\n\ngo 1.27.3\n\nrequire (\n\tgolang.org/x/term v0.47.0\n\tgolang.org/x/sys v0.49.0 // indirect\n)\n\nrequire github.com/a/b v1.2.3\n' >"$dp/cli/go.mod"
printf '{"dependencies":{"react":"^19.2.0"},"devDependencies":{"vitest":"4.1.0"}}\n' >"$dp/web/package.json"
printf '{"dependencies":{"ignored":"1.0.0"}}\n' >"$dp/web/node_modules/x/package.json"
printf '[project]\nname = "p"\ndependencies = ["httpx>=0.28", "pydantic==2.12.0"]\n' >"$dp/pyproject.toml"
# A fake makeit records the sync the hook starts; the real one is never reached from here.
mkdir -p "$tmp/mbin"
printf '#!/usr/bin/env bash\nprintf "%%s\\n" "$*" >>"%s/makeit.log"\n' "$tmp" >"$tmp/mbin/makeit"; chmod +x "$tmp/mbin/makeit"
nomakeit=/usr/bin:/bin # makeit is never installed there
docs_hook() { PATH="$1" bash "$here/docs.sh"; }
waitlog() { for _ in 1 2 3 4 5 6 7 8 9 10; do [ -s "$tmp/makeit.log" ] && break; sleep 0.2; done; cat "$tmp/makeit.log" 2>/dev/null; : >"$tmp/makeit.log"; }
ctx=$(printf '{"hook_event_name":"SessionStart","cwd":"%s","source":"startup"}' "$dp" | docs_hook "$tmp/mbin:$nomakeit" | jq -r '.hookSpecificOutput.additionalContext')
expect "docs SessionStart starts the sync" "docs sync --if-changed --quiet $dp" "$(waitlog)"
case "$ctx" in *"go 1.27.3"*) bad "docs leaves out the go directive (the standard library is not indexed)" ;; *) ok "docs leaves out the go directive (the standard library is not indexed)" ;; esac
expect "docs lists a direct go require" 'golang.org/x/term v0.47.0' "$ctx"
expect "docs lists a single-line require" 'github.com/a/b v1.2.3' "$ctx"
case "$ctx" in *x/sys*) bad "docs skips indirect requires" ;; *) ok "docs skips indirect requires" ;; esac
expect "docs lists package.json deps" 'react ^19.2.0 (web/package.json)' "$ctx"
expect "docs lists devDependencies" 'vitest 4.1.0' "$ctx"
case "$ctx" in *ignored*) bad "docs skips node_modules" ;; *) ok "docs skips node_modules" ;; esac
command -v python3 >/dev/null 2>&1 && expect "docs lists pyproject deps" 'pydantic ==2.12.0' "$ctx"
expect "docs says they are indexed and to search" 'These are indexed by makeit; search them (search_docs or the docs-search skill) before you rely on their API.' "$ctx"
case "$ctx" in *"docs-manage skill"*) bad "docs no longer tells the agent to index" ;; *) ok "docs no longer tells the agent to index" ;; esac
expect_empty "docs PostToolUse prints nothing" "$(printf '{"hook_event_name":"PostToolUse","cwd":"%s","tool_name":"Bash"}' "$dp" | docs_hook "$tmp/mbin:$nomakeit")"
expect "docs PostToolUse starts the sync" "docs sync --if-changed --quiet $dp" "$(waitlog)"
out=$(printf '{"hook_event_name":"PostToolUse","cwd":"%s","tool_name":"Edit"}' "$dp" | docs_hook "$nomakeit"); rc=$?
if [ -z "$out" ] && [ "$rc" = 0 ]; then ok "docs without makeit: silent, exit 0"; else bad "docs without makeit: rc $rc out [$out]"; fi
mkdir -p "$tmp/empty"
expect_empty "docs silent without manifests" "$(printf '{"hook_event_name":"SessionStart","cwd":"%s"}' "$tmp/empty" | docs_hook "$nomakeit")"


# --- check-task-stop.sh (optional) -------------------------------------------------------------
cts="$tmp/cts"; mkdir -p "$cts/src"; git -C "$cts" init -q; git -C "$cts" config user.email t@e; git -C "$cts" config user.name t
printf '{"name":"t","scripts":{"check:task":"node --test src/*.test.js"}}\n' >"$cts/package.json"; printf 'export const a = 1;\n' >"$cts/src/a.js"
git -C "$cts" add -A; git -C "$cts" commit -qm init
cpay() { printf '{"hook_event_name":"Stop","cwd":"%s","stop_hook_active":%s}' "$cts" "${1:-false}" | bash "$here/optional/check-task-stop.sh"; }
expect_empty "check-task clean tree: silent" "$(cpay)"
printf 'import test from "node:test"; import assert from "node:assert"; test("x", () => assert.equal(1, 2));\n' >"$cts/src/b.test.js"
expect "check-task failing task blocks" '"decision":"block"' "$(cpay)"
expect_empty "check-task does not block twice" "$(cpay true)"
printf 'import test from "node:test"; import assert from "node:assert"; test("x", () => assert.equal(1, 1));\n' >"$cts/src/b.test.js"
expect_empty "check-task passing task: silent" "$(cpay)"
printf '{"name":"t"}\n' >"$cts/package.json"
expect_empty "check-task no task defined: silent" "$(cpay)"

# --- options.sh: its own cases, under both locales (· and é are multibyte) ----------------------
for loc in C C.UTF-8; do
  if LC_ALL=$loc bash "$here/options_test.sh" >"$tmp/options-$loc.out" 2>&1; then
    ok "options_test.sh under LC_ALL=$loc ($(grep -c '^ok' "$tmp/options-$loc.out") cases)"
  else
    bad "options_test.sh under LC_ALL=$loc: $(grep '^FAIL' "$tmp/options-$loc.out" | head -3)"
  fi
done

# --- skills/init/SKILL.md: the /makeit:init agent step (cli/INIT.md) ---------------------------
sk="$here/../skills/init/SKILL.md"
if [ -f "$sk" ]; then
  ok "init skill exists"
  front=$(awk 'NR == 1 && /^---$/ { f = 1; next } f && /^---$/ { exit } f' "$sk")
  body=$(awk 'NR == 1 && /^---$/ { f = 1; next } f && /^---$/ { f = 0; b = 1; next } b' "$sk")
  expect "init skill: only the user starts it" "disable-model-invocation: true" "$front"
  expect "init skill: pre-approves exactly the init call" "allowed-tools: Bash(makeit init --yes --json)" "$front"
  if grep -Eq '^description: [^>|].*' <<<"$front" && ! grep -Eq '^description: .*: ' <<<"$front"; then ok "init skill: one-line description, no ': '"; else bad "init skill: description"; fi
  if grep -Eq '^name:' <<<"$front"; then bad "init skill: no name: line (the directory names it)"; else ok "init skill: no name: line"; fi
  if grep -Fq '!`' "$sk"; then bad "init skill: no !\` injection"; else ok "init skill: no shell injection"; fi
  expect "init skill: runs the CLI first, with a long timeout" "makeit init --yes --json" "$body"
  expect "init skill: timeout" "600000" "$body"
  expect "init skill: run recipe path" ".claude/skills/run-<project>/SKILL.md" "$body"
  if grep -Fq '.claude/skills/run/' "$sk"; then bad "init skill: never .claude/skills/run/"; else ok "init skill: never .claude/skills/run/"; fi
  expect "init skill: first commit asked" "AskUserQuestion" "$body"
  expect "init skill: --rulesets only on request" "--rulesets" "$body"
  expect "init skill: the init call stands alone (the exact allowed-tools rule)" "as a command on its own" "$body"
  expect "init skill: Coverage and Mutation rows are never deleted" "Never delete the Coverage or Mutation row" "$body"
  wire=$(grep -n 'Wire the check' "$sk" | head -1 | cut -d: -f1)
  full=$(grep -n 'Run check:full' "$sk" | head -1 | cut -d: -f1)
  if [ -n "$wire" ] && [ -n "$full" ] && [ "$wire" -lt "$full" ]; then ok "init skill: check:full after the stubs are wired"; else bad "init skill: order of wiring and check:full ($wire, $full)"; fi
  if command -v claude >/dev/null 2>&1; then
    if claude plugin validate "$here/.." --strict >"$tmp/validate.out" 2>&1; then ok "claude plugin validate --strict"; else bad "claude plugin validate: $(tail -3 "$tmp/validate.out")"; fi
  fi
else
  bad "init skill missing: $sk"
fi

[ "$fail" = 0 ] && echo "HOOKS OK" || { echo "HOOKS FAILURES"; exit 1; }

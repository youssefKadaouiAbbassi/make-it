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
# macOS ships bash 3.2, where an empty array under set -u is an error: with no token, ntfy must
# still post. Run there when docker can.
if command -v docker >/dev/null && docker image inspect bash:3.2 >/dev/null 2>&1 || docker pull -q bash:3.2 >/dev/null 2>&1; then
  : >"$tmp/curl.log"
  # 77: the image could not get jq (no network), so this is a skip, not a failure.
  printf '{"hook_event_name":"Stop","cwd":"/home/u/proj","session_id":"s"}' |
    docker run --rm -i -v "$here:/h:ro" -v "$tmp:$tmp" -e NTFY_TOPIC=t0pic -e NTFY_URL=https://ntfy.example bash:3.2 \
      sh -c "apk add -q jq >/dev/null 2>&1 || exit 77; PATH=$tmp/bin:\$PATH bash /h/ntfy.sh"
  if [ $? = 77 ]; then echo "SKIP  ntfy under bash 3.2: no jq in the image (offline)"; else
    expect "ntfy under bash 3.2 with no token posts" "-d done https://ntfy.example/t0pic" "$(cat "$tmp/curl.log")"
  fi
else
  echo "SKIP  ntfy under bash 3.2: no docker"
fi
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
expect "handoff Stop stale HANDOFF reminds" '"hookEventName":"Stop","additionalContext":"Files changed this session' "$out"
out=$(payload Stop ',"stop_hook_active":true' | bash "$here/handoff.sh")
expect_empty "handoff Stop does not block twice" "$out"
sleep 1; printf '# Handoff\n- **Goal:** rewritten\n' >"$repo/HANDOFF.md"
out=$(payload Stop ',"stop_hook_active":false' | bash "$here/handoff.sh")
expect_empty "handoff Stop after rewrite: no block" "$out"
sleep 1; printf 'c\n' >"$repo/new.txt"
out=$(payload Stop ',"stop_hook_active":false' | bash "$here/handoff.sh")
expect "handoff Stop untracked newer file reminds" '"hookEventName":"Stop","additionalContext":"Files changed this session' "$out"
out=$(printf '{"hook_event_name":"Stop","cwd":"%s/notgit","stop_hook_active":false}' "$tmp" | bash "$here/handoff.sh")
expect_empty "handoff Stop outside git: silent" "$out"
# Edits made before this session started (another session, a copy) do not count.
late() { printf '{"hook_event_name":"%s","cwd":"%s","session_id":"late"%s}' "$1" "$repo" "${2:-}" | bash "$here/handoff.sh"; }
sleep 1; printf 'd\n' >>"$repo/a.txt"
sleep 1; late SessionStart ',"source":"startup"' >/dev/null
expect_empty "handoff Stop ignores edits from before the session" "$(late Stop ',"stop_hook_active":false')"
sleep 1; printf 'e\n' >>"$repo/a.txt"
expect "handoff Stop reminds on an edit in this session" '"hookEventName":"Stop","additionalContext":"Files changed this session' "$(late Stop ',"stop_hook_active":false')"
sleep 1; late SessionStart ',"source":"compact"' >/dev/null; printf '# Handoff\n- **Goal:** again\n' >"$repo/HANDOFF.md"
expect_empty "handoff compact keeps the start marker" "$(late Stop ',"stop_hook_active":false')"
out=$(printf '{"hook_event_name":"Stop","cwd":"%s","session_id":"nostart","stop_hook_active":false}' "$repo" | bash "$here/handoff.sh")
expect_empty "handoff Stop without a start marker: silent" "$out"
# Per-branch handoff file, and the plan after compaction.
git -C "$repo" checkout -qb feat/x
bsid() { printf '{"hook_event_name":"%s","cwd":"%s","session_id":"br"%s}' "$1" "$repo" "${2:-}" | bash "$here/handoff.sh"; }
ctx() { printf '%s' "$1" | jq -r '.hookSpecificOutput.additionalContext // .systemMessage // empty'; }
out=$(bsid SessionStart ',"source":"startup"')
expect "handoff branch without its file reads HANDOFF.md" 'again' "$(ctx "$out")"
expect "handoff branch names HANDOFF.feat-x.md to write" 'HANDOFF.feat-x.md' "$(ctx "$out")"
printf '# Handoff\n- **Goal:** on the branch\n' >"$repo/HANDOFF.feat-x.md"
expect "handoff branch prefers its own file" 'on the branch' "$(ctx "$(bsid SessionStart ',"source":"startup"')")"
/bin/rm "$repo/HANDOFF.feat-x.md"
sleep 1; printf 'f\n' >>"$repo/a.txt"
expect "handoff Stop on a branch names the branch file" 'after HANDOFF.feat-x.md was last written' "$(ctx "$(bsid Stop ',"stop_hook_active":false')")"
out=$(bsid PreCompact ',"trigger":"auto"')
expect "handoff PreCompact names the branch file" 'HANDOFF.feat-x.md' "$out"
for w in 'open tasks' 'plan' 'last decision'; do expect "handoff PreCompact asks for $w" "$w" "$out"; done
mkdir -p "$repo/docs/superpowers/plans"; printf '# plan\n' >"$repo/docs/superpowers/plans/p.md"
out=$(ctx "$(bsid SessionStart ',"source":"compact"')")
expect "handoff compact names the newest plan" 'p.md' "$out"
expect "handoff compact says to re-read plan and task list" 're-read it and the task list' "$out"
printf '# Handoff\n- **Next:** docs/superpowers/plans/q.md task 3\n' >"$repo/HANDOFF.feat-x.md"; printf '# q\n' >"$repo/docs/superpowers/plans/q.md"
touch -d '30 days ago' "$repo/docs/superpowers/plans/p.md"
expect "handoff compact prefers the plan the handoff names" 'q.md' "$(ctx "$(bsid SessionStart ',"source":"compact"')")"
/bin/rm "$repo/HANDOFF.feat-x.md" "$repo/docs/superpowers/plans/q.md"
case "$(ctx "$(bsid SessionStart ',"source":"compact"')")" in *p.md*) bad "handoff compact ignores a plan older than 14 days" ;; *) ok "handoff compact ignores a plan older than 14 days" ;; esac
expect_empty "handoff startup does not mention the plan" "$(ctx "$(bsid SessionStart ',"source":"startup"')" | grep 'active plan')"
git -C "$repo" checkout -q --detach
expect "handoff detached HEAD uses HANDOFF.md" 'again' "$(ctx "$(bsid SessionStart ',"source":"startup"')")"
case "$(ctx "$(bsid SessionStart ',"source":"startup"')")" in *HANDOFF.-*|*HANDOFF..md*) bad "handoff detached HEAD names no branch file" ;; *) ok "handoff detached HEAD names no branch file" ;; esac
git -C "$repo" checkout -q master 2>/dev/null || git -C "$repo" checkout -q main
git -C "$repo" worktree add -q -b 'we#ird/b' "$tmp/wt" 2>/dev/null
wt=$(printf '{"hook_event_name":"SessionStart","cwd":"%s","session_id":"wt","source":"startup"}' "$tmp/wt" | bash "$here/handoff.sh")
expect "handoff worktree (.git file) uses the hook cwd and a safe slug" 'HANDOFF.we-ird-b.md' "$(ctx "$wt")"
# A repo with no commits yet: main is the default, not a feature branch.
ub="$tmp/unborn"; mkdir -p "$ub"; git -C "$ub" init -q -b main; printf "# h\n" >"$ub/HANDOFF.md"
expect "handoff unborn main uses HANDOFF.md" 'rewrite HANDOFF.md' "$(printf '{"hook_event_name":"PreCompact","cwd":"%s"}' "$ub" | bash "$here/handoff.sh"; true)"
ub2="$tmp/unborn2"; mkdir -p "$ub2"; git -C "$ub2" init -q -b feat/new; printf "# h\n" >"$ub2/HANDOFF.md"
expect "handoff unborn branch of any name uses HANDOFF.md" 'rewrite HANDOFF.md' "$(printf '{"hook_event_name":"PreCompact","cwd":"%s"}' "$ub2" | bash "$here/handoff.sh")"
# A cwd with a space still finds the plan.
sp="$tmp/a b"; mkdir -p "$sp/docs/superpowers/plans"; printf '# s\n' >"$sp/docs/superpowers/plans/s.md"
expect "handoff compact finds a plan in a cwd with a space" 's.md' "$(printf '{"hook_event_name":"SessionStart","cwd":"%s","source":"compact"}' "$sp" | bash "$here/handoff.sh")"
# origin/HEAD decides: with develop as the default, main is a feature branch.
od="$tmp/od"; mkdir -p "$od"; git -C "$od" init -q -b main; git -C "$od" -c user.email=t@e -c user.name=t commit -q --allow-empty -m i
git -C "$od" update-ref refs/remotes/origin/develop HEAD; git -C "$od" symbolic-ref refs/remotes/origin/HEAD refs/remotes/origin/develop
expect "handoff origin/HEAD develop makes main a feature branch" 'HANDOFF.main.md' "$(printf '{"hook_event_name":"PreCompact","cwd":"%s"}' "$od" | bash "$here/handoff.sh")"
git -C "$od" checkout -q -b develop
expect "handoff origin/HEAD target is the default" 'rewrite HANDOFF.md' "$(printf '{"hook_event_name":"PreCompact","cwd":"%s"}' "$od" | { printf '# h\n' >"$od/HANDOFF.md"; bash "$here/handoff.sh"; })"

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


# --- docs.sh (SessionStart dependency list) -----------------------------------------------------
dp="$tmp/docsproj"; mkdir -p "$dp/cli" "$dp/web/node_modules/x"
printf 'module m\n\ngo 1.27.3\n\nrequire (\n\tgolang.org/x/term v0.47.0\n\tgolang.org/x/sys v0.49.0 // indirect\n)\n\nrequire github.com/a/b v1.2.3\n' >"$dp/cli/go.mod"
printf '{"dependencies":{"react":"^19.2.0"},"devDependencies":{"vitest":"4.1.0"}}\n' >"$dp/web/package.json"
printf '{"dependencies":{"ignored":"1.0.0"}}\n' >"$dp/web/node_modules/x/package.json"
printf '[project]\nname = "p"\ndependencies = ["httpx>=0.28", "pydantic==2.12.0"]\n' >"$dp/pyproject.toml"
# A fake makeit records any call; docs.sh must never reach it.
mkdir -p "$tmp/mbin"
printf '#!/usr/bin/env bash\nprintf "%%s\\n" "$*" >>"%s/makeit.log"\n' "$tmp" >"$tmp/mbin/makeit"; chmod +x "$tmp/mbin/makeit"
nomakeit=/usr/bin:/bin # makeit is never installed there
docs_hook() { PATH="$1" bash "$here/docs.sh"; }
waitlog() { for _ in 1 2 3 4 5 6 7 8 9 10; do [ -s "$tmp/makeit.log" ] && break; sleep 0.2; done; cat "$tmp/makeit.log" 2>/dev/null; : >"$tmp/makeit.log"; }
ctx=$(printf '{"hook_event_name":"SessionStart","cwd":"%s","source":"startup"}' "$dp" | docs_hook "$tmp/mbin:$nomakeit" | jq -r '.hookSpecificOutput.additionalContext')
sleep 0.3
[ ! -s "$tmp/makeit.log" ] && ok "docs.sh never calls makeit" || bad "docs.sh called makeit: $(cat "$tmp/makeit.log")"
expect "docs tells the agent to fetch the docs of the version in use" 'fetch the docs of the version in use' "$ctx"
case "$ctx" in *"go 1.27.3"*) bad "docs leaves out the go directive" ;; *) ok "docs leaves out the go directive" ;; esac
expect "docs lists a direct go require" 'golang.org/x/term v0.47.0' "$ctx"
expect "docs lists a single-line require" 'github.com/a/b v1.2.3' "$ctx"
case "$ctx" in *x/sys*) bad "docs skips indirect requires" ;; *) ok "docs skips indirect requires" ;; esac
expect "docs lists package.json deps" 'react ^19.2.0 (web/package.json)' "$ctx"
expect "docs lists devDependencies" 'vitest 4.1.0' "$ctx"
case "$ctx" in *ignored*) bad "docs skips node_modules" ;; *) ok "docs skips node_modules" ;; esac
command -v python3 >/dev/null 2>&1 && expect "docs lists pyproject deps" 'pydantic ==2.12.0' "$ctx"
case "$ctx" in *"indexed by makeit"*) bad "docs no longer mentions an index" ;; *) ok "docs no longer mentions an index" ;; esac
# A folder of repositories: the list is each repository's (never a worktree's or a plain folder's).
ws="$tmp/docsws"; mkdir -p "$ws/a/.git" "$ws/a-wt" "$ws/notes"
printf '{"dependencies":{"left-pad":"1.0.0"}}\n' >"$ws/a/package.json"
printf 'gitdir: %s/a/.git/worktrees/a-wt\n' "$ws" >"$ws/a-wt/.git"
printf '{"dependencies":{"lodash":"4.17.21"}}\n' >"$ws/a-wt/package.json"
printf '{"dependencies":{"chalk":"5.0.0"}}\n' >"$ws/notes/package.json"
wsctx=$(printf '{"hook_event_name":"SessionStart","cwd":"%s","source":"startup"}' "$ws" | docs_hook "$nomakeit" | jq -r '.hookSpecificOutput.additionalContext')
expect "docs in a workspace lists a repository's deps" 'left-pad 1.0.0 (a/package.json)' "$wsctx"
case "$wsctx" in *lodash* | *chalk*) bad "docs in a workspace skips worktrees and plain folders" ;; *) ok "docs in a workspace skips worktrees and plain folders" ;; esac
expect_empty "docs ignores PostToolUse" "$(printf '{"hook_event_name":"PostToolUse","cwd":"%s","tool_name":"Bash"}' "$dp" | docs_hook "$nomakeit")"
mkdir -p "$tmp/empty"
expect_empty "docs silent without manifests" "$(printf '{"hook_event_name":"SessionStart","cwd":"%s"}' "$tmp/empty" | docs_hook "$nomakeit")"
out=$(printf '{"hook_event_name":"SessionStart","cwd":"%s"}' "$dp" | PATH="$tmp/nojq" bash "$here/docs.sh" 2>&1; echo "rc=$?")
[ "$out" = "rc=0" ] && ok "docs without jq: silent, exit 0" || bad "docs without jq: [$out]"

# --- docs-nudge.sh and docs-mark.sh --------------------------------------------------------------
# Dates come from release-dates.json (vitest 5.1.0 is there, 2026-11-02) or a seeded cache; the
# network is never reached (no curl on this PATH).
export XDG_CACHE_HOME="$tmp/cache"; export TMPDIR="$tmp"
mkdir -p "$XDG_CACHE_HOME/makeit-docs" "$tmp/nobin"
for b in bash cat basename dirname head sed awk grep tr cut jq mkdir touch date python3; do ln -sf "$(command -v $b)" "$tmp/nobin/$b" 2>/dev/null; done
printf 'npm:oldlib@1.0.0\t2024-01-01\ngo:github.com/new/mod@v1.4.0\t2026-10-01\npypi:newpy@2.0.0\t2026-09-01\ncrates:newcrate@0.9.0\t2026-09-01\n' >"$XDG_CACHE_HOME/makeit-docs/dates.tsv"
nudge() { # session cwd tool file text -> hook output
  jq -n --arg s "$1" --arg c "$2" --arg t "$3" --arg f "$4" --arg x "$5" '{hook_event_name:"PreToolUse",session_id:$s,cwd:$c,tool_name:$t,tool_input:{file_path:$f,content:$x}}' |
    PATH="${NPATH:-$tmp/nobin}" bash "$here/docs-nudge.sh"
}
np="$tmp/nudgeproj"; mkdir -p "$np/src" "$np/cli" "$np/py" "$np/rs"
printf '{"devDependencies":{"vitest":"^5.1.0","oldlib":"^1.0.0"}}\n' >"$np/package.json"
printf '{"packages":{"node_modules/vitest":{"version":"5.1.0","resolved":"https://registry.npmjs.org/vitest/-/vitest-5.1.0.tgz"},"node_modules/oldlib":{"version":"1.0.0","resolved":"https://registry.npmjs.org/oldlib/-/oldlib-1.0.0.tgz"}}}\n' >"$np/package-lock.json"
printf 'module m\n\ngo 1.27\n\nrequire github.com/new/mod v1.4.0\n' >"$np/cli/go.mod"
printf '[project]\nname = "p"\ndependencies = ["newpy>=2"]\n' >"$np/py/pyproject.toml"
printf '[[package]]\nname = "newpy"\nversion = "2.0.0"\nsource = { registry = "https://pypi.org/simple" }\n' >"$np/py/uv.lock"
printf '[package]\nname = "p"\n\n[dependencies]\nnewcrate = "0.9"\n' >"$np/rs/Cargo.toml"
printf '[[package]]\nname = "newcrate"\nversion = "0.9.0"\nsource = "registry+https://github.com/rust-lang/crates.io-index"\n' >"$np/rs/Cargo.lock"
out=$(nudge s1 "$np" Write "$np/src/a.ts" 'import { test } from "vitest";')
expect "nudge denies a dependency newer than the cutoff" '"permissionDecision":"deny"' "$out"
expect "nudge names the library and its version" 'vitest 5.1.0' "$out"
expect_empty "nudge lets the second attempt through" "$(nudge s1 "$np" Write "$np/src/a.ts" 'import { test } from "vitest";')"
printf '{"hook_event_name":"PostToolUse","session_id":"s2","cwd":"%s","tool_name":"WebFetch","tool_input":{"url":"https://vitest.dev/guide","prompt":"vitest 5 API"}}' "$np" | PATH="$tmp/nobin" bash "$here/docs-mark.sh"
expect_empty "nudge stays quiet after a docs lookup this session" "$(nudge s2 "$np" Write "$np/src/a.ts" 'import { test } from "vitest";')"
expect_empty "nudge stays quiet for a dependency older than the cutoff" "$(nudge s3 "$np" Write "$np/src/a.ts" 'import x from "oldlib";')"
out=$(DOCS_NUDGE_CUTOFF=2026-12-01 nudge s4 "$np" Write "$np/src/a.ts" 'import { test } from "vitest";')
expect_empty "DOCS_NUDGE_CUTOFF moves the cutoff" "$out"
out=$(nudge s5 "$np" Write "$np/cli/main.go" 'import "github.com/new/mod/sub"')
expect "nudge covers a go.mod module in a Go edit" 'github.com/new/mod v1.4.0' "$out"
expect "nudge reads go.mod from the file's own folder" '"permissionDecision":"deny"' "$out"
out=$(nudge s6 "$np" Write "$np/py/app.py" 'import newpy')
expect "nudge covers a python dependency locked in uv.lock" 'newpy 2.0.0 (uv.lock)' "$out"
out=$(nudge s7 "$np" Write "$np/rs/main.rs" 'use newcrate::Thing;')
expect "nudge covers a cargo dependency locked in Cargo.lock" 'newcrate 0.9.0 (Cargo.lock)' "$out"
expect_empty "nudge ignores an edit that does not use a dependency" "$(nudge s8 "$np" Write "$np/src/a.ts" 'const x = 1;')"
expect_empty "nudge silent without manifests" "$(nudge s9 "$tmp/empty" Write "$tmp/empty/a.ts" 'import { test } from "vitest";')"
out=$(PATH="$tmp/nojq" bash "$here/docs-nudge.sh" <<<'{"tool_name":"Write","session_id":"s10","cwd":"x"}' 2>&1; echo "rc=$?")
[ "$out" = "rc=0" ] && ok "nudge without jq: silent, exit 0" || bad "nudge without jq: [$out]"
out=$(printf 'not json' | PATH="$tmp/nobin" bash "$here/docs-nudge.sh" 2>&1; echo "rc=$?")
[ "$out" = "rc=0" ] && ok "nudge on unreadable input: silent, exit 0" || bad "nudge on unreadable input: [$out]"
out=$(printf '{"tool_name":"WebFetch","session_id":"s11","tool_input":{"url":"u"}}' | PATH="$tmp/nojq" bash "$here/docs-mark.sh" 2>&1; echo "rc=$?")
[ "$out" = "rc=0" ] && ok "mark without jq: silent, exit 0" || bad "mark without jq: [$out]"
[ -s "$tmp/makeit-docs/s2/lookups.txt" ] && ok "mark logs under makeit-docs/<session>" || bad "mark state dir missing"
# Review additions: every nudge path, public registries only, network and cache behaviour.
nudgeb() { # session cwd command -> hook output (Bash tool)
  jq -nc --arg s "$1" --arg c "$2" --arg x "$3" '{hook_event_name:"PreToolUse",session_id:$s,cwd:$c,tool_name:"Bash",tool_input:{command:$x}}' | PATH="${NPATH:-$tmp/nobin}" bash "$here/docs-nudge.sh"
}
nudgem() { # session cwd file text -> hook output (MultiEdit tool)
  jq -nc --arg s "$1" --arg c "$2" --arg f "$3" --arg x "$4" '{hook_event_name:"PreToolUse",session_id:$s,cwd:$c,tool_name:"MultiEdit",tool_input:{file_path:$f,edits:[{old_string:"a",new_string:"b"},{old_string:"c",new_string:$x}]}}' | PATH="${NPATH:-$tmp/nobin}" bash "$here/docs-nudge.sh"
}
seed() { printf '%s\t%s\n' "$1" "$2" >>"$XDG_CACHE_HOME/makeit-docs/dates.tsv"; }
out=$(nudgem m1 "$np" "$np/src/b.ts" 'import { test } from "vitest";')
expect "nudge reads MultiEdit edits[].new_string" 'vitest 5.1.0' "$out"
# helm: Chart.lock edited from a nested folder, Chart.yaml alone through a Bash helm command
mkdir -p "$np/chart" "$np/chart2"
printf 'apiVersion: v2\nname: c\nversion: 0.1.0\n' >"$np/chart/Chart.yaml"
printf 'dependencies:\n- name: traefik\n  repository: https://traefik.github.io/charts\n  version: 42.3.1\n' >"$np/chart/Chart.lock"
printf 'apiVersion: v2\nname: c\nversion: 0.1.0\ndependencies:\n- name: traefik\n  version: 42.3.1\n  repository: https://traefik.github.io/charts\n' >"$np/chart2/Chart.yaml"
out=$(nudge h1 "$np" Write "$np/chart/values.yaml" $'traefik:\n  replicas: 2')
expect "nudge covers a helm dependency in Chart.lock, found from the edited file" 'traefik 42.3.1 (Chart.lock)' "$out"
out=$(nudgeb h2 "$np/chart2" 'helm template .')
expect "nudge covers a helm command with Chart.yaml only" 'traefik 42.3.1 (Chart.yaml)' "$out"
expect_empty "nudge ignores a Bash command that is not helm or tofu" "$(nudgeb h3 "$np/chart2" 'ls -la')"
# tofu: lock file, Bash tofu and terraform commands, and an edit of a .tf from a nested folder
mkdir -p "$np/tf"
printf 'terraform {\n  required_providers {\n    aws = {\n      source  = "hashicorp/aws"\n      version = "~> 6.0"\n    }\n  }\n}\n' >"$np/tf/main.tf"
printf 'provider "registry.opentofu.org/hashicorp/aws" {\n  version     = "6.99.0"\n  constraints = "~> 6.0"\n}\n' >"$np/tf/.terraform.lock.hcl"
seed 'tofu:hashicorp/aws@6.99.0' 2026-10-15
out=$(nudgeb t1 "$np/tf" 'tofu plan')
expect "nudge covers a tofu plan" 'hashicorp/aws 6.99.0 (.terraform.lock.hcl)' "$out"
expect "nudge covers a terraform apply" 'hashicorp/aws 6.99.0' "$(nudgeb t2 "$np/tf" 'terraform apply')"
expect "nudge covers a .tf edit, found from the edited file" 'hashicorp/aws 6.99.0' "$(nudge t3 "$np" Write "$np/tf/b.tf" 'resource "aws_s3_bucket" "b" {}')"
# python: poetry.lock and uv.lock nudge (a package from another source is skipped); requirements.txt and pyproject alone get no lookup
mkdir -p "$np/req" "$np/poe" "$np/pyo" "$np/py2"
printf 'newreq==3.0.0\n# c\n' >"$np/req/requirements.txt"
printf '[[package]]\nname = "poepy"\nversion = "1.5.0"\n\n[package.dependencies]\nidna = ">=2"\n\n[[package]]\nname = "privpoe"\nversion = "1.0.0"\n\n[package.source]\ntype = "legacy"\nurl = "https://pypi.corp.example/simple"\n' >"$np/poe/poetry.lock"
printf '[project]\nname = "p"\ndependencies = ["onlypy>=4.0"]\n' >"$np/pyo/pyproject.toml"
printf '[[package]]\nname = "p"\nversion = "0.1.0"\nsource = { editable = "." }\n\n[[package]]\nname = "privpy"\nversion = "1.0.0"\nsource = { registry = "https://pypi.corp.example/simple" }\n' >"$np/py2/uv.lock"
seed pypi:newreq@3.0.0 2026-09-15; seed pypi:poepy@1.5.0 2026-09-15; seed pypi:privpoe@1.0.0 2026-09-15; seed pypi:onlypy@4.0 2026-09-15; seed pypi:privpy@1.0.0 2026-09-15
expect_empty "nudge does not look up requirements.txt without a lock" "$(nudge p1 "$np" Write "$np/req/app.py" 'from newreq import x')"
out=$(nudge p2 "$np" Write "$np/poe/app.py" $'import poepy\nimport privpoe')
expect "nudge covers poetry.lock" 'poepy 1.5.0 (poetry.lock)' "$out"
case "$out" in *privpoe*) bad "nudge skips a poetry package from another source" ;; *) ok "nudge skips a poetry package from another source" ;; esac
expect_empty "nudge skips a uv.lock package from another registry" "$(nudge p3 "$np" Write "$np/py2/app.py" 'import privpy')"
expect_empty "nudge does not look up pyproject.toml alone (no lock, source unknown)" "$(nudge p4 "$np" Write "$np/pyo/app.py" 'import onlypy')"
# cargo: Cargo.toml alone (no lookup), a Cargo.lock package without a registry source
mkdir -p "$np/rso" "$np/rs2"
printf '[package]\nname = "p"\n\n[dependencies]\nonlycrate = "1.2"\nlocalcrate = { path = "../x", version = "3.0" }\nwscrate.workspace = true\n' >"$np/rso/Cargo.toml"
printf '[[package]]\nname = "privcrate"\nversion = "1.0.0"\n' >"$np/rs2/Cargo.lock"
seed crates:onlycrate@1.2 2026-09-01; seed crates:localcrate@3.0 2026-09-01; seed crates:privcrate@1.0.0 2026-09-01
out=$(nudge c1 "$np" Write "$np/rso/main.rs" $'use onlycrate::A;\nuse localcrate::B;\nuse wscrate::C;')
expect_empty "nudge does not look up Cargo.toml without a Cargo.lock" "$out"
expect_empty "nudge skips a Cargo.lock package without a registry source" "$(nudge c2 "$np" Write "$np/rs2/main.rs" 'use privcrate::A;')"
# go: private modules (GOPRIVATE, a host without a dot) are never looked up or nudged
mkdir -p "$np/gop"
printf 'module m\n\nrequire (\n\tcorp.example/x/y v1.0.0\n\tinternal/z v1.0.0\n\tgithub.com/new/mod v1.4.0\n)\n' >"$np/gop/go.mod"
seed go:corp.example/x/y@v1.0.0 2026-09-01; seed go:internal/z@v1.0.0 2026-09-01
out=$(GOPRIVATE=corp.example nudge g1 "$np" Write "$np/gop/main.go" $'import "corp.example/x/y"\nimport "internal/z"\nimport "github.com/new/mod"')
expect "nudge keeps a public go module" 'github.com/new/mod v1.4.0' "$out"
case "$out" in *corp.example* | *internal/z*) bad "nudge skips GOPRIVATE and dotless go modules" ;; *) ok "nudge skips GOPRIVATE and dotless go modules" ;; esac
# lookups match the library name as a whole token
mkdir -p "$np/tok"
printf '{"dependencies":{"vit":"1.0.0"}}\n' >"$np/tok/package.json"; seed npm:vit@1.0.0 2026-10-10
printf '{"packages":{"node_modules/vit":{"version":"1.0.0","resolved":"https://registry.npmjs.org/vit/-/vit-1.0.0.tgz"},"node_modules/@sc/pk":{"version":"1.0.0","resolved":"https://registry.npmjs.org/@sc/pk/-/pk-1.0.0.tgz"},"node_modules/socket.io":{"version":"4.0.0","resolved":"https://registry.npmjs.org/socket.io/-/socket.io-4.0.0.tgz"}}}\n' >"$np/tok/package-lock.json"
printf '{"hook_event_name":"PostToolUse","session_id":"k1","tool_name":"WebFetch","tool_input":{"url":"https://vitest.dev/guide"}}' | PATH="$tmp/nobin" bash "$here/docs-mark.sh"
expect "a lookup of vitest does not count for vit" 'vit 1.0.0' "$(nudge k1 "$np" Write "$np/tok/a.ts" 'import x from "vit";')"
printf '{"hook_event_name":"PostToolUse","session_id":"k2","tool_name":"WebSearch","tool_input":{"query":"vit 1.0 changelog"}}' | PATH="$tmp/nobin" bash "$here/docs-mark.sh"
expect_empty "a lookup naming vit counts for vit" "$(nudge k2 "$np" Write "$np/tok/a.ts" 'import x from "vit";')"
# npm usage forms: require with both quotes, bare import, subpaths (scoped too)
printf '{"dependencies":{"vit":"1.0.0","@sc/pk":"1.0.0","socket.io":"4.0.0"}}\n' >"$np/tok/package.json"; seed 'npm:@sc/pk@1.0.0' 2026-10-10; seed npm:socket.io@4.0.0 2026-10-10
i=0
for form in 'const x = require("vit");' "const x = require('vit');" 'import "vit";' "import 'vit';" 'import x from "vit/sub/deep";' 'const x = require("@sc/pk/sub");' 'import "@sc/pk/sub";' 'import("vit")'; do
  i=$((i + 1))
  out=$(nudge "u$i" "$np" Write "$np/tok/a.ts" "$form")
  expect "nudge matches the npm usage form: $form" '"permissionDecision":"deny"' "$out"
done
expect "a dot in a package name matches only a dot" 'socket.io 4.0.0' "$(nudge d1 "$np" Write "$np/tok/a.ts" 'import x from "socket.io";')"
expect_empty "a dot in a package name is not a wildcard" "$(nudge d2 "$np" Write "$np/tok/a.ts" 'import x from "socketXio";')"
# no lock, no lookup: the registry a package comes from cannot be told
mkdir -p "$np/nolock" "$np/gor"
printf '{"devDependencies":{"vitest":"^5.1.0"}}\n' >"$np/nolock/package.json"
mkdir -p "$np/nolock2"
printf '{"dependencies":{"vitest":"^5.1.0","execa":"^10.2.1"}}\n' >"$np/nolock2/package.json"
printf '{"packages":{"node_modules/vitest":{"version":"5.1.0"}}}\n' >"$np/nolock2/package-lock.json"
expect_empty "nudge does not look up a package missing from the lock or without resolved" "$(nudge nl2 "$np" Write "$np/nolock2/a.ts" $'import { test } from "vitest";\nimport { execa } from "execa";')"
expect_empty "nudge does not look up package.json without a lock" "$(nudge nl1 "$np" Write "$np/nolock/a.ts" 'import { test } from "vitest";')"
printf 'module m\n\nrequire (\n\tgithub.com/new/mod v1.4.0\n\tgithub.com/new/two v1.0.0\n\tgithub.com/new/three v1.0.0\n)\n\nreplace github.com/new/mod => ../mod\n\nreplace (\n\tgithub.com/new/two v1.0.0 => ../two\n)\n' >"$np/gor/go.mod"
seed go:github.com/new/two@v1.0.0 2026-09-01; seed go:github.com/new/three@v1.0.0 2026-09-01
out=$(nudge gr1 "$np" Write "$np/gor/main.go" $'import "github.com/new/mod"\nimport "github.com/new/two"\nimport "github.com/new/three"')
expect "nudge keeps a go module that is not replaced" 'github.com/new/three v1.0.0' "$out"
case "$out" in *new/mod* | *new/two*) bad "nudge skips replaced go modules (both replace forms)" ;; *) ok "nudge skips replaced go modules (both replace forms)" ;; esac
expect_empty "nudge does not match another package with the same prefix" "$(nudge u9 "$np" Write "$np/tok/a.ts" 'import x from "vit-extra"; require("vitest/x")')"
# network: a fake curl answers per URL and logs its arguments
mkdir -p "$tmp/netbin" "$np/net"
for f in "$tmp"/nobin/*; do ln -sf "$(readlink "$f")" "$tmp/netbin/$(basename "$f")"; done
cat >"$tmp/netbin/curl" <<CURL
#!/bin/bash
printf '%s\n' "\$*" >>"$tmp/curl.log"
case "\$*" in
  *registry.npmjs.org/netlib*) echo '{"time":{"1.0.0":"2026-10-20T00:00:00Z"}}' ;;
  *registry.npmjs.org/nodate*) echo '{"time":{}}' ;;
  *registry.npmjs.org/gone404*) exit 22 ;;
  *registry.npmjs.org/later*) echo '{"time":{"1.0.0":"2026-10-21T00:00:00Z"}}' ;;
  *charts.example/index.yaml*) printf 'entries:\n  foo:\n  - apiVersion: v2\n    created: "2026-10-30T00:00:00Z"\n    version: 1.0.0\n' ;;
  *) exit 7 ;;
esac
CURL
chmod +x "$tmp/netbin/curl"; : >"$tmp/curl.log"
printf '{"dependencies":{"netlib":"1.0.0","nodate":"1.0.0","failing":"1.0.0","privlib":"1.0.0"}}\n' >"$np/net/package.json"
printf '{"packages":{"node_modules/netlib":{"version":"1.0.0","resolved":"https://registry.npmjs.org/netlib/-/netlib-1.0.0.tgz"},"node_modules/nodate":{"version":"1.0.0","resolved":"https://registry.npmjs.org/nodate/-/nodate-1.0.0.tgz"},"node_modules/failing":{"version":"1.0.0","resolved":"https://registry.npmjs.org/failing/-/failing-1.0.0.tgz"},"node_modules/privlib":{"version":"1.0.0","resolved":"https://npm.corp.example/privlib/-/privlib-1.0.0.tgz"}}}\n' >"$np/net/package-lock.json"
imports=$'import a from "netlib";\nimport b from "nodate";\nimport c from "failing";\nimport d from "privlib";'
out=$(NPATH="$tmp/netbin" nudge n1 "$np" Write "$np/net/a.ts" "$imports")
expect "nudge looks the release date up in the registry" 'netlib 1.0.0 (package-lock.json): released 2026-10-20' "$out"
expect "nudge denies when the registry gives no date" 'nodate 1.0.0 (package-lock.json): release date unknown' "$out"
case "$out" in *failing* | *privlib*) bad "nudge does not deny on a failed lookup or a private package" ;; *) ok "nudge does not deny on a failed lookup or a private package" ;; esac
expect "registry lookups use a 3 s cap" '-m 3' "$(cat "$tmp/curl.log")"
case "$(cat "$tmp/curl.log")" in *privlib*) bad "nudge never sends a private package name out" ;; *) ok "nudge never sends a private package name out" ;; esac
expect "a registry date is cached" "npm:netlib@1.0.0	2026-10-20" "$(cat "$XDG_CACHE_HOME/makeit-docs/dates.tsv")"
expect "a failed lookup is cached as skip for the day" "npm:failing@1.0.0	skip:" "$(cat "$XDG_CACHE_HOME/makeit-docs/dates.tsv")"
before=$(grep -c . "$tmp/curl.log")
out=$(NPATH="$tmp/netbin" nudge n2 "$np" Write "$np/net/a.ts" 'import c from "failing";'; NPATH="$tmp/netbin" nudge n3 "$np" Write "$np/net/a.ts" 'import a from "netlib";')
expect "a cached date serves a new session without the network" 'netlib 1.0.0' "$out"
[ "$(grep -c . "$tmp/curl.log")" = "$before" ] && ok "a cached skip means no new lookup" || bad "a cached skip means no new lookup: curl ran again"
# an HTTP error is per library; only a connection failure stops the later lookups
mkdir -p "$np/net2"
printf '{"dependencies":{"gone404":"1.0.0","later":"1.0.0"}}\n' >"$np/net2/package.json"
printf '{"packages":{"node_modules/gone404":{"version":"1.0.0","resolved":"https://registry.npmjs.org/gone404/-/g.tgz"},"node_modules/later":{"version":"1.0.0","resolved":"https://registry.npmjs.org/later/-/l.tgz"}}}\n' >"$np/net2/package-lock.json"
out=$(NPATH="$tmp/netbin" nudge e1 "$np" Write "$np/net2/a.ts" $'import a from "gone404";\nimport b from "later";')
expect "a 404 for one library does not stop the lookup of the next" 'later 1.0.0 (package-lock.json): released 2026-10-21' "$out"
expect "a 404 is cached as skip for that library" "npm:gone404@1.0.0	skip:" "$(cat "$XDG_CACHE_HOME/makeit-docs/dates.tsv")"
mkdir -p "$np/net3"
printf '{"dependencies":{"failing3":"1.0.0","later3":"1.0.0"}}\n' >"$np/net3/package.json"
printf '{"packages":{"node_modules/failing3":{"version":"1.0.0","resolved":"https://registry.npmjs.org/failing3/-/f.tgz"},"node_modules/later3":{"version":"1.0.0","resolved":"https://registry.npmjs.org/later3/-/l.tgz"}}}\n' >"$np/net3/package-lock.json"
: >"$tmp/curl.log"
out=$(NPATH="$tmp/netbin" nudge e2 "$np" Write "$np/net3/a.ts" $'import a from "failing3";\nimport b from "later3";')
expect_empty "a connection failure denies nothing" "$out"
case "$(cat "$tmp/curl.log")" in *later3*) bad "a connection failure stops the later lookups" ;; *) ok "a connection failure stops the later lookups" ;; esac
# helm repository: https only
mkdir -p "$np/chart3"
printf 'dependencies:\n- name: foo\n  repository: https://charts.example\n  version: 1.0.0\n' >"$np/chart3/Chart.lock"
expect "nudge reads a helm repository index (https only)" 'foo 1.0.0 (Chart.lock): released 2026-10-30' "$(NPATH="$tmp/netbin" nudgeb h9 "$np/chart3" 'helm dep build')"
expect "helm repository lookups pass --proto =https" '--proto =https' "$(grep charts.example "$tmp/curl.log")"
unset XDG_CACHE_HOME TMPDIR

# --- autoupdate.sh -------------------------------------------------------------------------------
au_stack="$tmp/austack"; mkdir -p "$au_stack"
au_hook() { PATH="$1" MAKEIT_DIR="$au_stack" bash "$here/autoupdate.sh"; }
out=$(printf '{"hook_event_name":"SessionStart","source":"startup"}' | au_hook "$tmp/mbin:$nomakeit")
expect_empty "autoupdate with nothing to report prints nothing" "$out"
expect "autoupdate starts the daily run" "update --auto" "$(waitlog)"
printf 'makeit updated: superpowers 7.1.0 · undo: makeit rollback\n' >"$au_stack/pending-note"
out=$(printf '{"hook_event_name":"SessionStart","source":"startup"}' | au_hook "$tmp/mbin:$nomakeit")
expect "autoupdate shows the pending note to the user" 'makeit updated: superpowers 7.1.0 · undo: makeit rollback' "$(jq -r '.systemMessage' <<<"$out" 2>/dev/null)"
expect "autoupdate tells Claude too" 'superpowers 7.1.0' "$(jq -r '.hookSpecificOutput.additionalContext' <<<"$out" 2>/dev/null)"
[ ! -e "$au_stack/pending-note" ] && ok "autoupdate shows a note once" || bad "autoupdate shows a note once: the file is still there"
waitlog >/dev/null
out=$(printf '{"hook_event_name":"SessionStart","source":"startup"}' | au_hook "$nomakeit"); rc=$?
if [ -z "$out" ] && [ "$rc" = 0 ]; then ok "autoupdate without makeit: silent, exit 0"; else bad "autoupdate without makeit: rc $rc out [$out]"; fi

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
# The node path needs marked: installed once here, the newest, as makeit setup does.
if command -v npm >/dev/null && npm install --silent --no-audit --no-fund --prefix "$tmp/marked" marked >/dev/null 2>&1; then
  export MARKED_PREFIX="$tmp/marked"
else
  echo "SKIP  options.sh node path: npm could not install marked"
fi
for loc in C C.UTF-8; do
  if LC_ALL=$loc bash "$here/options_test.sh" >"$tmp/options-$loc.out" 2>&1; then
    ok "options_test.sh under LC_ALL=$loc ($(grep -c '^ok' "$tmp/options-$loc.out") cases)"
  else
    bad "options_test.sh under LC_ALL=$loc: $(grep '^FAIL' "$tmp/options-$loc.out" | head -3)"
  fi
done

# --- push.sh: a bare push from a deploy branch asks ------------------------------------------------
prepo="$tmp/prepo"; mkdir -p "$prepo/.claude"; git -C "$prepo" init -q -b dev; git -C "$prepo" config user.email t@e; git -C "$prepo" config user.name t
git -C "$prepo" commit -q --allow-empty -m init
printf '{"permissions":{"ask":["Bash(git push * dev)","Bash(git push *:dev)","Bash(git push * release)"]}}' >"$prepo/.claude/settings.json"
push() { jq -n --arg c "$1" --arg d "$2" '{hook_event_name:"PreToolUse",tool_name:"Bash",cwd:$d,tool_input:{command:$c}}' | bash "$here/push.sh"; }
out=$(push 'git push' "$prepo"); expect "push: bare push on a ruled branch asks" '"permissionDecision":"ask"' "$out"
expect "push: the reason names the branch" 'from dev' "$out"
out=$(push 'git push -u origin' "$prepo"); expect "push: remote only, with a flag, asks" '"permissionDecision":"ask"' "$out"
git -C "$prepo" checkout -q -b feat/x
out=$(push 'git push' "$prepo"); expect_empty "push: bare push on feat/x is silent" "$out"
git -C "$prepo" checkout -q dev
out=$(push 'git push origin feat/x' "$prepo"); expect_empty "push: a named push is silent" "$out"
out=$(push 'cd x && git push origin' "$prepo"); expect "push: chained bare push asks" '"permissionDecision":"ask"' "$out"
git -C "$prepo" checkout -q -b main
/bin/rm -rf "$prepo/.claude"
out=$(push 'git push' "$prepo"); expect "push: main asks with no settings file" '"permissionDecision":"ask"' "$out"
git -C "$prepo" checkout -q --detach
out=$(push 'git push' "$prepo"); expect_empty "push: detached HEAD is silent" "$out"
mkdir -p "$tmp/notgit"; out=$(push 'git push' "$tmp/notgit"); expect_empty "push: not a git repo is silent" "$out"
out=$(printf 'not json' | bash "$here/push.sh"; echo "rc=$?"); expect "push: bad input exits 0" "rc=0" "$out"

# --- skills/init/SKILL.md: the /makeit:init agent step (cli/INIT.md) ---------------------------
sk="$here/../skills/init/SKILL.md"
if [ -f "$sk" ]; then
  ok "init skill exists"
  front=$(awk 'NR == 1 && /^---$/ { f = 1; next } f && /^---$/ { exit } f' "$sk")
  body=$(awk 'NR == 1 && /^---$/ { f = 1; next } f && /^---$/ { f = 0; b = 1; next } b' "$sk")
  expect "init skill: only the user starts it" "disable-model-invocation: true" "$front"
  expect "init skill: pre-approves exactly the init call" "allowed-tools: Bash(makeit init --yes --json)" "$front"
  expect "init skill: pre-approves the record call" "Bash(makeit init --applied:*)" "$front"
  expect "init skill: keep lines in single quotes" "--keep '<file>: <what differs>'" "$body"
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
  expect "init skill: the commit carries the floor-guard reason line" "floor-guard: makeit init adds the CI checks, the check tasks and the hook config" "$body"
  expect "init skill: a repository with no commit yet gets its first commit on the default branch" "no commit yet" "$body"
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

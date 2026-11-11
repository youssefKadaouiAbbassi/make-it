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
skipped="" # what did not run, for the last line

tmp=$(mktemp -d "${TMPDIR:-/tmp}/hooks-test.XXXXXX")
trap '/bin/rm -rf "$tmp"' EXIT

# --- notify.sh: fake notify-send and curl record the calls ----------------------------------
unset NTFY_TOPIC NTFY_URL NTFY_TOKEN
export XDG_STATE_HOME="$tmp/state"
mkdir -p "$tmp/bin"
printf '#!/usr/bin/env bash\nprintf "%%s\\n" "$*" >>"%s/curl.log"\n' "$tmp" >"$tmp/bin/curl"; chmod +x "$tmp/bin/curl"
printf '#!/usr/bin/env bash\nprintf "%%s\\n" "$*" >>"%s/ns.log"\n' "$tmp" >"$tmp/bin/notify-send"; chmod +x "$tmp/bin/notify-send"
run_notify() { PATH="$tmp/bin:$PATH" NTFY_TOPIC="${NTFY_TOPIC-t0pic}" NTFY_URL="${NTFY_URL-https://ntfy.example}" NTFY_TOKEN="${NTFY_TOKEN-}" bash "$here/notify.sh"; }
reset_logs() { : >"$tmp/curl.log"; : >"$tmp/ns.log"; }
reset_logs
printf '{"hook_event_name":"Stop","cwd":"/home/u/proj","session_id":"s"}' | run_notify; rc=$?
[ "$rc" = 0 ] && ok "notify Stop exits 0" || bad "notify Stop exit $rc"
expect "notify Stop posts done" "-d done https://ntfy.example/t0pic" "$(cat "$tmp/curl.log")"
expect "notify title is repo name" "Title: claude - proj" "$(cat "$tmp/curl.log")"
expect "notify Stop shows done on the desktop" "claude - proj done" "$(cat "$tmp/ns.log")"
reset_logs
printf '{"hook_event_name":"Stop","cwd":"/x/repo","background_tasks":[{"id":"a"},{"id":"b"}]}' | run_notify
expect "notify Stop with two background tasks (array), ntfy" "still running: 2" "$(cat "$tmp/curl.log")"
expect "notify Stop with two background tasks (array), desktop" "still running: 2" "$(cat "$tmp/ns.log")"
reset_logs
printf '{"hook_event_name":"Stop","cwd":"/x/repo","background_tasks":[]}' | run_notify
case "$(cat "$tmp/ns.log")" in *running*) bad "notify empty background_tasks said running" ;; *) ok "notify empty background_tasks is plain done" ;; esac
reset_logs
# macOS ships bash 3.2, where an empty array under set -u is an error: with no token, notify must
# still post. Run there when docker can.
if command -v docker >/dev/null && docker image inspect bash:3.2 >/dev/null 2>&1 || docker pull -q bash:3.2 >/dev/null 2>&1; then
  # 77: the image could not get jq (no network), so this is a skip, not a failure.
  printf '{"hook_event_name":"Stop","cwd":"/home/u/proj","session_id":"s"}' |
    docker run --rm -i -v "$here:/h:ro" -v "$tmp:$tmp" -e NTFY_TOPIC=t0pic -e NTFY_URL=https://ntfy.example bash:3.2 \
      sh -c "apk add -q jq >/dev/null 2>&1 || exit 77; PATH=$tmp/bin:\$PATH bash /h/notify.sh"
  if [ $? = 77 ]; then echo "SKIP  notify under bash 3.2: no jq in the image (offline)"; else
    expect "notify under bash 3.2 with no token posts" "-d done https://ntfy.example/t0pic" "$(cat "$tmp/curl.log")"
  fi
else
  echo "SKIP  notify under bash 3.2: no docker"
fi
reset_logs
printf '{"hook_event_name":"PreToolUse","cwd":"/x/repo","tool_name":"AskUserQuestion","tool_input":{}}' | run_notify
expect "notify PreToolUse names the tool, ntfy" "needs you: AskUserQuestion" "$(cat "$tmp/curl.log")"
expect "notify PreToolUse names the tool, desktop" "needs you: AskUserQuestion" "$(cat "$tmp/ns.log")"
reset_logs
printf '{"hook_event_name":"Notification","cwd":"/x/repo","message":"Claude is waiting for your input","notification_type":"idle_prompt"}' | run_notify
[ ! -s "$tmp/curl.log" ] && [ ! -s "$tmp/ns.log" ] && ok "notify ignores idle_prompt" || bad "notify idle_prompt notified"
printf '{"hook_event_name":"Notification","cwd":"/x/repo","message":"Claude needs your permission to use Bash","notification_type":"permission_prompt"}' | run_notify
expect "notify permission_prompt, ntfy" "needs permission" "$(cat "$tmp/curl.log")"
expect "notify permission_prompt, desktop" "needs permission" "$(cat "$tmp/ns.log")"
reset_logs
NTFY_TOKEN=abc bash -c 'printf "{\"hook_event_name\":\"Stop\",\"cwd\":\"/x/repo\"}" | PATH="'"$tmp"'/bin:$PATH" NTFY_TOPIC=t NTFY_URL=https://n bash "'"$here"'/notify.sh"'
expect "notify sends bearer token when set" "Authorization: Bearer abc" "$(cat "$tmp/curl.log")"
reset_logs
out=$(printf '{"hook_event_name":"Stop","cwd":"/x/repo"}' | NTFY_TOPIC= run_notify)
[ ! -s "$tmp/curl.log" ] && ok "notify without topic posts nothing" || bad "notify without topic posted"
expect "notify without topic still notifies the desktop" "done" "$(cat "$tmp/ns.log")"
expect_empty "notify prints nothing to stdout" "$out"
reset_logs
out=$(printf '{"hook_event_name":"SessionStart","cwd":"/x/repo"}' | run_notify; echo "rc=$?")
[ ! -s "$tmp/curl.log" ] && [ ! -s "$tmp/ns.log" ] && expect "notify ignores unknown events" "rc=0" "$out" || bad "notify unknown event notified"
# No notifier and no topic: only the tools the hook needs, none of the fakes.
mkdir -p "$tmp/bare"; for b in bash cat basename jq tr; do ln -sf "$(command -v $b)" "$tmp/bare/$b"; done
out=$(printf '{"hook_event_name":"Stop","cwd":"/x/repo"}' | PATH="$tmp/bare" bash "$here/notify.sh" 2>&1; echo "rc=$?")
expect "notify with no notifier and no topic exits 0 silently" "rc=0" "$out"
[ "$out" = "rc=0" ] && ok "notify with no notifier and no topic prints nothing" || bad "notify bare printed [$out]"
# Desktop fallbacks: only the tools the hook needs plus one fake notifier.
for n in osascript powershell.exe; do
  mkdir -p "$tmp/d-$n"; for b in bash cat basename jq tr; do ln -sf "$(command -v $b)" "$tmp/d-$n/$b"; done
  printf '#!/usr/bin/env bash\nprintf "%%s\\n" "$*" >>"%s/%s.log"\n' "$tmp" "$n" >"$tmp/d-$n/$n"; chmod +x "$tmp/d-$n/$n"
  : >"$tmp/$n.log"
  printf '{"hook_event_name":"Stop","cwd":"/x/repo"}' | PATH="$tmp/d-$n" bash "$here/notify.sh"
  expect "notify falls back to $n: message" "done" "$(cat "$tmp/$n.log")"
  expect "notify falls back to $n: title" "claude - repo" "$(cat "$tmp/$n.log")"
done
: >"$tmp/powershell.exe.log"
# Hostile text in both the message (tool_name) and the cwd basename, each with a quote and $( ).
jq -n --arg t "it's \$(touch $tmp/pwned) 'x'" --arg c "$tmp/it's\$(x)" \
  '{hook_event_name:"PreToolUse",tool_name:$t,cwd:$c}' | PATH="$tmp/d-powershell.exe" bash "$here/notify.sh"
bad_nodes=$(grep -o "CreateTextNode('[^']*')" "$tmp/powershell.exe.log" | sed "s/CreateTextNode('//; s/')$//" | grep -v '^[A-Za-z0-9 :,._-]*$')
nodes=$(grep -o "CreateTextNode('[^']*')" "$tmp/powershell.exe.log" | wc -l | tr -d ' ')
[ -z "$bad_nodes" ] && [ "$nodes" = 2 ] && [ ! -e "$tmp/pwned" ] && expect "notify powershell hostile cwd reaches the title" "CreateTextNode('claude - it_s__x_')" "$(cat "$tmp/powershell.exe.log")" && ok "notify powershell text holds only plain characters" || bad "notify powershell hostile text: [$bad_nodes] nodes=$nodes"
reset_logs
printf '{"hook_event_name":"Stop","cwd":"/x/repo","stop_hook_active":true}' | run_notify
expect "notify still says done on a Stop with stop_hook_active" "-d done" "$(cat "$tmp/curl.log")"
reset_logs
long=$(printf 'x%.0s' $(seq 1 300))
printf '{"hook_event_name":"PreToolUse","cwd":"/x/repo","tool_name":"%s"}' "$long" | run_notify
sent=$(sed 's/.* -d \(.*\) https:.*/\1/' "$tmp/curl.log")
[ "${#sent}" = 200 ] && ok "notify truncates the message to 200 characters" || bad "notify message length ${#sent}"
reset_logs
printf '{"hook_event_name":"Stop","cwd":"/x/repo"}' | NTFY_TOKEN=abc run_notify
expect "notify with a token still notifies the desktop" "done" "$(cat "$tmp/ns.log")"
expect "notify with a token sends the bearer header" "Authorization: Bearer abc" "$(cat "$tmp/curl.log")"
mkdir -p "$tmp/nojq"; for b in bash cat basename head; do ln -sf "$(command -v $b)" "$tmp/nojq/$b"; done
out=$(printf '{"hook_event_name":"Stop","cwd":"/x/repo"}' | PATH="$tmp/nojq" NTFY_TOPIC=t bash "$here/notify.sh" 2>&1; echo "rc=$?")
expect "notify without jq exits 0" "rc=0" "$out"

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
# A background job still runs: the turn comes back when it ends, so the reminder waits, up to 30
# minutes (a dev server runs for the whole session).
bgjob=',"stop_hook_active":false,"background_tasks":[{"id":"a","type":"shell","status":"running"}]'
out=$(payload Stop "$bgjob" | bash "$here/handoff.sh")
expect_empty "handoff Stop waits while a background job runs" "$out"
holdf=$(find "$tmp/state/makeit/sessions" -name 's*.hold' | head -n 1)
[ -n "$holdf" ] && ok "handoff Stop marks when its wait began" || bad "handoff Stop left no wait marker"
touch -d '31 minutes ago' "${holdf:-$tmp/none}"
out=$(payload Stop "$bgjob" | bash "$here/handoff.sh")
expect "handoff Stop reminds after 30 minutes of waiting" '"hookEventName":"Stop","additionalContext":"Files changed this session' "$out"
if [ -e "${holdf:-$tmp/none}" ]; then bad "handoff Stop keeps its wait marker after reminding"; else ok "handoff Stop clears its wait marker after reminding"; fi
out=$(payload Stop ',"stop_hook_active":false,"background_tasks":[]' | bash "$here/handoff.sh")
expect "handoff Stop reminds once no job runs" '"hookEventName":"Stop","additionalContext":"Files changed this session' "$out"
# When the wait marker cannot be written, the reminder must not stay silent for the whole session.
if [ "$(id -u)" != 0 ]; then
  chmod a-w "$tmp/state/makeit/sessions"
  out=$(payload Stop "$bgjob" | bash "$here/handoff.sh")
  chmod u+w "$tmp/state/makeit/sessions"
  expect "handoff Stop reminds when it cannot mark its wait" '"hookEventName":"Stop","additionalContext":"Files changed this session' "$out"
else
  echo "SKIP  handoff Stop without a writable state folder: running as root"
fi
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
case "$out" in *verification-before-completion*) bad "skills still names verification-before-completion" ;; *) ok "skills does not name verification-before-completion" ;; esac
for short in 1 a done ""; do
  expect_empty "skills silent on short prompt '$short'" "$(jq -n --arg p "$short" '{hook_event_name:"UserPromptSubmit",cwd:"/x",prompt:$p}' | bash "$here/skills.sh")"
done
for long in "go for it" "fix it" "keep going"; do
  expect "skills reminds on '$long'" 'hookEventName' "$(jq -n --arg p "$long" '{hook_event_name:"UserPromptSubmit",cwd:"/x",prompt:$p}' | bash "$here/skills.sh")"
done
expect "skills reminds on a 5-word prompt" 'hookEventName' "$(jq -n '{hook_event_name:"UserPromptSubmit",cwd:"/x",prompt:"fix the login page bug"}' | bash "$here/skills.sh")"
expect "skills reminds on a multi-line prompt" 'hookEventName' "$(jq -n '{hook_event_name:"UserPromptSubmit",cwd:"/x",prompt:"fix this\nplease now"}' | bash "$here/skills.sh")"
expect_empty "skills ignores other events" "$(printf '{"hook_event_name":"Stop","cwd":"/x"}' | bash "$here/skills.sh")"
# Background notices arrive as prompts (a finished job, a message from another session): no reminder.
expect_empty "skills silent on a task notification" "$(jq -n --arg p $'<task-notification>\n<task-id>x</task-id>\n<status>completed</status> the job finished' '{hook_event_name:"UserPromptSubmit",cwd:"/x",prompt:$p}' | bash "$here/skills.sh")"
expect_empty "skills silent on a message from another session" "$(jq -n --arg p $'Another Claude session sent a message:\n<agent-message from="a1">done with the search</agent-message>' '{hook_event_name:"UserPromptSubmit",cwd:"/x",prompt:$p}' | bash "$here/skills.sh")"
expect "skills reminds when a prompt only quotes a notice" 'hookEventName' "$(jq -n '{hook_event_name:"UserPromptSubmit",cwd:"/x",prompt:"why did <task-notification> show up twice here"}' | bash "$here/skills.sh")"
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
# I1: a big package.json + lock must not cost per-dependency jq passes when the edit imports nothing
mkdir -p "$np/big"
bigpk=$(for i in $(seq 1 80); do printf '"lib%s":"1.0.0",' "$i"; done)
printf '{"dependencies":{%s"last":"1.0.0"}}\n' "$bigpk" >"$np/big/package.json"
for i in $(seq 1 80); do printf '"node_modules/lib%s":{"version":"1.0.0","resolved":"https://registry.npmjs.org/lib%s/-/l.tgz"},' "$i" "$i"; done >"$tmp/biglock"
printf '{"packages":{%s"node_modules/last":{"version":"1.0.0","resolved":"https://registry.npmjs.org/last/-/l.tgz"}}}\n' "$(cat "$tmp/biglock")" >"$np/big/package-lock.json"
if [ -n "${EPOCHREALTIME:-}" ]; then
  t0=${EPOCHREALTIME/[.,]/}
  out=$(NPATH="$tmp/netbin" nudge big1 "$np" Write "$np/big/a.ts" 'const x = 1;')
  ms=$(( (${EPOCHREALTIME/[.,]/} - t0) / 1000 ))
  [ -z "$out" ] && [ "$ms" -lt 200 ] && ok "nudge with 80 dependencies and no import takes ${ms} ms (< 200)" || bad "nudge with 80 dependencies: ${ms} ms, out [$out]"
else echo "SKIP  nudge timing: no EPOCHREALTIME (bash < 5)"; fi
expect "nudge still finds the one imported dependency in a big lock" 'lib42 1.0.0' "$(seed npm:lib42@1.0.0 2026-10-10; nudge big2 "$np" Write "$np/big/a.ts" 'import x from "lib42";')"
# one call spends about 3 s on registry lookups at most (a lookup that takes 3 s uses it all): the later libraries are skipped, not cached
mkdir -p "$np/budget"
printf '{"dependencies":{"b1":"1.0.0","b2":"1.0.0","b3":"1.0.0"}}\n' >"$np/budget/package.json"
printf '{"packages":{"node_modules/b1":{"version":"1.0.0","resolved":"https://registry.npmjs.org/b1/-/b.tgz"},"node_modules/b2":{"version":"1.0.0","resolved":"https://registry.npmjs.org/b2/-/b.tgz"},"node_modules/b3":{"version":"1.0.0","resolved":"https://registry.npmjs.org/b3/-/b.tgz"}}}\n' >"$np/budget/package-lock.json"
mkdir -p "$tmp/slowbin"; for f in "$tmp"/nobin/*; do ln -sf "$(readlink "$f")" "$tmp/slowbin/$(basename "$f")"; done
ln -sf "$(command -v sleep)" "$tmp/slowbin/sleep"
printf '#!/bin/bash\nprintf "%%s\\n" "$*" >>"%s/slow.log"\nsleep 3\necho "{\\"time\\":{\\"1.0.0\\":\\"2020-01-01T00:00:00Z\\"}}"\n' "$tmp" >"$tmp/slowbin/curl"; chmod +x "$tmp/slowbin/curl"
NPATH="$tmp/slowbin" nudge bud1 "$np" Write "$np/budget/a.ts" $'import a from "b1";\nimport b from "b2";\nimport c from "b3";' >/dev/null
[ "$(grep -c . "$tmp/slow.log")" = 1 ] && ok "registry lookups stop after the time budget (1 of 3 ran)" || bad "registry budget: $(grep -c . "$tmp/slow.log") lookups"
expect "the first lookup gets the whole 3 s" '-m 3' "$(sed -n 1p "$tmp/slow.log")"
# milliseconds: two 2 s lookups leave the second one 1 s minus the overhead, never a whole second or more
if [ -n "${EPOCHREALTIME:-}" ]; then
  : >"$tmp/slow.log"; sed -i 's/sleep 3/sleep 2/' "$tmp/slowbin/curl"
  NPATH="$tmp/slowbin" nudge bud2 "$np" Write "$np/budget/a.ts" $'import a from "b1";\nimport b from "b2";\nimport c from "b3";' >/dev/null
  [ "$(grep -c . "$tmp/slow.log")" = 2 ] && ok "the budget is counted in milliseconds (2 of 3 ran)" || bad "ms budget: $(grep -c . "$tmp/slow.log") lookups"
  expect "the second lookup gets a fraction of a second" '-m 0.' "$(sed -n 2p "$tmp/slow.log")"
fi
expect "registry lookups ask for a compressed answer" '--compressed' "$(cat "$tmp/curl.log")"
# M7: session folders older than 7 days are pruned on the next run
mkdir -p "$tmp/makeit-docs/oldsess" "$tmp/makeit-docs/newsess"; touch -d '10 days ago' "$tmp/makeit-docs/oldsess"
for b in find rm; do ln -sf "$(command -v $b)" "$tmp/nobin/$b"; done
nudge prune1 "$np" Write "$np/src/a.ts" 'const x = 1;' >/dev/null
{ [ ! -e "$tmp/makeit-docs/oldsess" ] && [ -d "$tmp/makeit-docs/newsess" ]; } && ok "nudge prunes session folders older than 7 days" || bad "nudge prune: old=$(ls -d "$tmp/makeit-docs/oldsess" 2>&1) new=$(ls -d "$tmp/makeit-docs/newsess" 2>&1)"
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
out=$(push 'cd . && git push origin' "$prepo"); expect "push: chained bare push asks" '"permissionDecision":"ask"' "$out"
git -C "$prepo" checkout -q -b main
/bin/rm -rf "$prepo/.claude"
out=$(push 'git push' "$prepo"); expect "push: main asks with no settings file" '"permissionDecision":"ask"' "$out"
git -C "$prepo" checkout -q --detach
out=$(push 'git push' "$prepo"); expect_empty "push: detached HEAD is silent" "$out"
mkdir -p "$tmp/notgit"; out=$(push 'git push' "$tmp/notgit"); expect_empty "push: not a git repo is silent" "$out"
out=$(printf 'not json' | bash "$here/push.sh" 2>&1; echo "rc=$?")
if [ "$out" = "rc=0" ]; then ok "push: bad input exits 0, no output"; else bad "push: bad input exits 0, no output: $out"; fi
qrepo="$tmp/qrepo"; mkdir -p "$qrepo/.claude" "$qrepo/sub" "$tmp/files"; : >"$tmp/files/a"; : >"$tmp/files/b"
git -C "$qrepo" init -q -b dev; git -C "$qrepo" config user.email t@e; git -C "$qrepo" config user.name t; git -C "$qrepo" commit -q --allow-empty -m init
printf '{"permissions":{"ask":["Bash(git push * dev)"]}}' >"$qrepo/.claude/settings.json"
out=$(push 'git push && git push origin x' "$qrepo"); expect "push: a bare push before a named one asks" '"permissionDecision":"ask"' "$out"
out=$(push 'git push origin x && git push' "$qrepo"); expect "push: a bare push after a named one asks" '"permissionDecision":"ask"' "$out"
out=$(push 'git push origin HEAD' "$qrepo"); expect "push: origin HEAD is bare" '"permissionDecision":"ask"' "$out"
out=$(push 'git push origin HEAD:HEAD' "$qrepo"); expect "push: HEAD:HEAD is bare" '"permissionDecision":"ask"' "$out"
out=$(push 'git push origin HEAD:other' "$qrepo"); expect_empty "push: HEAD:other is named" "$out"
out=$(push 'git push --all' "$qrepo"); expect "push: --all asks when a deploy branch exists" '"permissionDecision":"ask"' "$out"
out=$(push 'git push --mirror' "$qrepo"); expect "push: --mirror asks when a deploy branch exists" '"permissionDecision":"ask"' "$out"
out=$(push 'git push --tags' "$qrepo"); expect_empty "push: --tags alone is silent" "$out"
out=$(push 'git push -o ci.skip' "$qrepo"); expect "push: -o value is not a ref" '"permissionDecision":"ask"' "$out"
out=$(push 'git push --push-option x --repo r' "$qrepo"); expect "push: value flags are not refs" '"permissionDecision":"ask"' "$out"
out=$(cd "$tmp/files" && push 'git push *' "$qrepo"); expect "push: a * in the command is not expanded" '"permissionDecision":"ask"' "$out"
git -C "$qrepo" checkout -q -b feat/y; git -C "$qrepo" checkout -q dev
out=$(push "cd $tmp/notgit && git push" "$qrepo"); expect_empty "push: the cd target, not cwd, is judged" "$out"
out=$(push 'cd sub && git push' "$qrepo"); expect "push: cd into a dir of the repo, branch dev, asks" '"permissionDecision":"ask"' "$out"
git -C "$qrepo" checkout -q feat/y
out=$(push 'git -C "'"$qrepo"'" push' "$qrepo"); expect_empty "push: git -C on feat/y is silent" "$out"
git -C "$qrepo" checkout -q dev
out=$(push 'git -C "'"$qrepo"'" push' "$qrepo"); expect "push: git -C on dev asks" '"permissionDecision":"ask"' "$out"
out=$(push 'git push origin 2>&1' "$qrepo"); expect "push: a 2>&1 redirect is dropped" '"permissionDecision":"ask"' "$out"
out=$(push 'git push 2>/dev/null' "$qrepo"); expect "push: a 2>file redirect is dropped" '"permissionDecision":"ask"' "$out"
out=$(push 'git push &>/dev/null' "$qrepo"); expect "push: a &>file redirect is dropped" '"permissionDecision":"ask"' "$out"
out=$(push 'git push >out.log' "$qrepo"); expect "push: a >file redirect is dropped" '"permissionDecision":"ask"' "$out"
out=$(push '(cd sub && git push)' "$qrepo"); expect "push: a subshell cd resolves" '"permissionDecision":"ask"' "$out"
out=$(push "(cd $tmp/notgit && git push)" "$qrepo"); expect_empty "push: a subshell cd to a non-repo is silent" "$out"
out=$(push '{ git push; }' "$qrepo"); expect "push: a brace group asks" '"permissionDecision":"ask"' "$out"
out=$(push 'git -c core.x=1 push' "$qrepo"); expect "push: git -c k=v push asks" '"permissionDecision":"ask"' "$out"
out=$(push 'git --git-dir=.git --no-pager push' "$qrepo"); expect "push: --git-dir= is skipped" '"permissionDecision":"ask"' "$out"
# Without jq the guard cannot read the command: it asks instead of letting the push through.
out=$(printf '{"cwd":"%s","tool_input":{"command":"git push"}}' "$qrepo" | PATH="$tmp/nojq" bash "$here/push.sh" 2>&1; echo "rc=$?"); expect "push: without jq exits 0" "rc=0" "$out"
expect "push: without jq asks, since it cannot check" '"permissionDecision":"ask"' "$out"
expect "push: without jq says why" 'jq is missing' "$out"
if printf '%s' "${out%rc=0}" | jq -e '.hookSpecificOutput.hookEventName == "PreToolUse"' >/dev/null 2>&1; then ok "push: the no-jq answer is valid JSON"; else bad "push: the no-jq answer is not valid JSON: $out"; fi
# A guard that crashes, times out or cannot start blocks the push (Claude Code 2.1.295+).
expect "push entries block when the hook fails" "block block block" "$(jq -r '[.hooks.PreToolUse[].hooks[] | select(.command | test("push.sh")) | .onFailure // "unset"] | join(" ")' "$here/hooks.json")"


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
  expect "init skill: asks once what ships on a green suite" 'CLAUDE.md "Shipping": ask once, with AskUserQuestion' "$body"
  expect "init skill: a deploy branch Claude may push to gets no ask rule" "gets no rule" "$body"
  expect "init skill: times each check task" "run each task once and time it" "$body"
  expect "init skill: check:task runs what a change touches" "only what a change touches" "$body"
  expect "init skill: one Run part per UI surface" "one Run part per surface" "$body"
  expect "init skill: names the surfaces that cannot start here" "Not runnable here" "$body"
  tpl=$(cat "$here/../../../templates/project/CLAUDE.md" 2>&1)
  expect "project template: Shipping section" "## Shipping" "$tpl"
  expect "project template: a red suite asks" 'red, or not run: ask before any push' "$tpl"
  expect "project template: the end of a task ships by that section" "do the steps the Shipping section allows" "$tpl"
  expect "verify template: every surface the change reaches" "on every surface the change reaches" "$(cat "$here/../../../templates/project/verify-skill.md" 2>&1)"
  wire=$(grep -n 'Wire the check' "$sk" | head -1 | cut -d: -f1)
  full=$(grep -n 'Run check:full' "$sk" | head -1 | cut -d: -f1)
  if [ -n "$wire" ] && [ -n "$full" ] && [ "$wire" -lt "$full" ]; then ok "init skill: check:full after the stubs are wired"; else bad "init skill: order of wiring and check:full ($wire, $full)"; fi
  if command -v claude >/dev/null 2>&1; then
    if claude plugin validate "$here/.." --strict >"$tmp/validate.out" 2>&1; then ok "claude plugin validate --strict"; else bad "claude plugin validate: $(tail -3 "$tmp/validate.out")"; fi
  fi
else
  bad "init skill missing: $sk"
fi


# --- .mcp.json: the browser server starts from makeit's local copy, npx only as a fallback -------
# The entry file comes from the package's own "bin": upstream moved it between releases (1.13.0),
# and a fixed path fell back to npx without a word.
if command -v node >/dev/null 2>&1; then
  loader=$(jq -r '.mcpServers["chrome-devtools"].args[1]' "$here/../.mcp.json")
  mk="$tmp/mk"; pkgd="$mk/mcp/chrome-devtools/node_modules/chrome-devtools-mcp"; mkdir -p "$mk/bin"
  printf '#!/usr/bin/env bash\nprintf "%%s\\n" "$*" >"%s/npx.log"\n' "$mk" >"$mk/bin/npx"; chmod +x "$mk/bin/npx"
  fake_pkg() { # version, bin value as JSON, entry path relative to the package ("" for none)
    /bin/rm -rf "$pkgd" "$mk/ran" "$mk/npx.log"; mkdir -p "$pkgd"
    printf '{"name":"chrome-devtools-mcp","version":"%s","bin":%s}\n' "$1" "$2" >"$pkgd/package.json"
    if [ -n "$3" ]; then mkdir -p "$pkgd/$(dirname "$3")"; printf 'require("fs").writeFileSync(process.env.MARK, process.argv.slice(2).join(" "));\n' >"$pkgd/$3"; fi
  }
  run_loader() { MAKEIT_DIR="$mk" MARK="$mk/ran" PATH="$mk/bin:$PATH" node -e "$loader" chrome-devtools-mcp@9.9.9 --isolated >/dev/null 2>&1; }
  fake_pkg 9.9.9 '{"chrome-devtools-mcp":"./out/Default/gen/src/bin/chrome-devtools-mcp.js","chrome-devtools":"./out/x.js"}' out/Default/gen/src/bin/chrome-devtools-mcp.js
  run_loader; expect "mcp loader: runs the local copy at the path its package names" "--isolated" "$(cat "$mk/ran" 2>&1)"
  [ -e "$mk/npx.log" ] && bad "mcp loader: npx was called although the local copy is there" || ok "mcp loader: no npx when the local copy is there"
  fake_pkg 9.9.9 '{"chrome-devtools-mcp":"./build/src/bin/chrome-devtools-mcp.js"}' build/src/bin/chrome-devtools-mcp.js
  run_loader; expect "mcp loader: the older layout still loads" "--isolated" "$(cat "$mk/ran" 2>&1)"
  fake_pkg 9.9.9 '"./cli.js"' cli.js
  run_loader; expect "mcp loader: a bin given as one path loads" "--isolated" "$(cat "$mk/ran" 2>&1)"
  fake_pkg 9.9.8 '{"chrome-devtools-mcp":"./cli.js"}' cli.js
  run_loader; expect "mcp loader: another version goes to npx" "-y chrome-devtools-mcp@9.9.9 --isolated" "$(cat "$mk/npx.log" 2>&1)"
  [ -e "$mk/ran" ] && bad "mcp loader: ran a local copy of another version" || ok "mcp loader: a local copy of another version is not run"
  fake_pkg 9.9.9 '{"chrome-devtools-mcp":"./gone.js"}' ""
  run_loader; expect "mcp loader: a missing entry file goes to npx" "-y chrome-devtools-mcp@9.9.9 --isolated" "$(cat "$mk/npx.log" 2>&1)"
  /bin/rm -rf "$pkgd" "$mk/npx.log"
  run_loader; expect "mcp loader: no local copy goes to npx" "-y chrome-devtools-mcp@9.9.9 --isolated" "$(cat "$mk/npx.log" 2>&1)"
else
  echo "SKIP  mcp loader: node is not installed"; skipped="$skipped, mcp loader"
fi


# --- behind.sh: a session start says when the project's setup is behind -------------------------
bh="$tmp/behind"; mkdir -p "$bh/bin" "$bh/proj" "$bh/plain"
printf '{}\n' >"$bh/proj/.makeit.json"
# A fake makeit: logs its arguments, prints the JSON the test prepared.
printf '#!/usr/bin/env bash\nprintf "%%s\\n" "$*" >>"%s/calls.log"\ncat "%s/out.json"\n' "$bh" "$bh" >"$bh/bin/makeit"; chmod +x "$bh/bin/makeit"
: >"$bh/calls.log"
behind() { printf '{"hook_event_name":"SessionStart","source":"startup","cwd":"%s","session_id":"b"}' "$1" | PATH="$bh/bin:$PATH" bash "$here/behind.sh"; }
newday() { find "$tmp/state/makeit/behind" -type f -delete 2>/dev/null; }
printf '{"changes":[{"id":"deploy-ask-rules"},{"id":"verify-skill"},{"id":"task-start-end"}],"initTarget":"0.4.0"}' >"$bh/out.json"
out=$(behind "$bh/proj")
expect "behind: says how many changes wait" '3 changes behind (deploy-ask-rules, verify-skill, task-start-end)' "$(printf '%s' "$out" | jq -r '.systemMessage' 2>&1)"
expect "behind: names what to type" '/makeit:init' "$out"
expect "behind: asks makeit for a dry run" 'init --dry-run --json' "$(cat "$bh/calls.log")"
expect_empty "behind: once a day per project" "$(behind "$bh/proj")"
: >"$bh/calls.log"
expect_empty "behind: silent in a folder makeit did not set up" "$(behind "$bh/plain")"
expect_empty "behind: no makeit call without a record" "$(cat "$bh/calls.log")"
newday; printf '{"changes":[{"id":"shipping-section"}]}' >"$bh/out.json"
expect "behind: one change reads 1 change" '1 change behind (shipping-section)' "$(behind "$bh/proj" | jq -r '.systemMessage' 2>&1)"
newday; printf '{"changes":[]}' >"$bh/out.json"
expect_empty "behind: up to date is silent" "$(behind "$bh/proj")"
newday; printf 'garbage' >"$bh/out.json"
out=$(behind "$bh/proj" 2>&1; echo "rc=$?")
if [ "$out" = "rc=0" ]; then ok "behind: garbage from makeit exits 0, silent"; else bad "behind: garbage from makeit: [$out]"; fi
printf '{"changes":[{"id":"x"}]}' >"$bh/out.json"
expect "behind: a check that failed does not use up the day" '1 change behind (x)' "$(behind "$bh/proj" | jq -r '.systemMessage' 2>&1)"
newday; out=$(printf '{"hook_event_name":"SessionStart","source":"startup","cwd":"%s"}' "$bh/proj" | PATH="$tmp/nojq" bash "$here/behind.sh" 2>&1; echo "rc=$?")
if [ "$out" = "rc=0" ]; then ok "behind: without jq or makeit exits 0, silent"; else bad "behind: without jq or makeit: [$out]"; fi
newday; printf '{"changes":[{"id":"x"}]}' >"$bh/out.json"
expect_empty "behind: ignores other events" "$(printf '{"hook_event_name":"Stop","cwd":"%s"}' "$bh/proj" | PATH="$bh/bin:$PATH" bash "$here/behind.sh")"
expect "behind runs at session start" "behind.sh" "$(jq -r '.hooks.SessionStart[] | select(.matcher == "startup") | .hooks[].command' "$here/hooks.json")"


# --- deja-vu plugin: hint.sh and the generated hook events --------------------------------------
dv="$here/../../deja-vu"
hint_events=$(jq -r '[.hooks // {} | keys[]] | sort | join(",")' "$dv/.claude-plugin/plugin.json" "$dv/hooks/hooks.json" 2>&1 | paste -sd' ' -)
expect "deja-vu hook events: SessionStart, PreCompact, SessionEnd in the manifest, UserPromptSubmit in hooks.json" "PreCompact,SessionEnd,SessionStart UserPromptSubmit" "$hint_events"
expect "deja-vu UserPromptSubmit runs hint.sh" "hooks/hint.sh" "$(jq -r '.hooks.UserPromptSubmit[].hooks[].command' "$dv/hooks/hooks.json" 2>&1)"
expect_empty "deja-vu: no per-prompt or per-tool recall hook left" "$(grep -rlE 'hook-(prompt|tool|tool-after)' "$dv/.claude-plugin" "$dv/hooks/hooks.json" 2>/dev/null)"
mkdir -p "$tmp/hintbin"; for b in sh cat tr sed wc printf; do p=$(command -v $b) && ln -sf "$p" "$tmp/hintbin/$b"; done
hint_case() { # name expect(hint|silent) input
  for mode in jq nojq; do
    if [ $mode = jq ]; then out=$(printf '%s' "$3" | sh "$dv/hooks/hint.sh" 2>&1; echo "rc=$?"); else out=$(printf '%s' "$3" | PATH="$tmp/hintbin" sh "$dv/hooks/hint.sh" 2>&1; echo "rc=$?"); fi
    case "$out" in *rc=0) ;; *) bad "deja hint $mode $1: exit [$out]"; continue ;; esac
    out=${out%rc=0}; got=silent; [ -n "$out" ] && got=hint
    [ "$got" = hint ] && ! printf '%s' "$out" | jq -e '.hookSpecificOutput.hookEventName == "UserPromptSubmit"' >/dev/null 2>&1 && got=badjson
    [ "$got" = "$2" ] && ok "deja hint $mode $1: $got" || bad "deja hint $mode $1: expected $2, got $got"
  done
}
hint_case digit silent '{"session_id":"x","prompt":"2"}'
hint_case letter silent '{"prompt":"a","cwd":"/x"}'
hint_case three-words silent '{"prompt":"go do it"}'
hint_case empty-prompt silent '{"prompt":""}'
hint_case task hint '{"session_id":"x","prompt":"Fix the DNS record now please","cwd":"/x"}'
hint_case escaped-quotes hint '{"prompt":"Set \"app\" back to the new IP now","cwd":"/x"}'
hint_case multiline hint '{"prompt":"Deploy the\nParcelo integration today please"}'
hint_case garbage hint 'not json at all'
hint_case task-notice silent '{"prompt":"<task-notification>\n<task-id>x</task-id> the job finished fine"}'
hint_case peer-message silent '{"prompt":"Another Claude session sent a message:\n<agent-message from=\"a\">four more words here</agent-message>"}'
hint_case quoted-notice hint '{"prompt":"why did <task-notification> show up twice"}'
# --- bin/shots: every page at two widths, with its console errors, in one command ----------------
# Runs against the chrome-devtools-mcp copy makeit installed, so an upstream change shows here.
shots="$here/../bin/shots"
cdt_pkg="${MAKEIT_DIR:-$HOME/.makeit}/mcp/chrome-devtools/node_modules/chrome-devtools-mcp/package.json"
# Only a browser the command line can drive: it looks for Google Chrome, not Chromium.
has_chrome() {
  local b
  for b in google-chrome google-chrome-stable; do
    command -v "$b" >/dev/null 2>&1 && return 0
  done
  [ -d "/Applications/Google Chrome.app" ]
}
mkdir -p "$tmp/chromium-only"; printf '#!/bin/sh\n' >"$tmp/chromium-only/chromium"; chmod +x "$tmp/chromium-only/chromium"
if PATH="$tmp/chromium-only" has_chrome; then bad "shots: Chromium alone counts as a browser for the live cases"; else ok "shots: Chromium alone does not count as a browser for the live cases"; fi
out=$(bash "$shots" 2>&1); rc=$?
[ "$rc" = 2 ] && ok "shots: no URL is a usage error" || bad "shots: no URL: exit $rc: $out"
expect "shots: usage names the command" "shots [--out DIR]" "$out"
for w in , 0x0 375x0 0x812 x800; do
  out=$(MAKEIT_DIR="$tmp/no-makeit" bash "$shots" --widths "$w" "file://$here/testdata/shots/page.html" 2>&1); rc=$?
  [ "$rc" = 2 ] && ok "shots: --widths '$w' is a usage error" || bad "shots: --widths '$w': exit $rc: $out"
done
out=$(MAKEIT_DIR="$tmp/no-makeit" bash "$shots" "file://$here/testdata/shots/page.html" 2>&1); rc=$?
[ "$rc" = 1 ] && ok "shots: without makeit's browser copy it exits 1" || bad "shots: no local copy: exit $rc: $out"
expect "shots: without the copy it names makeit setup" "makeit setup" "$out"
if command -v node >/dev/null 2>&1 && [ -f "$cdt_pkg" ] && has_chrome; then
  sd="$tmp/shots"; mkdir -p "$sd"
  page="file://$here/testdata/shots/page.html"
  runtime="${XDG_RUNTIME_DIR:-/tmp}"
  daemons() { find "$runtime" -maxdepth 1 -name 'chrome-devtools-mcp-*' 2>/dev/null | wc -l; }
  watchers() { pgrep -f 'shots watcher' | wc -l; }
  before=$(daemons)
  before_list=$(find "$runtime" -maxdepth 1 -name 'chrome-devtools-mcp-*' 2>/dev/null)
  wbefore=$(watchers)
  out=$(bash "$shots" --out "$sd/a" "$page" 2>&1); rc=$?
  [ "$rc" = 0 ] && ok "shots: exits 0 when every shot is saved" || bad "shots: exit $rc: $out"
  expect "shots: the folder comes first" "out: $sd/a" "$(head -n 1 <<<"$out")"
  if [ -s "$sd/a/01-page-html-1280x800.png" ] && [ -s "$sd/a/01-page-html-375x812.png" ]; then ok "shots: one file per width, named after the page"; else bad "shots: files: $(ls "$sd/a" 2>&1)"; fi
  expect "shots: the phone shot is flagged wider than its viewport" "01-page-html 375x812: 01-page-html-375x812.png · wider than the viewport: 700 px" "$out"
  case "$(grep '01-page-html 1280x800:' <<<"$out")" in *wider*) bad "shots: the desktop shot is flagged wider: $out" ;; *1280x800.png) ok "shots: the desktop shot is not flagged" ;; *) bad "shots: no line for the desktop shot: $out" ;; esac
  expect "shots: console errors are counted per page, the same text once" "01-page-html console errors: 1" "$out"
  expect "shots: the error's text is shown" "shots test error" "$out"
  [ "$(daemons)" = "$before" ] && ok "shots: nothing of the run is left behind" || bad "shots: runtime folders before $before, after $(daemons)"
  [ "$(watchers)" = "$wbefore" ] && ok "shots: no watcher is left after a run" || bad "shots: watchers before $wbefore, after $(watchers)"
  # A page that picks its layout at load is loaded again at each width.
  out=$(bash "$shots" --out "$sd/l" "file://$here/testdata/shots/layout.html" 2>&1)
  expect "shots: each width loads the page anew, so the phone layout is the one shot at 375" "01-layout-html 375x812: 01-layout-html-375x812.png · wider than the viewport: 700 px" "$out"
  expect "shots: the console errors are those of every load" "01-layout-html console errors: 2" "$out"
  expect "shots: the errors of the desktop load are listed" "loaded at 1280" "$out"
  expect "shots: the errors of the phone load are listed" "loaded at 375" "$out"
  out=$(bash "$shots" --out "$sd/b" --widths 800x600 "file:///nonexistent/missing.html" "$page" 2>&1); rc=$?
  [ "$rc" = 1 ] && ok "shots: a page that does not load makes the run exit 1" || bad "shots: missing page: exit $rc: $out"
  expect "shots: the page that did not load is named" "01-missing-html: did not load (file:///nonexistent/missing.html)" "$out"
  [ -s "$sd/b/02-page-html-800x600.png" ] && ok "shots: the next page is still taken, at the width asked" || bad "shots: after a missing page: $(ls "$sd/b" 2>&1)"
  # A stand-in command line: the real one, except the command named in SHOTS_TEST_BREAK fails.
  fk="$tmp/fakemk/mcp/chrome-devtools/node_modules/chrome-devtools-mcp"; mkdir -p "$fk"
  printf '{"version":"0.0.0-test","bin":{"chrome-devtools":"./cli.js"}}\n' >"$fk/package.json"
  printf '%s\n' "const {spawnSync} = require('child_process');" \
    "if (process.argv[2] === process.env.SHOTS_TEST_BREAK) { console.error('Error: unknown command ' + process.argv[2]); process.exit(1); }" \
    "const r = spawnSync(process.execPath, [process.env.SHOTS_TEST_CLI, ...process.argv.slice(2)], {stdio: 'inherit'});" \
    "process.exit(r.status === null ? 1 : r.status);" >"$fk/cli.js"
  real_cli="$(dirname "$cdt_pkg")/$(jq -r '.bin["chrome-devtools"]' "$cdt_pkg")"
  broken() { SHOTS_TEST_BREAK=$1 SHOTS_TEST_CLI="$real_cli" MAKEIT_DIR="$tmp/fakemk" bash "$shots" --out "$sd/break-$1" --widths 800x600 "$page" 2>&1; }
  out=$(broken emulate); rc=$?
  [ "$rc" = 1 ] && ok "shots: a viewport that could not be set makes the run exit 1" || bad "shots: emulate broken: exit $rc: $out"
  expect "shots: a viewport that could not be set is named" "01-page-html 800x600: the viewport could not be set" "$out"
  out=$(broken list_console_messages); rc=$?
  [ "$rc" = 1 ] && ok "shots: console errors that could not be read make the run exit 1" || bad "shots: console broken: exit $rc: $out"
  expect "shots: console errors that could not be read are named" "01-page-html console errors: could not be read" "$out"
  out=$(broken take_screenshot); rc=$?
  [ "$rc" = 1 ] && ok "shots: a screenshot that failed makes the run exit 1" || bad "shots: screenshot broken: exit $rc: $out"
  expect "shots: a screenshot that failed is named" "01-page-html 800x600: no screenshot was saved" "$out"
  out=$(broken navigate_page); rc=$?
  [ "$rc" = 1 ] && ok "shots: a load command that fails makes the run exit 1" || bad "shots: navigate broken: exit $rc: $out"
  expect "shots: a load command that fails gives its error as the reason" "01-page-html: did not load ($page): Error: unknown command navigate_page" "$out"
  out=$(broken list_pages); rc=$?
  [ "$rc" = 1 ] && ok "shots: a browser that does not start makes the run exit 1" || bad "shots: list_pages broken: exit $rc: $out"
  expect "shots: a browser that does not start is one line with the error" "shots: the browser did not start: Error: unknown command list_pages" "$out"
  case "$out" in *"did not load"*) bad "shots: a browser that does not start still tried the pages: $out" ;; *) ok "shots: a browser that does not start tries no page" ;; esac
  # A page behind a login: the setup file signs in, the page shots opens next is the signed-in one.
  node "$here/testdata/shots/server.mjs" "$sd/server.log" >"$sd/port" 2>/dev/null &
  srv=$!
  for _ in $(seq 50); do [ -s "$sd/port" ] && break; sleep 0.1; done
  port=$(cat "$sd/port")
  out=$(bash "$shots" --out "$sd/n" "http://127.0.0.1:$port/private" 2>&1)
  expect "shots: without a setup file the page behind the login is refused" "private refused" "$(tail -n 1 "$sd/server.log" 2>&1)"
  expect "shots: without a setup file the line says it landed on the login page" "01-private 1280x800: 01-private-1280x800.png · landed on http://127.0.0.1:$port/login" "$out"
  out=$(SHOTS_TEST_PORT="$port" bash "$shots" --out "$sd/c" --setup "$here/testdata/shots/login.sh" "http://127.0.0.1:$port/private" 2>&1); rc=$?
  if [ "$rc" = 0 ] && [ "$(tail -n 1 "$sd/server.log" 2>/dev/null)" = "private ok" ] && [ -s "$sd/c/01-private-1280x800.png" ]; then ok "shots: the setup file signs in and the page behind the login is taken"; else bad "shots: setup: exit $rc, server log [$(tr '\n' ' ' <"$sd/server.log" 2>&1)]: $out"; fi
  case "$out" in *"landed on"*) bad "shots: the signed-in page says it landed elsewhere: $out" ;; *) ok "shots: a page at the address asked has no landing note" ;; esac
  # A page that rendered while one of its requests never answers.
  out=$(bash "$shots" --out "$sd/s" --widths 800x600 "http://127.0.0.1:$port/slow" 2>&1); rc=$?
  [ "$rc" = 0 ] && ok "shots: a page still loading after the timeout is not a failure" || bad "shots: slow page: exit $rc: $out"
  expect "shots: a page still loading after the timeout is taken and says so" "01-slow 800x600: 01-slow-800x600.png · still loading after the timeout" "$out"
  # A run ended by a signal, or killed outright, leaves no daemon, no runtime folder, no Chrome.
  new_runtime() {
    local d
    for d in "$runtime"/chrome-devtools-mcp-*; do
      case "$before_list" in *"$d"*) ;; *) [ -s "$d/daemon.pid" ] && { echo "$d"; return; } ;; esac
    done
  }
  # HUP goes to the whole process group, as a closed terminal sends it; KILL to the run alone.
  signal_case() { # SIGNAL
    local pid d="" dpid="" kids="" k alive target
    ${setsid[@]+"${setsid[@]}"} bash "$shots" --out "$sd/sig-$1" --widths 800x600 "http://127.0.0.1:$port/never" >"$sd/sig-$1.log" 2>&1 &
    pid=$!
    for _ in $(seq 200); do
      d=$(new_runtime)
      [ -n "$d" ] && dpid=$(cat "$d/daemon.pid" 2>/dev/null) && kids=$(pgrep -P "$dpid" | tr '\n' ' ') && [ -n "$kids" ] && break
      sleep 0.1
    done
    sleep 1
    target=$pid; [ "$1" = HUP ] && [ -n "${setsid[*]-}" ] && target=-$pid
    kill -"$1" -- "$target"
    for _ in $(seq 150); do
      alive=""
      for k in $dpid $kids; do kill -0 "$k" 2>/dev/null && alive="$alive $k"; done
      [ -z "$alive" ] && [ "$(daemons)" = "$before" ] && [ "$(watchers)" = "$wbefore" ] && break
      sleep 0.1
    done
    wait "$pid" 2>/dev/null
    if [ -n "$kids" ] && [ -z "$alive" ] && [ "$(daemons)" = "$before" ] && [ "$(watchers)" = "$wbefore" ]; then
      ok "shots: kill -$1 of a run leaves no daemon, Chrome, runtime folder or watcher within 15 s"
    else
      bad "shots: kill -$1: Chrome seen [$kids], still alive [$alive], runtime folders before $before, after $(daemons), watchers before $wbefore, after $(watchers): $(cat "$sd/sig-$1.log")"
    fi
  }
  setsid=(); command -v setsid >/dev/null 2>&1 && setsid=(setsid)
  signal_case HUP
  signal_case KILL
  kill "$srv" 2>/dev/null
  printf 'false\n' >"$sd/bad-setup.sh"
  out=$(bash "$shots" --out "$sd/d" --setup "$sd/bad-setup.sh" "$page" 2>&1); rc=$?
  if [ "$rc" = 1 ] && ! ls "$sd/d"/*.png >/dev/null 2>&1; then ok "shots: a failing setup stops the run before any screenshot"; else bad "shots: failing setup: exit $rc: $out"; fi
  expect "shots: a failing setup is named" "setup failed: $sd/bad-setup.sh" "$out"
  printf 'true\nfalse\ntrue\n' >"$sd/mid-setup.sh"
  out=$(bash "$shots" --out "$sd/m" --setup "$sd/mid-setup.sh" "$page" 2>&1); rc=$?
  if [ "$rc" = 1 ] && ! ls "$sd/m"/*.png >/dev/null 2>&1; then ok "shots: a setup whose middle command fails stops the run before any screenshot"; else bad "shots: setup failing in the middle: exit $rc: $out"; fi
  # A bare name is the file in the current folder, not one of that name on PATH.
  mkdir -p "$sd/cwd" "$sd/pathdir"
  printf 'echo cwd >"%s/picked"\n' "$sd" >"$sd/cwd/pick.sh"
  printf 'echo path >"%s/picked"\n' "$sd" >"$sd/pathdir/pick.sh"
  (cd "$sd/cwd" && PATH="$sd/pathdir:$PATH" bash "$shots" --out "$sd/e" --setup pick.sh "$page" >/dev/null 2>&1)
  expect "shots: a bare setup name is the file in the current folder" "cwd" "$(cat "$sd/picked" 2>&1)"
  [ "$(daemons)" = "$before" ] && ok "shots: nothing is left behind after failed runs" || bad "shots: runtime folders before $before, after $(daemons)"
else
  echo "SKIP  shots: needs node, Google Chrome and makeit's local chrome-devtools-mcp"
  skipped="$skipped, shots"
fi

# The last line names what did not run, so a green run without the live cases reads as one.
summary() { [ -n "$skipped" ] && echo "HOOKS OK (skipped: ${skipped#, })" || echo "HOOKS OK"; }
expect "suite: the last line names what was skipped" "HOOKS OK (skipped: shots, mcp loader)" "$(skipped=", shots, mcp loader"; summary 2>&1)"
[ "$(skipped=""; summary 2>&1)" = "HOOKS OK" ] && ok "suite: the last line is plain when nothing was skipped" || bad "suite: nothing skipped: [$(skipped=""; summary 2>&1)]"

[ "$fail" = 0 ] && summary || { echo "HOOKS FAILURES"; exit 1; }

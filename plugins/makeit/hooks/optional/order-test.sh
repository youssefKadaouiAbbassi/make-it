#!/usr/bin/env bash
# Tests for order-note.sh + order-ask.sh with fake payloads. Called from ../test.sh.
set -uo pipefail
here=$(cd "$(dirname "$0")" && pwd)
fail=0
ok() { echo "ok   $1"; }
bad() { echo "FAIL $1"; fail=1; }
expect() { case "$3" in *"$2"*) ok "$1" ;; *) bad "$1: expected [$2] in [$3]" ;; esac; }
expect_empty() { [ -z "$2" ] && ok "$1" || bad "$1: expected no output, got [$2]"; }

tmp=$(mktemp -d "${TMPDIR:-/tmp}/order-test.XXXXXX")
trap '/bin/rm -rf "$tmp"' EXIT
repo="$tmp/repo"; mkdir -p "$repo"
export MAKEIT_STATE="$tmp/state" HOME="$tmp/home" CLAUDE_PROJECT_DIR="$repo"
mkdir -p "$HOME"

note() { printf '{"hook_event_name":"UserPromptSubmit","session_id":"%s","cwd":"%s","prompt":%s}' "$1" "$repo" "$(printf '%s' "$2" | jq -Rs .)" | bash "$here/order-note.sh"; }
ask() { printf '{"hook_event_name":"PreToolUse","session_id":"%s","tool_name":"Bash","cwd":"%s","tool_input":{"command":%s}}' "$1" "$repo" "$(printf '%s' "$2" | jq -Rs .)" | bash "$here/order-ask.sh"; }

expect_empty "order-note prints nothing" "$(note s1 'please push this to main now')"
[ -f "$tmp/state/s1" ] && ok "order-note stored the prompt" || bad "order-note did not store"
expect "order-note file is private" "600" "$(stat -c %a "$tmp/state/s1")"

expect_empty "ordered push to main runs" "$(ask s1 'git push origin main')"
expect "ordered push does not clear a force push" '"ask"' "$(ask s1 'git push --force origin feature')"
note s1 'force push the branch'
expect_empty "ordered force push runs" "$(ask s1 'git push -f origin feature')"
expect "force order does not clear reset --hard" '"ask"' "$(ask s1 'git reset --hard HEAD~3')"
note s1 'discard my local changes'
expect_empty "ordered reset --hard runs" "$(ask s1 'git reset --hard')"
note s1 'merge the PR'
expect_empty "ordered merge runs" "$(ask s1 'gh pr merge 12 --squash')"
expect "merge order does not clear rm -rf outside the project" '"ask"' "$(ask s1 'rm -rf /opt/data')"
note s1 'delete /opt/data'
expect_empty "ordered rm -rf runs" "$(ask s1 'rm -rf /opt/data')"
expect_empty "routine command stays quiet" "$(ask s1 'npm test')"
expect "unknown session asks" "not named in your last message" "$(ask s9 'git push origin main')"
expect "missing session id asks" '"ask"' "$(printf '{"hook_event_name":"PreToolUse","tool_name":"Bash","cwd":"%s","tool_input":{"command":"git push origin main"}}' "$repo" | bash "$here/order-ask.sh")"
note s1 'PUSH TO MAIN'
expect_empty "order match is case-insensitive" "$(ask s1 'git push origin main')"
note s1 'make the tests pass'
expect "later unrelated prompt re-arms the ask" '"ask"' "$(ask s1 'git push origin main')"
printf '{"hook_event_name":"UserPromptSubmit","session_id":"../../x","cwd":"%s","prompt":"push to main"}' "$repo" | bash "$here/order-note.sh"
[ ! -e "$tmp/x" ] && ok "session id is sanitised" || bad "path traversal in session id"

[ "$fail" = 0 ] && echo "ORDER OK" || { echo "ORDER FAILED"; exit 1; }

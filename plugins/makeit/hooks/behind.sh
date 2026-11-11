#!/usr/bin/env bash
# behind.sh: SessionStart (startup). When makeit set this project up (.makeit.json in the session's
# folder) and newer init changes wait for it, say so in one line, once a day per project. The line
# is for the person, who types /makeit:init; nothing is added to the model's context.
# `makeit init --dry-run --json` does the comparing: it writes nothing and takes no lock.
# Exits 0 on every path it does not understand. Needs jq and makeit; without them it does nothing.
set -u
input=$(cat)
command -v jq >/dev/null 2>&1 || exit 0
command -v makeit >/dev/null 2>&1 || exit 0
[ "$(jq -r '.hook_event_name // empty' <<<"$input" 2>/dev/null)" = "SessionStart" ] || exit 0
cwd=$(jq -r '.cwd // "."' <<<"$input" 2>/dev/null)
[ -f "$cwd/.makeit.json" ] || exit 0
seen="${XDG_STATE_HOME:-$HOME/.local/state}/makeit/behind"
mark="$seen/$(printf '%s' "$cwd" | cksum | tr -c '0-9' '_')"
today=$(date +%F)
[ "$(cat "$mark" 2>/dev/null)" = "$today" ] && exit 0
mkdir -p "$seen" 2>/dev/null && printf '%s\n' "$today" >"$mark" 2>/dev/null
find "$seen" -type f -mtime +7 -delete 2>/dev/null
ids=$(cd "$cwd" 2>/dev/null && makeit init --dry-run --json 2>/dev/null | jq -r '[.changes[]?.id] | join(", ")' 2>/dev/null)
[ -n "$ids" ] || exit 0
n=$(awk -F', ' '{print NF}' <<<"$ids")
word=changes; [ "$n" = 1 ] && word=change
jq -cn --arg m "makeit: this project's setup is $n $word behind ($ids). Type /makeit:init to bring it up to date." '{systemMessage: $m}'
exit 0

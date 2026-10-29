#!/usr/bin/env bash
# variant-ask.sh (optional): PreToolUse on Bash. Returns permissionDecision "ask" for the
# irreversible core in spellings the literal ask rules miss. It only ever returns "ask" or
# nothing, so all it can do is turn a bypass of the ask list into a prompt. Best effort:
# interpreters, aliases and commands inside scripts are not seen.
# Wire it by hand in ~/.claude/settings.json:
#   "hooks": {"PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command",
#      "command": "~/.claude/hooks/variant-ask.sh", "timeout": 5}]}]}
set -u
input=$(cat)
command -v jq >/dev/null 2>&1 || exit 0
[ "$(jq -r '.tool_name // empty' <<<"$input")" = "Bash" ] || exit 0
cmd=$(jq -r '.tool_input.command // empty' <<<"$input")
[ -n "$cmd" ] || exit 0
# Normalise: strip env prefixes, full program paths and wrappers so the core verbs are visible.
norm=$(printf '%s' "$cmd" | sed -E \
  -e 's#(^|[;&| ])(env +)?([A-Za-z_][A-Za-z0-9_]*=[^ ]* +)+#\1#g' \
  -e 's#(^|[;&| ])(ba|z|da)?sh +-l?c +["'"'"']?#\1#g' \
  -e 's#(/usr/local/bin/|/usr/bin/|/bin/|/opt/homebrew/bin/)(git|gh|rm|npm|cargo|uv|twine|eas)#\2#g' \
  -e 's#["'"'"']+$##')
reason=""
if printf '%s' "$norm" | grep -Eq '(^|[;&| ])git( +-C +[^ ]+)?( +[^ ]+)* +push\b'; then
  push=$(printf '%s' "$norm" | grep -Eo 'git( +-C +[^ ]+)?( +[^ ]+)* +push\b.*' | head -1)
  if printf '%s' "$push" | grep -Eq '(^|[[:space:]:])(main|master)([[:space:]]|$)|(^|[[:space:]])(-f|--force|--force-with-lease|--force-if-includes)([[:space:]=]|$)|[[:space:]]\+[^ ]+'; then
    reason="push to the default branch or a force push, in a spelling the ask rules miss"
  fi
fi
if [ -z "$reason" ] && printf '%s' "$norm" | grep -Eq '(^|[;&| ])git( +-C +[^ ]+)?( +[^ ]+)* +reset +--hard\b'; then
  reason="git reset --hard in a spelling the ask rules miss"
fi
if [ -z "$reason" ] && printf '%s' "$norm" | grep -Eq '(^|[;&| ])gh +(pr +merge|stack +merge|api +[^;&|]*(merge|-X +DELETE|--method +DELETE|DELETE))'; then
  reason="merge or DELETE through gh in a spelling the ask rules miss"
fi
if [ -z "$reason" ] && printf '%s' "$norm" | grep -Eq '(^|[;&| ])rm +(-[a-zA-Z]*r[a-zA-Z]*f[a-zA-Z]*|-[a-zA-Z]*f[a-zA-Z]*r[a-zA-Z]*|--recursive +--force|-r +-f|-f +-r)\b'; then
  target=$(printf '%s' "$norm" | grep -Eo 'rm +(-[a-zA-Z]+ +|--[a-z]+ +)+[^ ;&|]+' | head -1 | awk '{print $NF}')
  home=${HOME:-/nonexistent}; proj=${CLAUDE_PROJECT_DIR:-$(jq -r '.cwd // "."' <<<"$input")}
  case "$target" in
    '~'*|"$home"*|/|/*) resolved=$(printf '%s' "$target" | sed -e "s#^~#$home#") ;;
    *) resolved="$proj/$target" ;;
  esac
  resolved=$(realpath -m "$resolved" 2>/dev/null || printf '%s' "$resolved")
  projr=$(realpath -m "$proj" 2>/dev/null || printf '%s' "$proj")
  case "$resolved" in
    "$projr"/*) ;;
    *) reason="rm -rf whose target ($resolved) is the home directory, the root or outside the project" ;;
  esac
fi
[ -n "$reason" ] || exit 0
printf '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"ask","permissionDecisionReason":%s}}\n' "$(printf '%s' "$reason" | jq -Rs .)"
exit 0

#!/bin/sh
# UserPromptSubmit: one search hint, no recall content, never calls deja.
# Skips prompts under 4 words (covers digit/letter-only replies) and background notices that
# arrive as prompts (a finished job, a message from another session or a subagent).
input=$(cat)
if command -v jq >/dev/null 2>&1; then
  parsed=$(printf '%s' "$input" | jq -r '"P:" + (.prompt // "")' 2>/dev/null)
else
  # shortcut: no jq, take the raw "prompt" string (escapes left in); word count stays close enough.
  parsed=$(printf '%s' "$input" | tr '\n' ' ' | sed -n 's/.*"prompt"[[:space:]]*:[[:space:]]*"\(\([^"\\]\|\\.\)*\)".*/P:\1/p')
fi
# Unparsable input (no P: marker): emit the hint rather than stay silent.
case "$parsed" in
  'P:<task-notification>'* | 'P:Another Claude session sent a message:'*) exit 0 ;;
  P:*) [ "$(printf '%s' "${parsed#P:}" | wc -w)" -lt 4 ] && exit 0 ;;
esac
printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"UserPromptSubmit","additionalContext":"If this touches something that may have been decided, fixed, tried or set up before (a value, a rollback, a credential'"'"'s location, an error), search deja with its specific names before acting."}}'

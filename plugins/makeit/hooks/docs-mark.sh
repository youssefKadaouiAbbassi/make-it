#!/usr/bin/env bash
# docs-mark.sh, PostToolUse (WebFetch|WebSearch): logs the tool input so docs-nudge.sh knows which
# libraries this session already looked up.
set -u
command -v jq >/dev/null 2>&1 || exit 0
input=$(cat)
tool=$(jq -r '.tool_name // empty' <<<"$input" 2>/dev/null)
case "$tool" in WebFetch | WebSearch) ;; *) exit 0 ;; esac
sid=$(jq -r '.session_id // empty' <<<"$input" | tr -cd 'A-Za-z0-9_-')
state="${TMPDIR:-/tmp}/makeit-docs/${sid:-nosession}"
mkdir -p "$state" 2>/dev/null && jq -c '.tool_input' <<<"$input" >>"$state/lookups.txt" 2>/dev/null
exit 0

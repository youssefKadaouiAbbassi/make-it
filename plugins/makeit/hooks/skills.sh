#!/usr/bin/env bash
# skills.sh: UserPromptSubmit. Superpowers injects its skill rules once, at session start, and on
# later prompts (features, continuations) the model tends to go straight to tools and skip them.
# This puts a one-line reminder next to every prompt: check the skills, with the usual skill per
# kind of work as examples. It does not re-invoke using-superpowers, whose text is already in
# context.
# One-token prompts get no reminder, and neither do background notices that arrive as prompts
# (a finished job, a message from another session or a subagent).
# Exits 0 on every path it does not understand. Needs jq; without it it does nothing.
set -u
input=$(cat)
command -v jq >/dev/null 2>&1 || exit 0
# One jq call: the event on the first line, the prompt after it.
{ read -r ev || true; prompt=$(cat); } < <(jq -r '.hook_event_name // "", .prompt // ""' <<<"$input")
[ "$ev" = "UserPromptSubmit" ] || exit 0
# Not typed by the person: Claude Code starts these prompts with a fixed marker.
case "$prompt" in '<task-notification>'* | 'Another Claude session sent a message:'*) exit 0 ;; esac
# A one-token prompt ("1", "a", "done") is a reply, not new work.
set -f
# shellcheck disable=SC2086
set -- $prompt
[ $# -ge 2 ] || exit 0
note='Before your first tool call for a task, including a follow-up that starts new work ("go for it", "fix it") and including a deja recall or any other lookup, check whether a superpowers skill fits (the using-superpowers rules from session start apply) and invoke it with the Skill tool. Usual matches, as examples:
- bug, failing test or unexpected behaviour: superpowers:systematic-debugging
- new feature, new command or behaviour change: superpowers:brainstorming
- continuing planned work (HANDOFF.md, a plan or a task list): superpowers:executing-plans
- writing implementation code: superpowers:test-driven-development
- about to say done, fixed or passing: superpowers:verification-before-completion
Questions and conversation need no skill.'
printf '{"hookSpecificOutput":{"hookEventName":"UserPromptSubmit","additionalContext":%s}}\n' "$(jq -Rs . <<<"$note")"
exit 0

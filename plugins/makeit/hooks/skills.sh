#!/usr/bin/env bash
# skills.sh: UserPromptSubmit. Superpowers injects its skill rules once, at session start; in a
# repo with context, on features and continuations, Opus 5.5 goes straight to tools and skips them
# (measured 2026-10-27, private eval harness). This puts a one-line reminder next to every
# prompt: check the skills, with the usual skill per kind of work as examples. It does not
# re-invoke using-superpowers, whose text is already in context from session start.
# Exits 0 on every path it does not understand. Needs jq; without it it does nothing.
set -u
input=$(cat)
command -v jq >/dev/null 2>&1 || exit 0
[ "$(jq -r '.hook_event_name // empty' <<<"$input")" = "UserPromptSubmit" ] || exit 0
note='Before your first tool call for a task, including a follow-up that starts new work ("go for it", "fix it") and including a deja recall or any other lookup, check whether a superpowers skill fits (the using-superpowers rules from session start apply) and invoke it with the Skill tool. Usual matches, as examples:
- bug, failing test or unexpected behaviour: superpowers:systematic-debugging
- new feature, new command or behaviour change: superpowers:brainstorming
- continuing planned work (HANDOFF.md, a plan or a task list): superpowers:executing-plans
- writing implementation code: superpowers:test-driven-development
- about to say done, fixed or passing: superpowers:verification-before-completion
Questions and conversation need no skill.'
printf '{"hookSpecificOutput":{"hookEventName":"UserPromptSubmit","additionalContext":%s}}\n' "$(jq -Rs . <<<"$note")"
exit 0

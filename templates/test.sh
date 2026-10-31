#!/usr/bin/env bash
# Tests for the templates. Static checks run on the host (no network, nothing installed).
# FLOOR=1 runs the floor-guard contract against our CONSTRAINTS.md in Docker (node + git).
# LIVE=1 runs one `claude -p` load test in the eval image with the subscription token file.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
fail=0
check() { if "$@"; then echo "ok   $*"; else echo "FAIL $*"; fail=1; fi; }
lines_under() { [ "$(wc -l <"$1")" -lt "$2" ]; }
has() { grep -qF -- "$2" "$1"; }
frontmatter_paths() { head -1 "$1" | grep -qx -- '---' && sed -n '2,8p' "$1" | grep -q '^paths:'; }

g=$here/global/CLAUDE.md
p=$here/project/CLAUDE.md
c=$here/project/CONSTRAINTS.md
h=$here/project/HANDOFF.md

check lines_under "$g" 200
check has "$g" 'Read `CONSTRAINTS.md` before writing code'
check has "$g" 'No "done", "fixed" or "passing" without the command run this turn'
check has "$g" 'HANDOFF.md'
check has "$g" 'AskUserQuestion tool'
for r in "$here"/global/rules/*.md; do check frontmatter_paths "$r"; done
check lines_under "$p" 80
check has "$p" '**Product priority:**'
check has "$p" 'Read `CONSTRAINTS.md` before writing code. Do not weaken it to make a change pass.'
for n in 1 2 3 4 5 6; do check grep -q "^$n\. " "$p"; done
check has "$p" 'check:fast'
check has "$p" 'check:task'
check has "$p" 'check:full'
check has "$c" '## Floor'
check has "$c" '## Exceptions'
check has "$c" 'betterleaks:allow'
check has "$c" 'Changed lines >= 80% covered'
check lines_under "$h" 30
for k in Goal Done 'Next step' 'Open questions' 'Blocked on' 'Verify with'; do check has "$h" "**$k:**"; done

if [ "${FLOOR:-0}" = 1 ]; then
  # Contract: our CONSTRAINTS.md as the base; a no-op edit exits 0, a loosened coverage row exits 1,
  # a removed floor bullet exits 1, a tightened row exits 0, a new exception row exits 1.
  guard=${GUARD:-$here/../ci/floor-guard.mjs}
  [ -f "$guard" ] || { echo "FAIL floor-guard reference missing at $guard"; exit 1; }
  work=$(mktemp -d "${TMPDIR:-/tmp}/tpl-floor.XXXXXX")
  cp "$guard" "$work/floor-guard.mjs"; cp "$c" "$work/CONSTRAINTS.md"; cp "$here/floor-cases.sh" "$work/cases.sh"
  docker run --rm -u node -v "$work:/w" -w /w claude-eval:latest bash cases.sh || fail=1
  /bin/rm -rf "$work"
fi

if [ "${LIVE:-0}" = 1 ]; then
  token_env=${TOKEN_ENV:-$HOME/.config/claude-eval/token.env}
  work=$(mktemp -d "${TMPDIR:-/tmp}/tpl-live.XXXXXX")
  mkdir -p "$work/home/.claude/rules" "$work/repo/src"
  cp "$g" "$work/home/.claude/CLAUDE.md"; cp "$here"/global/rules/*.md "$work/home/.claude/rules/"
  awk 'BEGIN{skip=0} /^\*\*Product priority:\*\*/{print "**Product priority:** status and assignee at a glance first; scan speed over refinement. Every UI or UX decision defers to it."; skip=1; next} skip && /defers to it\.$/{skip=0; next} !skip' "$p" | sed 's/<project name>/Issue tracker/' >"$work/repo/CLAUDE.md"
  cp "$c" "$work/repo/CONSTRAINTS.md"; cp "$h" "$work/repo/HANDOFF.md"
  printf '%s\n' 'export const x: number = 1;' >"$work/repo/src/index.ts"
  cp "$here/live-probe.sh" "$work/probe.sh"
  docker run --rm -u node --env-file "$token_env" -e HOME=/w/home -v "$work:/w" -w /w/repo claude-eval:latest bash /w/probe.sh | tee "$here/live-probe.out" || fail=1
  /bin/rm -rf "$work"
fi

[ "$fail" = 0 ] && echo "ALL OK" || { echo "FAILURES"; exit 1; }

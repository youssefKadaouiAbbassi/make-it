#!/usr/bin/env bash
# Run the makeit eval cases in the eval image (redteam/eval's claude-eval:latest) with the
# subscription token file. Nothing is installed on the host. Results land in
# $EVAL_OUT/<tag>/ (default ~/.local/state/makeit/evals), never inside the plugin: Claude
# Code copies the whole plugin folder on install, and the sandbox leaves unreadable directories.
#   TOKEN_ENV=~/.config/claude-eval/token.env bash plugins/makeit/evals/run-local.sh [--runs 1] [--case name]
set -uo pipefail
here=$(cd "$(dirname "$0")" && pwd)
stack=$(cd "$here/../../.." && pwd)
token_env=${TOKEN_ENV:-$HOME/.config/claude-eval/token.env}
tag=${RUN_TAG:-$(date -u +%Y%m%dT%H%M%SZ)}
out="${EVAL_OUT:-${XDG_STATE_HOME:-$HOME/.local/state}/makeit/evals}/$tag"; mkdir -p "$out/tmp" "$out/report"
work=$(mktemp -d "${TMPDIR:-/tmp}/stack-eval.XXXXXX"); cp -r "$stack" "$work/stack"
docker run --rm --name "stack-eval-$tag" -u node \
  --security-opt seccomp=unconfined --security-opt apparmor=rootlesskit --security-opt systempaths=unconfined \
  --env-file "$token_env" -e TMPDIR=/tmp -e STACK_TEMPLATES=/stack/templates/project \
  -v "$work/stack:/stack" -v "$out/tmp:/tmp" -v "$out:/out" -w /stack/plugins/makeit claude-eval:latest \
  claude plugin eval /stack/plugins/makeit --eval-dir evals --ablation none --scaffold --keep-temp \
  --trust-plugin --no-publish --model "${MODEL:-claude-opus-5-5}" --mocks off --threshold 0 --judge-model "${JUDGE:-sonnet}" -j 1 \
  --output-dir /out/report --json /out/eval.json "$@" \
  --allow-tools Bash Write Edit >"$out/eval.log" 2>&1
status=$?
echo "$status" >"$out/exit-code"
/bin/rm -rf "$work"
echo "exit=$status results=$out"
tail -25 "$out/eval.log"

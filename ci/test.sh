#!/usr/bin/env bash
# Tests for the CI brick. Docker only (nothing installed on the host) except zizmor when present.
#   bash ci/test.sh
# 1. floor-guard contract (ci/floor-cases.sh) in the eval image.
# 2. zizmor at the auditor persona on every workflow (local binary, or the zizmor image).
# 3. actionlint 1.7.12 on every workflow (rhysd/actionlint image, run as the host user).
# 4. JSON parse of the rulesets, renovate.json, budget.json and the release-please templates.
set -uo pipefail
here=$(cd "$(dirname "$0")" && pwd)
fail=0
ref=${GUARD_REF:-/tmp/claude-1000/-home-izno/69d7285c-501b-429e-8bf7-027759a65e0c/scratchpad/fg/floor-guard.mjs}
cd "$here" || exit 1

echo "== floor-guard contract"
if [ -f "$ref" ]; then
  docker run --rm -u node -v "$here:/w" -v "$(dirname "$ref"):/fg:ro" -w /w claude-eval:latest \
    python3 -I /w/make-floor-guard.py "/fg/$(basename "$ref")" /w/floor-guard.mjs >/dev/null || fail=1
else
  echo "reference not present; testing the committed floor-guard.mjs as is"
fi
cp ../templates/project/CONSTRAINTS.md . && docker run --rm -u node -v "$here:/w" -w /w claude-eval:latest bash floor-cases.sh | tail -1 || fail=1
/bin/rm -rf repo out.txt CONSTRAINTS.md

# Adding the floor-guard reason to the PR description must re-run the gate.
if grep -qF "types: [opened, synchronize, reopened, edited]" .github/workflows/gate-caller.yml; then echo "ok caller re-runs on an edited PR"; else echo "FAIL the caller does not re-run when the PR description is edited"; fail=1; fi

echo "== zizmor (auditor)"
if command -v zizmor >/dev/null 2>&1; then
  zizmor --no-progress --min-severity low --persona auditor .github/workflows || fail=1
else
  docker run --rm -v "$here:/w" -w /w ghcr.io/zizmorcore/zizmor:1.31.1 --no-progress --min-severity low --persona auditor .github/workflows || fail=1
fi

echo "== actionlint"
t=$(mktemp -d "${TMPDIR:-/tmp}/actionlint.XXXXXX"); cp -r .github "$t/"; git -C "$t" init -q
docker run --rm -u "$(id -u):$(id -g)" -v "$t:/repo" -w /repo rhysd/actionlint:1.7.12 -color || fail=1
/bin/rm -rf "$t"

echo "== json"
for f in rulesets/*.json renovate.json budget.json release-please-config.json .release-please-manifest.json; do
  python3 -c "import json,sys; json.load(open('$f'))" && echo "ok $f" || { echo "FAIL $f"; fail=1; }
done

[ "$fail" = 0 ] && echo "CI OK" || { echo "CI FAILURES"; exit 1; }

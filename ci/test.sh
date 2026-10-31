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
# GUARD_REF: the upstream agent-skills floor-guard.mjs, to check make-floor-guard.py against it.
ref=${GUARD_REF:-}
cd "$here" || exit 1

echo "== floor-guard contract"
if [ -f "$ref" ]; then
  docker run --rm -u node -v "$here:/w" -v "$(dirname "$ref"):/fg:ro" -w /w claude-eval:latest \
    python3 -I /w/make-floor-guard.py "/fg/$(basename "$ref")" /w/floor-guard.mjs >/dev/null || fail=1
else
  echo "GUARD_REF not set or missing; testing the committed floor-guard.mjs as is"
fi
cp ../templates/project/CONSTRAINTS.md . && docker run --rm -u node -v "$here:/w" -w /w claude-eval:latest bash floor-cases.sh | tail -1 || fail=1
/bin/rm -rf repo out.txt CONSTRAINTS.md

# The cases set a git identity; it must land in their own config, never the user's ~/.gitconfig.
t=$(mktemp -d "${TMPDIR:-/tmp}/floor-home.XXXXXX"); mkdir -p "$t/w/.github/workflows" "$t/home"
cp floor-cases.sh floor-guard.mjs ../templates/project/CONSTRAINTS.md "$t/w/"; cp .github/workflows/gate.yml "$t/w/.github/workflows/"
printf '[user]\n\tname = Keep Me\n\temail = keep@example.org\n' >"$t/home/.gitconfig"; cp "$t/home/.gitconfig" "$t/want"
(cd "$t/w" && env -u GIT_CONFIG_GLOBAL HOME="$t/home" XDG_CONFIG_HOME="$t/home/.config" bash floor-cases.sh >/dev/null 2>&1)
if cmp -s "$t/home/.gitconfig" "$t/want"; then echo "ok floor-cases leaves ~/.gitconfig alone"; else echo "FAIL floor-cases changed ~/.gitconfig"; fail=1; fi
/bin/rm -rf "$t"

# Adding the floor-guard reason to the PR description must re-run the gate.
if grep -qF "types: [opened, synchronize, reopened, edited]" .github/workflows/gate-caller.yml; then echo "ok caller re-runs on an edited PR"; else echo "FAIL the caller does not re-run when the PR description is edited"; fail=1; fi

# Nothing private in a public file: the working files (HANDOFF.md, docs/superpowers, cli/research)
# stay local and are left out of the public copy.
echo "== no personal strings"
leaks=$(git -C .. ls-files -z -- . ':!HANDOFF.md' ':!docs/superpowers' ':!cli/research' | (cd .. && xargs -0 grep -lE '/home/[i]zno|-home-[i]zno|claude\.ai/code/[s]ession|red[t]eam/' 2>/dev/null))
if [ -z "$leaks" ]; then echo "ok no personal strings in public files"; else echo "FAIL personal strings in: $(echo $leaks)"; fail=1; fi

# The reusable gate callers name lives at the repository root, where GitHub runs workflows from.
if cmp -s .github/workflows/gate.yml ../.github/workflows/gate.yml; then echo "ok root gate.yml matches ci/"; else echo "FAIL ../.github/workflows/gate.yml differs from ci/.github/workflows/gate.yml"; fail=1; fi

# makeit's release: a normal release (GitHub's /releases/latest, which self update reads, skips
# pre-releases), and only for the owner's pushed tag (--verify-tag: never create one).
rel=../.github/workflows/release.yml
if grep -q -- '--prerelease' "$rel" || ! grep -q -- 'gh release create "$TAG" --draft --verify-tag' "$rel"; then
  echo "FAIL release.yml must draft a normal release for an existing tag (--draft --verify-tag, no --prerelease)"; fail=1
else echo "ok release.yml drafts a normal release for the pushed tag"; fi

echo "== zizmor (auditor)"
if command -v zizmor >/dev/null 2>&1; then
  zizmor --no-progress --min-severity low --persona auditor .github/workflows ../.github/workflows || fail=1
else
  docker run --rm -v "$here/..:/w" -w /w ghcr.io/zizmorcore/zizmor:1.31.1 --no-progress --min-severity low --persona auditor ci/.github/workflows .github/workflows || fail=1
fi

echo "== actionlint"
for d in . ..; do
  t=$(mktemp -d "${TMPDIR:-/tmp}/actionlint.XXXXXX"); cp -r "$d/.github" "$t/"; git -C "$t" init -q
  docker run --rm -u "$(id -u):$(id -g)" -v "$t:/repo" -w /repo rhysd/actionlint:1.7.12 -color || fail=1
  /bin/rm -rf "$t"
done

echo "== json"
for f in rulesets/*.json renovate.json budget.json release-please-config.json .release-please-manifest.json; do
  python3 -c "import json,sys; json.load(open('$f'))" && echo "ok $f" || { echo "FAIL $f"; fail=1; }
done

[ "$fail" = 0 ] && echo "CI OK" || { echo "CI FAILURES"; exit 1; }

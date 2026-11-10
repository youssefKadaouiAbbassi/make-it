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
# pre-releases), and only for a pushed tag (--verify-tag: never create one).
rel=../.github/workflows/release.yml
if grep -q -- '--prerelease' "$rel" || ! grep -q -- 'gh release create "$TAG" --draft --verify-tag' "$rel"; then
  echo "FAIL release.yml must draft a normal release for an existing tag (--draft --verify-tag, no --prerelease)"; fail=1
else echo "ok release.yml drafts a normal release for the pushed tag"; fi
# The scheduled-removal test skips on a "dev" build: the release runs it with the tag's version.
if grep -q 'go test.*-ldflags "-X makeit/internal/buildinfo.Version=$v".*TestMigrationsRemovedOnSchedule' "$rel"; then
  echo "ok release.yml runs the migration-removal test with the release version"
else echo "FAIL release.yml must run TestMigrationsRemovedOnSchedule with -X makeit/internal/buildinfo.Version=\$v"; fail=1; fi

# Public docs: relative links resolve, none points at the local-only INSTALL.md or cli/ design notes,
# and no emoji.
echo "== public docs"
if python3 -I - .. <<'PY'
import pathlib, re, sys
root = pathlib.Path(sys.argv[1]).resolve()
files = [root / f for f in ("README.md", "SECURITY.md", "CHANGELOG.md", "CONTRIBUTING.md") if (root / f).exists()]
files += sorted((root / "docs").glob("*.md")) + sorted((root / ".github/ISSUE_TEMPLATE").glob("*.md"))
# \u2713 and \u2717 are makeit's own text status marks in output samples, not emoji.
emoji = re.compile("[\U0001F000-\U0001FAFF\u2600-\u2712\u2714-\u2716\u2718-\u27BF\uFE0F]")
bad = []
for f in files:
    text = f.read_text(encoding="utf-8")
    for target in re.findall(r'\]\(([^)\s]+)\)|src="([^"]+)"', text):
        t = (target[0] or target[1]).split("#")[0]
        if not t or re.match(r"[a-z]+:", t):
            continue
        dest = (f.parent / t).resolve()
        rel = dest.relative_to(root).as_posix() if dest.is_relative_to(root) else str(dest)
        if not dest.exists():
            bad.append(f"{f.relative_to(root)}: broken link {t}")
        elif rel == "INSTALL.md" or rel.startswith("cli/"):
            bad.append(f"{f.relative_to(root)}: links {rel}, which is not public")
    if emoji.search(text):
        bad.append(f"{f.relative_to(root)}: emoji")
print("\n".join(bad))
sys.exit(1 if bad else 0)
PY
then echo "ok public docs: links resolve, none to INSTALL.md or cli/, no emoji"; else echo "FAIL public docs"; fail=1; fi

echo "== changelog"
# Every version of the plugin set has its CHANGELOG.md section: the release notes come from it.
v=$(python3 -c "import json; print(json.load(open('../.claude-plugin/marketplace.json'))['metadata']['version'])")
if bash ../cli/scripts/release-notes.sh "$v" >/dev/null; then echo "ok CHANGELOG.md has $v"; else echo "FAIL changelog"; fail=1; fi

# plugins/deja-vu is generated: regenerating at the UPSTREAM tag must reproduce it byte for byte.
echo "== generated deja-vu plugin"
dtag=$(sed -n 's/^tag //p' ../plugins/deja-vu/UPSTREAM 2>/dev/null)
dt=$(mktemp -d "${TMPDIR:-/tmp}/deja-gen.XXXXXX")
if [ -n "$dtag" ] && bash ../cli/scripts/gen-deja-plugin.sh "$dtag" "$dt/out" && diff -r ../plugins/deja-vu "$dt/out" >/dev/null \
  && [ "$(cd ../plugins/deja-vu && find . -type f -printf '%m %p\n' | sort)" = "$(cd "$dt/out" && find . -type f -printf '%m %p\n' | sort)" ]; then echo "ok plugins/deja-vu matches a fresh generation at $dtag"
else echo "FAIL plugins/deja-vu differs from gen-deja-plugin.sh at its UPSTREAM tag ($dtag)"; fail=1; fi
/bin/rm -rf "$dt"
# Every local plugin carries the catalog version: a change to a generated plugin must reach users.
if python3 -I -c "
import json,sys
m=json.load(open('../.claude-plugin/marketplace.json')); v=m['metadata']['version']
bad=[p['name'] for p in m['plugins'] if isinstance(p['source'],str) and p.get('version')!=v]
print('local plugins off version',v,bad) if bad else None; sys.exit(1 if bad else 0)"; then echo "ok local plugin entries carry the catalog version"; else echo "FAIL local plugin version"; fail=1; fi
if command -v claude >/dev/null 2>&1; then
  if claude plugin validate ../plugins/deja-vu --strict >/dev/null 2>&1; then echo "ok claude plugin validate --strict deja-vu"; else echo "FAIL claude plugin validate deja-vu"; fail=1; fi
fi

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

# docs/hooks.md is generated from every bundled plugin's hooks (external ones at their pinned sha).
echo "== hooks doc"
hd=$(mktemp -d "${TMPDIR:-/tmp}/hooks-doc.XXXXXX")
if bash gen-hooks-doc.sh "$hd/hooks.md" && diff -u ../docs/hooks.md "$hd/hooks.md" >/dev/null; then echo "ok docs/hooks.md matches a fresh generation"
else echo "FAIL docs/hooks.md differs from ci/gen-hooks-doc.sh output (bash ci/gen-hooks-doc.sh docs/hooks.md)"; fail=1; fi
/bin/rm -rf "$hd"

echo "== json"
for f in rulesets/*.json renovate.json budget.json release-please-config.json .release-please-manifest.json; do
  python3 -c "import json,sys; json.load(open('$f'))" && echo "ok $f" || { echo "FAIL $f"; fail=1; }
done

echo "== global CLAUDE.md"
g=../templates/global/CLAUDE.md
for k in 'skips the design and plan steps' 'claude -w <name>' 'or with Monitor; never a fixed foreground sleep' 'may run past two minutes' 'run `check:task`; run the full suite once' 'criteria its reviewer will check' 'mechanical subagents run on Sonnet' 'touch different files may run in parallel' 'captcha or 2FA screen'; do
  grep -qF -- "$k" "$g" && echo "ok $k" || { echo "FAIL $g lacks: $k"; fail=1; }
done
[ "$(wc -l < "$g")" -lt 200 ] && echo "ok under 200 lines" || { echo "FAIL $g is 200+ lines"; fail=1; }

echo "== draft skill"
sk=../plugins/makeit/skills/draft/SKILL.md
if grep -q '^description: "Use when writing text another person will read' "$sk" && [ -f ../plugins/makeit/evals/draft/case.yaml ]; then echo "ok draft skill and eval case"; else echo "FAIL draft skill or eval case"; fail=1; fi

[ "$fail" = 0 ] && echo "CI OK" || { echo "CI FAILURES"; exit 1; }

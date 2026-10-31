#!/usr/bin/env bash
# Contract test for ci/floor-guard.mjs (the stack copy), run inside the eval image:
#   docker run --rm -u node -v "$PWD:/w" -w /w claude-eval:latest bash floor-cases.sh
# Fresh repo with the project CONSTRAINTS.md template, a source file, a test file, a tsconfig,
# a package.json with scripts and a workflow on main; one edit per case on a branch.
# Loosening moves and watched-file changes (the guard at ci/floor-guard.mjs included) exit 1; no-op,
# tightening, dependency bumps, a seeded CONSTRAINTS.md and a watched change with a reason in the
# PR description (PR_BODY) exit 0. origin/guarded is main plus ci/floor-guard.mjs, for edits to the guard;
# origin/skipped is main with the test skipped.
set -uo pipefail
git config --global user.email t@example.com; git config --global user.name t; git config --global init.defaultBranch main
/bin/rm -rf repo; git init -q repo && cp CONSTRAINTS.md floor-guard.mjs repo/ && cd repo
mkdir -p .github/workflows
printf 'export const x = 1;\n' >src.ts; printf 'test("x", () => { expect(1).toBe(1); });\n' >src.test.ts
printf '{ "compilerOptions": { "strict": true } }\n' >tsconfig.json
printf '{\n  "name": "t",\n  "scripts": {\n    "check:full": "tsc && vitest run"\n  },\n  "dependencies": {\n    "left-pad": "1.0.0"\n  }\n}\n' >package.json
printf 'name: ci\non: push\npermissions: {}\njobs:\n  a:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo hi\n' >.github/workflows/ci.yml
git add -A && git commit -qm base && git update-ref refs/remotes/origin/main HEAD
git checkout -q -b guarded && mkdir ci && cp floor-guard.mjs ci/ && git add -A && git commit -qm guarded && git update-ref refs/remotes/origin/guarded HEAD
git checkout -q main && git branch -q -D guarded
git checkout -q -b skipped && sed -i 's/^test(/test.skip(/' src.test.ts && git commit -qam skipped && git update-ref refs/remotes/origin/skipped HEAD
git checkout -q main && git branch -q -D skipped
fail=0
run_case() { # name expected-exit edit-command [extra guard args] [PR body] [text the output must hold]; BASE= overrides origin/main
  git checkout -q main; git checkout -q -B case
  eval "$3"; git add -A; git commit -qm "$1" >/dev/null
  PR_BODY="${5:-}" eval "node floor-guard.mjs --base ${BASE:-origin/main} ${4:-}" >../out.txt 2>&1; rc=$?
  if [ "$rc" = "$2" ] && { [ -z "${6:-}" ] || grep -qF -- "$6" ../out.txt; }; then echo "ok   $1 (exit $rc)"; else echo "FAIL $1: expected $2 got $rc"; cat ../out.txt; fail=1; fi
  git checkout -q main; git branch -q -D case
}
run_case noop-edit 0 "printf '\n' >>src.ts"
run_case tighten-coverage 0 "sed -i 's/Changed lines >= 80% covered/Changed lines >= 90% covered/' CONSTRAINTS.md"
run_case loosen-coverage 1 "sed -i 's/Changed lines >= 80% covered/Changed lines >= 50% covered/' CONSTRAINTS.md"
run_case loosen-mutation 1 "sed -i 's/Diff mutation score >= 60%/Diff mutation score >= 20%/' CONSTRAINTS.md"
run_case remove-floor-bullet 1 "sed -i '/No skipped or deleted tests/d' CONSTRAINTS.md"
run_case new-exception 1 "printf '| W1 | no-explicit-any | src/legacy/** | rewrite | @o | 2027-01-01 |\n' >>CONSTRAINTS.md"
run_case skip-test 1 "sed -i 's/^test(/test.skip(/' src.test.ts"
run_case new-suppression-eslint 1 "printf '// eslint-disable-next-line\nexport const y = 2;\n' >>src.ts"
run_case new-suppression-betterleaks 1 "printf 'const k = \"x\"; // betterleaks:allow\n' >>src.ts"
run_case delete-test 1 "git rm -q src.test.ts"
run_case watched-tsconfig 1 "sed -i 's/\"strict\": true/\"strict\": false/' tsconfig.json"
run_case watched-workflow 1 "printf '      - run: echo more\n' >>.github/workflows/ci.yml"
run_case watched-workflow-renamed-away 1 "git mv .github/workflows/ci.yml ci.yml.off" "" "" "[watched-file-changed] .github/workflows/ci.yml"
run_case file-renamed-into-workflows 1 "git mv src.ts .github/workflows/src.yml" "" "" "[watched-file-changed] .github/workflows/src.yml"
# A rename is not a deletion: a test moved to another test path keeps its tests, and the markers a
# moved file already held are not new. A test moved to a path no test pattern matches is deleted.
run_case test-renamed 0 "git mv src.test.ts moved.test.ts"
BASE=origin/skipped run_case test-with-old-skip-renamed 0 "git reset -q --hard origin/skipped && git mv src.test.ts moved.test.ts"
run_case test-renamed-away 1 "git mv src.test.ts src.bak" "" "" "[test-deleted] src.test.ts"
run_case watched-package-scripts 1 "sed -i 's/tsc \&\& vitest run/vitest run/' package.json"
run_case package-dependency-bump-quiet 0 "sed -i 's/1.0.0/1.0.1/' package.json"
run_case watched-new-config 1 "printf '[lint]\nselect = []\n' >ruff.toml"
run_case custom-watch-only 1 "printf 'x\n' >>src.ts" "--watch '^src\\.ts\$'"
run_case custom-watch-miss 0 "printf 'x\n' >>src.ts" "--watch '^docs/'"
# What makeit init seeds: the guard's own source and a CONSTRAINTS.md name every marker they forbid.
# The guard is a watched file, so seeding it needs a reason; a reason excuses no marker, so exit 0
# still means none of the guard's own patterns was flagged.
run_case seeded-guard-not-flagged 0 "mkdir -p ci && cp floor-guard.mjs ci/floor-guard.mjs" "" "floor-guard: makeit init seeds the gate"
run_case seeded-constraints-not-flagged 0 "mkdir -p svc && cp CONSTRAINTS.md svc/CONSTRAINTS.md"
# Weakening the guard is a watched change; only ci/floor-guard.mjs skips the marker scan.
BASE=origin/guarded run_case weaken-guard 1 "git reset -q --hard origin/guarded && sed -i 's/eslint-disable|//' ci/floor-guard.mjs" "" "" "[watched-file-changed] ci/floor-guard.mjs"
run_case guard-name-elsewhere-scanned 1 "mkdir -p lib && printf '// eslint-disable-next-line\\nexport const y = 2;\\n' >lib/floor-guard.mjs" "" "" "[silenced-checker] lib/floor-guard.mjs"
# A watched change passes with a reason in the PR description: "floor-guard: <reason>" for every
# watched file, "floor-guard <path>: <reason>" for one. A reason excuses nothing else.
wf="printf '      - run: echo more\\n' >>.github/workflows/ci.yml"
run_case reason-empty 1 "$wf" "" "floor-guard:   "
run_case reason-placeholder 1 "$wf" "" "floor-guard: <reason>"
run_case reason-placeholder-for-the-file 1 "$wf" "" "floor-guard .github/workflows/ci.yml: <why>"
run_case reason-punctuation-only 1 "$wf" "" "floor-guard: ..."
run_case reason-in-html-comment 1 "$wf" "" $'<!--\nfloor-guard: the release needs the extra step\n-->'
run_case reason-in-unclosed-html-comment 1 "$wf" "" $'<!-- template\nfloor-guard: the release needs the extra step'
run_case reason-after-html-comment 0 "$wf" "" $'<!-- say why -->\nfloor-guard: the release needs the extra step' "the release needs the extra step"
run_case reason-for-all 0 "$wf" "" $'Adds a step.\n\nfloor-guard: the release needs the extra step' "the release needs the extra step"
run_case reason-for-the-file 0 "$wf" "" "- floor-guard .github/workflows/ci.yml: the release needs it" ".github/workflows/ci.yml"
run_case reason-for-another-file 1 "$wf" "" "floor-guard tsconfig.json: unrelated"
run_case reason-does-not-excuse-a-suppression 1 "$wf; printf '// eslint-disable-next-line\\nexport const y = 2;\\n' >>src.ts" "" "floor-guard: the release needs the extra step"
[ "$fail" = 0 ] && echo "FLOOR OK" || { echo "FLOOR FAILURES"; exit 1; }

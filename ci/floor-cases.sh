#!/usr/bin/env bash
# Contract test for ci/floor-guard.mjs (the stack copy), run inside the eval image:
#   docker run --rm -u node -v "$PWD:/w" -w /w claude-eval:latest bash floor-cases.sh
# Fresh repo with the project CONSTRAINTS.md template, a source file, a test file, a tsconfig,
# a package.json with scripts and a workflow on main; one edit per case on a branch.
# Loosening moves and watched-file changes exit 1; no-op, tightening and dependency bumps exit 0.
set -uo pipefail
git config --global user.email t@example.com; git config --global user.name t; git config --global init.defaultBranch main
/bin/rm -rf repo; git init -q repo && cp CONSTRAINTS.md floor-guard.mjs repo/ && cd repo
mkdir -p .github/workflows
printf 'export const x = 1;\n' >src.ts; printf 'test("x", () => { expect(1).toBe(1); });\n' >src.test.ts
printf '{ "compilerOptions": { "strict": true } }\n' >tsconfig.json
printf '{\n  "name": "t",\n  "scripts": {\n    "check:full": "tsc && vitest run"\n  },\n  "dependencies": {\n    "left-pad": "1.0.0"\n  }\n}\n' >package.json
printf 'name: ci\non: push\npermissions: {}\njobs:\n  a:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo hi\n' >.github/workflows/ci.yml
git add -A && git commit -qm base && git update-ref refs/remotes/origin/main HEAD
fail=0
run_case() { # name expected-exit edit-command [extra guard args]
  git checkout -q main; git checkout -q -B case
  eval "$3"; git add -A; git commit -qm "$1" >/dev/null
  eval "node floor-guard.mjs --base origin/main ${4:-}" >../out.txt 2>&1; rc=$?
  if [ "$rc" = "$2" ]; then echo "ok   $1 (exit $rc)"; else echo "FAIL $1: expected $2 got $rc"; cat ../out.txt; fail=1; fi
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
run_case watched-package-scripts 1 "sed -i 's/tsc \&\& vitest run/vitest run/' package.json"
run_case package-dependency-bump-quiet 0 "sed -i 's/1.0.0/1.0.1/' package.json"
run_case watched-new-config 1 "printf '[lint]\nselect = []\n' >ruff.toml"
run_case custom-watch-only 1 "printf 'x\n' >>src.ts" "--watch '^src\\.ts\$'"
run_case custom-watch-miss 0 "printf 'x\n' >>src.ts" "--watch '^docs/'"
[ "$fail" = 0 ] && echo "FLOOR OK" || { echo "FLOOR FAILURES"; exit 1; }

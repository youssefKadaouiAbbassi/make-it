#!/usr/bin/env bash
# Runs inside the eval image: fresh git repo with our CONSTRAINTS.md on main, then one edit per case
# on a branch; floor-guard must exit 0 for no-op/tightening and 1 for loosening moves.
set -uo pipefail
# Its own global config: the identity below must never reach the user's ~/.gitconfig.
GIT_CONFIG_GLOBAL=$(mktemp); export GIT_CONFIG_GLOBAL
trap '/bin/rm -f "$GIT_CONFIG_GLOBAL"' EXIT
git config --global user.email t@example.com; git config --global user.name t; git config --global init.defaultBranch main
git init -q repo && cp CONSTRAINTS.md floor-guard.mjs repo/ && cd repo || exit 1
printf 'export const x = 1;\n' >src.ts; printf 'test("x", () => { expect(1).toBe(1); });\n' >src.test.ts
git add -A && git commit -qm base && git update-ref refs/remotes/origin/main HEAD
fail=0
run_case() { # name expected-exit edit-command
  git checkout -q main
  git checkout -q -B case
  eval "$3"; git add -A; git commit -qm "$1" >/dev/null
  node floor-guard.mjs --base origin/main >../out.txt 2>&1; rc=$?
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
run_case new-suppression 1 "printf '// eslint-disable-next-line\nexport const y = 2;\n' >>src.ts"
run_case delete-test 1 "git rm -q src.test.ts"
[ "$fail" = 0 ] && echo "FLOOR OK" || { echo "FLOOR FAILURES"; exit 1; }

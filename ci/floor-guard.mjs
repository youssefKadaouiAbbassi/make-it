#!/usr/bin/env node
// floor-guard.mjs — diff-scoped enforcement of the CONSTRAINTS.md floor.
// Usage: node floor-guard.mjs [--base <ref>] [--watch <regex>]   (default base: origin/$BASE_REF, else origin/main)
// Stack copy (makeit v3.5): adds --watch, a default watch list of files whose change turns
// the check red (checker configs, check tasks, workflows, CONSTRAINTS.md), and betterleaks:allow.
// Upstream: addyosmani/agent-skills 0.6.12, references/floor-guard.md (MIT).
import { execFileSync } from 'node:child_process';

const argAfter = (flag) => { const i = process.argv.indexOf(flag); return i > -1 ? process.argv[i + 1] : undefined; };
const base = argAfter('--base') ?? `origin/${process.env.BASE_REF || 'main'}`;
// Files whose change is always a review item: checker configs, coverage and test-runner configs,
// secret-scanner configs, workflows, the check tasks, CONSTRAINTS.md itself and this guard.
const DEFAULT_WATCH = String.raw`(^|/)(tsconfig[^/]*\.json|eslint\.config\.[cm]?[jt]s|\.eslintrc[^/]*|biome\.jsonc?|ruff\.toml|pyproject\.toml|mypy\.ini|pyrightconfig\.json|\.golangci\.ya?ml|clippy\.toml|\.coveragerc|vitest\.config\.[cm]?[jt]s|jest\.config\.[cm]?[jt]s|playwright\.config\.[cm]?[jt]s|pytest\.ini|stryker\.config\.[^/]+|\.gitleaks\.toml|\.betterleaks\.toml|\.semgrepignore|\.grype\.ya?ml|\.grant\.ya?ml|package\.json|Makefile|Taskfile\.ya?ml|mise\.toml|CONSTRAINTS\.md)$|^\.github/workflows/|^\.pre-commit-config\.ya?ml$|^ci/floor-guard\.mjs$`;
const watch = new RegExp(argAfter('--watch') ?? DEFAULT_WATCH);

// `git diff --no-index` exits 1 whenever the two sides differ, which is the normal case for a
// new file, so that output is kept. Any other failure is null, and null never reads as clean.
const git = (args, { diffExit = false } = {}) => {
  try { return execFileSync('git', args, { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] }); }
  catch (e) { return diffExit && e.status === 1 && typeof e.stdout === 'string' ? e.stdout : null; }
};
const bail = (msg) => { console.error('floor-guard: ' + msg); process.exit(2); };

// Run from the top of the work tree. `git ls-files` lists only the current directory's subtree,
// relative to it, so a guard started in a subfolder would miss untracked files elsewhere and name
// the rest differently from `git diff`, which always covers the whole tree.
const top = git(['rev-parse', '--show-toplevel'])?.trim();
if (!top) bail('not inside a git work tree');
process.chdir(top);

// Merge base; bail to exit 2 rather than pretending a shallow/rootless clone is clean.
const mergeBase = git(['merge-base', base, 'HEAD'])?.trim();
if (!mergeBase) bail('no merge base against ' + base);

// Unified diff plus untracked files (git diff alone cannot see new files).
// --no-renames: a pure rename has no +/- lines, so a watched file moved away would pass unseen.
const tracked = git(['diff', '--no-renames', '--unified=0', mergeBase, '--']);
if (tracked === null) bail('could not diff against ' + mergeBase);
const untrackedFiles = git(['ls-files', '--others', '--exclude-standard']);
if (untrackedFiles === null) bail('could not list untracked files');
const untracked = untrackedFiles.split('\n').filter(Boolean).map((f) => {
  const d = git(['diff', '--no-index', '--unified=0', '/dev/null', f], { diffExit: true });
  if (d === null) bail('could not diff untracked file ' + f);
  return d;
}).join('\n');
const diff = tracked + '\n' + untracked;

// Walk the diff. `---` and `+++` are file headers only between a file's `diff` line and its first
// `@@` hunk; inside a hunk every line is content, so an added `++i` (shown as `+++i`) or a removed
// `-- comment` is a change, not a header. Both headers name the file, so a deletion
// (`+++ /dev/null`) keeps its name.
const added = [], removed = [], deleted = [];
const pathOf = (s) => s.replace(/^[ab]\//, '');
let file = '', oldFile = '', inHeader = false;
for (const line of diff.split('\n')) {
  if (line.startsWith('diff ')) inHeader = true;
  else if (line.startsWith('@@')) inHeader = false;
  else if (inHeader) {
    if (line.startsWith('--- ')) oldFile = pathOf(line.slice(4));
    else if (line.startsWith('+++ ')) {
      const newFile = pathOf(line.slice(4));
      file = newFile === '/dev/null' ? oldFile : newFile;
      if (newFile === '/dev/null') deleted.push(file);
    }
  }
  else if (line.startsWith('+')) added.push({ file, text: line.slice(1) });
  else if (line.startsWith('-')) removed.push({ file, text: line.slice(1) });
}

const findings = [];
const flag = (rule, f, text) => findings.push({ rule, file: f, text: text.trim().slice(0, 120) });
const isTest = (f) => /\.(test|spec)\.|_test\.|test_/.test(f);
const isConstraints = (f) => /CONSTRAINTS\.md$/.test(f);

// 1. Silenced checker — extend this list for your ecosystem.
const SUPPRESSIONS = /@ts-ignore|@ts-nocheck|eslint-disable|biome-ignore|# *noqa|# *type: *ignore|istanbul ignore|nosemgrep|gitleaks:allow|betterleaks:allow|Stryker disable/;
// 4. Unfinished work.
const STUBS = /throw new (Error|NotImplemented).*[Nn]ot implemented|catch\s*\(\w*\)\s*\{\s*\}|catch\s*\{\s*\}|\bTODO\b|\bpass\s*# *stub/;
// 2. A test made easier (added skips).
const SKIPS = /\.(skip|todo)\b|\bxit\(|\bxdescribe\(|@pytest\.mark\.skip|t\.Skip\(/;

const isGuard = (f) => f === 'ci/floor-guard.mjs';

for (const { file, text } of added) {
  // Stack addition: a CONSTRAINTS.md names every marker it forbids (its rules get their own checks),
  // and the guard's source holds the patterns themselves, so neither is scanned for markers. The guard
  // is on the watch list instead, so editing it needs a reason in the PR description.
  if (!isConstraints(file) && !isGuard(file)) {
    if (SUPPRESSIONS.test(text)) flag('silenced-checker', file, text);
    if (STUBS.test(text)) flag('unfinished-work', file, text);
    if (SKIPS.test(text)) flag('test-made-easier', file, text);
  }
  if (isConstraints(file) && /^\| *(W|E)\d+ *\|/.test(text)) flag('new-exception', file, text);
}

// 2b. A test file deleted, or an assertion removed from a test file that still exists.
for (const f of deleted) if (isTest(f)) flag('test-deleted', f, 'file deleted');

// 6 (stack addition). A watched file changed: checker configs, check tasks, workflows, CONSTRAINTS.md.
// A rule switched off in a config file is invisible to the regexes above, so the change itself is
// the finding; the owner reads the diff at the merge prompt. package.json counts only when a
// scripts line changed (dependency bumps are Renovate's).
// A watched change passes when the PR description gives a reason, read from PR_BODY as data: a line
// "floor-guard: <reason>" covers every watched file, "floor-guard <path>: <reason>" one file. A
// reason excuses only this rule; push events have no description and keep the finding.
const reasons = new Map();
let reasonForAll = '';
for (const line of (process.env.PR_BODY ?? '').split(/\r?\n/)) {
  const m = line.match(/^\s*(?:[-*]\s+)?floor-guard(?:\s+(\S+))?:\s*(\S.*?)\s*$/);
  if (!m) continue;
  if (m[1]) reasons.set(m[1], m[2]);
  else reasonForAll = m[2];
}
const excused = [];
const touched = new Set([...added, ...removed].map((l) => l.file).concat(deleted));
for (const f of touched) {
  if (!watch.test(f)) continue;
  if (/(^|\/)package\.json$/.test(f)) {
    const scriptLine = [...added, ...removed].some((l) => l.file === f && /"[^"]*(check:|test|lint|build|format|typecheck)[^"]*"\s*:/.test(l.text));
    if (!scriptLine) continue;
  }
  if (isConstraints(f)) continue; // CONSTRAINTS.md has its own rule-level checks above and below
  const reason = reasons.get(f) ?? reasonForAll;
  if (reason) excused.push({ file: f, reason });
  else flag('watched-file-changed', f, 'on the watch list; give the reason in the PR description: floor-guard: <reason>');
}
for (const { file, text } of removed) {
  if (isTest(file) && !deleted.includes(file) && /\b(expect|assert|should)\b/.test(text)) {
    flag('assertion-removed', file, text);
  }
}

// 1b/2c. A rule in CONSTRAINTS.md weakened or removed. A rule is a floor bullet or a table row,
// identified by the bullet's text before its first colon or by the row's first cell. Each number
// carries a direction read from the words around it: a minimum (>=, at least, must not fall) is
// loosened by going down, a maximum (<=, at most, under, must not grow) by going up. A number whose
// direction cannot be read is reported whenever it changes, because the guard cannot tell
// tightening from loosening and staying quiet is the wrong default. Numbers are paired within
// their direction (the first minimum with the first minimum, and so on), so a number added
// elsewhere in the text does not shift the pairing; a threshold with no counterpart after the
// edit was removed, and an added one tightens.
const ruleKey = (t) => {
  const s = t.trim();
  if (s.startsWith('|')) return s.split('|').map((c) => c.trim()).filter(Boolean)[0] ?? '';
  if (/^[-*] /.test(s)) return s.slice(2).split(':')[0].trim();
  return null; // prose, headings, dates: not a rule
};
const isException = (t) => /^\| *(W|E)\d+ *\|/.test(t.trim());
const MIN_BEFORE = /(>=|>|≥|at least|minimum|\bmin\b|no less than|not fall|not drop)\s*$/;
const MAX_BEFORE = /(<=|<|≤|at most|maximum|\bmax\b|no more than|under|below|not grow|not exceed)\s*$/;
const MIN_AFTER = /^\s*\S*\s*(or more|or higher|must not fall|must not drop)/;
const MAX_AFTER = /^\s*\S*\s*(or less|or lower|must not grow|must not exceed)/;
const thresholds = (t) => {
  const out = [], re = /\d+(?:\.\d+)?/g;
  let m;
  while ((m = re.exec(t))) {
    const before = t.slice(Math.max(0, m.index - 24), m.index).toLowerCase();
    const after = t.slice(m.index + m[0].length, m.index + m[0].length + 40).toLowerCase();
    const dir = MIN_BEFORE.test(before) || MIN_AFTER.test(after) ? 'min'
      : MAX_BEFORE.test(before) || MAX_AFTER.test(after) ? 'max' : null;
    out.push({ n: Number(m[0]), dir });
  }
  return out;
};
const removedRules = removed.filter((l) => isConstraints(l.file) && ruleKey(l.text) !== null);
const addedRules = added.filter((l) => isConstraints(l.file) && ruleKey(l.text) !== null);
for (const r of removedRules) {
  const a = addedRules.find((x) => ruleKey(x.text) === ruleKey(r.text));
  if (!a) {
    if (!isException(r.text)) flag('rule-removed', r.file, r.text); // dropping an exception tightens: silent
    continue;
  }
  const before = thresholds(r.text), after = thresholds(a.text);
  let verdict = null;
  for (const dir of ['min', 'max', null]) {
    const was = before.filter((x) => x.dir === dir), now = after.filter((x) => x.dir === dir);
    was.forEach((b, i) => {
      const n = now[i];
      if (verdict) return;
      if (!n) verdict = 'threshold-removed';
      else if (n.n === b.n) return;
      else if (dir === 'min' ? n.n < b.n : dir === 'max' ? n.n > b.n : true) {
        verdict = dir ? 'threshold-loosened' : 'threshold-changed';
      }
    });
  }
  if (verdict) flag(verdict, r.file, r.text + '  ->  ' + a.text);
}

const showExcused = (log) => {
  if (excused.length === 0) return;
  log('watched files changed, with the reason given in the PR description (review them at the merge):');
  for (const e of excused) log(`  ${e.file}: ${e.reason}`);
};
if (findings.length === 0) { console.log('floor-guard: clean'); showExcused(console.log); process.exit(0); }
showExcused(console.error);
console.error('floor-guard: ' + findings.length + ' floor violation(s):');
for (const f of findings) console.error(`  [${f.rule}] ${f.file}: ${f.text}`);
if (findings.some((f) => f.rule === 'rule-removed')) {
  console.error('\nA rule-removed finding can also mean the rule\'s label changed: rename a rule in one commit and change its thresholds in another.');
}
if (findings.some((f) => f.rule === 'threshold-removed')) {
  console.error('\nA threshold-removed finding can also mean a number gained or lost its direction words (">= 80%" becoming "80%", or the reverse): compare the two lines before assuming a threshold was deleted.');
}
console.error('\nEach is a move that lowers the bar. Fix the code, or route it through a tracked exception.');
process.exit(1);

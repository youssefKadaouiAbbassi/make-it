#!/usr/bin/env python3
"""Build ci/floor-guard.mjs from the upstream reference (agent-skills
constraint-driven-development/references/floor-guard.md, MIT) with the stack's
three changes: a --watch <regex> input, the default watch list, and
betterleaks:allow in the suppression regex. Aborts if an anchor is missing.
Usage: python3 -I make-floor-guard.py <reference.mjs> <out.mjs>
"""
import sys

src, out = sys.argv[1], sys.argv[2]
text = open(src, encoding="utf-8").read()


def rep(old, new):
    global text
    if text.count(old) != 1:
        sys.exit(f"anchor not unique: {old[:70]!r}")
    text = text.replace(old, new)


rep(
    "// Usage: node floor-guard.mjs [--base <ref>]   (default base: origin/main)",
    "// Usage: node floor-guard.mjs [--base <ref>] [--watch <regex>]   (default base: origin/$BASE_REF, else origin/main)\n"
    "// Stack copy (makeit v3.5): adds --watch, a default watch list of files whose change turns\n"
    "// the check red (checker configs, check tasks, workflows, CONSTRAINTS.md), and betterleaks:allow.\n"
    "// Upstream: addyosmani/agent-skills 0.6.12, references/floor-guard.md (MIT).",
)
rep(
    "const base = (() => {\n  const i = process.argv.indexOf('--base');\n  return i > -1 ? process.argv[i + 1] : 'origin/main';\n})();",
    "const argAfter = (flag) => { const i = process.argv.indexOf(flag); return i > -1 ? process.argv[i + 1] : undefined; };\n"
    "const base = argAfter('--base') ?? `origin/${process.env.BASE_REF || 'main'}`;\n"
    "// Files whose change is always a review item: checker configs, coverage and test-runner configs,\n"
    "// secret-scanner configs, workflows, the check tasks and CONSTRAINTS.md itself.\n"
    "const DEFAULT_WATCH = String.raw`(^|/)(tsconfig[^/]*\\.json|eslint\\.config\\.[cm]?[jt]s|\\.eslintrc[^/]*|biome\\.jsonc?|ruff\\.toml|pyproject\\.toml|mypy\\.ini|pyrightconfig\\.json|\\.golangci\\.ya?ml|clippy\\.toml|\\.coveragerc|vitest\\.config\\.[cm]?[jt]s|jest\\.config\\.[cm]?[jt]s|playwright\\.config\\.[cm]?[jt]s|pytest\\.ini|stryker\\.config\\.[^/]+|\\.gitleaks\\.toml|\\.betterleaks\\.toml|\\.semgrepignore|\\.grype\\.ya?ml|\\.grant\\.ya?ml|package\\.json|Makefile|Taskfile\\.ya?ml|mise\\.toml|CONSTRAINTS\\.md)$|^\\.github/workflows/|^\\.pre-commit-config\\.ya?ml$`;\n"
    "const watch = new RegExp(argAfter('--watch') ?? DEFAULT_WATCH);",
)
rep(
    "const SUPPRESSIONS = /@ts-ignore|@ts-nocheck|eslint-disable|biome-ignore|# *noqa|# *type: *ignore|istanbul ignore|nosemgrep|gitleaks:allow|Stryker disable/;",
    "const SUPPRESSIONS = /@ts-ignore|@ts-nocheck|eslint-disable|biome-ignore|# *noqa|# *type: *ignore|istanbul ignore|nosemgrep|gitleaks:allow|betterleaks:allow|Stryker disable/;",
)
rep(
    "for (const f of deleted) if (isTest(f)) flag('test-deleted', f, 'file deleted');",
    "for (const f of deleted) if (isTest(f)) flag('test-deleted', f, 'file deleted');\n\n"
    "// 6 (stack addition). A watched file changed: checker configs, check tasks, workflows, CONSTRAINTS.md.\n"
    "// A rule switched off in a config file is invisible to the regexes above, so the change itself is\n"
    "// the finding; the owner reads the diff at the merge prompt. package.json counts only when a\n"
    "// scripts line changed (dependency bumps are Renovate's).\n"
    "// A watched change passes when the PR description gives a reason, read from PR_BODY as data: a line\n"
    "// \"floor-guard: <reason>\" covers every watched file, \"floor-guard <path>: <reason>\" one file. A\n"
    "// reason excuses only this rule; push events have no description and keep the finding.\n"
    "const reasons = new Map();\n"
    "let reasonForAll = '';\n"
    "for (const line of (process.env.PR_BODY ?? '').split(/\\r?\\n/)) {\n"
    "  const m = line.match(/^\\s*(?:[-*]\\s+)?floor-guard(?:\\s+(\\S+))?:\\s*(\\S.*?)\\s*$/);\n"
    "  if (!m) continue;\n"
    "  if (m[1]) reasons.set(m[1], m[2]);\n"
    "  else reasonForAll = m[2];\n"
    "}\n"
    "const excused = [];\n"
    "const touched = new Set([...added, ...removed].map((l) => l.file).concat(deleted));\n"
    "for (const f of touched) {\n"
    "  if (!watch.test(f)) continue;\n"
    "  if (/(^|\\/)package\\.json$/.test(f)) {\n"
    "    const scriptLine = [...added, ...removed].some((l) => l.file === f && /\"[^\"]*(check:|test|lint|build|format|typecheck)[^\"]*\"\\s*:/.test(l.text));\n"
    "    if (!scriptLine) continue;\n"
    "  }\n"
    "  if (isConstraints(f)) continue; // CONSTRAINTS.md has its own rule-level checks above and below\n"
    "  const reason = reasons.get(f) ?? reasonForAll;\n"
    "  if (reason) excused.push({ file: f, reason });\n"
    "  else flag('watched-file-changed', f, 'on the watch list; give the reason in the PR description: floor-guard: <reason>');\n"
    "}",
)

rep(
    (
        'for (const { file, text } of added) {\n'
        "  if (SUPPRESSIONS.test(text)) flag('silenced-checker', file, text);\n"
        "  if (STUBS.test(text)) flag('unfinished-work', file, text);\n"
        "  if (SKIPS.test(text)) flag('test-made-easier', file, text);\n"
    ),
    (
        'const isGuard = (f) => /(^|\\/)floor-guard\\.mjs$/.test(f);\n'
        '\n'
        'for (const { file, text } of added) {\n'
        '  // Stack addition: a CONSTRAINTS.md names every marker it forbids (its rules get their own checks),\n'
        "  // and the guard's source holds the patterns themselves, so neither is scanned for markers.\n"
        '  if (!isConstraints(file) && !isGuard(file)) {\n'
        "    if (SUPPRESSIONS.test(text)) flag('silenced-checker', file, text);\n"
        "    if (STUBS.test(text)) flag('unfinished-work', file, text);\n"
        "    if (SKIPS.test(text)) flag('test-made-easier', file, text);\n"
        '  }\n'
    ),
)

rep(
    "if (findings.length === 0) { console.log('floor-guard: clean'); process.exit(0); }",
    "const showExcused = (log) => {\n"
    "  if (excused.length === 0) return;\n"
    "  log('watched files changed, with the reason given in the PR description (review them at the merge):');\n"
    "  for (const e of excused) log(`  ${e.file}: ${e.reason}`);\n"
    "};\n"
    "if (findings.length === 0) { console.log('floor-guard: clean'); showExcused(console.log); process.exit(0); }\n"
    "showExcused(console.error);"
)

open(out, "w", encoding="utf-8").write(text)
print("floor-guard written:", out)

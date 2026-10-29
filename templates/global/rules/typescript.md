---
paths:
  - "**/*.{ts,tsx,mts,cts,js,jsx}"
  - "**/package.json"
  - "**/tsconfig*.json"
---

# Node and TypeScript

Runtime: the current Active LTS. ESM only (`"type": "module"`). Use the package manager the repository already uses; never switch it.

| purpose | tool |
|---|---|
| lint | the repository's eslint or biome config |
| format | prettier or biome |
| types | `tsc --noEmit` |
| test | the repository's runner (vitest or jest) |

Strict tsconfig: `strict`, `noUncheckedIndexedAccess`, `exactOptionalPropertyTypes`, `noImplicitOverride`, `verbatimModuleSyntax`, `isolatedModules`. No `any`; prefer `unknown` plus a narrowing check. Never add a type assertion or a suppression to silence an error; fix the type. Exported functions get explicit return types.

Tests: colocated `*.test.ts`. Read two existing test files in the same package before writing one; mirror their setup.

Supply chain: audit before installing, pin exact versions, block postinstall scripts where the package manager supports it.

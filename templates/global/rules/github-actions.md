---
paths:
  - "**/.github/workflows/**"
  - "**/.github/actions/**"
  - "**/renovate.json"
---

# GitHub Actions

Pin every action to a full commit SHA with the version in a trailing comment (`uses: actions/checkout@<full-sha>  # vX.Y.Z`). Look up the current stable version and its SHA; never carry either from memory. `pinact` and the `sha_pinning_required` repository setting enforce it.

Hardening: set `permissions:` explicitly, default `contents: read`; `persist-credentials: false` on checkout unless the job pushes; never interpolate untrusted input into `run:`, pass it through `env:`; prefer `pull_request` over `pull_request_target`.

Before committing: `actionlint .github/workflows/` and `zizmor .github/workflows/`. Renovate manages the pins.

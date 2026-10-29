---
paths:
  - "**/*.{sh,bash,zsh}"
---

# Shell

Target bash. `set -euo pipefail` in any script longer than three lines. Quote every expansion. `$(...)`, not backticks. Check a command with `command -v`, then confirm it runs: a shim can resolve and still fail. Lint with `shellcheck`, format with `shfmt -i 2`. In scripts, call `/bin/rm` by its full path; an interactive alias is not a script's concern.

#!/usr/bin/env bash
# Prints CHANGELOG.md's section for a version: the release notes. Exits 1 when it has none, so a
# release cannot go out without its changelog.
#   bash cli/scripts/release-notes.sh 0.2.1
set -euo pipefail
v=${1:?usage: release-notes.sh <version>}
root=$(cd "$(dirname "$0")/../.." && pwd)
notes=$(awk -v h="## $v" '$0 == h { f = 1; next } /^## / { f = 0 } f' "$root/CHANGELOG.md" | sed -e '/./,$!d')
if [ -z "$(printf '%s' "$notes" | tr -d '[:space:]')" ]; then
  echo "CHANGELOG.md has no section for $v" >&2
  exit 1
fi
printf '%s\n' "$notes"

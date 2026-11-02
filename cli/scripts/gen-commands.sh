#!/usr/bin/env bash
# Writes docs/commands.md from a built makeit's help text, so the page cannot drift from the binary.
#   bash cli/scripts/gen-commands.sh <version>
set -euo pipefail
cli=$(cd "$(dirname "$0")/.." && pwd)
root=$(cd "$cli/.." && pwd)
version=${1:?usage: gen-commands.sh <version>}
mkdir -p "$cli/dist"
"$cli/scripts/go.sh" go generate ./... >/dev/null
"$cli/scripts/go.sh" sh -c "CGO_ENABLED=0 go build -o dist/makeit-docs -ldflags '-X makeit/internal/buildinfo.Version=$version' ./cmd/makeit"
bin="$cli/dist/makeit-docs"
out="$root/docs/commands.md"
{
  echo "# Commands"
  echo
  echo "Every command and flag, as \`makeit help\` prints them in makeit $version. Run \`makeit help <command>\` for the same text in your terminal."
  echo
  echo '```'
  NO_COLOR=1 "$bin" help
  echo '```'
  for c in setup init update doctor rollback uninstall version; do
    echo
    echo "## makeit $c"
    echo
    echo '```'
    NO_COLOR=1 "$bin" help "$c"
    echo '```'
  done
  for t in exit-codes environment; do
    echo
    echo "## $t"
    echo
    echo '```'
    NO_COLOR=1 "$bin" help "$t"
    echo '```'
  done
} >"$out"
/bin/rm -f "$bin"
echo "wrote $out"

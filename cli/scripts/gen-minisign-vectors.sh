#!/usr/bin/env bash
# Regenerate internal/minisign/testdata with the minisign CLI, in Docker. Throwaway keys: the
# secret keys stay inside the container.
set -euo pipefail
cli=$(cd "$(dirname "$0")/.." && pwd)
out="$cli/internal/minisign/testdata"
mkdir -p "$out"
docker run --rm -i -v "$out:/out" debian:trixie-slim bash -euo pipefail -c '
  apt-get update -qq >/dev/null && apt-get install -y -qq minisign >/dev/null
  minisign -v
  cd /tmp
  printf "sha256:abc  claude-stack-1.2.3-linux-x64\n" >checksums.txt
  minisign -G -W -p pub.key -s sec.key >/dev/null
  minisign -G -W -p other.pub -s other.sec >/dev/null
  minisign -S -s sec.key -m checksums.txt -t "claude-stack 1.2.3 checksums.txt" >/dev/null
  minisign -S -l -s sec.key -m checksums.txt -x legacy.minisig -t "claude-stack 1.2.3 checksums.txt" >/dev/null
  minisign -S -s other.sec -m checksums.txt -x other.minisig -t "claude-stack 1.2.3 checksums.txt" >/dev/null
  cp pub.key other.pub checksums.txt checksums.txt.minisig legacy.minisig other.minisig /out/
  chown -R '"$(id -u):$(id -g)"' /out
'
ls -l "$out"

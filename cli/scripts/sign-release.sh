#!/usr/bin/env bash
# Signs a draft release's checksums.txt with the release key and uploads the signature, so the
# release can be published. Run it in your own terminal: minisign asks for the key's password.
#   bash cli/scripts/sign-release.sh 0.2.1
# MAKEIT_KEY overrides the key (default ~/.minisign/makeit.key), MAKEIT_REPO the repository.
set -euo pipefail
v=${1:?usage: sign-release.sh <version>, for example 0.2.1}
v=${v#v}
repo=${MAKEIT_REPO:-youssefKadaouiAbbassi/make-it}
key=${MAKEIT_KEY:-$HOME/.minisign/makeit.key}
root=$(cd "$(dirname "$0")/../.." && pwd)
pub=$(tail -n 1 "$root/minisign.pub")
die() {
  echo "sign-release: $*" >&2
  exit 1
}

for c in gh minisign; do command -v "$c" >/dev/null || die "$c is not installed"; done
[ -f "$key" ] || die "no key at $key (set MAKEIT_KEY)"
draft=$(gh release view "v$v" -R "$repo" --json isDraft --jq .isDraft 2>/dev/null) || die "no release v$v on $repo"
[ "$draft" = true ] || die "v$v is already published; a published release cannot take a signature"
if gh release view "v$v" -R "$repo" --json assets --jq '.assets[].name' | grep -qx checksums.txt.minisig; then
  die "v$v already has checksums.txt.minisig"
fi

dir=$(mktemp -d)
trap '/bin/rm -rf "$dir"' EXIT
gh release download "v$v" -R "$repo" -p checksums.txt -D "$dir"
minisign -S -s "$key" -m "$dir/checksums.txt" -t "makeit $v checksums.txt"
# The same check makeit and install.sh make: the repository's public key and this exact comment.
out=$(minisign -V -P "$pub" -m "$dir/checksums.txt" 2>&1) || die "not uploaded: the signature does not verify against minisign.pub: $out"
grep -qx "Trusted comment: makeit $v checksums.txt" <<<"$out" || die "not uploaded: wrong trusted comment: $out"
gh release upload "v$v" "$dir/checksums.txt.minisig" -R "$repo"
echo "v$v signed: checksums.txt.minisig is on the draft. Publish when ready:"
echo "  gh release edit v$v -R $repo --draft=false --latest"

#!/bin/sh
# Install makeit: download the release binary for this machine, check it, then run
# `makeit self install --yes`, which puts it in ~/.local/bin and records the install.
#   curl -fsSL https://github.com/<repo>/releases/download/v<version>/install.sh | sh
# The copy attached to a release carries that release's version, repository and public key.
# MAKEIT_VERSION installs another version; MAKEIT_RELEASES_URL replaces https://github.com.
set -eu

version=${MAKEIT_VERSION:-@VERSION@}
repo=@REPO@
pubkey=@PUBKEY@
base=${MAKEIT_RELEASES_URL:-https://github.com}

fail() { echo "makeit install: $*" >&2; exit 1; }
case "$version$repo$pubkey" in *@*) fail "use the install.sh attached to a release";; esac
version=${version#v}

case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) fail "this script is for Linux and macOS; on Windows use install.ps1" ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch=x64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) fail "no makeit build for $(uname -m)" ;;
esac
name="makeit-$version-$os-$arch"

dir=$(mktemp -d)
trap 'rm -rf "$dir"' EXIT
get() { curl -fsSL --proto '=https' --tlsv1.2 -o "$dir/$1" "$base/$repo/releases/download/v$version/$1" || fail "could not download $1"; }
get "$name"
get checksums.txt
get checksums.txt.minisig

# The signature first, with this script's own key: a replaced binary must not vouch for itself.
if command -v minisign >/dev/null 2>&1; then
  out=$(minisign -V -P "$pubkey" -m "$dir/checksums.txt" -x "$dir/checksums.txt.minisig") || fail "the signature on checksums.txt does not verify"
  case "$out" in *"makeit $version checksums.txt"*) ;; *) fail "checksums.txt is signed for another release" ;; esac
  echo "signature verified"
else
  echo "signature not checked: install minisign to verify (sha256 only)"
fi

want=$(awk -v n="$name" '$2 == n { print $1 }' "$dir/checksums.txt")
[ -n "$want" ] || fail "$name is not in checksums.txt"
if command -v sha256sum >/dev/null 2>&1; then
  got=$(sha256sum "$dir/$name" | awk '{ print $1 }')
else
  got=$(shasum -a 256 "$dir/$name" | awk '{ print $1 }')
fi
[ "$got" = "$want" ] || fail "$name does not match checksums.txt"

chmod 755 "$dir/$name"
"$dir/$name" self install --yes

#!/bin/sh
# Install makeit: download the release binary for this machine, check it, then run
# `makeit self install --yes`, which puts it in ~/.local/bin and records the install.
#   curl -fsSL https://github.com/<repo>/releases/download/v<version>/install.sh | sh
# The copy attached to a release carries that release's version, repository and public key.
# MAKEIT_VERSION installs another version; MAKEIT_RELEASES_URL replaces https://github.com.
# Then it runs `makeit setup`, which shows its plan and asks to apply it, then asks about phone
# notifications once, on the terminal:
#   ... | sh -s -- --yes        set up without asking (MAKEIT_YES=1)
#   ... | sh -s -- --no-setup   install makeit only (MAKEIT_NO_SETUP=1)
# Any other flag goes to setup as is (--offline, --catalog <src>, --dry-run).
set -eu

yes=${MAKEIT_YES:-}
no_setup=${MAKEIT_NO_SETUP:-}
setup_args=""
for a in "$@"; do
  case "$a" in
    --yes | -y) yes=1 ;;
    --no-setup) no_setup=1 ;;
    *) setup_args="$setup_args $a" ;;
  esac
done

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
installed="$HOME/.local/bin/makeit"
# exec below skips the EXIT trap, so the download goes now (unless it is all there is to run).
if [ -x "$installed" ]; then rm -rf "$dir"; else installed="$dir/$name"; fi

if [ -n "$no_setup" ]; then
  echo "next: makeit setup"
  exit 0
fi
# The script itself arrives on stdin (curl | sh), so setup's question reads the terminal directly.
# shellcheck disable=SC2086 # setup_args is a list of flags, split on purpose
if [ -n "$yes" ]; then
  exec "$installed" setup --yes $setup_args
elif (: </dev/tty) 2>/dev/null; then
  exec "$installed" setup $setup_args </dev/tty
fi
echo "next: makeit setup  (no terminal to ask on; or rerun with: sh -s -- --yes)"

#!/usr/bin/env bash
# Tests install.sh in a throwaway ubuntu container against a fake signed release (tools/fakerelease,
# a throwaway key): without minisign it says the signature was not checked and installs on the
# sha256 alone; with minisign it verifies first; with the wrong key it stops and installs nothing.
#   bash cli/scripts/test-install.sh
set -euo pipefail
cli=$(cd "$(dirname "$0")/.." && pwd)
root=$(cd "$cli/.." && pwd)
mkdir -p "$cli/dist"
"$cli/scripts/go.sh" go generate ./... >/dev/null # the catalog data a build embeds
rel=$(mktemp -d "$cli/dist/install-test.XXXXXX")
trap '/bin/rm -rf "$rel"' EXIT
"$cli/scripts/go.sh" go build -o "dist/$(basename "$rel")/fakerelease" ./tools/fakerelease
seed=$(od -An -tx1 -N32 /dev/urandom | tr -d ' \n')
other=$(od -An -tx1 -N32 /dev/urandom | tr -d ' \n')
pub=$("$rel/fakerelease" pub -seed "$seed")
wrong=$("$rel/fakerelease" pub -seed "$other")
"$cli/scripts/go.sh" sh -c "CGO_ENABLED=0 go build -o dist/$(basename "$rel")/makeit-1.1.0 -ldflags '-X makeit/internal/buildinfo.Version=1.1.0 \
-X makeit/internal/buildinfo.Repo=test/makeit -X makeit/internal/buildinfo.KeyCurrent=$pub' ./cmd/makeit"
"$rel/fakerelease" release -seed "$seed" -repo test/makeit -version 1.1.0 -target linux-x64 -binary "$rel/makeit-1.1.0" -dir "$rel/site"
"$rel/fakerelease" release -seed "$seed" -repo test/makeit -version 1.2.0 -target linux-x64 -binary "$rel/makeit-1.1.0" -dir "$rel/other"
v11=$(dirname "$(find "$rel/site" -name checksums.txt)"); v12=$(dirname "$(find "$rel/other" -name checksums.txt)")
# tampered: the binary changed after checksums.txt was made. signed-for-1.2.0: a genuine signature
# and list, but for another release, served as 1.1.0.
cp -r "$rel/site" "$rel/tampered"; printf x >>"$rel/tampered/${v11#"$rel/site/"}/makeit-1.1.0-linux-x64"
cp -r "$rel/site" "$rel/signed12"; cp "$v12/checksums.txt" "$v12/checksums.txt.minisig" "$rel/signed12/${v11#"$rel/site/"}/"
# unsigned: a release published before the owner uploaded checksums.txt.minisig.
cp -r "$rel/site" "$rel/unsigned"; /bin/rm "$rel/unsigned/${v11#"$rel/site/"}/checksums.txt.minisig"
# What release.yml does to the attached copy: fill in the version, repository and key.
fill() { sed -e "s|@VERSION@|1.1.0|" -e "s|@REPO@|test/makeit|" -e "s|@PUBKEY@|$1|" "$root/install.sh"; }
fill "$pub" >"$rel/install.sh"
fill "$wrong" >"$rel/install-wrong.sh"
echo 1.1.0 >"$rel/latest"

docker run --rm -i -v "$rel:/rel:ro" ubuntu:24.04 bash -s <<'INNER'
set -euo pipefail
apt-get update -qq && apt-get install -y -qq curl ca-certificates >/dev/null
cp /etc/ssl/certs/ca-certificates.crt /tmp/bundle.pem
port=8443
for site in site tampered signed12 unsigned; do
  /rel/fakerelease serve -dir "/rel/$site" -repo test/makeit -addr "127.0.0.1:$port" -ca "/tmp/ca-$site.pem" -latest /rel/latest &
  i=0; while [ ! -s "/tmp/ca-$site.pem" ]; do i=$((i + 1)); [ "$i" -lt 30 ] || exit 1; sleep 1; done
  cat "/tmp/ca-$site.pem" >>/tmp/bundle.pem
  port=$((port + 1))
done
run() { HOME=$1 CURL_CA_BUNDLE=/tmp/bundle.pem MAKEIT_RELEASES_URL=https://127.0.0.1:${3:-8443} MAKEIT_NO_SETUP=1 sh "$2"; }
# runsetup: install.sh going on to setup, with its flags (curl | sh -s -- <flags>); no terminal here.
runsetup() { h=$1; shift; HOME=$h CURL_CA_BUNDLE=/tmp/bundle.pem MAKEIT_RELEASES_URL=https://127.0.0.1:8443 sh -s -- "$@" </rel/install.sh; }
same() { [ "$(sha256sum <"$1")" = "$(sha256sum </rel/makeit-1.1.0)" ]; }
mkdir -p /tmp/h1 /tmp/h2 /tmp/h3 /tmp/h4 /tmp/h5 /tmp/h6
out=$(run /tmp/h1 /rel/install.sh 2>&1); echo "$out"
grep -q "signature not checked: install minisign to verify (sha256 only)" <<<"$out"
same /tmp/h1/.local/bin/makeit
echo "install.sh without minisign: ok"
if out=$(run /tmp/h4 /rel/install.sh 8444 2>&1); then echo "a tampered binary passed: $out"; exit 1; fi
grep -q "does not match checksums.txt" <<<"$out"
[ ! -e /tmp/h4/.local/bin/makeit ]
echo "install.sh with a tampered binary: refused, nothing installed: ok"
if out=$(run /tmp/h6 /rel/install.sh 8446 2>&1); then echo "a release without its signature passed: $out"; exit 1; fi
grep -q "could not download checksums.txt.minisig" <<<"$out"
[ ! -e /tmp/h6/.local/bin/makeit ]
echo "install.sh with no checksums.txt.minisig: refused, nothing installed: ok"
apt-get install -y -qq minisign >/dev/null
out=$(run /tmp/h2 /rel/install.sh 2>&1); echo "$out"
grep -q "signature verified" <<<"$out"
same /tmp/h2/.local/bin/makeit
echo "install.sh with minisign: ok"
if out=$(run /tmp/h3 /rel/install-wrong.sh 2>&1); then echo "the wrong key passed: $out"; exit 1; fi
grep -q "does not verify" <<<"$out"
[ ! -e /tmp/h3/.local/bin/makeit ]
echo "install.sh with the wrong key: refused, nothing installed: ok"
if out=$(run /tmp/h5 /rel/install.sh 8445 2>&1); then echo "a signature for 1.2.0 passed as 1.1.0: $out"; exit 1; fi
grep -q "signed for another release" <<<"$out"
[ ! -e /tmp/h5/.local/bin/makeit ]
echo "install.sh with a signature for another release: refused, nothing installed: ok"
mkdir -p /tmp/h7 /tmp/h8 /tmp/h9
out=$(runsetup /tmp/h7 2>&1); echo "$out"
grep -q "next: makeit setup  (no terminal to ask on" <<<"$out"
same /tmp/h7/.local/bin/makeit
echo "install.sh with no terminal: installs, then says how to set up: ok"
out=$(runsetup /tmp/h8 --yes --dry-run 2>&1); echo "$out"
grep -qE "^[0-9]+ would-change$" <<<"$out"
echo "install.sh --yes --dry-run: setup ran with the flags: ok"
out=$(runsetup /tmp/h9 --no-setup 2>&1)
grep -q "^next: makeit setup$" <<<"$out"
same /tmp/h9/.local/bin/makeit
echo "install.sh --no-setup: installs only: ok"
INNER

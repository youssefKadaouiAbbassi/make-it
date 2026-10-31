#!/usr/bin/env bash
# Tests install.sh in a throwaway ubuntu container against a fake signed release (tools/fakerelease,
# a throwaway key): without minisign it says the signature was not checked and installs on the
# sha256 alone; with minisign it verifies first; with the wrong key it stops and installs nothing.
#   bash cli/scripts/test-install.sh
set -euo pipefail
cli=$(cd "$(dirname "$0")/.." && pwd)
root=$(cd "$cli/.." && pwd)
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
# What release.yml does to the attached copy: fill in the version, repository and key.
fill() { sed -e "s|@VERSION@|1.1.0|" -e "s|@REPO@|test/makeit|" -e "s|@PUBKEY@|$1|" "$root/install.sh"; }
fill "$pub" >"$rel/install.sh"
fill "$wrong" >"$rel/install-wrong.sh"
echo 1.1.0 >"$rel/latest"

docker run --rm -i -v "$rel:/rel:ro" ubuntu:24.04 bash -s <<'INNER'
set -euo pipefail
apt-get update -qq && apt-get install -y -qq curl ca-certificates >/dev/null
/rel/fakerelease serve -dir /rel/site -repo test/makeit -addr 127.0.0.1:8443 -ca /tmp/ca.pem -latest /rel/latest &
i=0; while [ ! -s /tmp/ca.pem ]; do i=$((i + 1)); [ "$i" -lt 30 ] || exit 1; sleep 1; done
cat /etc/ssl/certs/ca-certificates.crt /tmp/ca.pem >/tmp/bundle.pem
run() { HOME=$1 CURL_CA_BUNDLE=/tmp/bundle.pem MAKEIT_RELEASES_URL=https://127.0.0.1:8443 sh "$2"; }
same() { [ "$(sha256sum <"$1")" = "$(sha256sum </rel/makeit-1.1.0)" ]; }
mkdir -p /tmp/h1 /tmp/h2 /tmp/h3
out=$(run /tmp/h1 /rel/install.sh 2>&1); echo "$out"
grep -q "signature not checked: install minisign to verify (sha256 only)" <<<"$out"
same /tmp/h1/.local/bin/makeit
echo "install.sh without minisign: ok"
apt-get install -y -qq minisign >/dev/null
out=$(run /tmp/h2 /rel/install.sh 2>&1); echo "$out"
grep -q "signature verified" <<<"$out"
same /tmp/h2/.local/bin/makeit
echo "install.sh with minisign: ok"
if out=$(run /tmp/h3 /rel/install-wrong.sh 2>&1); then echo "the wrong key passed: $out"; exit 1; fi
grep -q "does not verify" <<<"$out"
[ ! -e /tmp/h3/.local/bin/makeit ]
echo "install.sh with the wrong key: refused, nothing installed: ok"
INNER

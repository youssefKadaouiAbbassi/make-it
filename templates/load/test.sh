#!/usr/bin/env bash
# Tests the Locust threshold hook in Docker (locustio/locust:2.46.7, nothing installed):
# a tiny HTTP server in the same container answers / and /health; three headless runs:
#   1. generous thresholds -> exit 0
#   2. p95 threshold of 0 ms -> exit 1 (the hook, not a request failure)
#   3. a host with no server -> exit 1 (failure ratio)
set -uo pipefail
here=$(cd "$(dirname "$0")" && pwd)
fail=0
img=locustio/locust:2.46.7
run() { # name expected-exit env...
  local name=$1 want=$2; shift 2
  docker run --rm -v "$here:/mnt/locust:ro" --entrypoint bash "$img" -c \
    "cd /tmp && (python3 -m http.server 8089 >/dev/null 2>&1 &) && sleep 1 && mkdir -p w && touch w/health && cd w && (python3 -m http.server 8090 >/dev/null 2>&1 &) && sleep 1 && env $* locust -f /mnt/locust/locustfile.py --headless -u 2 -r 2 -t 4s --host http://127.0.0.1:8090 --only-summary >/dev/null 2>&1; echo \"rc=\$?\"" | tail -1 | tee /dev/stderr | grep -q "rc=$want" && echo "ok   $name" || { echo "FAIL $name (wanted exit $want)"; fail=1; }
}
run generous 0 LOCUST_P95_MS=5000 LOCUST_FAIL_RATIO=0.5
run p95-hook 1 LOCUST_P95_MS=0 LOCUST_FAIL_RATIO=0.5
docker run --rm -v "$here:/mnt/locust:ro" --entrypoint bash "$img" -c \
  "LOCUST_P95_MS=5000 LOCUST_FAIL_RATIO=0.01 locust -f /mnt/locust/locustfile.py --headless -u 2 -r 2 -t 4s --host http://127.0.0.1:9 --only-summary >/dev/null 2>&1; echo rc=\$?" | tail -1 | grep -q 'rc=1' && echo "ok   fail-ratio" || { echo "FAIL fail-ratio"; fail=1; }
[ "$fail" = 0 ] && echo "LOAD OK" || { echo "LOAD FAILURES"; exit 1; }

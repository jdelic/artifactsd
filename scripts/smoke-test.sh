#!/usr/bin/env bash
# scripts/smoke-test.sh — spawns a real artifactsd serve process (a separate
# OS process over a real TCP listener, not the in-process internal/testserver
# used by the e2e tests in artifactsd-3ln.2) and drives it end-to-end through
# the compiled artifactctl binary, plus curl for the handful of things no CLI
# command exposes directly (raw HTTP status/content over /c). See
# artifactsd-3ln.3.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ARTIFACTSD="${ARTIFACTSD_BIN:-$ROOT/dist/artifactsd}"
ARTIFACTCTL="${ARTIFACTCTL_BIN:-$ROOT/dist/artifactctl}"
PORT="${SMOKE_PORT:-18123}"
BASE_URL="http://127.0.0.1:${PORT}"
# No tok_ prefix required (design/12 §2.1) — deliberately using an
# operator-shaped secret here to exercise that normalization path too.
BOOTSTRAP_TOKEN="smoke-$(date +%s)-$$"

WORKDIR="$(mktemp -d)"
DATA_DIR="$WORKDIR/data"
mkdir -p "$DATA_DIR"

SERVER_PID=""
cleanup() {
  if [[ -n "$SERVER_PID" ]] && kill -0 "$SERVER_PID" 2>/dev/null; then
    kill "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
  fi
  rm -rf "$WORKDIR"
}
trap cleanup EXIT

log() { echo "[smoke] $*" >&2; }
fail() {
  echo "[smoke] FAIL: $*" >&2
  if [[ -f "$WORKDIR/server.log" ]]; then
    echo "[smoke] --- server log ---" >&2
    cat "$WORKDIR/server.log" >&2
  fi
  exit 1
}

[[ -x "$ARTIFACTSD" ]] || fail "missing $ARTIFACTSD — run 'make build' first"
[[ -x "$ARTIFACTCTL" ]] || fail "missing $ARTIFACTCTL — run 'make build' first"
command -v curl >/dev/null || fail "curl is required"
command -v jq >/dev/null || fail "jq is required"

log "starting artifactsd on $BASE_URL (data=$DATA_DIR)"
ARTIFACTSD_BOOTSTRAP_TOKEN="$BOOTSTRAP_TOKEN" \
  "$ARTIFACTSD" serve --listen "127.0.0.1:${PORT}" --base-url "$BASE_URL" --data "$DATA_DIR" \
  >"$WORKDIR/server.log" 2>&1 &
SERVER_PID=$!

log "waiting for /healthz"
healthy=false
for _ in $(seq 1 50); do
  if curl -fsS "$BASE_URL/healthz" >/dev/null 2>&1; then
    healthy=true
    break
  fi
  if ! kill -0 "$SERVER_PID" 2>/dev/null; then
    fail "artifactsd exited before becoming healthy"
  fi
  sleep 0.2
done
[[ "$healthy" == true ]] || fail "server never became healthy within 10s"
log "server healthy"

export ARTIFACTSD_BASE_URL="$BASE_URL"
export ARTIFACTSD_TOKEN="$BOOTSTRAP_TOKEN"

ctl() { "$ARTIFACTCTL" "$@"; }

log "collection create"
ctl collection create smoke >/dev/null

log "collection list shows it"
ctl collection list | grep -q '^smoke[[:space:]]' || fail "collection list missing 'smoke'"

log "put a file"
echo "hello from smoke test $$" >"$WORKDIR/hello.txt"
ctl put smoke/hello.txt "$WORKDIR/hello.txt" >/dev/null

log "get roundtrips the same bytes"
ctl get smoke/hello.txt -o "$WORKDIR/hello.downloaded.txt" >/dev/null
cmp -s "$WORKDIR/hello.txt" "$WORKDIR/hello.downloaded.txt" || fail "get content mismatch"

log "meta get/set"
ctl meta get smoke/hello.txt >/dev/null
ctl meta set smoke/hello.txt --crawlable >/dev/null
ctl meta get smoke/hello.txt --output json | jq -e '.effective.crawlable == true' >/dev/null \
  || fail "meta set --crawlable did not take effect"

log "tree"
ctl tree smoke | grep -q hello.txt || fail "tree missing hello.txt"

log "share create/list/revoke"
share_id=$(ctl share create smoke/hello.txt --output json | jq -r '.share_id')
[[ -n "$share_id" && "$share_id" != "null" ]] || fail "share create did not return a share_id"
ctl share list smoke | grep -q "$share_id" || fail "share list missing $share_id"
ctl share revoke "$share_id" >/dev/null

log "token create/list/revoke"
token_id=$(ctl token create smoke-test-token --scope collections:read --output json | jq -r '.id')
[[ -n "$token_id" && "$token_id" != "null" ]] || fail "token create did not return an id"
ctl token list | grep -q "^${token_id}[[:space:]]" || fail "token list missing id $token_id"
ctl token revoke "$token_id" >/dev/null

log "maintenance cleanup is expected to fail (no janitor wired yet — artifactsd-n18)"
if ctl maintenance cleanup >/dev/null 2>"$WORKDIR/cleanup.err"; then
  fail "maintenance cleanup unexpectedly succeeded — once artifactsd-n18 wires a janitor, update this script to expect success"
fi
grep -q "cleanup is not configured" "$WORKDIR/cleanup.err" \
  || fail "unexpected cleanup error: $(cat "$WORKDIR/cleanup.err")"

log "curl-verify public serving over /c (no CLI command exercises raw HTTP semantics)"
curl -fsS -o "$WORKDIR/hello.curl.txt" "$BASE_URL/c/smoke/hello.txt" || fail "curl GET /c/smoke/hello.txt failed"
expected_sha=$(sha256sum "$WORKDIR/hello.txt" | awk '{print $1}')
actual_sha=$(sha256sum "$WORKDIR/hello.curl.txt" | awk '{print $1}')
[[ "$expected_sha" == "$actual_sha" ]] || fail "SHA256 mismatch: expected $expected_sha, got $actual_sha"

log "curl-verify an unauthenticated management API request is rejected"
status=$(curl -s -o /dev/null -w '%{http_code}' "$BASE_URL/api/v1/collections")
[[ "$status" == "401" ]] || fail "expected 401 for unauthenticated /api/v1/collections, got $status"

log "collection delete (cascade)"
ctl collection delete smoke --force >/dev/null

log "all smoke checks passed"

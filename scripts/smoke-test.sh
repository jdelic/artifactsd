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
ctl collection create smoke --list >/dev/null

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

log "large-upload session: begin, capability PUT (no token), then re-PUT is rejected"
echo "large upload bytes $$" >"$WORKDIR/big.bin"
upload=$(curl -fsS -X POST "$BASE_URL/api/v1/collections/smoke/uploads" \
  -H "Authorization: Bearer $BOOTSTRAP_TOKEN" -H "Content-Type: application/json" \
  -d '{"path":"big.bin"}') || fail "begin upload session failed"
upload_url=$(echo "$upload" | jq -r '.upload_url')
[[ -n "$upload_url" && "$upload_url" != "null" ]] || fail "begin upload did not return an upload_url"
curl -fsS -X PUT "$upload_url" --data-binary "@$WORKDIR/big.bin" >/dev/null \
  || fail "capability PUT to $upload_url failed"
ctl get smoke/big.bin -o "$WORKDIR/big.downloaded.bin" >/dev/null
cmp -s "$WORKDIR/big.bin" "$WORKDIR/big.downloaded.bin" || fail "large-upload content mismatch"
second_put_status=$(curl -s -o /dev/null -w '%{http_code}' -X PUT "$upload_url" --data-binary "@$WORKDIR/big.bin")
[[ "$second_put_status" == "409" ]] || fail "second PUT to a consumed upload_id = $second_put_status, want 409"

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

log "maintenance cleanup (janitor wired via artifactsd-ltb/zgj/n18)"
ctl maintenance cleanup --output json | jq -e 'has("cleanup") and (.cleanup | has("nodes_deleted"))' >/dev/null \
  || fail "maintenance cleanup did not return a janitor summary"

log "curl-verify public serving over /c (no CLI command exercises raw HTTP semantics)"
curl -fsS -o "$WORKDIR/hello.curl.txt" "$BASE_URL/c/smoke/hello.txt" || fail "curl GET /c/smoke/hello.txt failed"
expected_sha=$(sha256sum "$WORKDIR/hello.txt" | awk '{print $1}')
actual_sha=$(sha256sum "$WORKDIR/hello.curl.txt" | awk '{print $1}')
[[ "$expected_sha" == "$actual_sha" ]] || fail "SHA256 mismatch: expected $expected_sha, got $actual_sha"

log "curl-verify directory listing over /c (JSON)"
listing=$(curl -fsS "$BASE_URL/c/smoke/?format=json") || fail "curl GET /c/smoke/?format=json failed"
echo "$listing" | jq -e '.entries[] | select(.name == "hello.txt")' >/dev/null \
  || fail "directory listing missing hello.txt entry: $listing"

log "curl-verify agent crawl API"
crawl=$(curl -fsS -H "Authorization: Bearer $BOOTSTRAP_TOKEN" "$BASE_URL/api/v1/collections/smoke/crawl") \
  || fail "curl GET /api/v1/collections/smoke/crawl failed"
echo "$crawl" | jq -e '.entries[] | select(.path == "hello.txt")' >/dev/null \
  || fail "crawl API missing hello.txt entry (set crawlable earlier): $crawl"

log "curl-verify root robots.txt (deliberately no per-collection one, RFC 9309)"
robots_root=$(curl -fsS "$BASE_URL/robots.txt") || fail "curl GET /robots.txt failed"
echo "$robots_root" | grep -q '^Disallow: /api/$' || fail "root robots.txt missing Disallow: /api/: $robots_root"

log "curl-verify sitemap.xml endpoints return a well-formed urlset"
sitemap_root=$(curl -fsS "$BASE_URL/sitemap.xml") || fail "curl GET /sitemap.xml failed"
echo "$sitemap_root" | grep -q '<urlset' || fail "root sitemap.xml missing <urlset>: $sitemap_root"
curl -fsS "$BASE_URL/c/smoke/sitemap.xml" | grep -q '<urlset' \
  || fail "per-collection sitemap.xml missing <urlset>"

log "curl-verify an unauthenticated management API request is rejected"
status=$(curl -s -o /dev/null -w '%{http_code}' "$BASE_URL/api/v1/collections")
[[ "$status" == "401" ]] || fail "expected 401 for unauthenticated /api/v1/collections, got $status"

log "collection delete (cascade)"
ctl collection delete smoke --force >/dev/null

log "all smoke checks passed"

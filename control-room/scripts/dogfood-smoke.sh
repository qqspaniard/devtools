#!/usr/bin/env bash
#
# dogfood-smoke.sh — drive the built control-room binary through the full usable
# loop against a throwaway state directory, exercising the browser decision via
# curl (no real browser). Verifies: publish -> approve -> poll(+digest) ->
# claim(win) -> replay(reject) -> restart -> durable decision -> replay(reject).
#
# Usage: ./scripts/dogfood-smoke.sh
# Requires: go, curl, python3.
set -euo pipefail

cd "$(dirname "$0")/.."

BIN="$(mktemp -d)/control-room"
go build -o "$BIN" ./cmd/control-room

SD="$(mktemp -d)/state"
mkdir -p "$SD"
COOKIES="$SD/cookies.txt"
cleanup() {
  [[ -n "${SERVE_PID:-}" ]] && kill "$SERVE_PID" 2>/dev/null || true
  wait 2>/dev/null || true
  rm -rf "$SD"
}
trap cleanup EXIT

start_broker() {
  "$BIN" serve --state-dir "$SD" >"$SD/serve.out" 2>"$SD/serve.err" &
  SERVE_PID=$!
  # Wait for the review base URL to be announced.
  for _ in $(seq 1 50); do
    if grep -q 'review base:' "$SD/serve.err" 2>/dev/null; then break; fi
    sleep 0.1
  done
  BASE="$(grep 'review base:' "$SD/serve.err" | sed 's/.*review base: //')"
}

fail() { echo "SMOKE FAIL: $*" >&2; exit 1; }

echo "== starting broker =="
start_broker
echo "review base: $BASE"

echo "== session create =="
SID="$("$BIN" session create --state-dir "$SD" --workspace-id ws-devtools --workspace-name devtools \
  | python3 -c 'import sys,json;print(json.load(sys.stdin)["id"])')"
echo "session: $SID"

echo "== plan publish =="
python3 -c "import json;p=json.load(open('examples/sample-plan.json'));p['session_id']='$SID';print(json.dumps(p))" \
  | "$BIN" plan publish --state-dir "$SD" >/dev/null

echo "== open (no-browser) =="
OPEN_URL="$("$BIN" open --state-dir "$SD" --session "$SID" --no-browser)"

echo "== browser bootstrap -> cookie =="
curl -s -c "$COOKIES" -o /dev/null "$OPEN_URL"
grep -q cr_session "$COOKIES" || fail "no session cookie issued"

echo "== fetch page, extract CSRF =="
PAGE="$(curl -s -b "$COOKIES" "$BASE/session/$SID")"
CSRF="$(echo "$PAGE" | grep -o 'name="csrf" value="[0-9a-f]*"' | sed 's/.*value="//;s/"//')"
[[ -n "$CSRF" ]] || fail "no CSRF token on page"

echo "== approve via same-origin POST =="
APPROVE="$(curl -s -b "$COOKIES" -X POST "$BASE/api/decide" \
  -H "Content-Type: application/json" -H "Origin: $BASE" \
  -d "{\"csrf\":\"$CSRF\",\"session\":\"$SID\",\"revision\":1,\"decision\":\"approve\",\"selected_action_ids\":[\"action-1\",\"action-2\"]}")"
echo "$APPROVE" | grep -q '"ok":true' || fail "approve rejected: $APPROVE"

echo "== poll (expect approve + digest) =="
POLL="$("$BIN" decision poll --state-dir "$SD" --session "$SID")"
DIGEST="$(echo "$POLL" | python3 -c 'import sys,json;print(json.load(sys.stdin)["decision"]["digest"])')"
[[ "$DIGEST" == sha256:* ]] || fail "no digest in poll: $POLL"

echo "== claim (first, should win) =="
"$BIN" approval claim --state-dir "$SD" --session "$SID" --digest "$DIGEST" >/dev/null \
  || fail "first claim did not win"

echo "== claim replay (should fail closed) =="
if "$BIN" approval claim --state-dir "$SD" --session "$SID" --digest "$DIGEST" >/dev/null 2>&1; then
  fail "replay claim unexpectedly succeeded"
fi

echo "== restart broker =="
kill "$SERVE_PID"; wait "$SERVE_PID" 2>/dev/null || true
start_broker

echo "== decision durable after restart =="
POLL2="$("$BIN" decision poll --state-dir "$SD" --session "$SID")"
echo "$POLL2" | grep -q '"kind": "approve"' || fail "decision not durable after restart: $POLL2"

echo "== claim replay after restart (should fail closed) =="
if "$BIN" approval claim --state-dir "$SD" --session "$SID" --digest "$DIGEST" >/dev/null 2>&1; then
  fail "post-restart replay claim unexpectedly succeeded"
fi

echo "SMOKE PASS"

#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${BASE_URL:-http://localhost:8080}"
PROMPT="use calculator to compute 13 * 7"
EXPECTED_ANSWER="13 * 7 = 91"

require_cmd() {
  local cmd="$1"
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "[FAIL] missing command: $cmd" >&2
    exit 1
  fi
}

fail() {
  echo "[FAIL] $1" >&2
  if [[ "${2:-}" != "" ]]; then
    echo "----- response -----" >&2
    printf '%s\n' "$2" >&2
    echo "--------------------" >&2
  fi
  exit 1
}

ok() {
  echo "[OK] $1"
}

contains() {
  local haystack="$1"
  local needle="$2"
  [[ "$haystack" == *"$needle"* ]]
}

json_field() {
  local field="$1"
  python3 -c 'import json,sys; print(json.load(sys.stdin)[sys.argv[1]])' "$field"
}

post_json() {
  local path="$1"
  local body="$2"
  curl -sS -X POST "$BASE_URL$path" \
    -H 'Content-Type: application/json' \
    -d "$body"
}

post_sse() {
  local path="$1"
  local body="$2"
  curl -sS -N -X POST "$BASE_URL$path" \
    -H 'Content-Type: application/json' \
    -H 'Accept: text/event-stream' \
    -d "$body"
}

create_session() {
  local body
  local id
  body="$(post_json /v1/sessions "{\"input\":\"$PROMPT\"}")" || fail "create session request failed"
  id="$(printf '%s' "$body" | json_field id)" || fail "create session response has no id" "$body"
  if [[ -z "$id" ]]; then
    fail "create session returned empty id" "$body"
  fi
  printf '%s' "$id"
}

require_cmd curl
require_cmd python3

echo "Testing App API at $BASE_URL"

health_body="$(curl -sS "$BASE_URL/v1/health")" || fail "health request failed"
ok "health endpoint reachable"

run_session_id="$(create_session)"
ok "created sync session $run_session_id"

run_body="$(post_json "/v1/sessions/$run_session_id/runs" "{\"input\":\"$PROMPT\"}")" || fail "sync run request failed"
contains "$run_body" "$EXPECTED_ANSWER" || fail "sync run did not contain expected answer" "$run_body"
ok "sync run returned expected answer"

stream_session_id="$(create_session)"
ok "created stream session $stream_session_id"

stream_body="$(post_sse "/v1/sessions/$stream_session_id/runs/stream" "{\"input\":\"$PROMPT\"}")" || fail "stream request failed"
contains "$stream_body" "event:tool_call" || contains "$stream_body" "event: tool_call" || fail "stream missing tool_call event" "$stream_body"
contains "$stream_body" "event:tool_result" || contains "$stream_body" "event: tool_result" || fail "stream missing tool_result event" "$stream_body"
contains "$stream_body" "event:done" || contains "$stream_body" "event: done" || fail "stream missing done event" "$stream_body"
contains "$stream_body" "$EXPECTED_ANSWER" || fail "stream missing expected answer" "$stream_body"
ok "stream returned tool and done events"

error_body="$(post_sse "/v1/sessions/$stream_session_id/runs/stream" '{"input":""}')" || fail "stream validation request failed"
contains "$error_body" "event:error" || contains "$error_body" "event: error" || fail "validation response missing error event" "$error_body"
contains "$error_body" "input must not be empty" || fail "validation response missing error message" "$error_body"
ok "stream validation error returned as SSE"

ok "manual SSE smoke test passed"

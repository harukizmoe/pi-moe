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

get_json() {
  local path="$1"
  curl -sS "$BASE_URL$path"
}

json_assert_session_detail() {
  DETAIL_JSON="$1" SESSION_ID="$2" EXPECTED_ANSWER="$EXPECTED_ANSWER" python3 - <<'PY'
import json
import os
import sys

payload = json.loads(os.environ["DETAIL_JSON"])
if payload.get("id") != os.environ["SESSION_ID"]:
    raise SystemExit("session detail returned wrong id")
messages = payload.get("messages")
if not isinstance(messages, list) or len(messages) != 4:
    raise SystemExit(f"session detail message count = {len(messages) if isinstance(messages, list) else 'invalid'}")
if messages[0].get("role") != "user" or messages[0].get("content") != "use calculator to compute 13 * 7":
    raise SystemExit("session detail missing user prompt")
tool_calls = messages[1].get("tool_calls") or []
if messages[1].get("role") != "assistant" or len(tool_calls) != 1 or tool_calls[0].get("tool") != "calculator":
    raise SystemExit("session detail missing assistant calculator tool call")
if messages[2].get("role") != "tool" or messages[2].get("tool") != "calculator" or messages[2].get("content") != "91":
    raise SystemExit("session detail missing calculator tool result")
if messages[3].get("role") != "assistant" or messages[3].get("content") != os.environ["EXPECTED_ANSWER"]:
    raise SystemExit("session detail missing final assistant answer")
PY
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

detail_body="$(get_json "/v1/sessions/$run_session_id")" || fail "session detail request failed"
json_assert_session_detail "$detail_body" "$run_session_id" || fail "session detail response is invalid" "$detail_body"
ok "session detail returned persisted transcript"

resume_body="$(post_json "/v1/sessions/$run_session_id/runs" '{"input":"what was the previous result?"}')" || fail "resume run request failed"
contains "$resume_body" "previous result was 91" || fail "resume run did not use restored transcript" "$resume_body"
ok "resume run used restored transcript"

resume_detail_body="$(get_json "/v1/sessions/$run_session_id")" || fail "resume detail request failed"
contains "$resume_detail_body" "previous result was 91" || fail "resume detail missing second answer" "$resume_detail_body"
ok "session detail returned resumed transcript"

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

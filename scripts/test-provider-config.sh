#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${BASE_URL:-http://localhost:8080}"

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

json_assert_provider_ready() {
  PROVIDER_JSON="$1" python3 - <<'PY'
import json
import os

payload = json.loads(os.environ["PROVIDER_JSON"])
for key in ("name", "type", "model", "ready", "error"):
    if key not in payload:
        raise SystemExit(f"missing field: {key}")
for forbidden in ("api_key", "apiKey", "key", "secret"):
    if forbidden in payload:
        raise SystemExit(f"response leaks key material field: {forbidden}")
if not isinstance(payload["ready"], bool):
    raise SystemExit("ready must be boolean")
if payload["ready"] and payload["error"] != "":
    raise SystemExit("ready provider must have empty error")
print(f"provider={payload['name']} type={payload['type']} model={payload['model']} ready={payload['ready']}")
PY
}

require_cmd curl
require_cmd python3

echo "Testing provider diagnostics at $BASE_URL"

body="$(curl -sS "$BASE_URL/v1/providers/current")" || fail "provider diagnostics request failed"
summary="$(json_assert_provider_ready "$body")" || fail "provider diagnostics response is invalid" "$body"
ok "$summary"
ok "provider diagnostics smoke test passed"

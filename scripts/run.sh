#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

load_env_file() {
  local file="$1"
  if [[ -f "$file" ]]; then
    set -a
    # shellcheck disable=SC1090
    source "$file"
    set +a
  fi
}

if [[ ! -f ".env" ]]; then
  cp ".env.example" ".env"
  echo "Created .env from .env.example; fill in API keys if you use real providers." >&2
fi

load_env_file ".env"
load_env_file ".env.local"

if [[ ! -f "configs/providers.yaml" ]]; then
  cp "configs/providers.example.yaml" "configs/providers.yaml"
  echo "Created configs/providers.yaml from configs/providers.example.yaml" >&2
fi

if [[ "$#" -gt 0 ]]; then
  exec go run ./cmd/cli "$@"
fi

if [[ -n "${PIMOE_PROMPT:-}" ]]; then
  exec go run ./cmd/cli "$PIMOE_PROMPT"
fi

exec go run ./cmd/cli "use calculator to compute 13 * 7"

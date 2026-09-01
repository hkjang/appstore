#!/usr/bin/env bash
set -Eeuo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd -- "$script_dir/.." && pwd)"
cd "$repo_root"

expected=$'BOOTSTRAP_ADMIN\nBOOTSTRAP_ADMIN_PASSWORD\nENCRYPTION_KEY\nPOSTGRES_DSN'
actual="$({
  find cmd internal -type f -name '*.go' ! -name '*_test.go' -print0 2>/dev/null \
    | xargs -0 grep -hoE 'os\.(Getenv|LookupEnv)\("[A-Z][A-Z0-9_]*"\)' 2>/dev/null \
    | sed -E 's/.*\("([A-Z][A-Z0-9_]*)"\)/\1/'
} | sort -u)"

if [[ "$actual" != "$expected" ]]; then
  echo "error: application environment contract changed" >&2
  echo "expected:" >&2
  echo "$expected" >&2
  echo "actual:" >&2
  echo "${actual:-<none>}" >&2
  exit 1
fi

compose_env="$(grep -E '^      [A-Z][A-Z0-9_]+:' docker-compose.yml | sed -E 's/^      ([A-Z][A-Z0-9_]+):.*/\1/' | sort -u)"
if [[ "$compose_env" != "$expected" ]]; then
  echo "error: docker-compose.yml passes an unexpected application environment variable" >&2
  echo "$compose_env" >&2
  exit 1
fi

echo "Environment contract verified: exactly four application settings"

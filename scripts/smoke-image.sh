#!/usr/bin/env bash
set -Eeuo pipefail

image_ref="${1:-}"
if [[ ! "$image_ref" =~ ^appstore:v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
  echo "usage: $0 appstore:vX.Y.Z" >&2
  exit 2
fi
if ! command -v docker >/dev/null 2>&1; then
  echo "error: docker is required" >&2
  exit 1
fi

suffix="$$"
network="appstore-smoke-$suffix"
database="appstore-smoke-db-$suffix"
application="appstore-smoke-app-$suffix"
postgres_image="${POSTGRES_TEST_IMAGE:-postgres:17-alpine}"

cleanup() {
  docker rm -f "$application" "$database" >/dev/null 2>&1 || true
  docker network rm "$network" >/dev/null 2>&1 || true
}
trap cleanup EXIT

docker network create --internal "$network" >/dev/null
docker run -d --name "$database" --network "$network" \
  -e POSTGRES_DB=appstore \
  -e POSTGRES_USER=appstore \
  -e POSTGRES_PASSWORD=smoke-test-password \
  "$postgres_image" >/dev/null

for _ in $(seq 1 30); do
  if docker exec "$database" pg_isready -U appstore -d appstore >/dev/null 2>&1; then
    break
  fi
  sleep 2
done
docker exec "$database" pg_isready -U appstore -d appstore >/dev/null

version="${image_ref#appstore:}"
docker run -d --name "$application" --network "$network" \
  --read-only --tmpfs /tmp:size=64m,mode=1777 \
  --cap-drop ALL --security-opt no-new-privileges:true \
  -e POSTGRES_DSN='postgres://appstore:smoke-test-password@appstore-smoke-db-'"$suffix"':5432/appstore?sslmode=disable' \
  -e BOOTSTRAP_ADMIN=smoke-admin \
  -e BOOTSTRAP_ADMIN_PASSWORD=smoke-test-password-2026 \
  -e ENCRYPTION_KEY=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef \
  "$image_ref" >/dev/null

ready=false
for _ in $(seq 1 45); do
  if docker exec "$application" wget -q -T 2 -O /dev/null http://127.0.0.1:8080/health/ready >/dev/null 2>&1; then
    ready=true
    break
  fi
  sleep 2
done
if [[ "$ready" != true ]]; then
  docker logs "$application" >&2
  echo "error: application did not become ready" >&2
  exit 1
fi

version_payload="$(docker exec "$application" wget -q -T 2 -O - http://127.0.0.1:8080/api/version)"
if [[ "$version_payload" != *"\"version\":\"$version\""* ]]; then
  echo "error: /api/version does not report $version: $version_payload" >&2
  exit 1
fi
docker exec "$application" wget -q -T 2 -O /dev/null http://127.0.0.1:8080/

echo "Smoke test passed for $image_ref on an internal-only Docker network"

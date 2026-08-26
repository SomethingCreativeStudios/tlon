#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_dir"

export COMPOSE_PROJECT_NAME="${TLON_E2E_PROJECT:-tlon-e2e}"
export TLON_DB_PORT="${TLON_DB_PORT:-55432}"
export TLON_HTTP_PORT="${TLON_HTTP_PORT:-18080}"
export TLON_PUBLIC_URL="http://localhost:${TLON_HTTP_PORT}"
base_url="$TLON_PUBLIC_URL"

cleanup() {
  status=$?
  if [[ $status -ne 0 ]]; then docker compose logs --no-color || true; fi
  docker compose down --volumes --remove-orphans >/dev/null 2>&1 || true
  exit "$status"
}
trap cleanup EXIT

export TLON_AUTH_MODE=deny
docker compose up -d --build

for _ in $(seq 1 60); do
  if curl --fail --silent --show-error "$base_url/readyz" >/dev/null; then break; fi
  sleep 2
done
curl --fail --silent --show-error "$base_url/readyz" >/dev/null

docker compose run --rm -v "$repo_dir/examples:/examples:ro" tlon catalog apply /examples/catalog.json >/dev/null
docker compose run --rm tlon demo seed --catalog demo --count 24 --seed 42 --reset >/dev/null
curl --fail --silent --show-error "$base_url/playground" | grep -q 'Tlon playground'
curl --fail --silent --show-error "$base_url/collections/demo/items?limit=0" | grep -q '"numberMatched":24'
curl --fail --silent --show-error "$base_url/collections/demo/items?limit=0&facets=organizations,quality,resourceGroups" | grep -q '"resourceGroups"'

record='{"id":"harvest-record","type":"Feature","geometry":null,"properties":{"type":"dataset","title":"Harvested","keywords":["harvest"],"score":25}}'
status="$(curl --silent --output /tmp/tlon-e2e-denied.json --write-out '%{http_code}' -X PUT -H 'Content-Type: application/geo+json' --data "$record" "$base_url/collections/records/items/harvest-record")"
test "$status" = "403"

export TLON_AUTH_MODE=external
docker compose up -d --force-recreate tlon
for _ in $(seq 1 30); do
  if curl --fail --silent --show-error "$base_url/readyz" >/dev/null; then break; fi
  sleep 1
done

status="$(curl --silent --dump-header /tmp/tlon-e2e-create.headers --output /tmp/tlon-e2e-create.json --write-out '%{http_code}' -X PUT -H 'Content-Type: application/geo+json' --data "$record" "$base_url/collections/records/items/harvest-record")"
test "$status" = "201"
grep -qi '^Location: http://localhost:'"$TLON_HTTP_PORT"'/collections/records/items/harvest-record' /tmp/tlon-e2e-create.headers
etag="$(awk 'tolower($1) == "etag:" {gsub("\r", "", $2); print $2}' /tmp/tlon-e2e-create.headers)"
test -n "$etag"

replacement='{"id":"harvest-record","type":"Feature","geometry":{"type":"Point","coordinates":[-77,39]},"properties":{"type":"dataset","title":"Harvested replacement","keywords":["harvest"],"score":26}}'
status="$(curl --silent --dump-header /tmp/tlon-e2e-replace.headers --output /dev/null --write-out '%{http_code}' -X PUT -H 'Content-Type: application/geo+json' -H "If-Match: $etag" --data "$replacement" "$base_url/collections/records/items/harvest-record")"
test "$status" = "204"

temporary='{"type":"Feature","geometry":null,"properties":{"type":"dataset","title":"Temporary"}}'
curl --fail --silent --output /dev/null -X PUT -H 'Content-Type: application/geo+json' --data "$temporary" "$base_url/collections/records/items/harvest-delete"
curl --fail --silent --output /dev/null -X DELETE "$base_url/collections/records/items/harvest-delete"

docker compose restart tlon >/dev/null
for _ in $(seq 1 30); do
  if curl --fail --silent --show-error "$base_url/readyz" >/dev/null; then break; fi
  sleep 1
done
curl --fail --silent --show-error "$base_url/collections/records/items/harvest-record" | grep -q 'Harvested replacement'
status="$(curl --silent --output /dev/null --write-out '%{http_code}' "$base_url/collections/records/items/harvest-delete")"
test "$status" = "404"

curl --fail --silent --output /dev/null -X DELETE "$base_url/collections/records/items/harvest-record"

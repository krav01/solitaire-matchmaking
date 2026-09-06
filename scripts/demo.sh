#!/usr/bin/env bash

set -euo pipefail

GO=${GO:-go}
DOCKER=${DOCKER:-docker}
POSTGRES_IMAGE=${POSTGRES_IMAGE:-postgres:18-alpine}
POSTGRES_START_ATTEMPTS=${POSTGRES_START_ATTEMPTS:-60}

container_id=

fail() {
  echo "demo: $*" >&2
  exit 1
}

require_tool() {
  command -v "$1" >/dev/null 2>&1 || fail "$1 is required"
}

cleanup() {
  local status=$?
  trap - EXIT
  if [[ -n "${container_id}" ]]; then
    "${DOCKER}" rm --force "${container_id}" >/dev/null 2>&1 || true
  fi
  exit "${status}"
}

trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

require_tool "${GO}"
require_tool "${DOCKER}"
[[ "${POSTGRES_START_ATTEMPTS}" =~ ^[1-9][0-9]*$ ]] ||
  fail "POSTGRES_START_ATTEMPTS must be a positive integer"
"${DOCKER}" info >/dev/null 2>&1 || fail "Docker daemon is not available"

echo "demo: starting disposable PostgreSQL"
container_id=$("${DOCKER}" run \
  --detach \
  --rm \
  --env POSTGRES_DB=matchmaking_canary \
  --env POSTGRES_USER=matchmaking \
  --env POSTGRES_PASSWORD=matchmaking \
  --publish 127.0.0.1::5432 \
  "${POSTGRES_IMAGE}")

port_binding=$("${DOCKER}" port "${container_id}" 5432/tcp)
postgres_port=${port_binding##*:}
[[ "${postgres_port}" =~ ^[0-9]+$ ]] || fail "could not resolve the PostgreSQL host port"

postgres_ready=0
for ((attempt = 1; attempt <= POSTGRES_START_ATTEMPTS; attempt++)); do
  if "${DOCKER}" exec "${container_id}" \
    pg_isready --username matchmaking --dbname matchmaking_canary >/dev/null 2>&1; then
    postgres_ready=1
    break
  fi
  sleep 1
done

if [[ "${postgres_ready}" != "1" ]]; then
  "${DOCKER}" logs "${container_id}" >&2 || true
  fail "PostgreSQL did not become ready"
fi

echo "demo: running five-player matchmaking lifecycle"
CANARY_RUN=1 \
CANARY_DATABASE_URL="postgres://matchmaking:matchmaking@127.0.0.1:${postgres_port}/matchmaking_canary?sslmode=disable" \
  "${GO}" test -count=1 -v -run '^TestCanaryLifecycleWithGameBackend$' ./examples/game-backend

echo "demo: complete"

#!/usr/bin/env bash

set -euo pipefail

GO=${GO:-go}
ADMIN_DATABASE_URL=${ADMIN_DATABASE_URL:-}
SOURCE_DATABASE_URL=${SOURCE_DATABASE_URL:-}
RESTORE_DATABASE_URL=${RESTORE_DATABASE_URL:-}
SOURCE_DATABASE_NAME=${SOURCE_DATABASE_NAME:-}
RESTORE_DATABASE_NAME=${RESTORE_DATABASE_NAME:-}
POSTGRES_CLIENT_CONTAINER=${POSTGRES_CLIENT_CONTAINER:-}
REHEARSAL_CONFIRM_DISPOSABLE=${REHEARSAL_CONFIRM_DISPOSABLE:-}
REHEARSAL_ROWS=${REHEARSAL_ROWS:-100000}
REHEARSAL_REPORT=${REHEARSAL_REPORT:-backup-restore-rehearsal.md}

fail() {
  echo "backup/restore rehearsal: $*" >&2
  exit 1
}

require_value() {
  local name=$1
  local value=$2
  [[ -n "${value}" ]] || fail "${name} is required"
}

require_tool() {
  command -v "$1" >/dev/null 2>&1 || fail "$1 is required"
}

db_tool() {
  if [[ -n "${POSTGRES_CLIENT_CONTAINER}" ]]; then
    docker exec -i "${POSTGRES_CLIENT_CONTAINER}" "$@"
    return
  fi
  "$@"
}

db_query() {
  local database_url=$1
  local query=$2
  db_tool psql \
    --no-psqlrc \
    --set ON_ERROR_STOP=1 \
    --tuples-only \
    --no-align \
    --dbname="${database_url}" \
    --command="${query}"
}

write_manifest() {
  local database_url=$1
  local output=$2
  {
    db_query "${database_url}" "SELECT 'schema_migrations|' || count(*) || '|' || md5(COALESCE(string_agg(version::text || ':' || name || ':' || checksum, ',' ORDER BY version), '')) FROM schema_migrations"
    db_query "${database_url}" "SELECT 'player_ratings|' || count(*) || '|' || md5(COALESCE(string_agg(player_id || ':' || games::text || ':' || revision::text, ',' ORDER BY player_id), '')) FROM player_ratings"
    db_query "${database_url}" "SELECT 'matchmaking_tickets|' || count(*) || '|' || md5(COALESCE(string_agg(ticket_id || ':' || status || ':' || request_digest, ',' ORDER BY ticket_id), '')) FROM matchmaking_tickets"
    db_query "${database_url}" "SELECT 'outbox_events|' || count(*) || '|' || md5(COALESCE(string_agg(event_id || ':' || aggregate_id || ':' || payload::text, ',' ORDER BY event_id), '')) FROM outbox_events"
    db_query "${database_url}" "SELECT 'constraints|' || count(*) || '|' || md5(COALESCE(string_agg(c.relname || ':' || con.conname || ':' || pg_get_constraintdef(con.oid, true), E'\\n' ORDER BY c.relname, con.conname), '')) FROM pg_constraint con JOIN pg_class c ON c.oid = con.conrelid JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = 'public'"
    db_query "${database_url}" "SELECT 'indexes|' || count(*) || '|' || md5(COALESCE(string_agg(indexname || ':' || indexdef, E'\\n' ORDER BY indexname), '')) FROM pg_indexes WHERE schemaname = 'public'"
    db_query "${database_url}" "SELECT 'functions|' || count(*) || '|' || md5(COALESCE(string_agg(p.proname || ':' || pg_get_functiondef(p.oid), E'\\n' ORDER BY p.proname), '')) FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace WHERE n.nspname = 'public' AND p.prokind = 'f'"
    db_query "${database_url}" "SELECT 'triggers|' || count(*) || '|' || md5(COALESCE(string_agg(c.relname || ':' || t.tgname || ':' || pg_get_triggerdef(t.oid, true), E'\\n' ORDER BY c.relname, t.tgname), '')) FROM pg_trigger t JOIN pg_class c ON c.oid = t.tgrelid JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = 'public' AND NOT t.tgisinternal"
  } > "${output}"
}

require_value ADMIN_DATABASE_URL "${ADMIN_DATABASE_URL}"
require_value SOURCE_DATABASE_URL "${SOURCE_DATABASE_URL}"
require_value RESTORE_DATABASE_URL "${RESTORE_DATABASE_URL}"
require_value SOURCE_DATABASE_NAME "${SOURCE_DATABASE_NAME}"
require_value RESTORE_DATABASE_NAME "${RESTORE_DATABASE_NAME}"

[[ "${REHEARSAL_CONFIRM_DISPOSABLE}" == "1" ]] ||
  fail "REHEARSAL_CONFIRM_DISPOSABLE=1 is required"
[[ "${SOURCE_DATABASE_NAME}" =~ ^[a-z][a-z0-9_]{0,52}_rehearsal$ ]] ||
  fail "SOURCE_DATABASE_NAME must end in _rehearsal and contain only lowercase letters, digits, and underscores"
[[ "${RESTORE_DATABASE_NAME}" =~ ^[a-z][a-z0-9_]{0,54}_restore$ ]] ||
  fail "RESTORE_DATABASE_NAME must end in _restore and contain only lowercase letters, digits, and underscores"
[[ "${SOURCE_DATABASE_NAME}" != "${RESTORE_DATABASE_NAME}" ]] ||
  fail "source and restore database names must differ"
[[ "${REHEARSAL_ROWS}" =~ ^[0-9]+$ ]] || fail "REHEARSAL_ROWS must be an integer"
((REHEARSAL_ROWS >= 1000 && REHEARSAL_ROWS <= 1000000)) ||
  fail "REHEARSAL_ROWS must be between 1000 and 1000000"

require_tool "${GO}"
if [[ -n "${POSTGRES_CLIENT_CONTAINER}" ]]; then
  require_tool docker
  docker inspect "${POSTGRES_CLIENT_CONTAINER}" >/dev/null
else
  for tool in psql pg_dump pg_restore createdb dropdb; do
    require_tool "${tool}"
  done
fi
db_tool pg_dump --version

workdir=$(mktemp -d)
source_manifest="${workdir}/source.manifest"
restore_manifest="${workdir}/restore.manifest"
backup_file="${workdir}/matchmaking.dump"
started_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
total_started=${SECONDS}

cleanup() {
  local status=$?
  trap - EXIT
  set +e
  db_tool dropdb --if-exists --force --maintenance-db="${ADMIN_DATABASE_URL}" "${RESTORE_DATABASE_NAME}" >/dev/null 2>&1
  db_tool dropdb --if-exists --force --maintenance-db="${ADMIN_DATABASE_URL}" "${SOURCE_DATABASE_NAME}" >/dev/null 2>&1
  rm -rf -- "${workdir}"
  exit "${status}"
}
trap cleanup EXIT

admin_database=$(db_query "${ADMIN_DATABASE_URL}" "SELECT current_database()")
[[ "${admin_database}" != "${SOURCE_DATABASE_NAME}" && "${admin_database}" != "${RESTORE_DATABASE_NAME}" ]] ||
  fail "ADMIN_DATABASE_URL must not target a rehearsal database"

db_tool dropdb --if-exists --force --maintenance-db="${ADMIN_DATABASE_URL}" "${RESTORE_DATABASE_NAME}"
db_tool dropdb --if-exists --force --maintenance-db="${ADMIN_DATABASE_URL}" "${SOURCE_DATABASE_NAME}"
db_tool createdb --maintenance-db="${ADMIN_DATABASE_URL}" "${SOURCE_DATABASE_NAME}"

[[ "$(db_query "${SOURCE_DATABASE_URL}" "SELECT current_database()")" == "${SOURCE_DATABASE_NAME}" ]] ||
  fail "SOURCE_DATABASE_URL does not resolve to SOURCE_DATABASE_NAME"

DATABASE_URL="${SOURCE_DATABASE_URL}" "${GO}" run ./cmd/migrate

db_tool psql \
  --no-psqlrc \
  --set ON_ERROR_STOP=1 \
  --set row_count="${REHEARSAL_ROWS}" \
  --dbname="${SOURCE_DATABASE_URL}" <<'SQL'
BEGIN;

INSERT INTO rating_models (model_version, feature_schema, parameters_digest)
VALUES ('rehearsal-v1', '{}', repeat('a', 64));

INSERT INTO matching_policies (
    policy_version,
    rating_model_version,
    definition,
    definition_digest
)
VALUES ('rehearsal-v1', 'rehearsal-v1', '{"max_skill_gap":400}', repeat('b', 64));

INSERT INTO tournament_configs (
    tournament_id,
    version,
    mode_id,
    capacity,
    entry_fee_minor,
    currency,
    scoring_rules_version,
    settlement_version,
    policy_version,
    rating_model_version,
    result_timeout_ms,
    active_from
)
VALUES (
    'rehearsal',
    'v1',
    'classic',
    5,
    100,
    'USD',
    'rules-v1',
    'settlement-v1',
    'rehearsal-v1',
    'rehearsal-v1',
    300000,
    '2026-01-01T00:00:00Z'
);

INSERT INTO player_ratings (
    player_id,
    mode_id,
    model_version,
    mean,
    uncertainty,
    performance_deviation,
    games,
    updated_at,
    revision
)
SELECT
    'player-' || lpad(series::text, 8, '0'),
    'classic',
    'rehearsal-v1',
    1200 + (series % 800),
    80 + (series % 120),
    20 + (series % 40),
    series % 500,
    '2026-01-01T00:00:00Z'::timestamptz + series * interval '1 millisecond',
    series % 500
FROM generate_series(1, :row_count) AS series;

INSERT INTO matchmaking_tickets (
    ticket_id,
    entry_id,
    request_digest,
    player_id,
    tournament_id,
    tournament_version,
    status,
    requested_at,
    snapshot_at,
    rating_mean,
    rating_uncertainty,
    rating_performance_deviation,
    rating_games,
    rating_model_version,
    rating_updated_at,
    next_attempt_at
)
SELECT
    'ticket-' || lpad(series::text, 8, '0'),
    'entry-' || lpad(series::text, 8, '0'),
    md5('ticket-' || series::text) || md5('request-' || series::text),
    'player-' || lpad(series::text, 8, '0'),
    'rehearsal',
    'v1',
    'queued',
    '2026-01-02T00:00:00Z'::timestamptz + series * interval '1 millisecond',
    '2026-01-02T00:00:00Z'::timestamptz + series * interval '1 millisecond',
    1200 + (series % 800),
    80 + (series % 120),
    20 + (series % 40),
    series % 500,
    'rehearsal-v1',
    '2026-01-01T00:00:00Z'::timestamptz + series * interval '1 millisecond',
    '2026-01-02T00:00:00Z'::timestamptz + series * interval '1 millisecond'
FROM generate_series(1, :row_count) AS series;

INSERT INTO outbox_events (
    event_id,
    aggregate_type,
    aggregate_id,
    aggregate_version,
    event_type,
    payload,
    occurred_at,
    available_at
)
SELECT
    'event-' || lpad(series::text, 8, '0'),
    'player',
    'player-' || lpad(series::text, 8, '0'),
    1,
    'rating.snapshot',
    jsonb_build_object('player_id', 'player-' || lpad(series::text, 8, '0')),
    '2026-01-03T00:00:00Z'::timestamptz + series * interval '1 millisecond',
    '2026-01-03T00:00:00Z'::timestamptz + series * interval '1 millisecond'
FROM generate_series(1, :row_count) AS series;

COMMIT;
ANALYZE;
SQL

write_manifest "${SOURCE_DATABASE_URL}" "${source_manifest}"

dump_started=${SECONDS}
db_tool pg_dump \
  --format=custom \
  --compress=6 \
  --no-owner \
  --no-privileges \
  --dbname="${SOURCE_DATABASE_URL}" > "${backup_file}"
dump_seconds=$((SECONDS - dump_started))

db_tool createdb --maintenance-db="${ADMIN_DATABASE_URL}" "${RESTORE_DATABASE_NAME}"
[[ "$(db_query "${RESTORE_DATABASE_URL}" "SELECT current_database()")" == "${RESTORE_DATABASE_NAME}" ]] ||
  fail "RESTORE_DATABASE_URL does not resolve to RESTORE_DATABASE_NAME"

restore_started=${SECONDS}
db_tool pg_restore \
  --exit-on-error \
  --single-transaction \
  --no-owner \
  --no-privileges \
  --dbname="${RESTORE_DATABASE_URL}" < "${backup_file}"
restore_seconds=$((SECONDS - restore_started))

migration_started=${SECONDS}
restore_migration_output=$(DATABASE_URL="${RESTORE_DATABASE_URL}" "${GO}" run ./cmd/migrate 2>&1)
migration_seconds=$((SECONDS - migration_started))
echo "${restore_migration_output}"
grep -q '"applied":0' <<< "${restore_migration_output}" ||
  fail "restored database unexpectedly had pending migrations"

write_manifest "${RESTORE_DATABASE_URL}" "${restore_manifest}"
cmp --silent "${source_manifest}" "${restore_manifest}" || {
  diff --unified "${source_manifest}" "${restore_manifest}" >&2 || true
  fail "restored data or schema manifest differs from source"
}

backup_bytes=$(stat --format=%s "${backup_file}")
backup_sha256=$(sha256sum "${backup_file}" | awk '{print $1}')
manifest_sha256=$(sha256sum "${restore_manifest}" | awk '{print $1}')
total_seconds=$((SECONDS - total_started))
finished_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)

mkdir -p "$(dirname "${REHEARSAL_REPORT}")"
{
  echo "# Backup/restore rehearsal"
  echo
  echo "- Result: PASS"
  echo "- PostgreSQL client: $(db_tool pg_dump --version)"
  echo "- Fixture rows per operational table: ${REHEARSAL_ROWS}"
  echo "- Verified operational rows: $((REHEARSAL_ROWS * 3))"
  echo "- Backup size: ${backup_bytes} bytes"
  echo "- Backup SHA-256: \`${backup_sha256}\`"
  echo "- Restored manifest SHA-256: \`${manifest_sha256}\`"
  echo "- Dump duration: ${dump_seconds}s"
  echo "- Restore duration: ${restore_seconds}s"
  echo "- Post-restore migration check: ${migration_seconds}s"
  echo "- Total duration: ${total_seconds}s"
  echo "- Started: ${started_at}"
  echo "- Finished: ${finished_at}"
} > "${REHEARSAL_REPORT}"

cat "${REHEARSAL_REPORT}"

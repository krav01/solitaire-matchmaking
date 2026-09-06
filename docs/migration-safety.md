# Migration safety

Database migrations must support safe rolling deployment and recovery.

## Rules

- Prefer expand/contract changes over destructive one-step migrations.
- Add nullable/new columns or new tables before requiring them from application code.
- Backfill separately when data volume can make a migration long-running.
- Avoid table rewrites, unbounded updates, and long blocking locks in deployment migrations.
- Create large indexes using the safest PostgreSQL strategy available for the deployment environment.
- Do not drop or rename data still read by the currently deployed or immediately previous application version.
- Keep application changes backward compatible across the deployment window.
- Migration SQL must be deterministic and idempotency expectations must be explicit.

## Required review for schema changes

Review:

- lock level and expected lock duration;
- table size/data volume assumptions;
- compatibility with old and new binaries;
- rollback/recovery path;
- transaction boundaries;
- unique/foreign-key validation cost;
- whether backfill needs batching/rate limiting.

## Testing

At minimum:

- apply migrations to an empty database;
- apply the migration set repeatedly where repository tooling promises idempotent application;
- test application behavior against the migrated schema;
- test critical uniqueness, fencing and transactional invariants.

The `Backup and restore rehearsal` workflow and stable-tag release workflow run
`make backup-restore-rehearsal` with PostgreSQL 18. The script:

1. requires explicit confirmation and names ending in `_rehearsal` and `_restore`;
2. creates both databases from a separate administrative connection;
3. applies the embedded migration catalogue and inserts configurable synthetic
   rating, queued-ticket and outbox volume;
4. creates a compressed custom-format backup and restores it in one transaction;
5. reruns the migration binary and requires zero pending migrations;
6. compares deterministic data, migration-ledger, constraint, index, function and
   trigger manifests before deleting both disposable databases.

Run the same guard locally only against a disposable PostgreSQL server:

```bash
export ADMIN_DATABASE_URL='postgres://matchmaking:matchmaking@127.0.0.1:5432/postgres?sslmode=disable'
export SOURCE_DATABASE_URL='postgres://matchmaking:matchmaking@127.0.0.1:5432/matchmaking_rehearsal?sslmode=disable'
export RESTORE_DATABASE_URL='postgres://matchmaking:matchmaking@127.0.0.1:5432/matchmaking_restore?sslmode=disable'
export SOURCE_DATABASE_NAME=matchmaking_rehearsal
export RESTORE_DATABASE_NAME=matchmaking_restore
export REHEARSAL_CONFIRM_DISPOSABLE=1
export REHEARSAL_ROWS=100000
make backup-restore-rehearsal
```

The generated Markdown report records dump size and SHA-256 plus dump, restore,
post-restore migration and total durations. Synthetic CI evidence validates the
procedure and current schema; it does not replace a target-environment restore,
PITR test, lock observation or recovery-time measurement.

A destructive cleanup belongs in a later release after readers/writers of the old shape are gone.

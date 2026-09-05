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

A destructive cleanup belongs in a later release after readers/writers of the old shape are gone.

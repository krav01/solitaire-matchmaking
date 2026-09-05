# PostgreSQL migrations

Migrations are forward-only SQL files named `NNNNNN_name.up.sql`. They are
embedded in the migration binary so the reviewed SQL and the executable cannot
drift. An applied version is recorded with its SHA-256 checksum; changing an
applied file is rejected.

Apply migrations explicitly before deploying the service:

```bash
DATABASE_URL='postgres://...' go run ./cmd/migrate
```

The runner takes a PostgreSQL transaction-scoped advisory lock and applies all
pending migrations in one transaction. A failure rolls back the complete batch.
Application startup never modifies the schema.

`000003_matchmaking_worker` adds durable retry times and expiring claim leases.
Workers use the partial due-ticket index and `FOR UPDATE SKIP LOCKED`; scoring
runs after the claim transaction releases its row locks.

`000004_result_finalization` enforces canonical SHA-256 request digests for
idempotent verified-result ingestion.

`000005_rating_worker` adds durable rating retry times and fenced leases. The
oldest unprocessed result remains the global head so a retry or active lease
cannot let a later outcome overtake it.

Rollback uses a reviewed compensating forward migration. Destructive automatic
down migrations are intentionally unsupported.

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

`000006_outbox_delivery` constrains delivery state and adds the partial index
used to preserve aggregate order while independent events are delivered in
parallel.

`000007_rating_shadow_mode` adds nullable room deck-version metadata plus an
isolated, ordered shadow-evaluation timeline. Candidate deployments, pre-game
paired predictions, player state, update history and scored observations are
new tables; active rating rows and existing result-processing columns are not
rewritten. Old binaries ignore the additive schema, and rooms without a deck
version are recorded as skipped shadow work.

Rollback uses a reviewed compensating forward migration. Destructive automatic
down migrations are intentionally unsupported.

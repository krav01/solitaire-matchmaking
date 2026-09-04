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

Rollback uses a reviewed compensating forward migration. Destructive automatic
down migrations are intentionally unsupported.

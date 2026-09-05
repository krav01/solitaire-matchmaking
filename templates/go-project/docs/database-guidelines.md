# Database guidelines for Go services

Use this as the default persistence baseline for Go services using PostgreSQL or another relational database. Adapt engine-specific details when needed.

## Query and API rules

- Prefer explicit SQL for important queries; hide it behind small repository/store methods, not generic data-access abstractions.
- Always parameterize values. Never build SQL from untrusted input by string concatenation.
- Select only required columns; avoid `SELECT *` in production paths.
- Keep scan order explicit and close to the query definition.
- Propagate `context.Context` to every query/transaction call.
- Set practical request/statement timeouts at the appropriate layer.
- Distinguish not-found, conflict/uniqueness, retryable/transient, and permanent failures when callers need different behavior.

## Connection pool

- Treat the DB handle/pool as a shared concurrency-safe resource owned by application lifecycle.
- Configure max open/idle connections and lifetime based on database/server limits and measured workload, not arbitrary large numbers.
- Verify connectivity/readiness at startup or readiness boundary without turning temporary DB failure into endless startup loops.
- Monitor pool saturation, wait time, query latency, errors, and connection churn.

## Transactions

- Start a transaction only when one business invariant requires atomicity.
- Keep transactions short; avoid external network calls and long computation inside them.
- Pass the transaction explicitly through the use-case boundary; do not hide nested transactions.
- Roll back on every unsuccessful path and treat rollback failure as diagnostic context.
- Choose isolation/locking from the invariant and contention model; do not increase isolation blindly.
- Prevent lost updates using version checks, constraints, row locks, or serializable transactions as appropriate.
- Define lock ordering when multiple rows/resources can be locked.
- Test concurrent behavior, retries, deadlocks, cancellation, and partial failures for critical flows.

## Schema and constraints

- Put invariants in the database when the database can enforce them reliably: `NOT NULL`, `UNIQUE`, foreign keys, checks, and suitable data types.
- Do not rely only on application validation for persistence invariants.
- Use stable identifiers and define ownership/lifecycle for every table.
- Store timestamps with an explicit time model; normally use UTC and avoid ambiguous local times.
- Be deliberate about nullable fields: missing and zero are different states.
- Avoid unbounded JSON/blob fields on hot relational paths unless the trade-off is intentional and documented.

## Indexes and query plans

- Add indexes for actual access patterns, not every column.
- Review write amplification and storage cost when adding indexes.
- For important or slow queries, inspect `EXPLAIN (ANALYZE, BUFFERS)` on representative data where safe.
- Watch for sequential scans, poor cardinality estimates, unnecessary sorts, N+1 patterns, and queries whose cost grows unexpectedly with data volume.
- Use pagination appropriate to scale; prefer keyset/cursor pagination over large offsets on large mutable datasets.

## Migrations

- Prefer forward, backward-compatible expand/contract changes for production systems.
- Avoid one-step destructive column/table changes while old binaries may still run.
- Review lock behavior and table-rewrite risk before schema changes.
- Build indexes concurrently/online where the database and operational requirements justify it.
- Split large backfills from schema deployment and process them in bounded batches.
- Make migrations deterministic, versioned, observable, and tested against a real database engine.
- Define rollback/recovery strategy before risky migrations even when migrations are forward-only.

## Idempotency and distributed workflows

- Use unique constraints or durable idempotency records to enforce idempotent writes where duplicate requests/events are possible.
- Do not use in-memory deduplication for correctness across restarts or replicas.
- Use a transactional outbox when committed database state and external event publication must not diverge.
- Assume at-least-once delivery unless the complete end-to-end system proves stronger semantics.

## Testing

- Unit-test domain rules without a database when possible.
- Use real PostgreSQL integration tests for SQL, migrations, locking, isolation, constraints, transactions, and concurrency-sensitive repository behavior.
- Do not rely on SQLite/in-memory substitutes for PostgreSQL-specific correctness.
- Keep fixtures minimal and deterministic.
- Add regression tests for production query/migration bugs.

## Review checklist

For persistence changes review:
- invariant and transaction boundary;
- SQL injection and dynamic SQL safety;
- constraints and uniqueness;
- isolation, locking and deadlock risk;
- query plan/index impact;
- pool/timeout behavior;
- migration compatibility and lock duration;
- idempotency and retry behavior;
- observability and integration coverage.

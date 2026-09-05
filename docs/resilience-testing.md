# Resilience testing

The release-validation resilience suite exercises the transactional outbox at
the PostgreSQL and worker boundary. It is a deterministic correctness guardrail,
not a production capacity or availability claim.

## Scenario matrix

| Scenario | Workload or injected condition | Pass criteria |
| --- | --- | --- |
| Bounded concurrent load | 64 events across 16 aggregates and four versions, processed by four runners with bounded batches and concurrency | Every event is published and acknowledged once, no claim remains, and each aggregate is published in version order |
| Process recovery | A worker abandons a claim whose lease has expired before acknowledgement | The stale acknowledgement is fenced, a replacement worker reclaims attempt two, and the event is delivered |
| Publisher failure | The publisher fails the first attempt and succeeds on retry | The first attempt records a bounded failure and releases its claim; the due retry succeeds, clears the error, and leaves no claim |

Each run creates a dedicated PostgreSQL schema, applies the embedded migrations,
uses explicit clock advancement instead of sleeps, and removes the schema at the
end. The entire suite has a bounded context and all worker calls terminate before
assertions are evaluated.

Run the suite against a disposable PostgreSQL database:

```bash
TEST_DATABASE_URL='postgres://matchmaking:matchmaking@127.0.0.1:5432/matchmaking_test?sslmode=disable' \
  go test -count=1 -run '^TestOutboxResiliencePostgreSQL$' ./internal/postgres
```

## Coverage boundaries

The suite verifies queue contention, per-aggregate ordering, retry persistence,
lease fencing, and restart recovery for the outbound integration boundary.
Existing lifecycle integration tests separately cover concurrent matchmaking,
transactional rollback, result finalization, ordered rating updates, and complete
tournament processing.

The finite synthetic workload does not establish throughput, latency percentiles,
database sizing, or production SLOs. Before release under real traffic assumptions,
run sustained load and infrastructure-level failure exercises in a representative
environment, monitor pool saturation and queue age, and define budgets using the
measurement rules in `docs/performance-budget.md`.

# Project maturity levels

Choose the lowest level that safely matches the project. Raise the level when scale, reliability, security, or operational risk increases.

## Basic

Use for prototypes, exercises, internal utilities, and low-risk services.

Required:
- clear package boundaries;
- targeted tests for changed behavior;
- `go test ./...` and `go vet ./...`;
- no secrets in source;
- lightweight project map;
- ADRs only for expensive-to-reverse decisions.

## Production

Use for customer-facing services, persistent data, integrations, or regular releases.

Add:
- structured logging and basic metrics;
- lint and vulnerability scanning;
- migration safety and rollback/compatibility review;
- context cancellation/timeouts;
- API/persistence compatibility checks;
- race detector for high-risk changes and main/release gates;
- incident-to-regression-test workflow;
- dependency review when available.

## High-load / critical

Use for high-throughput systems, billing, matchmaking, financial/data-integrity paths, or distributed workflows where retries/concurrency can cause material harm.

Add:
- explicit performance budgets and benchmarks;
- load/recovery/failure-injection testing;
- idempotency, leases/fencing, retry and partial-failure design;
- architecture dependency guards;
- stronger observability and alerting;
- fuzz/property tests for invariant-heavy code;
- formal ADRs for major boundaries and consistency models;
- stricter release/security review.

## Promotion triggers

Promote a project when any of these become true:
- persistent or customer-critical data is introduced;
- external traffic or third-party integrations become material;
- concurrency/retry semantics affect correctness;
- a failure can create financial, fairness, or integrity impact;
- load or latency becomes a product requirement;
- the service becomes operationally owned in production.

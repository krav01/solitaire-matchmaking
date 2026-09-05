# Testing strategy

Testing follows a fail-fast model: run the cheapest relevant checks first and reserve broad or expensive checks for larger changes and release gates.

## Change levels

### Small change

Examples: isolated validation, mapping, deterministic helper, documentation-adjacent code.

Required:

- format changed Go files;
- targeted package/test selection;
- regression test for a bug fix.

### Medium change

Examples: package behavior, use-case orchestration, persistence query, HTTP contract implementation.

Required:

- affected package tests;
- `go vet`/`golangci-lint` through the repository lint command;
- architecture check when imports or package responsibilities change;
- relevant integration test when persistence behavior changes;
- API contract validation when handlers/contracts change.

### High-risk or release change

Examples: matchmaking algorithm, rating model, concurrency/worker lifecycle, schema change, authentication/security boundary, cross-package refactor.

Required:

- `go test -race -shuffle=on ./...`;
- full lint;
- architecture guard;
- PostgreSQL integration suite when persistence is involved;
- `govulncheck`;
- dependency review for pull requests;
- relevant fuzz/property checks for invariant-heavy input spaces;
- benchmark review for critical-path performance changes.

## Test design

- Domain tests should be deterministic and table-driven where appropriate.
- Bug fixes require a test that fails before the fix and passes after it.
- Concurrency tests must assert bounded behavior and termination rather than depend on sleeps where synchronization can be explicit.
- Persistence tests should verify transaction boundaries, uniqueness/idempotency, lease fencing, and rollback behavior.
- HTTP tests should verify status, response contract, validation, authentication/authorization behavior when implemented, and cancellation-sensitive paths.

## Fuzz and property testing

Use fuzz/property tests where the input space is large and invariants are more important than individual examples, especially for:

- validation and parsing;
- serialization/deserialization;
- matching/rating invariants;
- malformed external input;
- boundary numeric values.

PR CI runs a short fuzz smoke check for critical matchmaking invariants. Longer fuzz campaigns are optional local/release investigations and should preserve any discovered corpus/regression case as a deterministic test when a bug is found.

## Benchmarks

Critical algorithms should have stable microbenchmarks where practical.

- call `ReportAllocs` for allocation-sensitive paths;
- compare changes on the same environment;
- review `ns/op`, `B/op`, and `allocs/op`;
- use `docs/performance-budget.md` for interpretation;
- do not treat a synthetic microbenchmark as a production throughput/SLO claim.

The main branch runs the critical matchmaking selection benchmark to keep a visible baseline. A material regression is a review trigger until a stable automated comparison threshold is established.

## Matchmaking evaluation

Synthetic simulations validate algorithm behavior, invariants, and operational trade-offs; they do not establish real-world statistical accuracy.

Real evaluation must:

- preserve event-time ordering;
- use only features available before each game;
- prevent current/future outcome leakage;
- report fill latency together with fairness and calibration metrics;
- separate rating uncertainty from performance variability.

## CI gates

The repository CI keeps independent jobs for:

1. build and normal unit tests on pull requests/main;
2. short fuzz smoke tests for hard matchmaking invariants;
3. PostgreSQL integration tests;
4. lint/static analysis;
5. architecture dependency checks;
6. reachable-vulnerability scanning;
7. pull-request dependency review;
8. full race detection on `main`;
9. critical-path benchmark measurement on `main`.

This keeps PR feedback relatively cheap while retaining heavier release-quality checks after merge to `main`.

A failed gate should be fixed rather than bypassed with broad exclusions. Any necessary suppression must be narrow and documented.

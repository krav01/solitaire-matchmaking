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
- Dependabot/dependency changes reviewed;
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

PR CI runs a fixed-work short fuzz smoke check for critical matchmaking invariants. Longer fuzz campaigns are optional local/release investigations and should preserve any discovered corpus/regression case as a deterministic test when a bug is found.

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

The repository CI enforces these gates:

1. build and full race-enabled unit tests on pull requests/main;
2. scratch-based container build and non-root runtime-user verification;
3. short fuzz smoke tests for hard matchmaking invariants;
4. PostgreSQL integration and resilience tests;
5. lint/static analysis;
6. architecture dependency checks;
7. reachable-vulnerability scanning;
8. critical-path benchmark measurement on `main`.
9. backup, restore and migration rehearsal for migration-sensitive changes and on
   a weekly schedule.

GitHub Dependency Review is an additional PR gate for newly introduced vulnerable dependencies. Dependabot, `govulncheck`, and manual review remain complementary controls.

Stable release tags additionally repeat `make release-check` and the PostgreSQL
recovery rehearsal before any image is published. The release workflow scans the
exact local image and generates its SBOM without publish permissions, verifies
checksummed handoff artifacts in a separate job, rejects an existing version
tag, publishes to GHCR, and attests the resulting immutable digest. It does not
deploy the image.

This keeps correctness gates before merge while reserving comparative benchmark measurement for `main`.

A failed gate should be fixed rather than bypassed with broad exclusions. Any necessary suppression must be narrow and documented.

## Resilience scenarios

The PostgreSQL CI job includes deterministic load, recovery, and publisher
failure-injection scenarios for the transactional outbox. The scenario matrix,
pass criteria, local command, and production coverage boundaries are documented
in `docs/resilience-testing.md`.

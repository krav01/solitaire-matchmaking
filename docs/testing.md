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
- relevant integration test when persistence behavior changes.

### Large or release change

Examples: matchmaking algorithm, rating model, concurrency/worker lifecycle, schema change, cross-package refactor.

Required:

- `go test -race -shuffle=on ./...`;
- full lint;
- architecture guard;
- PostgreSQL integration suite;
- `govulncheck`;
- dependency review for pull requests.

## Test design

- Domain tests should be deterministic and table-driven where appropriate.
- Bug fixes require a test that fails before the fix and passes after it.
- Concurrency tests must assert bounded behavior and termination rather than depend on sleeps where synchronization can be explicit.
- Persistence tests should verify transaction boundaries, uniqueness/idempotency, lease fencing, and rollback behavior.
- HTTP tests should verify status, response contract, validation, authentication/authorization behavior when implemented, and cancellation-sensitive paths.

## Matchmaking evaluation

Synthetic simulations validate algorithm behavior, invariants, and operational trade-offs; they do not establish real-world statistical accuracy.

Real evaluation must:

- preserve event-time ordering;
- use only features available before each game;
- prevent current/future outcome leakage;
- report fill latency together with fairness and calibration metrics;
- separate rating uncertainty from performance variability.

## CI gates

The repository CI should keep independent jobs for:

1. build and race-enabled unit tests;
2. PostgreSQL integration tests;
3. lint/static analysis;
4. architecture dependency checks;
5. reachable-vulnerability scanning;
6. pull-request dependency review.

A failed gate should be fixed rather than bypassed with broad exclusions. Any necessary suppression must be narrow and documented.

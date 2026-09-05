# Project map

Read this file before exploring the repository. Update only when responsibilities
or stage status change.

| Area | Location | Current responsibility | Status |
| --- | --- | --- | --- |
| Process entry | `cmd/server` | Load config, signals, logging, call application | Implemented |
| Simulator | `internal/simulator`, `cmd/simulator` | Joint speed, timeout, fairness and calibration experiments | Stage 3 complete |
| Application | `internal/application` | Compose ticket, matching, result, rating and event-delivery use cases | Stage 4 complete |
| HTTP | `internal/httpapi` | Health, readiness, capabilities and authenticated result ingestion | Result endpoint implemented |
| Configuration | `internal/config` | Validated environment configuration | Implemented |
| PostgreSQL | `internal/postgres` | Pool, migrations, ticket/match/rating/outbox queues and atomic state transitions | Stage 4 complete |
| Tournament | `internal/tournament` | Versioned ticket, result and deadline lifecycle contracts | Stage 4 complete |
| Workers | `internal/worker` | Bounded matchmaking, deadline expiry, ordered rating and outbox delivery | Stage 4 workers implemented |
| Event delivery | `internal/eventdelivery` | Authenticated, idempotent HTTPS publication to the game backend | Implemented |
| Observability | `internal/observability` | Structured process logging | Foundation implemented |
| Rating | `pkg/rating` | Baseline plus versioned features, time splits, scaling and holdout calibration | Stage 5 evaluation harness implemented; baseline active |
| Matching | `pkg/matchmaking` | Bounded fair selection and deterministic retry scheduling | Stage 3 complete |
| Current API | `api/openapi.yaml` | Machine-readable health, capability and result endpoints | Implemented |
| Planned API | `docs/api-contract.md` | Remaining ticket, room and rating integration boundary | Draft |
| Persistence | `migrations` | Embedded schema including ticket, worker, result and outbox invariants | Stage 4 complete |

## Dependency direction

- `cmd` may depend on `internal`.
- `internal/application` composes adapters and domain packages.
- adapters may implement interfaces consumed by application use cases.
- `pkg/rating` and `pkg/matchmaking` never depend on `internal`, HTTP or storage.
- matching may use immutable pre-game rating contracts.

## Risk map

| Risk | Guardrail |
| --- | --- |
| Outcome leakage into opponent selection | Candidates carry pre-game rating snapshots; current-game results are absent |
| Fast but unfair room packing | Whole-room hard fairness constraints remain separate from fill priority |
| Queue fragmentation | Match on predicted outcomes; avoid hard partitions per rating feature |
| Duplicate assignment or rating | Lease fencing, aggregate versions, database uniqueness and event idempotency |
| Lost or duplicated external event | Transactional outbox, fenced retries and receiver deduplication by event id |
| Inflated accuracy claims | Time-ordered evaluation on real data; simulations validate behavior only |

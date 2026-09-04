# Project map

Read this file before exploring the repository. Update only when responsibilities
or stage status change.

| Area | Location | Current responsibility | Status |
| --- | --- | --- | --- |
| Process entry | `cmd/server` | Load config, signals, logging, call application | Implemented |
| Simulator | `cmd/simulator` | Reproducible traffic, skill and outcome simulation | Stage 3 |
| Application | `internal/application` | Compose resources and future use cases | Foundation implemented |
| HTTP | `internal/httpapi` | Health, readiness, authenticated capability API | Implemented |
| Configuration | `internal/config` | Validated environment configuration | Implemented |
| PostgreSQL | `internal/postgres` | Bounded connection pool and readiness | Foundation implemented |
| Tournament | `internal/tournament` | Versioned lifecycle contracts | Contracts implemented |
| Workers | `internal/worker` | Queue wake-ups, expiry and outbox delivery | Reserved for stage 4 |
| Observability | `internal/observability` | Structured process logging | Foundation implemented |
| Rating | `pkg/rating` | Baseline updates, placement predictions and calibration contracts | Stage 2 in progress |
| Matching | `pkg/matchmaking` | Portable candidates, rooms and policy | Contracts implemented |
| Current API | `api/openapi.yaml` | Machine-readable implemented endpoints | Implemented |
| Planned API | `docs/api-contract.md` | Integration boundary awaiting use cases | Draft |
| Persistence | `migrations` | Versioned schema migrations | Stage 4 |

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
| Duplicate assignment or rating | Database uniqueness and event idempotency in stage 4 |
| Lost external event after commit | Transactional outbox in stage 4 |
| Inflated accuracy claims | Time-ordered evaluation on real data; simulations validate behavior only |

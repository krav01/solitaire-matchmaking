# Project map

Read this file before exploring the repository. Update only when responsibilities
or stage status change.

| Area | Location | Current responsibility | Status |
| --- | --- | --- | --- |
| Process entry | `cmd/server` | Load config, signals, logging, call application | Implemented |
| Simulator | `internal/simulator`, `cmd/simulator` | Joint speed, timeout, fairness and calibration experiments | Stage 3 complete |
| Application | `internal/application` | Compose ticket, matching, result, rating and event-delivery use cases | Stage 4 complete |
| HTTP | `internal/httpapi` | Health, readiness, capabilities, ticket/result lifecycle and room/rating reads | Integration endpoints implemented |
| Configuration | `internal/config` | Validated environment configuration | Implemented |
| PostgreSQL | `internal/postgres` | Pool, migrations, ticket/match/rating/outbox queues, isolated shadow timeline and atomic state transitions | Shadow runtime implemented |
| Tournament | `internal/tournament` | Versioned ticket, result and deadline lifecycle contracts | Stage 4 complete |
| Workers | `internal/worker` | Bounded matchmaking, deadline expiry, ordered active/shadow rating and outbox delivery | Shadow runtime implemented |
| Event delivery | `internal/eventdelivery` | Authenticated, idempotent HTTPS publication to the game backend | Implemented |
| Observability | `internal/observability`, `deploy/observability`, `docs/slo.md` | Structured logging, bounded Prometheus and PostgreSQL pool metrics, pilot SLO recording rules, Grafana dashboard and alerts | Stage 7 pilot SLO profile implemented |
| Rating | `pkg/rating` | Baseline plus explicit-weight extended prediction, feature profiles, calibration, comparison and rollout contracts | Shadow runtime implemented; real evidence pending |
| Matching | `pkg/matchmaking` | Bounded fair selection and deterministic retry scheduling | Stage 3 complete |
| Current API | `api/openapi.yaml` | Machine-readable operational, ticket, room, rating, result and outbound event contracts | Integration surface implemented |
| Integration | `docs/api-contract.md`, `examples/game-backend`, `.github/workflows/canary.yml` | API contract, event payload catalogue, retry rules, tested Go receiver and external canary lifecycle | Stage 7 synthetic canary validation implemented |
| Persistence | `migrations` | Embedded schema including ticket, worker, result, outbox and isolated rating-shadow invariants | Shadow runtime implemented |
| Engineering guardrails | `.github`, `CONTRIBUTING.md`, `SECURITY.md`, `scripts/check-architecture.sh`, `docs/definition-of-done.md`, `docs/resilience-testing.md` | Contribution and disclosure policy plus risk-based dependency, architecture, resilience, fuzz, race, lint and security checks | Enforced in CI |
| Release operations | `.github/workflows/release.yml`, `.github/workflows/backup-restore-rehearsal.yml`, `.github/workflows/canary.yml`, `scripts/rehearse-backup-restore.sh`, `docs/deployment.md`, `docs/security-review.md`, `docs/release-checklist.md` | Tag-gated GHCR publication, backup/restore and canary rehearsals, pre-push image scanning, SBOM and provenance attestations, migration, SLO and rollback procedure | Stage 7 artifact, recovery, synthetic canary and pilot SLO validation implemented; real pilot evidence pending |

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
| Outcome leakage into shadow evidence | One logical room/result timeline; room prediction wins timestamp ties |
| Fast but unfair room packing | Whole-room hard fairness constraints remain separate from fill priority |
| Queue fragmentation | Match on predicted outcomes; avoid hard partitions per rating feature |
| Duplicate assignment or rating | Lease fencing, aggregate versions, database uniqueness and event idempotency |
| Lost or duplicated external event | Transactional outbox, fenced retries and receiver deduplication by event id |
| Inflated accuracy claims | Time-ordered evaluation on real data; simulations validate behavior only |

# ADR-003: Layered dependency direction

Status: Accepted

## Context

The service contains reusable domain logic, application orchestration, HTTP/persistence adapters, and process entry points. Without explicit dependency rules, adapter concerns can leak into matchmaking/rating logic and make the system harder to test or evolve.

## Decision

Dependencies point inward toward domain/application contracts:

- `cmd/*` composes and starts the process;
- `internal/application` orchestrates use cases and dependency wiring;
- HTTP, PostgreSQL, workers, and other adapters live under `internal/*` and implement interfaces consumed by the application/domain;
- `pkg/rating` and `pkg/matchmaking` remain independent from `internal/*`, HTTP, databases, and adapter-specific dependencies.

Interfaces are declared at the consumer when practical. New cross-layer dependencies require an ADR update or a new ADR.

CI enforces the most critical package boundary with `scripts/check-architecture.sh`.

## Alternatives

- Allow arbitrary imports and rely on review: rejected because architectural drift is easy to miss.
- Introduce a framework-heavy dependency-injection layer: rejected because explicit Go composition is simpler for the current service.

## Consequences

- Domain packages remain fast to test and reusable in simulators.
- Adapter changes do not force domain changes.
- Some translation code is expected at package boundaries.

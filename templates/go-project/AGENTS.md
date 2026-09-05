# Project instructions

## Working mode

- Read `docs/project-map.md`, relevant ADRs, and the current diff before broad repository exploration.
- Do not re-audit unchanged modules unless the change depends on them.
- Classify each task as low, medium, or high risk using `docs/definition-of-done.md` before choosing verification depth.
- Prefer cohesive changes over scattered micro-edits and avoid unrelated refactors.
- Use fail-fast verification: format/targeted tests first, then package/lint checks, then heavy race/security/performance gates when risk justifies them.
- Work sequentially by default; use additional agents only when parallel decomposition has clear value.

## Go rules

- Prefer the standard library unless an external dependency provides clear value.
- Follow `docs/design-principles.md` for Clean Code, DDD, Clean Architecture, SOLID, and package design.
- Use constructor dependency injection and small interfaces at the consumer.
- Avoid global mutable state and hidden initialization side effects.
- Propagate `context.Context` across request, persistence, and worker boundaries; never store contexts in structs.
- Wrap errors with useful operation context while preserving `errors.Is` and `errors.As` behavior.
- Every goroutine must have an owner, cancellation path, bounded lifetime, and clear synchronization ownership.
- Close resources deterministically.
- Prefer explicit readable code over clever abstractions; do not generalize before variation is real.

## Architecture

- Keep domain/business logic independent of HTTP, persistence, queues, and frameworks.
- Apply DDD selectively where business complexity justifies entities, value objects, aggregates, repositories, domain services, events, and bounded contexts.
- Application/use-case code coordinates domain behavior and external effects; infrastructure adapters implement consumer-owned ports.
- Dependency direction must point toward business policy: transport/infrastructure -> application -> domain.
- New cross-layer dependencies require an ADR or an explicit project-map update.
- Keep package dependency direction simple enough to explain in `docs/project-map.md`.
- Avoid generic `utils`, `helpers`, `common`, or manager packages when a domain/capability-specific home is available.

## Security and persistence

- Follow `docs/database-guidelines.md` for SQL, PostgreSQL, transactions, connection pools, constraints, indexing, migrations, idempotency, and database tests.
- Never commit credentials, tokens, private keys, production secrets, or real user data.
- Validate untrusted input at boundaries and apply size/time limits where appropriate.
- Parameterize SQL and keep transactional boundaries explicit.
- Keep transactions short and avoid external network calls inside database transactions.
- Enforce durable persistence invariants with database constraints where appropriate, not application checks alone.
- Do not log secrets or authentication material.
- Review new dependencies for necessity, maintenance quality, and reachable vulnerabilities.

## Testing and review

- Bug fixes require regression tests.
- Prefer deterministic table-driven tests for domain logic.
- Use fuzz/property tests for parser, validation, serialization, and invariant-heavy code when useful.
- Add benchmarks to genuinely performance-critical paths and compare before/after when those paths change.
- Use real database integration tests for SQL semantics, migrations, constraints, locking, transactions, and concurrency-sensitive persistence behavior.
- Review diffs for correctness, domain invariant placement, DDD/architecture boundaries, concurrency, context propagation, resource leaks, SQL safety, query/index impact, migration safety, security, compatibility, observability, performance, and missing tests.

## Documentation

- Keep `docs/project-map.md` short and current.
- Record durable architecture decisions in `docs/decisions/` and keep the decision index current.
- Update Definition of Done only when process reality changes.
- Final project report format: what changed / checks / risks / next step.

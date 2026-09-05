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
- Use constructor dependency injection and small interfaces at the consumer.
- Avoid global mutable state and hidden initialization side effects.
- Propagate `context.Context` across request, persistence, and worker boundaries; never store contexts in structs.
- Wrap errors with useful operation context while preserving `errors.Is` and `errors.As` behavior.
- Every goroutine must have an owner, cancellation path, bounded lifetime, and clear synchronization ownership.
- Close resources deterministically.

## Architecture

- Keep domain/business logic independent of HTTP, persistence, queues, and frameworks.
- Adapters implement interfaces consumed by application/domain code, not the reverse.
- New cross-layer dependencies require an ADR or an explicit project-map update.
- Keep package dependency direction simple enough to explain in `docs/project-map.md`.

## Security and persistence

- Never commit credentials, tokens, private keys, production secrets, or real user data.
- Validate untrusted input at boundaries and apply size/time limits where appropriate.
- Parameterize SQL and keep transactional boundaries explicit.
- Do not log secrets or authentication material.
- Review new dependencies for necessity, maintenance quality, and reachable vulnerabilities.

## Testing and review

- Bug fixes require regression tests.
- Prefer deterministic table-driven tests for domain logic.
- Use fuzz/property tests for parser, validation, serialization, and invariant-heavy code when useful.
- Add benchmarks to genuinely performance-critical paths and compare before/after when those paths change.
- Review diffs for correctness, concurrency, context propagation, resource leaks, SQL safety, security, compatibility, architecture drift, observability, and missing tests.

## Documentation

- Keep `docs/project-map.md` short and current.
- Record durable architecture decisions in `docs/decisions/`.
- Update Definition of Done only when process reality changes.
- Final project report format: what changed / checks / risks / next step.

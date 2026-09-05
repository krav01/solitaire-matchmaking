# Project instructions

Always load `samber/cc-skills-golang@golang-how-to` (or its installed equivalent) for Go work and select relevant skills.

## Working mode

- Read `docs/project-map.md`, relevant ADRs, and the current diff first. Do not re-audit unchanged modules.
- Code, names, commits, API contracts, documentation, and technical artifacts are in English. User reports are in Russian.
- Work sequentially without agents unless the user explicitly requests delegation.
- Prefer cohesive changes over scattered micro-commits. Do not refactor unrelated code.
- Use fail-fast verification: formatting and targeted tests first, then package/lint checks, then full race/security gates for substantial changes and releases.

## Go rules

- Prefer the standard library unless an external dependency provides clear, justified value.
- Inject dependencies through constructors; declare small interfaces at their consumer.
- Avoid global mutable state and hidden package initialization side effects.
- Propagate `context.Context` across request, persistence, and worker boundaries; do not store contexts in structs.
- Wrap errors with useful operation context while preserving `errors.Is`/`errors.As` behavior.
- Every goroutine must have an explicit owner, cancellation path, and bounded lifetime.
- Close resources deterministically and keep synchronization ownership obvious.

## Architecture

- Keep `pkg/rating` and `pkg/matchmaking` independent of HTTP, persistence, and `internal` packages.
- Domain packages must not import adapters or infrastructure-specific packages.
- `internal/application` owns composition and use-case orchestration; adapters implement interfaces consumed by application/domain code.
- New cross-layer dependencies require an ADR or an explicit update to an existing ADR.
- Run `scripts/check-architecture.sh` when package dependencies change.

## Matchmaking invariants

- Match only using information available before the participant's game. Never use an open room's submitted scores to choose opponents.
- Fairness limits apply to the entire room. Fill speed must not override a hard fairness limit or change an entry fee or room size.
- Keep skill uncertainty separate from performance variability. Missing features are not zero-valued observations.

## Security and persistence

- Never commit credentials, tokens, private keys, production endpoints with secrets, or real user data.
- Validate untrusted input at system boundaries and enforce size/time limits where applicable.
- Parameterize SQL. Persist state transitions and outgoing events atomically when persistence is implemented.
- Do not log secrets or authentication material. Treat player identifiers and telemetry as sensitive operational data.
- New dependencies must pass dependency review and `govulncheck`; justify dependencies that expand the attack surface.

## Testing and review

- Test changed behavior; bug fixes require regression tests.
- Prefer table-driven tests for deterministic domain logic.
- Small change: targeted tests. Medium change: affected package tests plus lint. Large/release change: full tests, race detector, lint, architecture guard, integration tests, and vulnerability scan.
- Review diffs specifically for correctness, concurrency, context propagation, resource leaks, SQL safety, breaking API changes, architecture-boundary violations, and missing tests.
- Never claim statistical accuracy from synthetic tests. Keep training and evaluation ordered by event time and data availability.

## Documentation

- Update `docs/project-map.md` when responsibilities or stage status change.
- Record durable architectural choices in `docs/decisions/`.
- Keep `docs/security.md` and `docs/testing.md` aligned with actual CI behavior.
- Report: what changed / checks / risks / next step.

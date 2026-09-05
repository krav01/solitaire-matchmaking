# Diff review checklist

Use this checklist for human or AI-assisted review. Review the changed diff first; inspect unchanged code only when the diff depends on it.

## Scope and risk

- Is the task classified as low, medium, or high risk using `docs/definition-of-done.md`?
- Is the change minimal for the requested behavior, without unrelated cleanup?
- Does the verification depth match the risk level?

## Correctness

- Does the change preserve existing invariants and API contracts?
- Are edge cases, nil/empty inputs, retries, and partial failures handled?
- Does a bug fix include a regression test?

## Failure scenarios

For non-trivial distributed/persistence changes, consider:

- timeout/cancellation;
- duplicate delivery or duplicate request;
- retry after partial success;
- process restart;
- stale lease/version;
- dependency or database unavailability;
- idempotent recovery.

## Concurrency and lifecycle

- Does every goroutine have an owner, cancellation path, and bounded lifetime?
- Can shared state race, deadlock, leak, or be processed twice?
- Are retries and worker concurrency bounded?

## Context and resources

- Is `context.Context` propagated across request, DB, and worker boundaries?
- Are rows, bodies, timers, connections, and other resources closed deterministically?
- Are network/database calls bounded by cancellation or timeout?

## Security

- Is untrusted input validated at the boundary?
- Is SQL parameterized?
- Could secrets, tokens, connection credentials, or sensitive player data be logged?
- Does a new dependency materially expand the attack surface?

## Architecture

- Do `pkg/rating` and `pkg/matchmaking` remain independent from adapters and `internal/*`?
- Did any domain code gain HTTP, persistence, or framework coupling?
- Does a new cross-layer dependency require an ADR?

## Matchmaking integrity

- Does matching use only pre-game information?
- Can fill-speed optimization bypass a hard fairness constraint?
- Are entry fee and room size preserved?
- Are missing features kept distinct from zero values?
- Is skill uncertainty kept distinct from performance variability?

## Compatibility and migrations

- Are public/API/persistence changes backward compatible or explicitly versioned?
- Do schema changes follow `docs/migration-safety.md`?
- Could deployment overlap old/new binaries safely?
- Are lock, backfill, rollback, and destructive-change risks understood?

## Performance

- Is a critical path changed?
- Does an existing/new benchmark cover the changed algorithm where practical?
- Are `ns/op`, `B/op`, and `allocs/op` materially worse?
- Is any regression justified without trading away correctness/fairness/security?
- Are benchmark results kept separate from production capacity claims?

## Observability

- Are new failure modes diagnosable?
- Are metrics/logging/tracing assumptions updated when needed?
- Could sensitive data leak into logs or telemetry?
- Could the change alter operational metrics silently?

## Verification

- Low: targeted tests and relevant static checks.
- Medium: affected package tests + lint + architecture/integration checks where applicable.
- High/release: full tests + race + lint + architecture + integration + `govulncheck` + relevant fuzz/performance/failure checks.

## Learning and debt

- If this fixes an escaped defect, can a reusable guardrail prevent the same class of problem?
- Is unrelated but real debt recorded in `docs/technical-debt.md` instead of being mixed into the PR?

# Diff review checklist

Use this checklist for human or AI-assisted review. Review the changed diff first; inspect unchanged code only when the diff depends on it.

## Correctness

- Does the change preserve existing invariants and API contracts?
- Are edge cases, nil/empty inputs, retries, and partial failures handled?
- Does a bug fix include a regression test?

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

## Compatibility and observability

- Are public/API/persistence changes backward compatible or explicitly versioned?
- Are failures observable without logging secrets?
- Could the change alter metrics or operational assumptions silently?

## Verification

- Small: targeted tests.
- Medium: affected package tests + lint + architecture guard when imports changed.
- Large/release: full tests + race + lint + architecture + integration + `govulncheck`.

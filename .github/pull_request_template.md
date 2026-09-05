## Summary

- What changed:
- Why:
- Risk level: low / medium / high

## Definition of Done

- [ ] Change scope matches `docs/definition-of-done.md`
- [ ] No unrelated refactor is mixed into the change
- [ ] Durable architecture decisions are documented when changed

## Risk

- [ ] Correctness / edge cases reviewed
- [ ] Concurrency / lifecycle reviewed
- [ ] Context / resource handling reviewed
- [ ] Security / SQL / secret handling reviewed
- [ ] Architecture boundaries reviewed
- [ ] API / persistence compatibility reviewed
- [ ] Failure scenarios reviewed: timeout / retry / duplicate / partial failure / restart when applicable

## Matchmaking integrity (when applicable)

- [ ] Uses only pre-game information
- [ ] Hard fairness limits cannot be bypassed by fill-speed optimization
- [ ] Entry fee and room size invariants are preserved
- [ ] Missing features are not treated as zero

## Performance and observability

- [ ] Critical-path performance impact measured or explicitly not applicable
- [ ] Benchmark regression reviewed when a critical algorithm changed
- [ ] Metrics/logging/tracing impact reviewed
- [ ] No sensitive data is added to logs or telemetry

## Verification

- [ ] Targeted or affected package tests
- [ ] Fuzz/property test added or run for parser/validation/invariant-heavy changes when applicable
- [ ] API contract validated when handlers/contracts changed
- [ ] Lint/static analysis
- [ ] Architecture guard when imports/responsibilities changed
- [ ] Integration tests when persistence behavior changed
- [ ] Migration safety reviewed for schema changes
- [ ] Full race/security gates for high-risk or release changes

See `docs/review-checklist.md`, `docs/testing.md`, and `docs/definition-of-done.md` for the detailed rubric.

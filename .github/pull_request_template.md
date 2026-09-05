## Summary

- What changed:
- Why:

## Risk

- [ ] Correctness / edge cases reviewed
- [ ] Concurrency / lifecycle reviewed
- [ ] Context / resource handling reviewed
- [ ] Security / SQL / secret handling reviewed
- [ ] Architecture boundaries reviewed
- [ ] API / persistence compatibility reviewed

## Matchmaking integrity (when applicable)

- [ ] Uses only pre-game information
- [ ] Hard fairness limits cannot be bypassed by fill-speed optimization
- [ ] Entry fee and room size invariants are preserved
- [ ] Missing features are not treated as zero

## Verification

- [ ] Targeted or affected package tests
- [ ] Lint/static analysis
- [ ] Architecture guard when imports/responsibilities changed
- [ ] Integration tests when persistence behavior changed
- [ ] Full race/security gates for substantial or release changes

See `docs/review-checklist.md` for the detailed diff review rubric.

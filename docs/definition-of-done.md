# Definition of Done and risk levels

Every task gets a risk level before implementation. The level controls the required checks; it does not change product correctness requirements.

## Risk levels

### Low

Typical changes: documentation, isolated mapping/validation, deterministic helper, non-behavioral cleanup.

Required:
- changed behavior is covered when behavior changed;
- targeted tests;
- formatting/static checks for touched Go code;
- no unrelated refactor.

### Medium

Typical changes: package behavior, HTTP handler/use case, SQL query, configuration, non-critical dependency change.

Required:
- affected package tests;
- lint/static analysis;
- architecture guard when imports/responsibilities changed;
- integration tests for persistence/API boundary changes;
- observability impact reviewed.

### High

Typical changes: matchmaking/rating algorithm, concurrency/worker lifecycle, schema migration, authentication/security boundary, public API compatibility, cross-package architecture change.

Required:
- full test suite;
- race detector;
- lint and architecture guard;
- relevant integration tests;
- vulnerability/dependency review;
- failure scenarios documented/tested;
- performance impact measured for critical paths;
- ADR update when the durable architecture decision changes.

## Task-specific completion

### Feature

Done when implementation, tests, relevant docs/contracts, observability, failure behavior, and performance impact are covered.

### Bug fix

Done when a regression test reproduces the defect before the fix, the fix is minimal, and related invariants are rechecked.

### Refactor

Done when externally observable behavior is intentionally unchanged and tests demonstrate compatibility.

### Database change

Done when migration application is tested, compatibility/locking risk is reviewed, and rollout does not require an unsafe single-step destructive change.

### API change

Done when the machine-readable contract and implementation agree and compatibility/versioning is explicit.

## Learning loop

For a meaningful production defect or escaped review issue:

1. fix the defect;
2. add a regression test;
3. identify why existing checks missed it;
4. add or improve a reusable guardrail when practical.

The goal is that the same class of defect becomes harder to repeat.

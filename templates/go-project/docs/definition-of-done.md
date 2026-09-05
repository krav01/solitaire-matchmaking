# Definition of Done

Choose the risk level before implementation. Risk controls verification depth; it never weakens correctness requirements.

## Low risk

Typical: docs, isolated validation, mapping, deterministic helper, non-behavioral cleanup.

Required:
- targeted tests when behavior changes;
- formatting/static checks for touched Go code;
- no unrelated refactor.

## Medium risk

Typical: package behavior, HTTP/use case, configuration, SQL query, dependency change.

Required:
- affected package tests;
- lint/static analysis;
- architecture check when imports/responsibilities change;
- relevant integration tests for persistence or boundary changes;
- observability impact review.

## High risk

Typical: concurrency, schema migration, authentication/security boundary, public API compatibility, critical algorithm, cross-package architecture change.

Required:
- full tests;
- race detector;
- lint/static analysis;
- architecture guard;
- relevant integration/end-to-end tests;
- vulnerability/dependency review;
- failure scenarios reviewed;
- performance impact measured for critical paths;
- ADR update when the durable architecture decision changes.

## Task-specific completion

### Feature
Implementation + tests + relevant docs/contracts + observability + failure behavior.

### Bug fix
Regression test + minimal fix + invariant recheck.

### Refactor
Behavior intentionally unchanged + compatibility tests.

### Database change
Migration tested + rollback/forward compatibility considered + lock risk reviewed.

### API change
Machine-readable contract and implementation agree; compatibility/versioning is explicit.

## Learning loop

For meaningful escaped defects:

1. fix the defect;
2. add a regression test;
3. identify why previous checks missed it;
4. improve a reusable guardrail when practical.

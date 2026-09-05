# Development standards adoption

This project adopts `krav01/dev-standards` baseline version `1.4.0` at the `high-load-critical` maturity level.

## Precedence

Project-specific invariants in `AGENTS.md`, ADRs, and architecture documents take precedence when they are stricter or more specific than the shared baseline.

## Applied controls

- risk-based verification and Definition of Done;
- Clean Code, DDD, Clean Architecture, and pragmatic SOLID guidance;
- PostgreSQL transaction, migration, idempotency, and integration-test discipline;
- race testing, linting, and vulnerability scanning;
- project-specific PostgreSQL integration tests;
- architecture and invariant review appropriate to matchmaking correctness.

## Central workflow exception

The shared standards repository is private while this repository is public. GitHub Actions does not support a public repository calling a reusable workflow from a private repository. Therefore the project keeps its local CI workflow and records the exception in `.standards.yml` rather than duplicating central jobs.

The same restriction applies to the shared standards-drift workflow. Version adoption is recorded explicitly in `.standards.yml` until the repository visibility/access model changes.

## Upgrade process

Standards upgrades use a dedicated PR:

1. review the `dev-standards` changelog;
2. compare new rules against project-specific invariants;
3. update `.standards.yml`;
4. adopt only applicable controls;
5. run the project CI;
6. document deferred controls with a reason and revisit trigger.

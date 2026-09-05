# Contributing

Thank you for improving Solitaire Matchmaking. Keep changes small, reviewable,
and aligned with the service's fairness and durability invariants.

## Before changing code

1. Read `docs/project-map.md` and the relevant architecture decisions.
2. Classify the change using `docs/definition-of-done.md`.
3. Check `docs/technical-debt.md` before expanding the scope.
4. For API or persistence work, read `docs/api-contract.md` or
   `docs/migration-safety.md` respectively.

## Development workflow

- Use Go 1.26 or later and PostgreSQL 18 for integration tests.
- Create a focused branch and avoid unrelated refactors.
- Add tests for changed behavior; bug fixes require a regression test.
- Keep public contracts, code, documentation, and commit messages in English.
- Preserve the dependency direction enforced by
  `scripts/check-architecture.sh`.
- Do not add production credentials, private endpoints, or real player data.

Run the checks required by the change's risk level. The common local gate is:

```bash
make check
make security
```

Persistence and release changes also require a disposable PostgreSQL database:

```bash
TEST_DATABASE_URL='postgres://...' make release-check
```

## Pull requests

Complete the pull request template, explain user-visible and operational impact,
and call out compatibility, concurrency, security, observability, and performance
effects where relevant. Do not bypass a failing gate with a broad suppression.

Use the [review checklist](docs/review-checklist.md) for detailed guidance.
Report suspected vulnerabilities privately as described in
[SECURITY.md](SECURITY.md), not in a public issue.

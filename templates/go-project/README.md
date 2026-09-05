# Go project baseline template

Reusable baseline for new Go repositories. Copy the relevant files into a new repository and replace placeholders before implementation starts.

## Included

- `AGENTS.md` — working rules for AI-assisted development and review.
- `docs/project-map.md` — compact persistent project context.
- `docs/definition-of-done.md` — risk classification and verification depth.
- `docs/maturity-levels.md` — `basic`, `production`, and `high-load / critical` control levels.
- `docs/design-principles.md` — Clean Code, DDD, Clean Architecture, SOLID, and package design guidance.
- `docs/database-guidelines.md` — Go/SQL/PostgreSQL persistence, transactions, pools, constraints, indexes, migrations, idempotency, and database testing.
- `docs/decisions/README.md` — decision index.
- `docs/decisions/ADR-TEMPLATE.md` — durable architecture-decision template.
- `docs/incidents.md` — escaped-defect to regression-test/guardrail workflow.
- `docs/architecture-health.md` — periodic architecture health review.
- `docs/go-patterns.md` — reusable Go design patterns and invariants.
- `.github/pull_request_template.md` — risk/reasoning/review/verification checklist.
- `.github/workflows/ci.yml` — minimal Go CI baseline.
- `.golangci.yml` — baseline lint configuration.

## Bootstrap order

1. Create the Go module and minimal package skeleton.
2. Choose the maturity level using `docs/maturity-levels.md`.
3. Fill `docs/project-map.md` with actual boundaries and risks.
4. Define project-specific invariants in `AGENTS.md`.
5. Apply `docs/design-principles.md` and `docs/database-guidelines.md` where relevant.
6. Record irreversible or expensive architectural choices as ADRs and add them to the decision index.
7. Keep CI cheap on pull requests and reserve heavy race/security/performance gates for high-risk changes or `main`.
8. Enable GitHub Dependency Graph and branch/ruleset protection when available.

## Ongoing workflow

- Medium/high-risk PRs record the chosen approach, alternatives, accepted risk, and revisit trigger.
- Escaped defects follow `docs/incidents.md`: regression test first, then a reusable guardrail when justified.
- Production/high-load projects periodically run the architecture health checklist.
- Prefer patterns from `docs/go-patterns.md` as design guidance, not blind copy-paste.
- Review persistence changes against `docs/database-guidelines.md` and domain/architecture changes against `docs/design-principles.md`.

## Principle

The template is a starting point, not a checklist to copy blindly. Remove irrelevant controls and add project-specific invariants. Prefer a small set of enforced rules over a large set of ignored rules.

# Go project baseline template

Reusable baseline for new Go repositories. Copy the relevant files into a new repository and replace placeholders before implementation starts.

## Included

- `AGENTS.md` — working rules for AI-assisted development and review.
- `docs/project-map.md` — compact persistent project context.
- `docs/definition-of-done.md` — risk classification and verification depth.
- `docs/decisions/ADR-TEMPLATE.md` — durable architecture-decision template.
- `.github/pull_request_template.md` — risk/review/verification checklist.
- `.github/workflows/ci.yml` — minimal Go CI baseline.

## Bootstrap order

1. Create the Go module and minimal package skeleton.
2. Fill `docs/project-map.md` with actual boundaries and risks.
3. Define project-specific invariants in `AGENTS.md`.
4. Record irreversible or expensive architectural choices as ADRs.
5. Keep CI cheap on pull requests and reserve heavy race/security/performance gates for high-risk changes or `main`.
6. Enable GitHub Dependency Graph and branch/ruleset protection when available.

## Principle

The template is a starting point, not a checklist to copy blindly. Remove irrelevant controls and add project-specific invariants. Prefer a small set of enforced rules over a large set of ignored rules.

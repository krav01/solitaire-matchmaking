# Decision index

Keep this file as the entry point for durable technical decisions. Add one row whenever an ADR is accepted, superseded, or rejected.

| ADR | Status | Date | Decision | Revisit trigger |
| --- | --- | --- | --- | --- |
| `ADR-001-...md` | Accepted | YYYY-MM-DD | <one-line decision> | <scale/change trigger> |

## Rules

- Use an ADR for decisions that are expensive to reverse, cross package/service boundaries, affect persistence/API/security/concurrency, or define a long-lived invariant.
- Keep local implementation explanations in code comments or PR reasoning instead of creating unnecessary ADRs.
- When replacing a decision, mark the old ADR `Superseded` and link the replacement.

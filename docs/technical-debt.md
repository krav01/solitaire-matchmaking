# Technical debt register

Record debt that is real but not worth mixing into the current feature or fix.

| ID | Area | Severity | Debt | Why deferred | Trigger to fix | Status |
| --- | --- | --- | --- | --- | --- | --- |
| TD-001 | Observability | Medium | Logging exists but service metrics/tracing are still foundation-level | Current stage focuses on durable matchmaking lifecycle | Before production load testing or external rollout | Open |
| TD-002 | Performance | Medium | Stable benchmark baseline is not yet established in CI | Representative performance environment is not defined | Before performance SLO enforcement | Open |

## Rules

- Do not create debt entries for vague cleanup wishes.
- State the operational or engineering consequence.
- Give each item a trigger so debt does not become an endless backlog.
- High-severity correctness/security debt should normally be fixed immediately instead of registered.
- Remove or close entries when resolved.

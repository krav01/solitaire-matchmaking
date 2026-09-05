# Technical debt register

Record debt that is real but not worth mixing into the current feature or fix.

| ID | Area | Severity | Debt | Why deferred | Trigger to fix | Status |
| --- | --- | --- | --- | --- | --- | --- |
| TD-002 | Performance | Medium | Automated stable benchmark comparison thresholds are not established | CI records a baseline, but representative runner variability is not characterized | Before performance SLO enforcement | Open |

## Rules

- Do not create debt entries for vague cleanup wishes.
- State the operational or engineering consequence.
- Give each item a trigger so debt does not become an endless backlog.
- High-severity correctness/security debt should normally be fixed immediately instead of registered.
- Remove or close entries when resolved.

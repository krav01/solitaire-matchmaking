# Incident-to-guardrail workflow

Use this for escaped defects, production incidents, or repeated review misses.

## Required sequence

1. Capture the failure mode and user/system impact.
2. Add the smallest regression test that reproduces the defect class.
3. Fix the defect with minimal unrelated change.
4. Identify why existing tests/review/CI did not catch it.
5. Add a reusable guardrail when practical: test helper, lint rule, architecture check, CI gate, checklist item, invariant, or ADR update.
6. Record the verification that proves the guardrail works.

## Incident note template

- Date:
- Severity:
- Area:
- Failure mode:
- Impact:
- Detection gap:
- Regression test:
- Fix:
- New guardrail:
- Follow-up trigger:

## Principle

Do not create process for every typo. Promote a lesson into a reusable rule only when it can prevent a meaningful class of future defects.

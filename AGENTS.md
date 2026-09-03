# Project instructions

Always load `samber/cc-skills-golang@golang-how-to` (or its installed equivalent) for Go work and select relevant skills.

- Read `docs/project-map.md` and the current diff first. Do not re-audit unchanged modules.
- Code, names, commits, API contracts and documentation are in English. User reports are in Russian.
- Work sequentially without agents unless the user explicitly requests delegation.
- Implement the agreed roadmap in cohesive stages. Do not add later-stage algorithms to the foundation stage.
- Keep `pkg/rating` and `pkg/matchmaking` independent of HTTP, persistence and internal packages.
- Inject dependencies through constructors; declare small interfaces at their consumer.
- Match only using information available before the participant's game. Never use an open room's submitted scores to choose opponents.
- Fairness limits apply to the entire room. Fill speed must not override a hard fairness limit or change an entry fee or room size.
- Keep skill uncertainty separate from performance variability. Missing features are not zero-valued observations.
- Parameterize SQL. Persist state transitions and outgoing events atomically when persistence is implemented.
- Test changed behavior, using targeted tests for small changes and package tests plus lint for medium changes. Run the full test/race/lint/security gates for releases and substantial changes.
- Never claim statistical accuracy from synthetic tests. Keep training and evaluation ordered by event time and data availability.
- Update the project map and roadmap when a stage changes. Report what changed, checks, limitations and next step.

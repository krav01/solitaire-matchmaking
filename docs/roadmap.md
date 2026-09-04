# Delivery roadmap

## Stage 1 — Foundation

- [x] Go module and right-sized package boundaries
- [x] validated environment configuration and explicit dependency wiring
- [x] PostgreSQL connection pool and readiness
- [x] liveness and authenticated capability API
- [x] domain contracts for tournaments, rating and matching policy
- [x] documented trust boundary, data model and quality measures
- [x] unit tests, race/lint/security CI and container setup
- [x] publish the foundation branch and run hosted CI

## Stage 2 — Baseline rating

- [x] rating update from complete placements and opponent estimates
- [x] uncertainty-aware updates for five-, six- and seven-player rooms
- [x] immutable model versions and replayable rating history contract
- [x] predicted placement probabilities and calibration test harness
- [x] deterministic unit, property and simulation tests

## Stage 3 — Match formation and simulator

- [x] eligibility filtering and whole-room fairness validation
- [x] bounded skill-window expansion and waiting-age priority
- [x] preference for completing nearly full eligible rooms
- [x] event-driven selection plus periodic retry boundary
- [ ] reproducible arrivals, skills, outcomes and tournament partition simulation
- [ ] joint reports for fill latency, timeout rate and fairness

## Stage 4 — Transactional tournament lifecycle

- reviewed SQL migrations and explicit migration runner
- idempotent ticket acceptance, assignment and cancellation
- room/session state transitions and bounded worker claims
- verified-result ingestion, deadlines and finalized standings
- transactional outbox and retryable delivery
- PostgreSQL integration and end-to-end tests

## Stage 5 — Extended rating

- feature definitions aligned with scoring-rule and deck versions
- missing-value and correlated-feature handling
- time-ordered training and holdout evaluation
- baseline comparison, segment checks and version activation/rollback

## Stage 6 — Integration and release validation

- complete OpenAPI contract and game-backend example
- observability dashboards and alerts for speed, fairness and reliability
- load, recovery and failure-injection scenarios
- deployment guide, security review and release checklist

# Performance budget

Performance budgets are guardrails, not claims about production capacity. Baselines must be measured on representative hardware and workloads before hard numeric release thresholds are enforced.

## Critical paths

Track at minimum:

- matchmaking room selection latency and allocations;
- candidate filtering latency and allocations;
- queue claim/lease throughput and contention;
- PostgreSQL queries per ticket lifecycle transition;
- worker queue wait and processing latency;
- HTTP request p95/p99 latency once real traffic assumptions exist.

## Rules

- New critical-path code should include a benchmark when practical.
- A change that materially worsens benchmark time or allocations requires explanation or optimization before merge.
- Do not turn synthetic benchmark numbers into production capacity claims.
- Prefer comparing a change against the repository baseline on the same environment.
- Performance optimization must not bypass hard fairness, correctness, security, or durability constraints.

## Initial benchmark policy

Until a stable CI benchmark environment is available:

- run deterministic package benchmarks for critical algorithms;
- record `ns/op`, `B/op`, and `allocs/op` in performance-sensitive PRs;
- treat regressions above roughly 20% as a review trigger, not an automatic production SLO violation;
- establish hard budgets only after repeated baseline measurements are stable.

## Environment SLOs

The initial private-pilot objectives are defined in [`docs/slo.md`](slo.md) and
recorded by `deploy/observability/prometheus-slo-pilot.yaml`. They cover:

- p50/p95/p99 request latency;
- matchmaking queue wait time;
- room fill latency by percentile;
- worker processing/error/retry rates;
- DB query latency and pool saturation;
- memory and goroutine growth under sustained load.

Every wider-production SLO must still state workload, measurement window and
environment, and must be recalibrated from that environment's representative
evidence rather than copied from synthetic or pilot results.

# Performance budget

Performance budgets are guardrails, not claims about production capacity. Baselines must be measured on representative hardware and workloads before hard numeric release thresholds are enforced.

## Critical paths

Track at minimum:

- matchmaking room selection latency and allocations;
- end-to-end deterministic matchmaking simulation throughput;
- candidate filtering latency and allocations;
- queue claim/lease throughput and contention;
- PostgreSQL queries per ticket lifecycle transition;
- worker queue wait and processing latency;
- HTTP request p95/p99 latency once real traffic assumptions exist.

## Reproducible local and CI profile

Run:

```bash
make performance
```

The target measures two deterministic paths:

- `BenchmarkSelectRoom` for the critical room-selection algorithm;
- `BenchmarkSimulation10000Tickets` for a fixed 10,000-ticket synthetic workload generated at 250 arrivals/second with a stable seed and the baseline rating model.

The simulation benchmark reports Go benchmark time and allocation metrics plus a custom `tickets/s` processing-throughput metric. It exercises the complete in-memory simulation policy/rating path over the same immutable workload on every iteration.

These values are engineering baselines only. They do not include HTTP, network latency, PostgreSQL I/O, deployment topology, or real player traffic and therefore must not be presented as production RPS or capacity. Compare results on the same environment, Go version, commit and benchmark configuration.

The PostgreSQL resilience suite separately exercises bounded concurrent outbox delivery, expired-lease recovery and publisher-failure retry. It validates correctness under contention and failure injection rather than claiming a production throughput number.

## Rules

- New critical-path code should include a benchmark when practical.
- A change that materially worsens benchmark time or allocations requires explanation or optimization before merge.
- Do not turn synthetic benchmark numbers into production capacity claims.
- Prefer comparing a change against the repository baseline on the same environment.
- Performance optimization must not bypass hard fairness, correctness, security, or durability constraints.

## Initial benchmark policy

The main-branch CI records the deterministic performance profile after each merge. Use its output as a reproducible reference point, not a production SLO.

- record `ns/op`, `B/op`, `allocs/op`, and simulation `tickets/s`;
- treat regressions above roughly 20% as a review trigger, not an automatic production SLO violation;
- establish hard budgets only after repeated baseline measurements are stable;
- record any promoted reference baseline with the commit, Go version, date and runner environment.

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

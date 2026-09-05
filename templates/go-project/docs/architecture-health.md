# Architecture health check

Run this review periodically for production/high-load projects and before large refactors.

## Dependency health

- no cyclic package dependencies;
- domain packages do not import transport/storage/framework adapters;
- cross-layer dependencies are intentional and documented;
- interfaces are small and declared near consumers;
- shared packages are not becoming generic dumping grounds.

## Package health

- package responsibility can be described in one sentence;
- no package mixes domain, transport, persistence, and orchestration without a strong reason;
- files/functions are not growing because responsibilities are unclear;
- exported API surface is intentionally small.

## Runtime health

- goroutines have owners, cancellation, and bounded lifetimes;
- retries, queues, and worker concurrency are bounded;
- timeouts and context propagation are consistent;
- resources close deterministically;
- idempotency/fencing exists where retries or duplicate delivery can occur.

## Data/API health

- schema/API changes are backward compatible or versioned;
- migrations avoid unsafe destructive single-step rollout;
- transactional boundaries match business invariants;
- events have clear ownership, idempotency, and ordering assumptions.

## Operational health

- critical paths have useful logs/metrics/traces;
- alerts map to user/system impact rather than noisy implementation details;
- critical performance paths have budgets/benchmarks where justified;
- known debt has severity and a concrete trigger to fix it.

## Output

Record only actionable findings. For each finding include:
- severity: low / medium / high;
- area;
- evidence;
- recommended action;
- trigger/deadline if deferred.

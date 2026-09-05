# ADR-001: Rating model boundary

Status: Accepted

## Context

Matchmaking needs a stable skill signal without coupling rating calculation to HTTP, PostgreSQL, or worker implementation details. Rating inputs must reflect information available before the participant's next game and must distinguish uncertainty from observed performance variability.

## Decision

`pkg/rating` is a pure domain package. It may expose immutable rating snapshots and prediction/calibration contracts, but it must not import `internal/*`, HTTP, storage, or adapter-specific packages.

The rating model keeps uncertainty separate from performance variability. Missing features are represented as missing/unknown, not as zero-valued observations.

Training and evaluation must be ordered by event time and feature availability to prevent outcome leakage.

## Alternatives

- Compute ratings inside persistence or HTTP layers: rejected because it couples domain behavior to adapters and makes simulation/testing harder.
- Treat missing values as zero: rejected because it creates false evidence and biases predictions.

## Consequences

- Rating logic can be tested and simulated independently.
- Adapter code must translate persistence/API data into domain contracts.
- Model changes that alter semantics require an ADR update and time-ordered evaluation.

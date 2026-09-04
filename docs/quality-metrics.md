# Quality and service measures

Fairness and speed must be reported together and segmented by mode, room size,
entry fee, rating band, region if applicable, and policy/model version. An overall
average can hide a slow or unfair segment.

## Fill speed

| Metric | Definition |
| --- | --- |
| Ticket assignment latency | assignment time minus ticket creation time |
| Room fill duration | filled time minus room creation time |
| Player wait for full room | room filled time minus ticket creation time |
| Fill-timeout rate | timed-out forming rooms divided by rooms opened |
| Ticket cancellation rate | cancelled tickets divided by accepted tickets |

Report median, p90, p95 and p99 distributions. Timed-out and cancelled observations
stay visible and are not removed to improve latency percentiles.

## Fairness

| Metric | Definition |
| --- | --- |
| Predicted probability spread | maximum minus minimum predicted win probability in a room |
| Expected-place dispersion | dispersion of predicted final places before play starts |
| Calibration error | difference between predicted and observed win/placement rates |
| Upset and rank correlation | relationship between pre-game predictions and observed ordering |

For five, six and seven exactly equal players, first-place reference probabilities
are `1/5`, `1/6` and `1/7`. These are model checks, not production guarantees.

## Evaluation rules

- Simulation validates invariants, latency behavior and trade-offs under known inputs.
- Real, verified, time-ordered outcomes are required to claim predictive accuracy.
- Training uses events available by the training cutoff; evaluation uses later events.
- Compare each extended model with the placement-only baseline on the same evaluation set.
- Accept a model only when the gain is stable across relevant segments and fill speed remains acceptable.
- Set service-level objectives only after measuring arrival rates and tournament partitions.

## Simulation report

The stage-3 simulator reports overall values and one homogeneous report per
tournament partition. Each report keeps these groups together:

- player wait until the room fills or reaches its deadline;
- room resolution duration for both filled and timed-out rooms;
- filled-room duration, reported separately from resolution duration;
- room fill-timeout rate;
- rating skill gap, predicted first-place probability spread and expected-place
  dispersion for completed rooms;
- synthetic placement calibration per partition.

Distributions contain sample count, mean, maximum and linearly interpolated p50,
p90, p95 and p99. Timed-out rooms remain in player-wait and room-resolution
samples. Fairness and calibration require a complete room, so their sample counts
are reported explicitly instead of substituting zero-valued observations.

Every report includes its workload and matching configuration for replay.
Synthetic calibration measures consistency under known generated inputs; it
does not support a claim about production prediction accuracy.

The current scenario assigns every arrival immediately: it joins an eligible
forming room or opens a new one. Ticket-assignment latency is therefore zero and
is not presented as a measured service result. Player wait still runs until the
assigned room fills or reaches its deadline.

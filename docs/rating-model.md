# Baseline rating model

The stage-2 baseline is a versioned online Bayesian approximation over the final
placement. It intentionally ignores score and gameplay features; those belong to
the separately evaluated extended model.

For every pair of players in a finalized room, the lower place is a win, the
higher place is a loss, and equal places are a tie. The expected pairwise result
uses a logistic function over the mean-skill difference. Its scale combines both
players' skill uncertainty and performance deviation.

For each player, the model accumulates the pairwise score gradient and observed
information, then combines them with the prior precision. This produces the new
mean and a non-increasing uncertainty. A configurable uncertainty floor prevents
the model from becoming unrealistically certain.

## Replay contract

- A model configuration is immutable after activation; parameter changes require
  a new model version.
- Every participant supplies a valid pre-game estimate for that same version.
- Only complete verified outcomes for rooms of five, six or seven players are
  accepted.
- Result availability and processing timestamps are explicit inputs.
- Updates contain the source event, model version, before/after estimates and
  processing time.
- Participant input order does not affect the generated updates.

## Pre-game prediction

The model converts the room's Gaussian estimates into positive player weights.
A shared scale combines the room's skill uncertainty and performance deviation,
so greater uncertainty moves predictions toward equal chances without treating
uncertainty as lower skill.

An exact Plackett-Luce dynamic program evaluates every reachable selection state
for the room. It returns one probability per place for every player, the
first-place probability and the expected place. For each player the place
probabilities sum to one, and for each place the probabilities across the room
also sum to one. Equal players therefore receive `1/N` for every place.

Every prediction records its room, mode, model version and generation time.
Input estimates updated after that time are rejected.

## Calibration harness

Calibration observations join a stored pre-game prediction to a later verified
result. The evaluator rejects predictions generated after the game finished and
prevents aggregation across model versions, modes, scoring rules or room sizes.
It reports multiclass Brier score, mean log loss and binned expected calibration
error for the full placement distribution.

## Extended feature contract

Extended-model inputs are extracted by an immutable feature schema bound to one
mode, scoring-rules version and deck version. A changed context or definition set
requires a new schema version, and incompatible results are rejected instead of
being mixed into one dataset.

The encoder emits raw observations in schema order and players in identifier
order. Every observation carries a `present` flag, so an unavailable value is
never imputed as an observed zero. Integer observations outside the exact
`float64` range are rejected rather than silently rounded.

Each selected feature belongs to an exclusive signal family. A schema cannot
select two members of one family, and `score` can never be combined with
`elapsed_ms` because scoring rules may already include time. The schema therefore
prevents obvious double counting without inventing EasyWin-specific weights.

Statistical scaling is fitted only from a time-ordered training partition as
described below. No imputation or feature weights are introduced at this stage.
The placement-only baseline remains the active rating model until an extended
version passes holdout and segment comparisons.

## Time-ordered datasets and holdout

Feature batches retain result finish and availability timestamps. Dataset
splitting uses availability: results available at or before the training cutoff
form the training partition, while strictly later results form holdout. Both
partitions are non-empty, deterministically ordered and restricted to one feature
schema, mode, scoring-rules version, deck version and feature layout. Duplicate
events and rooms are rejected.

Standardization statistics are fitted only through the split's private training
partition using present observations. Missing observations neither contribute to
the mean and population standard deviation nor become zero-valued samples; their
presence mask remains false after transformation. Constant training features
remain centered at zero with a unit transform scale.

Holdout calibration records both the dataset cutoff and the model's declared
training horizon. It rejects a model trained past the cutoff and any evaluated
result already available by that cutoff. A prediction cannot predate its model's
training horizon. The evaluator then applies the existing placement calibration
metrics. Real verified outcomes are still required for accuracy claims; these
contracts and deterministic tests establish leakage prevention, not predictive
gain.

## Model comparison and rollout

Candidate and baseline predictions are paired with the same verified result, so
the candidate cannot be evaluated on a more favorable holdout sample. Callers
assign stable segment identifiers for relevant combinations such as mode, entry
fee and region; each segment is additionally constrained to one mode,
scoring-rules version and room size.

The comparison reports candidate-minus-baseline Brier, log-loss and calibration
deltas for every segment. Overall Brier and log loss are weighted by players,
while overall calibration error is weighted by probability cells. An explicit
policy sets the minimum rooms per segment, required overall Brier improvement
and maximum allowed regression for every segment. There are no guessed default
thresholds. A valid but weak candidate produces an ineligible report rather than
a processing error.

Activation consumes a self-consistent eligible report whose baseline matches the
currently active version. Deployment state uses a monotonic revision fence,
records an append-only transition and retains the replaced version as a single
rollback target. Rollback consumes that target instead of allowing accidental
version toggling. An integration adapter must persist the state and transition
atomically before this contract is used in production.

## Runtime shadow evaluation

The PostgreSQL adapter runs an extended candidate without changing active
matchmaking or the rating read API. A deployment is an explicit immutable tuple
of baseline and candidate versions, mode, scoring-rules version, deck version,
training cutoff, training horizon, feature schema, training statistics and
weights. There are no runtime defaults for feature weights or statistics.

Room fill and verified-result finalization append separate work items to one
shadow timeline. Items are ordered by logical event time, with a room prediction
before a result at the same timestamp. The worker therefore persists both
baseline and candidate placement distributions from information available at
room fill, then scores that pair only when the verified result becomes
available. Candidate rating state and running feature profiles advance on the
result-availability timeline; wall-clock worker delay is audit metadata and is
never an input to later historical predictions.

All candidate state lives in `rating_shadow_*` tables. In particular, the worker
does not write `player_ratings`, `rating_updates`, matchmaking tickets, rooms or
the active model version. Missing deck metadata or an absent deployment produces
an auditable skipped work item rather than a guessed prediction. A candidate
failure blocks only the ordered shadow timeline and is retried under a lease; it
does not block baseline rating processing.

The `cmd/shadow-report` command reconstructs paired observations from immutable
predictions and verified outcomes, then calls the same segment-aware comparison
contract used by activation. Comparison thresholds remain explicit operator
input:

```bash
export RATING_SHADOW_COMPARISON_POLICY='{
  "minimum_rooms_per_segment": 100,
  "minimum_overall_brier_improvement": 0.01,
  "maximum_segment_brier_regression": 0.005,
  "maximum_segment_log_loss_regression": 0.005,
  "maximum_segment_calibration_regression": 0.005
}'
go run ./cmd/shadow-report -candidate-version rating-extended-v1
```

The numbers above demonstrate the required JSON shape only. Production gates
must be approved from pilot evidence. Generating an eligible report does not
activate the candidate.

## Current limits

- The approximation treats pairwise placement comparisons as independent.
- Default parameters are conservative starting values, not calibrated production
  constants.
- Predictive-accuracy claims require time-ordered real verified outcomes;
  fixed-seed property and simulation tests only establish invariants,
  reproducibility and expected learning behavior on synthetic data.
- Production comparison thresholds and feature weights remain unset until real
  EasyWin outcomes are available; the placement-only baseline stays active.
- Runtime shadow support creates evidence but does not itself satisfy the
  minimum real-outcome observation window.

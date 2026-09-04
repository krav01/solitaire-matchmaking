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

## Current limits

- The approximation treats pairwise placement comparisons as independent.
- Default parameters are conservative starting values, not calibrated production
  constants.
- Predictive-accuracy claims require time-ordered real verified outcomes;
  fixed-seed property and simulation tests only establish invariants,
  reproducibility and expected learning behavior on synthetic data.

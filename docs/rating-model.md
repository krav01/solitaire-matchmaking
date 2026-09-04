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

## Current limits

- The approximation treats pairwise placement comparisons as independent.
- Default parameters are conservative starting values, not calibrated production
  constants.
- Predictive probabilities and calibration evaluation are delivered separately
  before the model is considered complete.
- Predictive-accuracy claims require time-ordered real verified outcomes; unit
  tests only establish invariants and deterministic behavior.

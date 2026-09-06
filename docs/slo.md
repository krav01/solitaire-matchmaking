# Pilot service-level objectives

These objectives apply only to the first private pilot environment. They are
release gates and operating targets, not claims about production capacity or
the statistical quality of the rating model. Replace this profile with measured
environment-specific values before wider production promotion.

## Scope and measurement

- Environment: private pilot routing path, PostgreSQL 18 and the promoted
  immutable service image.
- Window: rolling 28 days, evaluated from Prometheus data retained for the full
  window.
- Traffic: authenticated game-backend requests to stable `/v1/*` routes;
  health, readiness, metrics and rejected caller input are excluded from HTTP
  availability.
- Segmentation: assignment and room-fill objectives remain split by mode,
  capacity, policy version and rating-model version.
- Evidence: use real pilot traffic. Synthetic canary, resilience and benchmark
  results prove behavior only and do not satisfy the observation window.

The supplied recording and alert rules in
[`prometheus-slo-pilot.yaml`](../deploy/observability/prometheus-slo-pilot.yaml)
materialize each 28-day SLI with `environment="pilot"` and its objective as
labels and keep the pilot thresholds out of the universal alert file.

## Objectives

| Area | 28-day objective | SLI | Minimum evidence |
| --- | --- | --- | --- |
| HTTP availability | 99.9% | non-5xx `/v1/*` requests divided by all `/v1/*` requests | 1,000 requests |
| HTTP latency | 99% within 500 ms | `/v1/*` observations in the `le="0.5"` histogram bucket divided by all `/v1/*` observations | 1,000 requests |
| Ticket assignment | 95% within 10 s | assignment observations in the `le="10"` bucket divided by all assignments, per segment | 200 assignments per segment |
| Room fill budget | 95% within 90% of the policy timeout | fill-timeout-ratio observations in the `le="0.9"` bucket divided by all filled rooms, per segment | 100 rooms per segment |
| Match completion | 99% | matched terminal decisions divided by matched plus timed-out terminal decisions | 200 terminal decisions |
| Worker reliability | 99.5% | succeeded items divided by succeeded plus failed items, per worker | 200 items per worker |
| Database acquisition | 99.99% | successful pool acquisitions divided by successful plus context-canceled acquisitions | 1,000 acquisitions |

An objective is `insufficient data`, not passing or failing, until its minimum
evidence is present. Retry-scheduled matchmaking attempts are not terminal and
are excluded from the completion denominator. Intended 4xx responses are caller
outcomes; alert them separately at the ingress if abuse or integration defects
need a budget.

## Non-budget guardrails

- Hard fairness violations must remain exactly zero. Any occurrence is critical
  and stops rollout regardless of the SLO window.
- PostgreSQL pool utilization must remain below 80% during sustained traffic.
  This preserves headroom; it is an operational guardrail rather than a
  request-based objective.
- The mean pool-acquisition duration is diagnostic only. The pool exposes
  cumulative acquisition and empty-pool wait time, not a query-latency
  histogram, so it must not be presented as a database query percentile.
- A missing scrape or an unreachable service remains covered by the `up` alert;
  request-based ratios alone cannot detect the absence of traffic.

## Alerts and release decisions

Load both the general alert rules and the pilot recording rules. Warning alerts
use short windows and minimum sample gates so a single low-volume event does not
pretend to be a statistically meaningful SLO breach. The HTTP, match-completion,
worker and database budget alerts fire at roughly five times their allowed error
ratio; latency and fill alerts use their objective boundary directly.

Do not increase canary traffic while a warning remains active. Roll back for a
critical availability or fairness alert, repeated warning windows, or an
exhausted 28-day error budget. Record the decision, dashboard interval, affected
segment and linked logs in the release record.

Review this profile after the first complete 28-day pilot window. Changes to an
objective require measured evidence, an owner, an effective date and an updated
alert threshold; never silently tune an alert merely to clear a rollout.

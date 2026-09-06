# Operational observability

The service exposes Prometheus/OpenMetrics data at authenticated `GET /metrics`.
Use the same `API_TOKEN` bearer credential as the other service-facing reads and
store it in the Prometheus runtime secret store, not in repository configuration.

```yaml
scrape_configs:
  - job_name: solitaire-matchmaking
    authorization:
      credentials_file: /run/secrets/solitaire-matchmaking-api-token
    static_configs:
      - targets: [solitaire-matchmaking:8080]
```

The endpoint includes Go/process collectors plus these bounded application
families:

| Metric family | Purpose |
| --- | --- |
| `solitaire_matchmaking_http_*` | Requests and latency by method, stable route and status |
| `solitaire_matchmaking_matchmaking_attempts_total` | Persisted matched, retry and timeout decisions |
| `solitaire_matchmaking_matchmaking_ticket_assignment_seconds` | Ticket acceptance-to-assignment latency |
| `solitaire_matchmaking_matchmaking_room_fill_seconds` | Completed room fill duration |
| `solitaire_matchmaking_matchmaking_room_fill_timeout_ratio` | Fill duration relative to the policy timeout |
| `solitaire_matchmaking_matchmaking_room_*gap*` | Raw and hard-limit-relative skill gap |
| `solitaire_matchmaking_matchmaking_room_win_probability_spread*` | Raw and hard-limit-relative predicted spread |
| `solitaire_matchmaking_matchmaking_fairness_violations_total` | Defensive signal for a successful assignment outside hard limits |
| `solitaire_matchmaking_worker_*` | Cycle health and claimed/succeeded/failed work by worker |
| `solitaire_matchmaking_database_pool_*` | Process-local PostgreSQL pool capacity, acquisition count, cancellations and wait time |

Room metrics are segmented by mode, capacity, policy version and rating-model
version. Player, ticket, room, event and request identities are deliberately not
labels, which keeps cardinality bounded. HTTP observations use registered route
patterns rather than raw paths for the same reason.

Import [the Grafana dashboard](../deploy/observability/grafana-dashboard.json)
and load the universal
[Prometheus alert rules](../deploy/observability/prometheus-alerts.yaml). Load the
[pilot SLO recording and alert rules](../deploy/observability/prometheus-slo-pilot.yaml)
only in the private pilot environment. The exact pilot objectives, minimum
sample sizes and release decisions are defined in the [SLO profile](slo.md).
Production requires a separate profile based on its own representative evidence.

The fill warning measures p95 as a fraction of each policy's configured timeout.
Fairness-limit violations are critical because successful room formation is not
allowed to cross either hard boundary. Database pool metrics are process-local;
aggregate acquisition counters across replicas, but inspect saturation by
instance so one exhausted pool is not hidden by healthy replicas.

Histogram quantiles require enough observations in the selected window. Always
read fill speed and both fairness ratios together and preserve mode, room-size,
policy and model segmentation when investigating a regression.

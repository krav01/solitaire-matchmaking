# Release security review

## Scope and evidence

This review covers the Stage 6 release candidate: inbound HTTP, configuration,
PostgreSQL persistence and migrations, background workers, outbound event
delivery, observability, container packaging, and dependency/CI controls.

Evidence is provided by unit and race tests, PostgreSQL lifecycle and resilience
scenarios, architecture and lint checks, `govulncheck`, GitHub Dependency Review,
the OpenAPI contract, the non-root container build check, and the operational
documents linked from the release checklist.

## Review results

| Area | Result | Control or required deployment condition |
| --- | --- | --- |
| Authentication secrets | Pass | Inbound and outbound bearer tokens are validated, never logged, and must now be distinct |
| HTTP boundary | Pass with deployment condition | Bodies and headers are bounded, timeouts are configured, comparisons are constant-time, and errors are generic; TLS and request-rate controls belong at the private ingress |
| Outbound delivery | Pass | Remote clear-text HTTP and redirects are rejected; requests are authenticated, timeout-bounded, lease-fenced, retried, and idempotency-keyed |
| Persistence | Pass with deployment condition | SQL is parameterized and critical transitions are transactional; CI and releases rehearse synthetic backup/restore plus migrations, while production must use TLS, separate migration/runtime roles, and target-environment recovery testing |
| Concurrency and recovery | Pass | Work is bounded and cancellation-owned; duplicate claims, stale leases, process recovery, publisher failure, and ordered delivery are tested |
| Matchmaking integrity | Pass | Selection uses immutable pre-game snapshots and cannot relax hard fairness, fee, room-size, or version boundaries |
| Sensitive telemetry | Pass | Logs avoid credentials and metrics use bounded labels without player, ticket, room, event, or request identities |
| Container | Pass | The runtime is scratch-based, read-only compatible, and runs as `65532:65532`; CI builds and verifies that user |
| Supply chain | Pass with release action | Go vulnerabilities, dependency changes, actions, and standards drift are checked; stable tags run the full release gate, isolate third-party scanners from publish permissions, verify checksummed release inputs, and produce signed attestations before operators promote the recorded digest |

No unresolved high-severity repository finding remains in the reviewed scope.

## Accepted residual risks

### Shared service credential

The first release uses one inbound bearer token rather than per-caller identity,
scopes, or overlapping rotation keys. Keep the API private, allow only the game
backend and metrics collector at the network layer, store the token externally,
and rotate it through a coordinated deployment. Add workload identity or mTLS
before exposing the API to more callers or networks.

### Edge rate limiting

The process bounds request bodies, headers, timeouts, worker batches, and
concurrency but does not implement caller quotas. Enforce connection and request
rate limits at ingress. Revisit in-process quotas if the service becomes directly
reachable by untrusted or independently managed callers.

### Synthetic capacity evidence

The resilience suite proves correctness under bounded contention; it is not a
capacity result. Define production SLOs and size the database only after sustained
tests in a representative environment, as required by
`docs/performance-budget.md`.

## Re-review triggers

Repeat this review for a new trust boundary, authentication scheme, public
exposure, dependency, schema migration, event destination, sensitive-data field,
or material change to concurrency and retry behavior. Treat credential exposure,
fairness manipulation, duplicate settlement, or loss of an authoritative result
as incident-response events under `docs/security.md`.

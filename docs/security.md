# Security model

This document defines repository-level security expectations for the matchmaking service.

## Trust boundaries

Untrusted inputs enter through:

- HTTP request payloads, headers, query parameters, and path parameters;
- environment configuration;
- PostgreSQL data read from mutable tables;
- outbound event delivery, future inbound consumers, and external integrations.

Validate data at the boundary before it reaches domain logic. Reject malformed, oversized, unsupported, or inconsistent inputs early.

## Secrets and sensitive data

- Never commit credentials, tokens, private keys, production secrets, or real customer data.
- `.env.example` must contain placeholders only.
- Secrets must come from the deployment environment or an external secret manager.
- Do not write authorization credentials, connection strings containing passwords, or raw sensitive payloads to logs.
- Treat player identifiers, rating snapshots, tournament participation, and operational telemetry as sensitive service data.

## Persistence

- SQL must be parameterized; never concatenate untrusted values into SQL statements.
- State transitions that must be observed atomically belong in one database transaction.
- Outgoing integration events that must survive a successful state transition use the transactional outbox pattern.
- Leases, fencing tokens, unique constraints, and idempotency keys protect against duplicate concurrent processing.
- Database accounts should receive the minimum privileges needed by the process.

## HTTP and worker safety

- Every request and background operation must be cancellable through `context.Context`.
- Network and database operations must have bounded timeouts.
- Request/body sizes and batch sizes should be bounded before production exposure.
- Background workers must bound concurrency and retry behavior.
- Goroutines must have explicit ownership and shutdown behavior.

## Matchmaking integrity

Security includes protection against unfair or manipulable matchmaking behavior:

- matchmaking must use only information available before the participant's current game;
- current-game submitted scores must never influence opponent selection for that room;
- hard fairness constraints cannot be relaxed by fill-speed optimization;
- missing skill features are not interpreted as zero-valued observations;
- rating uncertainty is modeled separately from performance variability.

## Supply chain

Pull requests and release changes are checked with:

- `golangci-lint`, including `gosec` and static analysis;
- `govulncheck` for reachable Go vulnerabilities;
- GitHub dependency review for newly introduced vulnerable dependencies;
- Dependabot for Go modules and GitHub Actions updates.

New runtime dependencies require a clear reason. Prefer the standard library where it provides an adequate solution.

## Incident checklist

For a suspected security issue:

1. preserve logs and relevant identifiers without copying secrets;
2. identify the affected boundary, data, and version range;
3. stop or isolate unsafe processing if continued execution can increase impact;
4. patch with a regression test where practical;
5. rotate exposed credentials if disclosure is possible;
6. document the root cause and add a guardrail that prevents recurrence.

# Reusable Go patterns

This is a design-pattern reference, not a copy-paste library. Adapt each pattern to the project's invariants and dependencies.

## Worker lifecycle

Use when background work owns goroutines.

Required properties:
- explicit owner starts/stops the worker;
- `context.Context` cancellation reaches blocking operations;
- bounded concurrency;
- deterministic shutdown;
- no detached goroutines with unbounded lifetime;
- retry loops have backoff and a stop condition.

Avoid:
- `go func()` with hidden ownership;
- infinite retry without cancellation;
- sleep-based coordination when synchronization is available.

## Retry with backoff

Use only for transient, idempotent/retry-safe operations.

Required properties:
- classify retryable vs permanent errors;
- cap attempts or total elapsed time;
- exponential/controlled backoff with jitter where synchronized retries are possible;
- respect caller context/deadline;
- preserve idempotency across attempts;
- expose attempt/failure metrics for important flows.

Avoid retrying validation, authorization, or deterministic business-rule failures.

## Transaction boundary

Use when multiple persistence changes must preserve one business invariant.

Required properties:
- keep the transaction as short as practical;
- perform external network calls outside the DB transaction;
- lock/update in deterministic order where contention matters;
- use optimistic versioning, uniqueness, or row locks according to the invariant;
- rollback on every error path;
- test concurrent and partial-failure behavior.

## Transactional outbox

Use when a database state change and an external event must not diverge.

Required properties:
- persist aggregate/state transition and outbox record in one transaction;
- stable event ID/idempotency key;
- claim/lease/fencing for concurrent dispatchers;
- retry policy and terminal-failure handling;
- consumer idempotency assumption documented;
- delivery metrics and backlog visibility.

Do not promise exactly-once delivery unless the entire end-to-end system actually provides it.

## Graceful shutdown

Required order depends on the service, but commonly:
1. stop accepting new external work;
2. cancel workers/listeners;
3. allow bounded in-flight completion;
4. close DB/network resources;
5. force termination after a deadline.

Every shutdown wait must be bounded.

## HTTP handler boundary

Keep transport logic separate from business logic.

Handler responsibilities:
- parse and bound request input;
- authenticate/authorize at the appropriate boundary;
- call one application/use-case boundary;
- map domain/application errors to transport responses;
- avoid embedding SQL/business workflows in handlers;
- propagate request context.

## Configuration validation

Load configuration once at process startup and validate before serving traffic.

Required properties:
- explicit types and defaults;
- fail fast for invalid required values;
- never log secret values;
- separate parse errors from semantic validation errors;
- avoid mutable global configuration.

## Consumer-owned interfaces

Declare small interfaces near the code that consumes them rather than defining broad provider interfaces preemptively.

Benefits:
- lower coupling;
- easier tests;
- fewer accidental dependencies;
- implementations satisfy interfaces implicitly.

## Error handling

- add operation context when returning errors across boundaries;
- preserve `errors.Is` / `errors.As` semantics with `%w`;
- use sentinel/typed errors only when callers need programmatic classification;
- do not log and return the same error at every layer;
- classify retryability close to the boundary that understands the failure.

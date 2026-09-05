# Game-backend integration contract

The implemented inbound API and outbound webhook envelope are defined in
[OpenAPI](../api/openapi.yaml). A runnable Go receiver is available in
[examples/game-backend](../examples/game-backend/README.md).

## Implemented inbound API

| Endpoint | Authentication | Purpose |
| --- | --- | --- |
| GET /healthz | None | Process liveness |
| GET /readyz | None | PostgreSQL readiness and draining state |
| GET /metrics | API_TOKEN bearer | Prometheus/OpenMetrics operational telemetry |
| GET /v1/capabilities | API_TOKEN bearer | Current service capabilities |
| POST /v1/tickets | API_TOKEN bearer | Accept an eligible reserved entry and rating snapshot |
| GET /v1/tickets/{ticket_id} | API_TOKEN bearer | Read queue or assignment state |
| DELETE /v1/tickets/{ticket_id} | API_TOKEN bearer | Cancel an eligible queued entry |
| GET /v1/rooms/{room_id} | API_TOKEN bearer | Read composition and lifecycle state |
| GET /v1/ratings/{player_id}?mode_id=... | API_TOKEN bearer | Read the current persisted rating |
| POST /v1/results | API_TOKEN bearer | Complete, server-verified room result |

Ticket acceptance uses `entry_id` as its idempotency identity. The game backend
must persist the request, including `requested_at` and the pre-game rating
snapshot, before sending it. The initial request returns 201; an identical retry
returns 200 with the original `ticket_id` and `replay: true`. A different request
with the same entry identity returns `idempotency_conflict`.

The rating snapshot must have been available by `snapshot_at`, and `snapshot_at`
must not follow `requested_at`. Matching reads this immutable snapshot and never
uses a result from the room currently being formed. A queued ticket response has
no `assignment`. Once assigned, GET returns `room_id`, `session_id`, seat and
aggregate versions. Polling reads indexed identities and does not block the
matchmaking worker.

Cancellation requires a stable `Idempotency-Key` header. Reuse it after a timeout
or 5xx response. A queued ticket transitions to cancelled; an identical command
returns the stored decision. Assigned and expired tickets return
`ticket_not_queued` and remain unchanged.

| HTTP status | Ticket error code | Caller action |
| --- | --- | --- |
| 400 | invalid_ticket | Correct malformed, oversized, unknown-field or invalid data |
| 400 | invalid_cancellation | Supply a stable cancellation Idempotency-Key |
| 404 | tournament_not_found | Reconcile tournament identity and version |
| 404 | ticket_not_found | Reconcile the ticket identity |
| 409 | idempotency_conflict | Reconcile reused entry or cancellation identity |
| 409 | ticket_not_queued | Stop cancellation and read current state |
| 500 | internal_error | Retry persisted input with bounded backoff |

Room reads return immutable configuration identities, current lifecycle status
and timestamps, aggregate version, and members ordered by seat. Each member
contains the ticket, player and session identities plus the current session
status and its available lifecycle timestamps. Forming rooms return an empty
`members` array rather than null.

Rating reads require exactly one non-empty `mode_id`. They return the most
recently updated persisted estimate for the player and mode, including its
`model_version` and storage `revision`. A player without a persisted rating for
that mode returns `rating_not_found`; callers may continue using their approved
initial estimate until a verified result has produced the first persisted update.

Results use the body field `event_id` as their idempotency identity, not an
Idempotency-Key header. Persist the request before sending it and reuse that
identity and logical data after a timeout or 5xx response. The initial acceptance
returns 201; an identical replay returns 200 with `replay: true`. Participant
order and equivalent timestamp offsets do not change retry equality.
The replay's `rating_pending` reflects current processing state and may change.

A result must reference the allocated room, mode, deck, scoring-rules version
and every player/session pair. Send five to seven unique participants, with
places within the room size and at least one first place. Ties are allowed.
The finish time must not precede session allocation; availability must follow
or equal finish time and must not be in the future at service acceptance.
Initial acceptance must meet the room deadline. An already committed identical
result remains replayable after the deadline.

Missing features remain missing; zero and false are observations. In this
service, `completed: false` forfeits the session, while absent or true marks it
submitted. Only authoritative game-backend observations may feed rating.

| HTTP status | Error code | Caller action |
| --- | --- | --- |
| 400 | invalid_result | Correct malformed, oversized, unknown-field or invalid data |
| 401 | unauthorized | Correct bearer credentials |
| 404 | room_not_found | Reconcile room identity |
| 409 | result_conflict | Reconcile reused identity or mismatched mode/deck/rules |
| 409 | room_not_collecting | Reconcile terminal or incomplete room state |
| 409 | result_deadline_passed | Reconcile late initial result |
| 409 | participants_mismatch | Reconcile allocated sessions and participants |
| 500 | internal_error | Retry the persisted request with bounded backoff |

Log the response X-Request-ID for diagnosis. Do not generate a new result identity
to work around a conflict. Unknown routes and unsupported methods use standard
HTTP mux responses, which are not the JSON application-error schema.

## Outgoing event delivery

The service sends POST requests to OUTBOX_DELIVERY_URL with a distinct
OUTBOX_DELIVERY_TOKEN bearer credential, Content-Type application/json,
Idempotency-Key equal to event_id and User-Agent solitaire-matchmaking-outbox/1.
HTTPS is required except on loopback development endpoints. Redirects are not
followed. Network errors and all non-2xx responses are retried with capped
exponential delay; the attempt count is not capped.

The envelope contains event_id, aggregate_type, aggregate_id,
aggregate_version, event_type, payload and occurred_at. Persist event identity
and the business side effect atomically before acknowledging with 2xx. An
identical replay must succeed without reapplying the effect; conflicting data
for an existing identity must be investigated.

| Event | Aggregate | Payload fields |
| --- | --- | --- |
| ticket.accepted | ticket | ticket_id, entry_id, player_id, tournament_id, tournament_version, status, requested_at |
| ticket.cancelled | ticket | ticket_id, status, cancelled_at |
| ticket.assigned | ticket | ticket_id, room_id, session_id, player_id, seat, assigned_at |
| ticket.expired | ticket | ticket_id, status, deadline, expired_at |
| room.filled | room | room_id, filled_at, result_deadline |
| room.completed | room | result_event_id, room_id, completed_at, rating_pending, standings |
| room.expired | room | RoomID, RoomVersion, ResultDeadline, ExpiredAt |
| result.rated | result | result_event_id, mode_id, model_version, processed_at, updates |

The existing room.expired payload uses capitalized keys; consumers must preserve
this wire compatibility. Standings contain player_id, place and features.
Rating updates contain player_id, source_event_id, model_version, before, after
and processed_at. Each estimate contains mean, uncertainty, optional
performance_deviation, games, model_version and updated_at.

Delivery is ordered within an aggregate, not globally across tickets, rooms and
results. Aggregate versions may have gaps. Lost acknowledgements or expired
leases can cause duplicate requests, including stale retries; receivers must
deduplicate and prevent stale events from overwriting newer state. Cross-aggregate
dependencies require reconciliation by identity. Settlement remains owned by
the game backend.

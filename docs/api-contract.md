# Planned game-backend contract

This document defines the remaining integration direction. Implemented endpoints,
including verified-result ingestion, are defined in `api/openapi.yaml`.

## Commands

| Endpoint | Purpose | Idempotency identity |
| --- | --- | --- |
| `POST /v1/tickets` | Accept an eligible, reserved tournament entry and rating snapshot | entry id |
| `DELETE /v1/tickets/{ticket_id}` | Cancel a queued entry subject to tournament rules | ticket id + command id |
| `POST /v1/results` | Accept a server-verified complete room result (implemented) | result event id |

## Queries

| Endpoint | Purpose |
| --- | --- |
| `GET /v1/tickets/{ticket_id}` | Read queue or assignment state |
| `GET /v1/rooms/{room_id}` | Read composition and lifecycle state |
| `GET /v1/ratings/{player_id}?mode_id=...` | Read current versioned rating estimate |

Every command carries a stable external identity and returns the stored result on
an identical retry. A reused identity with a different body is a conflict. Result
requests must reference a known session, room, deck and scoring-rules version.

The authoritative game backend sends verified results. Client telemetry may be
retained separately for investigation but never becomes an authoritative rating
input merely because the client submitted it.

Outgoing assignment, room completion and settlement-request events use a
transactional outbox. Delivery is at least once, so consumers must deduplicate by
event id. The implemented baseline permits tied places, maps `completed: false`
to a forfeited session, rejects incomplete rooms, and rejects results accepted
after the room deadline. Settlement responses remain outside this service.

## Outgoing event delivery

The service sends every committed outbox record as `POST
$OUTBOX_DELIVERY_URL`. Requests use `Authorization: Bearer
<OUTBOX_DELIVERY_TOKEN>`, `Content-Type: application/json` and
`Idempotency-Key: <event_id>`. The endpoint must return any `2xx` status only
after it has durably accepted the event identity. Redirects are not followed.

```json
{
  "event_id": "event-identity",
  "aggregate_type": "ticket",
  "aggregate_id": "ticket-identity",
  "aggregate_version": 2,
  "event_type": "ticket.assigned",
  "payload": {},
  "occurred_at": "2026-09-05T06:00:00Z"
}
```

Consumers must treat an identical `event_id` as a replay, persist the
deduplication decision atomically with their side effect and tolerate retries
after timeouts or lost acknowledgements. Events are ordered within an aggregate,
not globally across tickets, rooms and results.

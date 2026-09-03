# Planned game-backend contract

This document defines integration direction. Only endpoints in `api/openapi.yaml`
exist in the foundation. Business endpoints become part of the OpenAPI document
when their use cases are implemented and tested.

## Commands

| Endpoint | Purpose | Idempotency identity |
| --- | --- | --- |
| `POST /v1/tickets` | Accept an eligible, reserved tournament entry and rating snapshot | entry id |
| `DELETE /v1/tickets/{ticket_id}` | Cancel a queued entry subject to tournament rules | ticket id + command id |
| `POST /v1/results` | Accept a server-verified session result | result event id |

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
event id. The settlement response contract and rules for ties, forfeits, incomplete
rooms and late results must be approved before the result workflow is implemented.

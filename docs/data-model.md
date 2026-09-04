# Logical data model

This logical contract is implemented by the initial schema in
`migrations/000001_initial_schema.up.sql`. Volume and retention assumptions still
require validation against production traffic before release.

| Record | Identity and immutable references | Mutable lifecycle data |
| --- | --- | --- |
| Tournament configuration | tournament id/version, mode, room size, fee/currency, rule versions | activation window |
| Matching policy | policy version, rating-model version, hard limits | none after activation |
| Rating model | model version, feature schema, parameters digest | none after activation |
| Ticket | entry id, player id, tournament version, pre-game rating snapshot | state, room assignment, timestamps |
| Room | tournament/policy/model versions, deck id, capacity | state, fill/result deadlines, completion timestamps |
| Session | room, ticket, player, seat | state, start/submission timestamps |
| Verified result | unique event id, room/player/session, rules version | normalized optional features |
| Rating update | player, source event, model version, before/after estimates | processing timestamp |
| Outbox event | aggregate/version, event type, serialized payload | attempts, delivery time |

Required integrity rules for stage 4:

- an `entry_id` creates at most one effective ticket;
- cancellation commands and room assignments have stable retry identities;
- a ticket has at most one active room assignment;
- seats are unique and bounded by the room capacity;
- a verified result is applied once by its event id;
- a verified result contains every room participant and at least one winner;
- a player receives one rating update per source event and model version;
- a completed room cannot change policy, model, deck or rules versions;
- state change and its outbox event commit together;
- matching workers claim bounded batches and do not hold locks during scoring.

Primary planned reads are: queued tickets by tournament and age, forming rooms by
tournament and available seats, sessions by room, unprocessed verified results,
player rating by mode/model, and pending outbox events. Index design follows those
queries. Partial indexes keep worker scans restricted to queued, forming,
unprocessed or undelivered rows. Worker transactions will claim bounded batches
with `FOR UPDATE SKIP LOCKED`; scoring remains outside row-locking transactions.

Ticket acceptance is idempotent by `entry_id`; cancellation is idempotent by
ticket plus command id; assignment is idempotent by assignment id. Assignment
locks only the selected ticket and room, rechecks the immutable tournament and
rating-model partition plus the room version scored by the matcher, chooses a bounded seat, and commits membership, session,
state versions and outbox records together. A concurrent attempt for the final
seat observes the filled room and can return to matching.

The physical result record represents one complete, game-backend-verified room
outcome with participant rows. Optional feature columns remain nullable so absent
telemetry cannot become a zero observation. Rating snapshots are copied onto
tickets and never replaced by results from the room being matched.

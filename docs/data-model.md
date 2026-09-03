# Logical data model

This is a logical contract, not migration SQL. Physical types and indexes require
review against expected volume, retention and access patterns before stage 4.

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
- a ticket has at most one active room assignment;
- seats are unique and bounded by the room capacity;
- a verified result is applied once by its event id;
- a player receives one rating update per source event and model version;
- a completed room cannot change policy, model, deck or rules versions;
- state change and its outbox event commit together;
- matching workers claim bounded batches and do not hold locks during scoring.

Primary planned reads are: queued tickets by tournament and age, forming rooms by
tournament and available seats, sessions by room, unprocessed verified results,
player rating by mode/model, and pending outbox events. Index design follows those
queries after representative volume assumptions are available.

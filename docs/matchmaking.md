# Match formation

Stage 3 separates hard eligibility from candidate ordering. The current evaluator
is side-effect free: it reads an immutable room view and candidate rating
snapshots, then returns decisions in input order.

## Eligibility order

1. Validate room, policy and candidate contracts.
2. Reject expired or already full rooms.
3. Reject duplicate tickets and players.
4. Require the policy's rating-model version.
5. Enforce the maximum skill gap over every proposed room member.
6. Enforce the room's active skill window. It expands in bounded, deterministic
   steps from the initial gap to the hard maximum as the room waits.
7. When a candidate completes the room, predict every member's first-place
   chance and enforce the maximum probability spread over the entire room.

Snapshot and evaluation timestamps prevent current-game outcomes from entering
opponent selection. Filtering does not mutate the room or rank accepted
candidates. Expansion can admit a wider skill range over time but cannot relax
the maximum skill gap or probability-spread limit.

## Waiting-age priority

Active rooms reaching `AgePriorityAfter` move ahead of younger rooms. Prioritized
rooms are ordered oldest first, while the relative order of young rooms is kept
for the later fill-speed ranking. This prevents starvation without allowing an
old room to bypass candidate eligibility or hard fairness limits.

## Room selection

`SelectRoom` evaluates one candidate against a bounded set of rooms from the same
mode, capacity and immutable policy. Ineligible rooms are removed before ranking.
Rooms beyond `AgePriorityAfter` come first and are ordered oldest-first. Among
young rooms, `PreferNearlyFull` selects the room with the most existing members,
then the smaller whole-room skill gap. Stable input order is retained when the
preference is disabled.

The selector returns a decision only; persistence must still claim the ticket
and room atomically. `RoomLimit` bounds evaluation work independently from the
per-room `CandidateLimit`.

## Integration boundary

`Evaluator` depends on the small `PlacementPredictor` interface. The baseline
rating model already implements it directly. Application and persistence layers
remain responsible for tournament compatibility, entry fee, reservations and
atomic room assignment.

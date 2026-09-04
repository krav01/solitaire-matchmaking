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
6. When a candidate completes the room, predict every member's first-place
   chance and enforce the maximum probability spread over the entire room.

Snapshot and evaluation timestamps prevent current-game outcomes from entering
opponent selection. Filtering does not mutate the room or rank accepted
candidates. Window expansion, waiting age and fill-speed priority are later
selection steps and cannot relax either hard fairness maximum.

## Integration boundary

`Evaluator` depends on the small `PlacementPredictor` interface. The baseline
rating model already implements it directly. Application and persistence layers
remain responsible for tournament compatibility, entry fee, reservations and
atomic room assignment.

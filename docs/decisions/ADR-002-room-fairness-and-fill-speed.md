# ADR-002: Room fairness and fill speed

Status: Accepted

## Context

The service must form rooms quickly while keeping opponents comparable enough that outcomes are competitive. Fill latency and match quality are competing objectives, especially when queue density is low.

## Decision

Hard fairness constraints apply to the entire room and are never relaxed by fill-speed optimization. Entry fee and room size are invariant matchmaking dimensions.

Within the feasible set, the matcher may optimize fill speed using deterministic candidate ordering and bounded search. Search tolerance may expand with wait time only inside the configured hard fairness ceiling.

Matchmaking uses pre-game rating snapshots only. An open room's submitted/current-game scores are never inputs to opponent selection.

## Alternatives

- Relax fairness without a hard ceiling to fill rooms faster: rejected because it can create systematically unfair rooms.
- Use fixed narrow windows: rejected because it fragments sparse queues and increases abandonment.
- Use current-game scores while a room is open: rejected because it creates outcome leakage and manipulable selection.

## Consequences

- Queue wait can rise when no fair room is possible.
- Operational tuning must report fill latency together with fairness metrics.
- Search-window changes must preserve the hard ceiling and deterministic behavior.

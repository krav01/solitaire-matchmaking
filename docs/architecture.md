# Architecture decisions

## ADR-001: One deployable with portable algorithms

**Status:** accepted for the foundation.

Use one Go module and one service process. Keep rating and matching contracts in
public packages without transport or persistence dependencies. This gives the
first release a small operational surface while preserving two integration paths:
HTTP from any backend, or direct package use from a Go backend.

## ADR-002: Asynchronous rooms and sessions

**Status:** accepted.

A room tracks membership, fill and result collection. A session tracks one
participant's play attempt. A participant may finish while the room is still
forming. Opponent selection uses only immutable information captured before that
participant received the deck. Submitted scores from an open room are excluded.

Every participant in a completed room references the same `deck_id`, tournament
version, scoring-rules version, settlement version, matching-policy version and
rating-model version.

## ADR-003: Fairness and fill speed are separate objectives

**Status:** accepted.

Candidate eligibility applies hard constraints first: tournament configuration,
room size, fee, version compatibility, eligibility and maximum fairness limits.
The selector then optimizes within eligible choices using predicted win chances,
room completion benefit and waiting age.

The policy supports an initial skill window, bounded expansion, a maximum skill
gap, a maximum spread between predicted win probabilities, fill timeout, age
priority, and separate candidate and room scan limits. Thresholds remain unset
until simulation and available production data support them.

Prefer completing a nearly full room when choices remain within the same hard
fairness boundary. Age priority prevents an older room from waiting forever.
Expansion never changes room size, entry fee or the hard fairness limit.

## ADR-004: Versioned, evidence-based rating

**Status:** accepted as a stage-2/5 constraint.

Store expected skill, uncertainty about skill and estimated performance
variation separately. The baseline learns from final placement and opponent
strength. The extended model may use verified score, completion, elapsed time,
moves, undo moves and revealed cards.

Features are optional. Missing data is not a zero observation. Correlated inputs
must not count the same scoring signal twice. Offline evaluation builds a model
using information available at the cutoff time and evaluates later outcomes.

## ADR-005: PostgreSQL as the initial source of truth

**Status:** accepted.

PostgreSQL will store tickets, room membership, sessions, verified outcomes,
rating history and outgoing events. Atomic state transitions will use explicit,
parameterized SQL transactions. Application startup checks connectivity and
never applies schema changes. The separate migration runner embeds reviewed,
checksummed SQL and serializes migration batches with a PostgreSQL advisory lock.

## ADR-006: Explicit trust boundaries

**Status:** accepted.

The game backend authenticates to this service. It owns identity, eligibility,
deck generation, result verification, reservations and settlement. This service
does not accept authoritative scores directly from game clients. Health is public;
business endpoints require service authentication. Detailed infrastructure errors
are logged internally and are not returned in API responses.

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

PostgreSQL stores tickets, room membership, sessions, verified outcomes,
rating history and outgoing events. Atomic state transitions use explicit,
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

## ADR-007: At-least-once transactional event delivery

**Status:** accepted.

Committed outbox events are delivered to one configured game-backend HTTPS
endpoint. Independent aggregates may be published concurrently, while an
undelivered lower aggregate version blocks later versions of the same aggregate.
Delivery uses short database leases, unique claim tokens and database-time
fencing; network calls never hold row locks.

Successful `2xx` responses acknowledge an event. Other statuses and transport
failures schedule a bounded exponential retry. Because a request can succeed
before its acknowledgement is committed, the contract is at least once and the
receiver must deduplicate `Idempotency-Key: <event_id>`. Redirects are disabled,
remote clear-text HTTP is rejected, and delivery uses a credential distinct from
the inbound API token.

## ADR-008: Bounded operational metrics

**Status:** accepted.

Expose authenticated Prometheus/OpenMetrics data from the service process.
Measure HTTP behavior by stable route, background work by fixed worker name, and
room outcomes by mode, capacity, policy version and rating-model version. Never
use player, ticket, room, event or request identities as metric labels.

Record assignment and fill duration beside skill-gap and predicted-probability
spread. Also expose each fairness measure relative to its immutable hard policy
limit so dashboards and alerts remain meaningful across policy versions. Metrics
observe only successfully persisted decisions; stale claims and failed mutations
cannot appear as completed rooms.

## ADR-009: Serialize shadow evidence on logical event time

**Status:** accepted.

Store room-fill and result-availability work in one globally ordered PostgreSQL
timeline, with room work ordered first on equal timestamps. Persist candidate
predictions, state, updates and observations in dedicated tables and keep model
activation outside the worker.

Generating a candidate prediction after seeing a result creates direct leakage,
while independently scheduled room and result workers can reorder the
information available to an online candidate. One logical timeline makes the
replay boundary explicit and testable. Dedicated tables make experimentation
non-authoritative and allow pause, retry and reporting without changing
player-facing behavior.

The shadow worker is intentionally serial and one poisoned head blocks later
shadow evidence until retry or operator intervention. This does not block
matchmaking, result ingestion or baseline rating. Higher throughput would
require proven-safe partitioning by independent player sets, which is deferred
until measured volume justifies the added ordering complexity.

package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/krav01/solitaire-matchmaking/internal/tournament"
	"github.com/krav01/solitaire-matchmaking/internal/worker"
	"github.com/krav01/solitaire-matchmaking/pkg/matchmaking"
)

// MatchmakingQueue persists leased queue work and builds bounded immutable
// matcher inputs from pre-game snapshots.
type MatchmakingQueue struct {
	pool *pgxpool.Pool
}

func NewMatchmakingQueue(pool *pgxpool.Pool) (*MatchmakingQueue, error) {
	if pool == nil {
		return nil, errors.New("PostgreSQL pool is required")
	}
	return &MatchmakingQueue{pool: pool}, nil
}

func (queue *MatchmakingQueue) ClaimMatchmakingTickets(ctx context.Context, request worker.ClaimRequest) ([]worker.TicketClaim, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	rows, err := queue.pool.Query(ctx, `
WITH due AS (
    SELECT ticket_id
    FROM matchmaking_tickets
    WHERE status = 'queued'
      AND next_attempt_at <= $1
      AND (claimed_until IS NULL OR claimed_until <= $1)
    ORDER BY next_attempt_at, requested_at, ticket_id
    FOR UPDATE SKIP LOCKED
    LIMIT $2
)
UPDATE matchmaking_tickets AS ticket
SET claim_token = $3,
    claimed_until = $4,
    attempt_count = ticket.attempt_count + 1
FROM due
WHERE ticket.ticket_id = due.ticket_id
RETURNING ticket.ticket_id, ticket.entry_id, ticket.player_id,
          ticket.tournament_id, ticket.tournament_version, ticket.status,
          ticket.requested_at, ticket.assigned_at, ticket.cancelled_at, ticket.expired_at,
          ticket.snapshot_at, ticket.rating_mean, ticket.rating_uncertainty,
          ticket.rating_performance_deviation, ticket.rating_games,
          ticket.rating_model_version, ticket.rating_updated_at,
          ticket.aggregate_version, ticket.request_digest,
          ticket.claim_token, ticket.claimed_until, ticket.attempt_count`,
		request.ClaimedAt, request.Limit, request.Token, request.LeaseUntil,
	)
	if err != nil {
		return nil, fmt.Errorf("claim queued tickets: %w", err)
	}
	defer rows.Close()

	claims := make([]worker.TicketClaim, 0, request.Limit)
	for rows.Next() {
		stored, err := scanStoredTicket(rows)
		if err != nil {
			return nil, fmt.Errorf("scan claimed ticket: %w", err)
		}
		claim := worker.TicketClaim{
			Ticket: stored.ticket, Token: request.Token,
			Attempt: stored.attemptCount, LeaseUntil: request.LeaseUntil,
		}
		if err := claim.Validate(); err != nil {
			return nil, fmt.Errorf("validate claimed ticket: %w", err)
		}
		claims = append(claims, claim)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read claimed tickets: %w", err)
	}
	return claims, nil
}

func (queue *MatchmakingQueue) ScheduleTicketRetry(ctx context.Context, ticketID, claimToken string, retryAt time.Time) error {
	if ticketID == "" || claimToken == "" || retryAt.IsZero() {
		return errors.New("ticket, claim and retry time are required")
	}
	result, err := queue.pool.Exec(ctx, `
UPDATE matchmaking_tickets
SET next_attempt_at = $3, claim_token = NULL, claimed_until = NULL
WHERE ticket_id = $1
  AND status = 'queued'
  AND claim_token = $2
  AND claimed_until > clock_timestamp()`, ticketID, claimToken, retryAt)
	if err != nil {
		return fmt.Errorf("schedule ticket retry: %w", err)
	}
	if result.RowsAffected() != 1 {
		return tournament.ErrTicketClaimLost
	}
	return nil
}

func (queue *MatchmakingQueue) LoadMatchAttempt(ctx context.Context, claim worker.TicketClaim, evaluatedAt time.Time) (matchmaking.MatchAttempt, error) {
	if err := claim.Validate(); err != nil {
		return matchmaking.MatchAttempt{}, err
	}
	if evaluatedAt.IsZero() {
		return matchmaking.MatchAttempt{}, errors.New("match evaluation time is required")
	}

	var modeID, policyVersion, ratingModelVersion string
	var capacity int
	var encodedPolicy []byte
	err := queue.pool.QueryRow(ctx, `
SELECT config.mode_id, config.capacity, policy.policy_version,
       policy.rating_model_version, policy.definition
FROM tournament_configs AS config
JOIN matching_policies AS policy
  ON policy.policy_version = config.policy_version
 AND policy.rating_model_version = config.rating_model_version
WHERE config.tournament_id = $1 AND config.version = $2`,
		claim.Ticket.TournamentID, claim.Ticket.TournamentVersion,
	).Scan(&modeID, &capacity, &policyVersion, &ratingModelVersion, &encodedPolicy)
	if errors.Is(err, pgx.ErrNoRows) {
		return matchmaking.MatchAttempt{}, errors.New("ticket tournament configuration is unavailable")
	}
	if err != nil {
		return matchmaking.MatchAttempt{}, fmt.Errorf("load ticket matching policy: %w", err)
	}
	if ratingModelVersion != claim.Ticket.RatingSnapshot.ModelVersion {
		return matchmaking.MatchAttempt{}, errors.New("ticket rating model does not match tournament policy")
	}
	policy, err := decodeMatchingPolicy(policyVersion, ratingModelVersion, encodedPolicy)
	if err != nil {
		return matchmaking.MatchAttempt{}, err
	}

	rooms, err := queue.loadCompatibleRooms(ctx, claim.Ticket, modeID, capacity, policy, evaluatedAt)
	if err != nil {
		return matchmaking.MatchAttempt{}, err
	}
	return matchmaking.MatchAttempt{
		Trigger: matchmaking.AttemptTriggerPeriodicRetry, EvaluatedAt: evaluatedAt,
		Candidate: candidateFromTicket(claim.Ticket), Policy: policy, Rooms: rooms,
	}, nil
}

func (queue *MatchmakingQueue) loadCompatibleRooms(ctx context.Context, ticket tournament.Ticket, modeID string, capacity int, policy matchmaking.Policy, evaluatedAt time.Time) ([]matchmaking.RoomView, error) {
	rows, err := queue.pool.Query(ctx, `
SELECT room_id, aggregate_version, created_at, fill_deadline
FROM rooms
WHERE tournament_id = $1
  AND tournament_version = $2
  AND mode_id = $3
  AND capacity = $4
  AND policy_version = $5
  AND rating_model_version = $6
  AND status = 'forming'
  AND fill_deadline >= $7
ORDER BY created_at, room_id
LIMIT $8`,
		ticket.TournamentID, ticket.TournamentVersion, modeID, capacity,
		policy.Version, policy.RatingModelVersion, evaluatedAt, policy.RoomLimit,
	)
	if err != nil {
		return nil, fmt.Errorf("load compatible rooms: %w", err)
	}
	defer rows.Close()

	rooms := make([]matchmaking.RoomView, 0, policy.RoomLimit)
	roomIndex := make(map[string]int, policy.RoomLimit)
	roomIDs := make([]string, 0, policy.RoomLimit)
	for rows.Next() {
		var room matchmaking.RoomView
		if err := rows.Scan(&room.RoomID, &room.AggregateVersion, &room.CreatedAt, &room.Deadline); err != nil {
			return nil, fmt.Errorf("scan compatible room: %w", err)
		}
		room.ModeID = modeID
		room.Capacity = capacity
		room.Policy = policy
		roomIndex[room.RoomID] = len(rooms)
		roomIDs = append(roomIDs, room.RoomID)
		rooms = append(rooms, room)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read compatible rooms: %w", err)
	}
	if len(roomIDs) == 0 {
		return rooms, nil
	}

	members, err := queue.pool.Query(ctx, `
SELECT membership.room_id,
       ticket.ticket_id, ticket.player_id, ticket.requested_at, ticket.snapshot_at,
       ticket.rating_mean, ticket.rating_uncertainty,
       ticket.rating_performance_deviation, ticket.rating_games,
       ticket.rating_model_version, ticket.rating_updated_at
FROM room_memberships AS membership
JOIN matchmaking_tickets AS ticket ON ticket.ticket_id = membership.ticket_id
WHERE membership.room_id = ANY($1::text[])
ORDER BY membership.room_id, membership.seat`, roomIDs)
	if err != nil {
		return nil, fmt.Errorf("load room members: %w", err)
	}
	defer members.Close()
	for members.Next() {
		var roomID string
		var candidate matchmaking.Candidate
		if err := members.Scan(
			&roomID, &candidate.TicketID, &candidate.PlayerID, &candidate.JoinedAt, &candidate.SnapshotAt,
			&candidate.Rating.Mean, &candidate.Rating.Uncertainty,
			&candidate.Rating.PerformanceDeviation, &candidate.Rating.Games,
			&candidate.Rating.ModelVersion, &candidate.Rating.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan room member: %w", err)
		}
		index, exists := roomIndex[roomID]
		if !exists {
			return nil, errors.New("room member references an unclaimed room view")
		}
		rooms[index].Members = append(rooms[index].Members, candidate)
	}
	if err := members.Err(); err != nil {
		return nil, fmt.Errorf("read room members: %w", err)
	}
	return rooms, nil
}

type matchingPolicyDefinition struct {
	InitialSkillGap         float64 `json:"initial_skill_gap"`
	MaxSkillGap             float64 `json:"max_skill_gap"`
	MaxWinProbabilitySpread float64 `json:"max_win_probability_spread"`
	ExpansionIntervalMS     int64   `json:"expansion_interval_ms"`
	FillTimeoutMS           int64   `json:"fill_timeout_ms"`
	AgePriorityAfterMS      int64   `json:"age_priority_after_ms"`
	CandidateLimit          int     `json:"candidate_limit"`
	RoomLimit               int     `json:"room_limit"`
	PreferNearlyFull        bool    `json:"prefer_nearly_full"`
}

func decodeMatchingPolicy(version, ratingModelVersion string, encoded []byte) (matchmaking.Policy, error) {
	var definition matchingPolicyDefinition
	if err := json.Unmarshal(encoded, &definition); err != nil {
		return matchmaking.Policy{}, fmt.Errorf("decode matching policy %q: %w", version, err)
	}
	expansion, err := milliseconds(definition.ExpansionIntervalMS)
	if err != nil {
		return matchmaking.Policy{}, fmt.Errorf("matching policy %q expansion interval: %w", version, err)
	}
	fillTimeout, err := milliseconds(definition.FillTimeoutMS)
	if err != nil {
		return matchmaking.Policy{}, fmt.Errorf("matching policy %q fill timeout: %w", version, err)
	}
	agePriority, err := millisecondsAllowZero(definition.AgePriorityAfterMS)
	if err != nil {
		return matchmaking.Policy{}, fmt.Errorf("matching policy %q age priority: %w", version, err)
	}
	policy := matchmaking.Policy{
		Version: version, RatingModelVersion: ratingModelVersion,
		InitialSkillGap: definition.InitialSkillGap, MaxSkillGap: definition.MaxSkillGap,
		MaxWinProbabilitySpread: definition.MaxWinProbabilitySpread,
		ExpansionInterval:       expansion, FillTimeout: fillTimeout, AgePriorityAfter: agePriority,
		CandidateLimit: definition.CandidateLimit, RoomLimit: definition.RoomLimit,
		PreferNearlyFull: definition.PreferNearlyFull,
	}
	if err := policy.Validate(); err != nil {
		return matchmaking.Policy{}, fmt.Errorf("validate matching policy %q: %w", version, err)
	}
	return policy, nil
}

func milliseconds(value int64) (time.Duration, error) {
	if value <= 0 {
		return 0, errors.New("duration must be positive")
	}
	return millisecondsAllowZero(value)
}

func millisecondsAllowZero(value int64) (time.Duration, error) {
	if value < 0 || value > math.MaxInt64/int64(time.Millisecond) {
		return 0, errors.New("duration is outside the supported range")
	}
	return time.Duration(value) * time.Millisecond, nil
}

func candidateFromTicket(ticket tournament.Ticket) matchmaking.Candidate {
	return matchmaking.Candidate{
		TicketID: ticket.ID, PlayerID: ticket.PlayerID,
		JoinedAt: ticket.RequestedAt, SnapshotAt: ticket.SnapshotAt,
		Rating: ticket.RatingSnapshot,
	}
}

var _ worker.QueueRepository = (*MatchmakingQueue)(nil)
var _ worker.AttemptRepository = (*MatchmakingQueue)(nil)

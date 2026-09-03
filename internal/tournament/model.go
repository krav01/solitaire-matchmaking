// Package tournament owns lifecycle data and rules for asynchronous tournaments.
package tournament

import (
	"errors"
	"time"

	"github.com/krav01/solitaire-matchmaking/pkg/rating"
)

type RoomStatus string

const (
	RoomForming    RoomStatus = "forming"
	RoomCollecting RoomStatus = "collecting"
	RoomCompleted  RoomStatus = "completed"
	RoomExpired    RoomStatus = "expired"
	RoomCancelled  RoomStatus = "cancelled"
)

type TicketStatus string

const (
	TicketQueued    TicketStatus = "queued"
	TicketAssigned  TicketStatus = "assigned"
	TicketCancelled TicketStatus = "cancelled"
	TicketExpired   TicketStatus = "expired"
)

type SessionStatus string

const (
	SessionAllocated SessionStatus = "allocated"
	SessionPlaying   SessionStatus = "playing"
	SessionSubmitted SessionStatus = "submitted"
	SessionForfeited SessionStatus = "forfeited"
)

// Config references externally approved scoring and settlement rules. The game
// backend owns eligibility, reservations, deck generation and money movement.
type Config struct {
	ID                  string
	Version             string
	ModeID              string
	Capacity            int
	EntryFeeMinor       int64
	Currency            string
	ScoringRulesVersion string
	SettlementVersion   string
	PolicyVersion       string
	RatingModelVersion  string
	ResultTimeout       time.Duration
}

func (c Config) Validate() error {
	if c.ID == "" || c.Version == "" || c.ModeID == "" || c.Currency == "" {
		return errors.New("tournament identity, mode and currency are required")
	}
	if c.Capacity != 5 && c.Capacity != 6 && c.Capacity != 7 {
		return errors.New("room capacity must be five, six or seven")
	}
	if c.EntryFeeMinor < 0 || c.ResultTimeout <= 0 {
		return errors.New("entry fee must be non-negative and result timeout positive")
	}
	if c.ScoringRulesVersion == "" || c.SettlementVersion == "" || c.PolicyVersion == "" || c.RatingModelVersion == "" {
		return errors.New("scoring, settlement, matching and rating versions are required")
	}
	return nil
}

type Ticket struct {
	ID                string
	EntryID           string
	PlayerID          string
	TournamentID      string
	TournamentVersion string
	Status            TicketStatus
	RequestedAt       time.Time
	AssignedAt        *time.Time
	SnapshotAt        time.Time
	RatingSnapshot    rating.Estimate
}

type Room struct {
	ID                 string
	TournamentID       string
	TournamentVersion  string
	PolicyVersion      string
	RatingModelVersion string
	DeckID             string
	Capacity           int
	Status             RoomStatus
	CreatedAt          time.Time
	FillDeadline       time.Time
	FilledAt           *time.Time
	ResultDeadline     *time.Time
	CompletedAt        *time.Time
}

// Session is separate from Room because play can finish while a room is forming.
type Session struct {
	ID          string
	TicketID    string
	RoomID      string
	PlayerID    string
	Seat        int
	Status      SessionStatus
	StartedAt   *time.Time
	SubmittedAt *time.Time
}

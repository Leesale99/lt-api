package schedule

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// Schedule events (the Game/Schedule split's event seam). They live here —
// in the publisher's package, in the publisher's language (ADR-027): game
// imports schedule (the one legal direction), schedule never imports game,
// and there is deliberately no shared events package. Payloads are fat:
// every fact a subscriber needs to act is inside the event, so handling
// needs no joins back into the schedule's tables.
//
// Delivery is post-commit, in-process, synchronous (internal/eventbus). A
// crash between the publisher's commit and the publish — or a failing
// handler — loses the event; that gap is accepted until the Phase 07 sweep
// / Phase 08 outbox, and every subscriber claims the EventID first
// (internal/idempotency) so a future delivery mechanism can replay freely.

// MatchResult is one settled match as the feed reports it: the facts of the
// result plus the identity needed to find and settle what rides on it.
type MatchResult struct {
	MatchID      int
	SeasonID     int
	RoundID      int
	HomeTeamID   int
	AwayTeamID   int
	Score        Score // both sides set (the resolve contract)
	Odds         Odds  // the pre-match odds the bonus accrual compounds on
	WinnerTeamID *int  // nil on a draw: a draw crowns no winner (Match.Winner)
	EndedAt      time.Time
}

// MatchResultRecorded announces that a match was resolved (scores written,
// status closed). Game settles the locked rides on the match from this
// payload — it never re-reads the match row to derive outcomes.
type MatchResultRecorded struct {
	EventID      string
	MatchID      int
	RoundID      int
	SeasonID     int
	HomeTeamID   int // which side's odds/teams the ride outcomes hang off
	AwayTeamID   int
	Score        Score
	Odds         Odds // fat payload: bonus accrual needs the ride's team odds
	WinnerTeamID *int // nil = draw: every locked ride lands lost
	EndedAt      time.Time
}

// RoundCompleted announces that a round was closed (all matches settled,
// status flipped). IsFinal marks the season's last round: its undecided
// rides are force-resolved to burned instead of unlocked. Results carries
// every match of the round — game's round settlement works purely from this
// payload (match IDs to find the rides, results to settle still-locked
// stragglers), never from a join through schedule's tables.
type RoundCompleted struct {
	EventID  string
	RoundID  int
	SeasonID int
	IsFinal  bool
	Results  []MatchResult
}

// NewEventID generates the event's dedup identity: 16 random bytes, hex.
// Subscribers claim it as the idempotency key (≤255 bytes per the
// idempotency_keys CHECK), so a replayed event re-executes nothing.
func NewEventID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failing means the process entropy source is broken;
		// an unidentifiable event must not be published.
		panic("schedule: cannot generate event id: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}

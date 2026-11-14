// Package model holds the schedule context's entities, validators and
// vocabulary — the anemic, transport-free half of the layered module (no
// json tags: the HTTP shape is the handler's DTO, which replicates the
// frozen wire format verbatim).
package model

import (
	"time"

	"lt-api.aleksrdvn.com/internal/validator"
)

type Odds struct {
	Home float64
	Away float64
}

type Score struct {
	Home *int
	Away *int
}

// MatchStatus is the lifecycle state of a match: created and postponed are
// interchangeable pre-start states, in_progress covers while it runs, closed
// is terminal.
type MatchStatus string

const (
	MatchCreated    MatchStatus = "created"
	MatchInProgress MatchStatus = "in_progress"
	MatchPostponed  MatchStatus = "postponed"
	MatchClosed     MatchStatus = "closed"
)

type Match struct {
	ID         int
	CreatedAt  time.Time
	StartsAt   time.Time
	EndedAt    *time.Time
	SeasonID   int
	RoundID    int
	HomeTeamID int
	AwayTeamID int
	Status     MatchStatus
	Odds       Odds
	Score      Score
	Version    int
}

// Winner is site 1 of 3 for the score→state rule: a closed match's final
// scores decide a ride's destination, a draw crowns no winner. The other
// two encodings: the payload derivation in game's MatchResultRecorded
// settlement handler (internal/game/events.go — the event consumer of
// the Game/Schedule split) and the result-agreement check in that same
// handler, which
// re-verifies the payload against the committed result through the
// SchedulePort (the app-level enforcement home since migration 000017;
// the old rides_state_gate check 5 read schedule's table from a game
// trigger, the cross-context read the split removed). Winner and the
// handler are derived copies tied by the shared fixture in
// internal/game/score_state_rule_test.go — change one, change all three.
func (m Match) Winner() *int {
	switch {
	case m.Status != MatchClosed:
		return nil
	case *m.Score.Home > *m.Score.Away:
		return &m.HomeTeamID
	case *m.Score.Away > *m.Score.Home:
		return &m.AwayTeamID
	default:
		return nil
	}
}

// MatchStatuses is the match status vocabulary. The DB CHECKs must accept
// every value (the drift detector in the repository tests pins it).
var MatchStatuses = []MatchStatus{MatchCreated, MatchInProgress, MatchPostponed, MatchClosed}

func ValidateMatchStatus(v *validator.Validator, status MatchStatus) {
	v.Check(validator.PermittedValue(status, MatchStatuses...), "status", "must be one of: created, in_progress, postponed, closed")
}

// validateMatchShape checks the time-independent invariants of a match:
// required fields, allowed statuses, the status/score matrix and the odds
// bounds. It is the shared core of ValidateNewMatch and ValidateMatchUpdate;
// each of those adds the rules that differ between creating and editing.
func validateMatchShape(v *validator.Validator, match Match) {
	v.Check(match.RoundID > 0, "round_id", "must be provided")
	v.Check(match.SeasonID > 0, "season_id", "must be provided")
	v.Check(match.HomeTeamID > 0, "home_team_id", "must be provided")
	v.Check(match.AwayTeamID > 0, "away_team_id", "must be provided")
	v.Check(match.HomeTeamID != match.AwayTeamID, "home_team_id", "home and away team cannot be the same")
	v.Check(match.Status != "", "status", "must be provided")
	ValidateMatchStatus(v, match.Status)
	v.Check(!match.StartsAt.IsZero(), "starts_at", "must be provided")
	v.Check(match.Status != MatchClosed || match.EndedAt != nil, "ended_at", "must be provided for the closed match")
	v.Check(match.EndedAt == nil || match.EndedAt.After(match.StartsAt), "ended_at", "must be after the match started")
	bothNil := match.Score.Home == nil && match.Score.Away == nil
	bothSet := match.Score.Home != nil && match.Score.Away != nil
	v.Check(bothNil || bothSet, "score", "must contain both home and away values or neither")
	v.Check(!bothSet || (*match.Score.Home >= 0 && *match.Score.Away >= 0), "score", "must not be negative")
	switch match.Status {
	case MatchInProgress, MatchClosed:
		v.Check(match.Score.Home != nil, "score", "must be provided when the match is in progress or closed")
	default: // created, postponed
		v.Check(match.Score.Home == nil, "score", "must not be set before the match is in progress or closed")
	}
	v.Check(match.Odds.Home > 1 && match.Odds.Away > 1, "odds", "must both be greater than one")
}

// ValidateNewMatch validates a match about to be created. The `now` argument
// is injected so tests control the clock instead of racing time.Now().
func ValidateNewMatch(v *validator.Validator, match Match, now time.Time) {
	validateMatchShape(v, match)
	if !match.StartsAt.IsZero() {
		v.Check(match.StartsAt.After(now), "starts_at", "must be in the future")
	}
	v.Check(match.Status == "created", "status", "new matches can only have status created")
}

// ValidateResolveMatch validates the request shape of a resolve payload:
// field presence and score arithmetic, the checks a handler runs on whatever
// bytes arrived. Domain-legality rules are not here — the one rule the
// resolve flow owns, "ended_at must be in the past", is time policy and
// lives in Service.ResolveMatch (ADR-026) so every caller passes it.
func ValidateResolveMatch(v *validator.Validator, match Match) {
	v.Check(match.EndedAt != nil, "ended_at", "must be provided")
	v.Check(match.ID > 0, "id", "must be provided")
	v.Check(match.Score.Home != nil && match.Score.Away != nil, "score", "must contain both home and away values")
	// The nil guard above runs first, but Check is not short-circuiting:
	// dereferencing a nil pointer here turned a 422 into a 500, so the
	// negativity check runs its own guard.
	if match.Score.Home != nil && match.Score.Away != nil {
		v.Check(*match.Score.Home >= 0 && *match.Score.Away >= 0, "score", "must not be negative")
	}
}

// matchStatusRank orders the statuses along their lifecycle so that backward
// transitions can be rejected. created and postponed share a rank: both are
// pre-start states, and a match may move between them freely — but only
// while it has not started. Whether the match has started is a time fact
// the validators cannot see, so "postponed only before start" is enforced
// by the matches_freeze_gate trigger (ADR-008); this rank rule merely keeps
// the pre-start states above in_progress and closed.
var matchStatusRank = map[MatchStatus]int{
	MatchCreated:    0,
	MatchPostponed:  0,
	MatchInProgress: 1,
	MatchClosed:     2,
}

// ValidateMatchUpdate validates a match about to be updated (new) against the
// currently stored version (old). Unlike creation, starts_at may be in the
// past — an existing match keeps being editable after it has started — but
// status must never move backwards along the lifecycle, and a closed match is
// terminal. The time-sensitive half of the freeze rule (no postponed after
// start) lives in the DB gate, keyed on the stored starts_at.
func ValidateMatchUpdate(v *validator.Validator, old, new Match) {
	validateMatchShape(v, new)

	// The old status comes from the database and is trusted; the new one has
	// already been shape-checked above.
	v.Check(old.Status != MatchClosed || new.Status == MatchClosed, "status", "cannot be changed after the match is closed")
	checkStatusRegression(v, "match", old.Status, new.Status, matchStatusRank)
}

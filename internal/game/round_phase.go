package game

import (
	"time"
)

// RoundPhase is the gameplay phase of a round within its lifecycle.
type RoundPhase string

const (
	ActionPhase   RoundPhase = "action"
	MatchPhase    RoundPhase = "match"
	DecisionPhase RoundPhase = "decision"
)

const (
	actionPhaseLead = -time.Hour
	matchPhaseLag   = time.Hour
)

// Phase derives the gameplay phase of a round from real-world facts: the
// round opens in ActionPhase, becomes MatchPhase one hour before its first
// match starts, and DecisionPhase one hour after its last match starts.
//
// Conventions (locked by boundary tests):
//   - a zero firstMatchAt (unscheduled round) reports ActionPhase
//   - boundary instants belong to the later phase
func Phase(now, firstMatchAt, lastMatchAt time.Time) RoundPhase {
	switch {
	case firstMatchAt.IsZero() || now.Before(firstMatchAt.Add(actionPhaseLead)):
		return ActionPhase
	case now.Before(lastMatchAt.Add(matchPhaseLag)):
		return MatchPhase
	default:
		return DecisionPhase
	}
}

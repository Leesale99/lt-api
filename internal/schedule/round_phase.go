package schedule

import (
	"time"
)

// RoundPhase is the gameplay phase of a round within its lifecycle. Phase B:
// the derivation is calendar knowledge (schedule), while the commands it
// gates and their wrong-phase error live in game — game imports this
// package, never the reverse.
type RoundPhase string

const (
	ActionPhase   RoundPhase = "action"
	MatchPhase    RoundPhase = "match"
	DecisionPhase RoundPhase = "decision"
)

const (
	actionPhaseLead  = time.Hour // actionPhase begins 1 hour before the start of the first match
	decisionPhaseLag = time.Hour // decisionPhase begins 1 hour after the end of the last match
)

// Phase derives the gameplay phase of a round from real-world facts: the
// round opens in ActionPhase, becomes MatchPhase one hour before its first
// match starts, and DecisionPhase one hour after its last match ends.
//
// A nil lastEndsAt (no match has ended yet) reports MatchPhase: matches are
// scheduled or running, so the decision window cannot be opened.
func Phase(now time.Time, firstStartsAt, lastEndedAt *time.Time) RoundPhase {
	switch {
	case firstStartsAt == nil || now.Before(firstStartsAt.Add(-actionPhaseLead)):
		return ActionPhase
	case lastEndedAt == nil || now.Before(lastEndedAt.Add(decisionPhaseLag)):
		return MatchPhase
	default:
		return DecisionPhase
	}
}

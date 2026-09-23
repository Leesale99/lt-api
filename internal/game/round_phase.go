package game

import (
	"errors"
	"time"
)

type RoundPhase string

var ErrorInvalidRoundPhase = errors.New("invalid round phase")

const (
	ActionPhase   RoundPhase = "action"
	MatchPhase    RoundPhase = "match"
	DecisionPhase RoundPhase = "decision"
)

const phaseTimeBuffer = time.Hour

func getRoundPhase(now, firstMatchAt, lastMatchAt time.Time) RoundPhase {
	switch {
	case firstMatchAt.IsZero() || now.Before(firstMatchAt.Add(-phaseTimeBuffer)):
		return ActionPhase
	case now.Before(lastMatchAt.Add(phaseTimeBuffer)):
		return MatchPhase
	default:
		return DecisionPhase
	}
}

package domain

// Phase is the gameplay phase of a round within its lifecycle — game's own
// vocabulary for the calendar fact every ride command gates on.
//
// The derivation is the calendar's (real-world match times: ActionPhase
// until an hour before the round's first start, MatchPhase until an hour
// after its last end, DecisionPhase after), and the anti-corruption layer at
// the module's outbound anti-corruption layer translates the calendar's round
// phase into this type before the value enters the core. The two vocabularies
// carry the same three semantics; the domain never imports the calendar.
type Phase string

const (
	ActionPhase   Phase = "action"
	MatchPhase    Phase = "match"
	DecisionPhase Phase = "decision"
)

// Phases is the phase vocabulary.
var Phases = []Phase{ActionPhase, MatchPhase, DecisionPhase}

package model

import (
	"fmt"
	"time"

	"lt-api.aleksrdvn.com/internal/kernel/validator"
)

// RoundsPerSeason is the total number of rounds in a season. The league has
// 20 teams playing each other home and away (double round-robin): 38 rounds
// of 10 matches each. It is calendar vocabulary, so it lives with the round
// entity: the validator bounds `number` by it and the close flow reads it as
// the "is this the season's last round" predicate.
const RoundsPerSeason = 38

type RoundStatus string

const (
	RoundCreated RoundStatus = "created"
	RoundOpen    RoundStatus = "open"
	RoundClosed  RoundStatus = "closed"
)

type Round struct {
	ID        int
	CreatedAt time.Time
	SeasonID  int
	Number    int
	Status    RoundStatus
	Version   int
}

// RoundStatuses is the round status vocabulary. The DB CHECKs must accept
// every value (the drift detector in the repository tests pins it).
var RoundStatuses = []RoundStatus{RoundCreated, RoundOpen, RoundClosed}

func ValidateRound(v *validator.Validator, round Round) {
	v.Check(round.SeasonID > 0, "season_id", "must be provided")
	v.Check(round.Number > 0 && round.Number <= RoundsPerSeason, "number", fmt.Sprintf("must be between 1 and %d", RoundsPerSeason))
	v.Check(round.Status != "", "status", "must be provided")
	ValidateRoundStatus(v, round.Status)
}

func ValidateRoundStatus(v *validator.Validator, status RoundStatus) {
	v.Check(validator.PermittedValue(status, RoundStatuses...), "status", "must be one of: created, open, closed")
}

// ValidateNewRound validates a round about to be created. A new round always
// starts in created: opening it belongs to the update flow, whose
// created → open transition also starts the season (RoundRepository.Open,
// ADR-008). Creating a round directly as open would skip that flip.
func ValidateNewRound(v *validator.Validator, round Round) {
	ValidateRound(v, round)
	v.Check(round.Status == RoundCreated, "status", "new rounds can only have status created")
}

// roundStatusRank orders the round lifecycle for transition checks.
var roundStatusRank = map[RoundStatus]int{
	RoundCreated: 0,
	RoundOpen:    1,
	RoundClosed:  2,
}

// ValidateRoundUpdate validates a round update (new) against the stored
// version (old): vocabulary plus no-regression. The freeze half — no
// created/open once a match of the round has started — is the
// rounds_freeze_gate trigger (ADR-008); this check is advisory UX.
func ValidateRoundUpdate(v *validator.Validator, old, new Round) {
	ValidateRound(v, new)
	checkStatusRegression(v, "round", old.Status, new.Status, roundStatusRank)
}

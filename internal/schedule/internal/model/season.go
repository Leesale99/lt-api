package model

import (
	"time"

	"lt-api.aleksrdvn.com/internal/kernel/validator"
)

// SeasonStatus is the lifecycle state of a season.
type SeasonStatus string

const (
	SeasonCreated    SeasonStatus = "created"
	SeasonOpen       SeasonStatus = "open"
	SeasonInProgress SeasonStatus = "in_progress"
	SeasonClosed     SeasonStatus = "closed"
)

type Season struct {
	ID        int
	CreatedAt time.Time
	Status    SeasonStatus
	Version   int
}

// SeasonStatuses is the season status vocabulary. The DB CHECKs must accept
// every value (the drift detector in the repository tests pins it).
var SeasonStatuses = []SeasonStatus{SeasonCreated, SeasonOpen, SeasonInProgress, SeasonClosed}

func ValidateSeason(v *validator.Validator, season Season) {
	v.Check(season.Status != "", "status", "must be provided")
	ValidateSeasonStatus(v, season.Status)
}

func ValidateSeasonStatus(v *validator.Validator, status SeasonStatus) {
	v.Check(validator.PermittedValue(status, SeasonStatuses...), "status", "must be one of: created, open, in_progress, closed")
}

// seasonStatusRank orders the season lifecycle for transition checks.
var seasonStatusRank = map[SeasonStatus]int{
	SeasonCreated:    0,
	SeasonOpen:       1,
	SeasonInProgress: 2,
	SeasonClosed:     3,
}

// ValidateNewSeason validates a season about to be created. A new season
// always starts in created: the client cannot choose the status, and the
// lifecycle progression belongs to Update (see checkStatusRegression).
func ValidateNewSeason(v *validator.Validator, season Season) {
	ValidateSeason(v, season)
	v.Check(season.Status == SeasonCreated, "status", "new seasons can only have status created")
}

// ValidateSeasonUpdate validates a season update (new) against the stored
// version (old): vocabulary plus no-regression. The freeze half — no
// created/open once a match has started — is the seasons_freeze_gate
// trigger (ADR-008); this check is advisory UX.
func ValidateSeasonUpdate(v *validator.Validator, old, new Season) {
	ValidateSeason(v, new)
	checkStatusRegression(v, "season", old.Status, new.Status, seasonStatusRank)
}

package model

// Lifecycle transition rules shared by the per-entity update validators
// (ADR-008): each level owns a status-rank map ordering its statuses along
// the lifecycle, and checkStatusRegression rejects transitions whose new
// status sits earlier than the stored one. App-side checks are advisory UX
// (friendly 422); the authoritative gates are DB triggers — app checks
// cannot be trusted with N writers (source-of-truth lesson).
//
// The DB is what makes regressions impossible: migration 000019 mirrors the
// three rank maps in the schedule gate functions, and the exported
// MatchStatusRegresses / RoundStatusRegresses / SeasonStatusRegresses
// helpers are the oracle the drift test pins those gates against
// (TestStatusRankGatesMirrorGoRanks). Without the advisory checks every
// illegal update would surface as a 409 instead of a 422.

import (
	"fmt"

	"lt-api.aleksrdvn.com/internal/kernel/validator"
)

// checkStatusRegression validates a status transition against the given
// stage-order map. It is generic over the per-entity status types (S must be
// a named string type): the compiler then guarantees that the two statuses
// and the rank map always come from the same vocabulary — Round statuses
// cannot be compared against matchStatusRank. Unknown statuses are skipped
// here: the vocabulary check on the new record already rejects them, and the
// stored status is trusted.
// rule:R-SCHEDULE-003
func checkStatusRegression[S ~string](v *validator.Validator, entity string, oldStatus, newStatus S, rank map[S]int) {
	if statusRegresses(oldStatus, newStatus, rank) {
		v.AddError("status", fmt.Sprintf("cannot move to an earlier stage of the %s lifecycle", entity))
	}
}

// statusRegresses is the single rank verdict shared by the advisory validator
// and the exported oracles below: newStatus sits earlier on the lifecycle
// than oldStatus. Unknown statuses are not regressions (the vocabulary check
// rejects them first, and the stored status is trusted).
func statusRegresses[S ~string](oldStatus, newStatus S, rank map[S]int) bool {
	oldRank, oldOK := rank[oldStatus]
	newRank, newOK := rank[newStatus]
	return oldOK && newOK && newRank < oldRank
}

// MatchStatusRegresses reports whether newStatus is an earlier stage of the
// match lifecycle than oldStatus. It is the oracle the DB gate is pinned
// against: migrations/000019_add_status_rank_gates.up.sql mirrors
// matchStatusRank in schedule.matches_block_started_regression and refuses
// exactly these pairs (P0001 'match_status_regression'). The frozen rank map
// stays unexported — the semantics are the contract, not the numbers.
func MatchStatusRegresses(oldStatus, newStatus MatchStatus) bool {
	return statusRegresses(oldStatus, newStatus, matchStatusRank)
}

// RoundStatusRegresses is MatchStatusRegresses for the round vocabulary; the
// DB mirror is schedule.rounds_block_illegal_progress (000019), refusing
// with P0001 'round_status_regression'.
func RoundStatusRegresses(oldStatus, newStatus RoundStatus) bool {
	return statusRegresses(oldStatus, newStatus, roundStatusRank)
}

// SeasonStatusRegresses is MatchStatusRegresses for the season vocabulary;
// the DB mirror is schedule.seasons_block_started_regression (000019),
// refusing with P0001 'season_status_regression'.
func SeasonStatusRegresses(oldStatus, newStatus SeasonStatus) bool {
	return statusRegresses(oldStatus, newStatus, seasonStatusRank)
}

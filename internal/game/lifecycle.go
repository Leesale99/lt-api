package game

// Lifecycle transition rules shared by the per-entity update validators
// (ADR-008): each level owns a status-rank map ordering its statuses along
// the lifecycle, and checkStatusRegression rejects transitions whose new
// status sits earlier than the stored one. App-side checks are advisory UX
// (friendly 422); the authoritative freeze gates are DB triggers — app
// checks cannot be trusted with N writers (source-of-truth lesson).
//
// The DB is what makes regressions impossible; without these advisory
// checks every illegal update would surface as a 409 instead of a 422.

import (
	"fmt"

	"lt-api.aleksrdvn.com/internal/validator"
)

// checkStatusRegression validates a status transition against the given
// stage-order map. Unknown statuses are skipped here: the vocabulary check
// on the new record already rejects them, and the stored status is trusted.
func checkStatusRegression(v *validator.Validator, entity, oldStatus, newStatus string, rank map[string]int) {
	oldRank, oldOK := rank[oldStatus]
	newRank, newOK := rank[newStatus]
	if !oldOK || !newOK {
		return
	}
	if newRank < oldRank {
		v.AddError("status", fmt.Sprintf("cannot move to an earlier stage of the %s lifecycle", entity))
	}
}

package spec

// The sabotage steps (style guide §8): the Owner pool — the schema's own role —
// attempts the forbidden write directly, bypassing every facade, use case and
// Go guard. The subject of the sentence is the database object, so the
// assertion reads the driver's refusal: the SQLSTATE and the constraint or
// RAISE name the migration spells.

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/cucumber/godog"
)

func registerSabotageSteps(sc *godog.ScenarioContext) {
	sc.Step(`^the Owner pool tries to insert a ride for "([^"]+)" on match day ([0-9]+) in the state "([^"]+)"$`, ownerInsertsRideInState)
	sc.Step(`^the Owner pool tries to insert a locked ride with (.+)$`, ownerInsertsLockedRide)
	sc.Step(`^the Owner pool tries to move the ride straight to "([^"]+)"$`, ownerMovesRideToState)
	sc.Step(`^the Owner pool tries to move the ride onto match day ([0-9]+)$`, ownerMovesRideToMatchDay)
	sc.Step(`^the Owner pool tries to move the frozen ride onto match day ([0-9]+)$`, ownerMovesRideToMatchDay)
	sc.Step(`^the Owner pool tries to unlock the ride without releasing its bonus$`, ownerUnlocksKeepingBonus)
	sc.Step(`^the Owner pool rewrites the frozen ride without changing its state or its match$`, ownerRewritesFrozenRide)
	sc.Step(`^the write is refused by ([a-z_]+)$`, writeRefusedBy)
	sc.Step(`^the refusal names ([a-z_]+)$`, refusalNames)
	sc.Step(`^the write is allowed$`, writeIsAllowed)
}

// ownerInsertsRideInState attempts a ride birth in a state other than locked —
// the insert gate's subject.
func ownerInsertsRideInState(ctx context.Context, player, matchDay, state string) (context.Context, error) {
	w := worldFrom(ctx)
	day, err := strconv.Atoi(matchDay)
	if err != nil {
		return stepFailed(ctx, err)
	}
	playerID, ok := w.players[player]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("player %q was never registered", player))
	}
	matchID, ok := w.matchDays[day]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("match day %d was never built", day))
	}
	facts, err := w.matchFacts(ctx, matchID)
	if err != nil {
		return stepFailed(ctx, err)
	}

	err = w.ownerWrite(ctx, `
		INSERT INTO game.rides (player_id, team_id, match_id, state, tokens_locked, base_at_lock)
		VALUES ($1, $2, $3, $4, 10, 1.95)
	`, playerID, facts.HomeTeamID, matchID, state)
	if err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

// ownerInsertsLockedRide attempts a locked ride carrying money outside the
// domain — the money CHECKs' subject.
func ownerInsertsLockedRide(ctx context.Context, amount string) (context.Context, error) {
	w := worldFrom(ctx)
	playerID, ok := w.players["Ana"]
	if !ok {
		return stepFailed(ctx, fmt.Errorf(`player "Ana" was never registered`))
	}
	matchID, ok := w.matchDays[1]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("match day 1 was never built"))
	}
	facts, err := w.matchFacts(ctx, matchID)
	if err != nil {
		return stepFailed(ctx, err)
	}

	tokens, base, bonus, streak, err := moneyOutsideDomain(amount)
	if err != nil {
		return stepFailed(ctx, err)
	}

	err = w.ownerWrite(ctx, `
		INSERT INTO game.rides (player_id, team_id, match_id, state, tokens_locked, base_at_lock, bonus_acc, streak)
		VALUES ($1, $2, $3, 'locked', $4, $5, $6, $7)
	`, playerID, facts.HomeTeamID, matchID, tokens, base, bonus, streak)
	if err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

// moneyOutsideDomain maps the feature's phrase onto the column that carries the
// violation; everything else stays inside the domain so the named CHECK is the
// only refusal.
func moneyOutsideDomain(amount string) (tokens int, base float64, bonus float64, streak int, err error) {
	tokens, base, bonus, streak = 10, 1.95, 0, 0

	switch {
	case strings.Contains(amount, "stake of zero"):
		tokens = 0
	case strings.Contains(amount, "stake of minus five"):
		tokens = -5
	case strings.Contains(amount, "base price of zero"):
		base = 0
	case strings.Contains(amount, "accrued bonus of minus one"):
		bonus = -1
	case strings.Contains(amount, "streak of minus one"):
		streak = -1
	default:
		return 0, 0, 0, 0, fmt.Errorf("no money case for %q", amount)
	}
	return tokens, base, bonus, streak, nil
}

func ownerMovesRideToState(ctx context.Context, state string) (context.Context, error) {
	w := worldFrom(ctx)
	ride, err := w.currentRide(ctx)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if err := w.ownerWrite(ctx, `UPDATE game.rides SET state = $2 WHERE id = $1`, ride.ID, state); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

func ownerMovesRideToMatchDay(ctx context.Context, matchDay string) (context.Context, error) {
	w := worldFrom(ctx)
	day, err := strconv.Atoi(matchDay)
	if err != nil {
		return stepFailed(ctx, err)
	}
	matchID, ok := w.matchDays[day]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("match day %d was never built", day))
	}
	ride, err := w.currentRide(ctx)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if err := w.ownerWrite(ctx, `UPDATE game.rides SET match_id = $2 WHERE id = $1`, ride.ID, matchID); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

func ownerUnlocksKeepingBonus(ctx context.Context) (context.Context, error) {
	w := worldFrom(ctx)
	ride, err := w.currentRide(ctx)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if err := w.ownerWrite(ctx, `UPDATE game.rides SET state = 'unlocked' WHERE id = $1`, ride.ID); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

// ownerRewritesFrozenRide is the carve-out: a state-preserving full-row rewrite
// of a terminal ride is not a transition, so the gate deliberately allows it.
func ownerRewritesFrozenRide(ctx context.Context) (context.Context, error) {
	w := worldFrom(ctx)
	ride, err := w.currentRide(ctx)
	if err != nil {
		return stepFailed(ctx, err)
	}
	err = w.ownerWrite(ctx, `
		UPDATE game.rides
		SET state = state, match_id = match_id, tokens_locked = tokens_locked, version = version + 1
		WHERE id = $1
	`, ride.ID)
	if err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

// writeRefusedBy asserts the named database object refused the write. P0001
// refusals carry the trigger's RAISE name in the message; a CHECK violation
// carries the constraint name.
func writeRefusedBy(ctx context.Context, object string) (context.Context, error) {
	w := worldFrom(ctx)
	pgErr := w.lastPgError
	if pgErr == nil {
		return stepFailed(ctx, fmt.Errorf("the write was allowed, want a refusal by %s", object))
	}

	switch object {
	case "rides_state_insert_gate":
		if pgErr.Code != "P0001" || pgErr.Message != "ride_insert_requires_locked_state" {
			return stepFailed(ctx, fmt.Errorf("the write was refused by %s (%s), want %s", pgErr.Code, pgErr.Message, object))
		}
	case "rides_state_gate":
		if pgErr.Code != "P0001" || !stateGateRaises[pgErr.Message] {
			return stepFailed(ctx, fmt.Errorf("the write was refused by %s (%s), want %s", pgErr.Code, pgErr.Message, object))
		}
	case "rides_money_state_coupling",
		"rides_tokens_locked_check",
		"rides_base_at_lock_check",
		"rides_bonus_acc_check",
		"rides_streak_check":
		if pgErr.Code != "23514" || pgErr.ConstraintName != object {
			return stepFailed(ctx, fmt.Errorf("the write was refused by %s (%s on %q), want %s",
				pgErr.Code, pgErr.Message, pgErr.ConstraintName, object))
		}
	default:
		return stepFailed(ctx, fmt.Errorf("no database object %q in the ride surface", object))
	}

	return stepOK(ctx)
}

// stateGateRaises is the transition trigger's refusal vocabulary
// (migrations/000017_app_level_result_agreement.up.sql, checks 1–3).
var stateGateRaises = map[string]bool{
	"ride_terminal_state_frozen":                   true,
	"ride_match_id_immutable_outside_continuation": true,
	"ride_state_transition_illegal":                true,
}

// refusalNames asserts the raised name (P0001) or the constraint name (a CHECK
// violation) the feature names.
func refusalNames(ctx context.Context, name string) (context.Context, error) {
	w := worldFrom(ctx)
	pgErr := w.lastPgError
	if pgErr == nil {
		return stepFailed(ctx, fmt.Errorf("no write was refused, want the name %s", name))
	}
	if pgErr.Code == "P0001" {
		if pgErr.Message != name {
			return stepFailed(ctx, fmt.Errorf("the refusal names %q, want %q", pgErr.Message, name))
		}
		return stepOK(ctx)
	}
	if pgErr.ConstraintName != name {
		return stepFailed(ctx, fmt.Errorf("the refusal names %q, want %q", pgErr.ConstraintName, name))
	}
	return stepOK(ctx)
}

func writeIsAllowed(ctx context.Context) (context.Context, error) {
	w := worldFrom(ctx)
	if w.lastPgError != nil {
		return stepFailed(ctx, fmt.Errorf("the write was refused by %s (%s), want it allowed",
			w.lastPgError.Code, w.lastPgError.Message))
	}
	return stepOK(ctx)
}

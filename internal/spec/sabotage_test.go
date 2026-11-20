package spec

// The sabotage steps (style guide §8): the Owner pool — the schema's own role —
// attempts the forbidden write directly, bypassing every facade, use case and
// Go guard. The subject of the sentence is the database object, so the
// assertion reads the driver's refusal: the SQLSTATE and the constraint or
// RAISE name the migration spells.
//
// The family covers every database-authority rule: the rides and the calendar
// gates first, then the game's registrations and the identity tables (the
// fingerprint key, the permission keys, the store-stamped id). The one refusal
// that carries no constraint name — a GENERATED ALWAYS identity column
// rejecting a caller-supplied value — is asserted by the SQLSTATE alone, in the
// shape `the write is refused for …` (still one of §8's `the write is refused`
// prefixes).

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

	// The calendar's team record: the fixtures' RESTRICT, and the three CHECKs
	// a club's shape is made of.
	sc.Step(`^the Owner pool tries to delete the team "([^"]+)"$`, ownerDeletesTeam)
	sc.Step(`^the Owner pool tries to record a team with (no name|an empty badge|no description)$`, ownerRecordsTeamWithAMissingPart)

	// The game's own registrations: the season key that makes one entry per
	// member, and the rides that keep a registration from being withdrawn.
	sc.Step(`^the Owner pool tries to register "([^"]+)" a second time in the season$`, ownerRegistersPlayerAgain)
	sc.Step(`^the Owner pool tries to delete "([^"]+)"'s registration$`, ownerDeletesRegistration)

	// The identity tables: the fingerprint key, the permission code key, the
	// role-permission key and the id the store stamps.
	sc.Step(`^the Owner pool tries to store the same token a second time$`, ownerStoresSameToken)
	sc.Step(`^the Owner pool tries to record the permission code "([^"]+)" a second time$`, ownerRecordsDuplicatePermission)
	sc.Step(`^the Owner pool tries to give the standard user role a code it already carries$`, ownerGivesDuplicatePermission)
	sc.Step(`^the Owner pool tries to record an account that names its id$`, ownerRecordsAccountNamingItsID)

	sc.Step(`^the write is refused by ([a-z_]+)$`, writeRefusedBy)
	sc.Step(`^the write is refused for the id the store stamps$`, writeRefusedForStoreStampedID)
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

// ownerDeletesTeam attempts the calendar's team delete directly: the fixtures
// that name the club are the RESTRICT that keeps it (R-SCHEDULE-025).
func ownerDeletesTeam(ctx context.Context, name string) (context.Context, error) {
	w := worldFrom(ctx)
	teamID, ok := w.teams[name]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("team %q was never built", name))
	}
	if err := w.ownerWrite(ctx, `DELETE FROM schedule.teams WHERE id = $1`, teamID); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

// ownerRecordsTeamWithAMissingPart attempts a club record with one of the three
// CHECKed columns emptied, everything else inside the domain so the named CHECK
// is the only refusal (R-SCHEDULE-029).
func ownerRecordsTeamWithAMissingPart(ctx context.Context, gap string) (context.Context, error) {
	w := worldFrom(ctx)
	name := "Zeta"
	badge := "https://spec.example/zeta.png"
	description := "a club the calendar carries"

	switch gap {
	case "no name":
		name = ""
	case "an empty badge":
		badge = ""
	case "no description":
		description = ""
	default:
		return stepFailed(ctx, fmt.Errorf("no team gap for %q", gap))
	}

	err := w.ownerWrite(ctx, `
		INSERT INTO schedule.teams (name, logo, description)
		VALUES ($1, $2, $3)
	`, name, badge, description)
	if err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

// ownerRegistersPlayerAgain attempts a second registration for the member who
// already holds one in the season — the season key's subject (R-GAME-014). The
// row is copied from the existing registration, so the member is the only fact
// repeated.
func ownerRegistersPlayerAgain(ctx context.Context, name string) (context.Context, error) {
	w := worldFrom(ctx)
	playerID, ok := w.players[name]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("player %q was never registered", name))
	}

	var seasonID, favouriteTeamID int
	var userID *int
	err := w.app.Owner.QueryRow(ctx, `
		SELECT season_id, favorite_team_id, user_id FROM game.players WHERE id = $1
	`, playerID).Scan(&seasonID, &favouriteTeamID, &userID)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if userID == nil {
		return stepFailed(ctx, fmt.Errorf("the registration for %q names no member, so a second one is not forbidden", name))
	}

	err = w.ownerWrite(ctx, `
		INSERT INTO game.players (season_id, favorite_team_id, name, user_id)
		VALUES ($1, $2, $3, $4)
	`, seasonID, favouriteTeamID, name+" again", *userID)
	if err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

// ownerDeletesRegistration attempts the registration delete while the rides
// still name it — rides_player_id_fkey's RESTRICT (R-GAME-035).
func ownerDeletesRegistration(ctx context.Context, name string) (context.Context, error) {
	w := worldFrom(ctx)
	playerID, ok := w.players[name]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("player %q was never registered", name))
	}
	if err := w.ownerWrite(ctx, `DELETE FROM game.players WHERE id = $1`, playerID); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

// ownerStoresSameToken attempts a second row for a fingerprint the table
// already holds: the fingerprint is the key, and there is no column a cleartext
// token could occupy (R-IDENTITY-006).
func ownerStoresSameToken(ctx context.Context) (context.Context, error) {
	w := worldFrom(ctx)
	err := w.ownerWrite(ctx, `
		INSERT INTO identity.user_tokens (hash, user_id, expiry, scope)
		SELECT hash, user_id, expiry, scope FROM identity.user_tokens
	`)
	if err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

// ownerRecordsDuplicatePermission attempts a second row for a permission code
// the seed already carries (R-IDENTITY-014).
func ownerRecordsDuplicatePermission(ctx context.Context, code string) (context.Context, error) {
	w := worldFrom(ctx)
	if err := w.ownerWrite(ctx, `INSERT INTO identity.permissions (code) VALUES ($1)`, code); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

// ownerGivesDuplicatePermission attempts the same role-permission pair twice:
// the pair is the key, so "this role carries this code" has one answer
// (R-IDENTITY-013).
func ownerGivesDuplicatePermission(ctx context.Context) (context.Context, error) {
	w := worldFrom(ctx)
	err := w.ownerWrite(ctx, `
		INSERT INTO identity.roles_permissions (role_id, permission_id)
		SELECT role_id, permission_id
		FROM identity.roles_permissions
		WHERE role_id = (SELECT id FROM identity.roles WHERE name = 'user')
		LIMIT 1
	`)
	if err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

// ownerRecordsAccountNamingItsID attempts an account whose id the caller
// supplies. The value is arbitrary: a GENERATED ALWAYS identity column refuses
// any non-DEFAULT value, not merely a taken one (R-IDENTITY-020).
func ownerRecordsAccountNamingItsID(ctx context.Context) (context.Context, error) {
	w := worldFrom(ctx)
	err := w.ownerWrite(ctx, `
		INSERT INTO identity.users (id, name, email, password_hash, activated, role_id)
		VALUES (7000, 'Zoe', 'zoe@example.com', $1, false,
		        (SELECT id FROM identity.roles WHERE name = 'user'))
	`, []byte{0})
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
		if raises, ok := scheduleGateRaises[object]; ok {
			if pgErr.Code != "P0001" || !raises[pgErr.Message] {
				return stepFailed(ctx, fmt.Errorf("the write was refused by %s (%s), want %s",
					pgErr.Code, pgErr.Message, object))
			}
			return stepOK(ctx)
		}
		for _, codes := range []map[string]string{
			scheduleConstraintCodes, gameConstraintCodes, identityConstraintCodes,
		} {
			code, ok := codes[object]
			if !ok {
				continue
			}
			if pgErr.Code != code || pgErr.ConstraintName != object {
				return stepFailed(ctx, fmt.Errorf("the write was refused by %s (%s on %q), want %s",
					pgErr.Code, pgErr.Message, pgErr.ConstraintName, object))
			}
			return stepOK(ctx)
		}
		return stepFailed(ctx, fmt.Errorf("no database object %q in the ride, calendar, game or identity surface", object))
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

// scheduleGateRaises is the calendar's gate vocabulary: each trigger and the
// RAISE names it can produce (migrations/000001, 000008 and 000019 — the
// trigger keeps its name while a later migration replaces its body). Triggers
// fire in name order, which is why a round's close gate speaks before its
// freeze gate before its progress gate.
var scheduleGateRaises = map[string]map[string]bool{
	"matches_freeze_gate": {
		"match_status_regression":                true,
		"started_match_blocks_status_regression": true,
	},
	"rounds_progress_gate": {
		"round_status_regression":        true,
		"round_progress_season_not_live": true,
		"round_sequence_incomplete":      true,
	},
	"rounds_freeze_gate": {
		"started_match_blocks_round_regression": true,
	},
	"rounds_close_gate": {
		"round_close_has_unclosed_matches": true,
		"round_close_has_unresolved_rides": true,
	},
	"rounds_delete_gate": {
		"round_lifecycle_blocks_delete": true,
	},
	"seasons_freeze_gate": {
		"season_status_regression":               true,
		"started_match_blocks_season_regression": true,
	},
	"seasons_delete_gate": {
		"season_lifecycle_blocks_delete": true,
	},
}

// scheduleConstraintCodes is the declarative half: the constraint a calendar
// sabotage write trips and the SQLSTATE it arrives with — CHECK (23514),
// foreign key (23503 for NO ACTION, 23001 for RESTRICT), unique (23505).
var scheduleConstraintCodes = map[string]string{
	"seasons_status_check":            "23514",
	"matches_home_team_id_fkey":       "23001",
	"teams_name_check":                "23514",
	"teams_logo_check":                "23514",
	"teams_description_check":         "23514",
	"rounds_status_check":             "23514",
	"rounds_number_check":             "23514",
	"matches_status_check":            "23514",
	"matches_status_score_check":      "23514",
	"matches_score_complete_check":    "23514",
	"matches_home_score_check":        "23514",
	"matches_away_score_check":        "23514",
	"matches_ended_at_check":          "23514",
	"matches_home_odds_check":         "23514",
	"matches_away_odds_check":         "23514",
	"matches_teams_differ_check":      "23514",
	"matches_season_id_round_id_fkey": "23503",
	"rounds_season_id_number_key":     "23505",
}

// gameConstraintCodes is the game schema's declarative half: the season entry's
// one-per-member key and the rides that keep a registration from being
// withdrawn.
var gameConstraintCodes = map[string]string{
	"players_season_id_user_id_key": "23505",
	"rides_player_id_fkey":          "23001",
}

// identityConstraintCodes is the identity schema's declarative half: the token
// fingerprint key, the permission code key and the grant's role-permission key.
var identityConstraintCodes = map[string]string{
	"user_tokens_pkey":       "23505",
	"permissions_code_key":   "23505",
	"roles_permissions_pkey": "23505",
}

// writeRefusedForStoreStampedID asserts the one refusal in the identity surface
// that carries no constraint name: a GENERATED ALWAYS identity column refuses a
// caller-supplied value outright (SQLSTATE 428C9), so the scenario names the
// column's rule rather than a database object (R-IDENTITY-020).
func writeRefusedForStoreStampedID(ctx context.Context) (context.Context, error) {
	w := worldFrom(ctx)
	pgErr := w.lastPgError
	if pgErr == nil {
		return stepFailed(ctx, fmt.Errorf("the write was allowed, want the store-stamped id refused"))
	}
	if pgErr.Code != "428C9" {
		return stepFailed(ctx, fmt.Errorf("the write was refused with %s (%s), want 428C9 (a value the store stamps)",
			pgErr.Code, pgErr.Message))
	}
	return stepOK(ctx)
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

package spec

// The ride lifecycle steps: the world the two pilot features share, the lock
// command, the five states, the phase windows and the one refusal vocabulary
// (see docs/spec/gherkin-style.md §7).

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/cucumber/godog"
	"github.com/shopspring/decimal"
)

func registerRideSteps(sc *godog.ScenarioContext) {
	// The world (Background).
	sc.Step(`^the season "([^"]+)" has ([0-9]+) rounds$`, seasonWithRounds)
	sc.Step(`^the teams "([^"]+)", "([^"]+)" and "([^"]+)" play in "([^"]+)"$`, teamsPlayInSeason)
	sc.Step(`^the player "([^"]+)" is registered in "([^"]+)" with ([0-9]+) tokens$`, registerPlayer)
	sc.Step(`^round ([0-9]+) holds match day ([0-9]+): "([^"]+)" against "([^"]+)"(?: at odds ([0-9.]+))?$`, roundHoldsMatchDay)

	// The clock.
	sc.Step(`^it is ([0-9]+) (hours|minutes) before round ([0-9]+)'s first match$`, clockBeforeFirstMatch)
	sc.Step(`^it is ([0-9]+) (hours|minutes) after round ([0-9]+)'s last match$`, clockAfterLastMatch)
	sc.Step(`^round ([0-9]+) is still open$`, roundIsStillOpen)
	sc.Step(`^the last match of round ([0-9]+) has not ended$`, lastMatchHasNotEnded)
	sc.Step(`^round ([0-9]+) closed without settling its rides$`, roundClosedWithoutSettling)

	// Riding: the lock and the decision commands.
	sc.Step(`^the player locks (-?[0-9]+) tokens on "([^"]+)" for match day ([0-9]+)$`, playerLocksTokens)
	sc.Step(`^the player (burns|unlocks|continues) the ride$`, playerDecides)

	// The world's rides, built the product's way.
	sc.Step(`^a ride locked by "([^"]+)" on "([^"]+)" for match day ([0-9]+) with ([0-9]+) tokens(?: at odds ([0-9.]+))?$`, rideLockedBy)
	sc.Step(`^a ride waiting on a decision for "([^"]+)" on match day ([0-9]+) with ([0-9]+) tokens$`, rideWaitingOnDecision)
	sc.Step(`^a ride for "([^"]+)" on match day ([0-9]+) that was burned$`, rideThatWasBurned)
	sc.Step(`^the ride on match day ([0-9]+) is in the state "([^"]+)"$`, rideInState)

	// Results.
	sc.Step(`^match day ([0-9]+) ends "([^"]+)"(?: to "([^"]+)")?$`, matchDayEnds)

	// Assertions.
	sc.Step(`^the ride is locked$`, rideIsLocked)
	sc.Step(`^the ride is locked on match day ([0-9]+)$`, rideIsLockedOnMatchDay)
	sc.Step(`^the ride is still locked$`, rideIsStillLocked)
	sc.Step(`^the ride is lost$`, rideIs("lost"))
	sc.Step(`^the ride is burned$`, rideIs("burned"))
	sc.Step(`^the ride is unlocked$`, rideIs("unlocked"))
	sc.Step(`^the ride waits on a decision$`, rideWaitsOnDecision)
	sc.Step(`^the ride still waits on a decision$`, rideStillWaitsOnDecision)
	sc.Step(`^the ride still reads "([^"]+)"$`, rideStillReads)
	sc.Step(`^the ride carries a stake of ([0-9]+) tokens$`, rideCarriesStake)
	sc.Step(`^the ride still carries a stake of ([0-9]+) tokens$`, rideStillCarriesStake)
	sc.Step(`^the ride carries a base price captured when it was locked$`, rideCarriesBasePrice)
	sc.Step(`^the ride has banked no streak and accrued no bonus$`, rideHasNoStreakAndNoBonus)
	sc.Step(`^the ride has banked one win$`, rideHasBankedOneWin)
	sc.Step(`^the ride has accrued a bonus$`, rideHasAccruedBonus)
	sc.Step(`^the command is refused$`, commandIsRefused)
	sc.Step(`^the command is refused as an illegal transition$`, commandRefusedAsIllegalTransition)
	sc.Step(`^the command is refused as the wrong phase$`, commandRefusedAsWrongPhase)
	sc.Step(`^the command is refused as the round not being open$`, commandRefusedAsRoundNotOpen)
	sc.Step(`^no ride exists for "([^"]+)" on match day ([0-9]+)$`, noRideExists)
	sc.Step(`^the ride was not created$`, rideWasNotCreated)
}

// --- the world -------------------------------------------------------------

func seasonWithRounds(ctx context.Context, name, rounds string) (context.Context, error) {
	w := worldFrom(ctx)
	n, err := strconv.Atoi(rounds)
	if err != nil {
		return stepFailed(ctx, fmt.Errorf("round count %q: %w", rounds, err))
	}
	if err := w.seasonWithRounds(ctx, name, n); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

func teamsPlayInSeason(ctx context.Context, first, second, third, season string) (context.Context, error) {
	w := worldFrom(ctx)
	if w.seasonName != season {
		return stepFailed(ctx, fmt.Errorf("no season %q was built (have %q)", season, w.seasonName))
	}
	if err := w.teamsInSeason(ctx, first, second, third); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

// registerPlayer registers the player in the season. The token figure is the
// player's bankroll — the ledger has no balances yet (the ride money columns
// are its phase-05/06 stand-in), so the registration itself is the fact.
func registerPlayer(ctx context.Context, player, season, _ string) (context.Context, error) {
	w := worldFrom(ctx)
	if w.seasonName != season {
		return stepFailed(ctx, fmt.Errorf("no season %q was built (have %q)", season, w.seasonName))
	}
	if err := w.registerPlayer(ctx, player); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

func roundHoldsMatchDay(ctx context.Context, roundNo, matchDay, home, away, odds string) (context.Context, error) {
	w := worldFrom(ctx)
	round, err := strconv.Atoi(roundNo)
	if err != nil {
		return stepFailed(ctx, err)
	}
	day, err := strconv.Atoi(matchDay)
	if err != nil {
		return stepFailed(ctx, err)
	}

	var priced *float64
	if odds != "" {
		value, err := strconv.ParseFloat(odds, 64)
		if err != nil {
			return stepFailed(ctx, fmt.Errorf("odds %q: %w", odds, err))
		}
		priced = &value
	}

	if err := w.holdMatchDay(ctx, round, day, home, away, priced); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

// --- the clock -------------------------------------------------------------

func clockBeforeFirstMatch(ctx context.Context, amount, unit, roundNo string) (context.Context, error) {
	w := worldFrom(ctx)
	round, err := strconv.Atoi(roundNo)
	if err != nil {
		return stepFailed(ctx, err)
	}
	lead, err := duration(amount, unit)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if err := w.clockBeforeFirstMatch(ctx, round, lead); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

func clockAfterLastMatch(ctx context.Context, amount, unit, roundNo string) (context.Context, error) {
	w := worldFrom(ctx)
	round, err := strconv.Atoi(roundNo)
	if err != nil {
		return stepFailed(ctx, err)
	}
	ago, err := duration(amount, unit)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if err := w.clockAfterLastMatch(ctx, round, ago); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

func duration(amount, unit string) (time.Duration, error) {
	n, err := strconv.Atoi(amount)
	if err != nil {
		return 0, fmt.Errorf("amount %q: %w", amount, err)
	}
	if unit == "minutes" {
		return time.Duration(n) * time.Minute, nil
	}
	return time.Duration(n) * time.Hour, nil
}

func roundIsStillOpen(ctx context.Context, roundNo string) (context.Context, error) {
	w := worldFrom(ctx)
	round, err := strconv.Atoi(roundNo)
	if err != nil {
		return stepFailed(ctx, err)
	}
	status, err := w.roundStatus(ctx, round)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if status != "open" {
		return stepFailed(ctx, fmt.Errorf("round %d reads %q, the scenario needs it open", round, status))
	}
	return stepOK(ctx)
}

func lastMatchHasNotEnded(ctx context.Context, roundNo string) (context.Context, error) {
	w := worldFrom(ctx)
	round, err := strconv.Atoi(roundNo)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if err := w.reopenLastMatch(ctx, round); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

func roundClosedWithoutSettling(ctx context.Context, roundNo string) (context.Context, error) {
	w := worldFrom(ctx)
	round, err := strconv.Atoi(roundNo)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if err := w.closeRoundWithoutSettlement(ctx, round); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

// --- commands --------------------------------------------------------------

func playerLocksTokens(ctx context.Context, tokens, team, matchDay string) (context.Context, error) {
	w := worldFrom(ctx)
	count, err := strconv.Atoi(tokens)
	if err != nil {
		return stepFailed(ctx, err)
	}
	day, err := strconv.Atoi(matchDay)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if _, err := w.lockTokens(ctx, "Ana", team, day, count); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

func playerDecides(ctx context.Context, command string) (context.Context, error) {
	w := worldFrom(ctx)
	verb := map[string]string{"burns": "burn", "unlocks": "unlock", "continues": "lock"}[command]
	ride, err := w.currentRide(ctx)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if _, err := w.rideCommand(ctx, ride.ID, verb); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

// --- world rides -----------------------------------------------------------

func rideLockedBy(ctx context.Context, player, team, matchDay, tokens, odds string) (context.Context, error) {
	w := worldFrom(ctx)
	day, err := strconv.Atoi(matchDay)
	if err != nil {
		return stepFailed(ctx, err)
	}
	count, err := strconv.Atoi(tokens)
	if err != nil {
		return stepFailed(ctx, err)
	}
	priced, err := optionalOdds(odds)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if err := w.lockedRideWorld(ctx, player, team, day, count, priced); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

func rideWaitingOnDecision(ctx context.Context, team, matchDay, tokens string) (context.Context, error) {
	w := worldFrom(ctx)
	day, err := strconv.Atoi(matchDay)
	if err != nil {
		return stepFailed(ctx, err)
	}
	count, err := strconv.Atoi(tokens)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if err := w.waitingRideWorld(ctx, "Ana", team, day, count, nil); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

func rideThatWasBurned(ctx context.Context, team, matchDay string) (context.Context, error) {
	w := worldFrom(ctx)
	day, err := strconv.Atoi(matchDay)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if err := w.waitingRideWorld(ctx, "Ana", team, day, 10, nil); err != nil {
		return stepFailed(ctx, err)
	}
	if err := w.clockAfterLastMatch(ctx, 1, 2*time.Hour); err != nil {
		return stepFailed(ctx, err)
	}
	res, err := w.decide(ctx, "burn")
	if err != nil {
		return stepFailed(ctx, err)
	}
	if res.Status != 200 {
		return stepFailed(ctx, fmt.Errorf("burn the ride: %d %s", res.Status, res.message()))
	}
	return stepOK(ctx)
}

// rideInState builds a ride in the named state the way the product reaches it.
// "locked" is the lock's own state; "won_pending" is a win; "burned" and
// "unlocked" are decision arrivals after a win; "lost" is a losing result.
func rideInState(ctx context.Context, matchDay, state string) (context.Context, error) {
	w := worldFrom(ctx)
	day, err := strconv.Atoi(matchDay)
	if err != nil {
		return stepFailed(ctx, err)
	}
	matchID, ok := w.matchDays[day]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("match day %d was never built", day))
	}
	facts, err := w.matchFacts(ctx, matchID)
	if err != nil {
		return stepFailed(ctx, err)
	}
	home := w.teamName(facts.HomeTeamID)

	switch state {
	case "locked":
		if _, err := w.createLockedRide(ctx, "Ana", home, day, 10, nil); err != nil {
			return stepFailed(ctx, err)
		}
	case "won_pending":
		if err := w.waitingRideWorld(ctx, "Ana", home, day, 10, nil); err != nil {
			return stepFailed(ctx, err)
		}
	case "burned", "unlocked":
		if err := w.waitingRideWorld(ctx, "Ana", home, day, 10, nil); err != nil {
			return stepFailed(ctx, err)
		}
		if err := w.clockAfterLastMatch(ctx, 1, 2*time.Hour); err != nil {
			return stepFailed(ctx, err)
		}
		command := map[string]string{"burned": "burn", "unlocked": "unlock"}[state]
		res, err := w.decide(ctx, command)
		if err != nil {
			return stepFailed(ctx, err)
		}
		if res.Status != 200 {
			return stepFailed(ctx, fmt.Errorf("reach %q: %d %s", state, res.Status, res.message()))
		}
	case "lost":
		if _, err := w.createLockedRide(ctx, "Ana", home, day, 10, nil); err != nil {
			return stepFailed(ctx, err)
		}
		if err := w.startMatch(ctx, matchID); err != nil {
			return stepFailed(ctx, err)
		}
		// The away side wins, so the ride (on the home side) is lost.
		if err := w.resolveMatchDay(ctx, matchID, 0, 1); err != nil {
			return stepFailed(ctx, err)
		}
	default:
		return stepFailed(ctx, fmt.Errorf("state %q has no builder", state))
	}

	ride, err := w.currentRide(ctx)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if ride.State != state {
		return stepFailed(ctx, fmt.Errorf("built ride reads %q, the scenario needs %q", ride.State, state))
	}
	return stepOK(ctx)
}

func optionalOdds(odds string) (*float64, error) {
	if odds == "" {
		return nil, nil
	}
	value, err := strconv.ParseFloat(odds, 64)
	if err != nil {
		return nil, fmt.Errorf("odds %q: %w", odds, err)
	}
	return &value, nil
}

// --- results ---------------------------------------------------------------

func matchDayEnds(ctx context.Context, matchDay, score, _ string) (context.Context, error) {
	w := worldFrom(ctx)
	day, err := strconv.Atoi(matchDay)
	if err != nil {
		return stepFailed(ctx, err)
	}
	matchID, ok := w.matchDays[day]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("match day %d was never built", day))
	}
	home, away, err := parseScore(score)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if err := w.resolveMatchDay(ctx, matchID, home, away); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

// parseScore reads the feature's scoreline ("2–1", en dash included).
func parseScore(text string) (home, away int, err error) {
	parts := strings.FieldsFunc(text, func(r rune) bool { return r < '0' || r > '9' })
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("score %q is not a two-sided scoreline", text)
	}
	if home, err = strconv.Atoi(parts[0]); err != nil {
		return 0, 0, err
	}
	if away, err = strconv.Atoi(parts[1]); err != nil {
		return 0, 0, err
	}
	return home, away, nil
}

// --- assertions ------------------------------------------------------------

func rideIsLocked(ctx context.Context) (context.Context, error) {
	return rideIs("locked")(ctx)
}

func rideIs(state string) func(context.Context) (context.Context, error) {
	return func(ctx context.Context) (context.Context, error) {
		w := worldFrom(ctx)
		ride, err := w.currentRide(ctx)
		if err != nil {
			return stepFailed(ctx, err)
		}
		if ride.State != state {
			return stepFailed(ctx, fmt.Errorf("the ride reads %q, want %q", ride.State, state))
		}
		return stepOK(ctx)
	}
}

func rideIsStillLocked(ctx context.Context) (context.Context, error) {
	return rideIs("locked")(ctx)
}

func rideIsLockedOnMatchDay(ctx context.Context, matchDay string) (context.Context, error) {
	w := worldFrom(ctx)
	day, err := strconv.Atoi(matchDay)
	if err != nil {
		return stepFailed(ctx, err)
	}
	ride, err := w.currentRide(ctx)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if ride.State != "locked" {
		return stepFailed(ctx, fmt.Errorf("the ride reads %q, want %q", ride.State, "locked"))
	}
	want, ok := w.matchDays[day]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("match day %d was never built", day))
	}
	if ride.MatchID != want {
		return stepFailed(ctx, fmt.Errorf("the ride is on match %d, want match day %d (%d)", ride.MatchID, day, want))
	}
	return stepOK(ctx)
}

func rideWaitsOnDecision(ctx context.Context) (context.Context, error) {
	return rideIs("won_pending")(ctx)
}

func rideStillWaitsOnDecision(ctx context.Context) (context.Context, error) {
	return rideIs("won_pending")(ctx)
}

func rideStillReads(ctx context.Context, state string) (context.Context, error) {
	return rideIs(state)(ctx)
}

func rideCarriesStake(ctx context.Context, tokens string) (context.Context, error) {
	return rideCarriesStakeOf(ctx, tokens)
}

func rideStillCarriesStake(ctx context.Context, tokens string) (context.Context, error) {
	return rideCarriesStakeOf(ctx, tokens)
}

func rideCarriesStakeOf(ctx context.Context, tokens string) (context.Context, error) {
	w := worldFrom(ctx)
	want, err := strconv.Atoi(tokens)
	if err != nil {
		return stepFailed(ctx, err)
	}
	ride, err := w.currentRide(ctx)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if ride.Tokens != int64(want) {
		return stepFailed(ctx, fmt.Errorf("the ride carries %d tokens, want %d", ride.Tokens, want))
	}
	return stepOK(ctx)
}

func rideCarriesBasePrice(ctx context.Context) (context.Context, error) {
	w := worldFrom(ctx)
	ride, err := w.currentRide(ctx)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if !ride.Base.GreaterThan(decimal.Zero) {
		return stepFailed(ctx, fmt.Errorf("the ride carries base price %s, want a captured price", ride.Base))
	}
	return stepOK(ctx)
}

func rideHasNoStreakAndNoBonus(ctx context.Context) (context.Context, error) {
	w := worldFrom(ctx)
	ride, err := w.currentRide(ctx)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if ride.Streak != 0 {
		return stepFailed(ctx, fmt.Errorf("the ride banked a streak of %d, want none", ride.Streak))
	}
	if !ride.Bonus.IsZero() {
		return stepFailed(ctx, fmt.Errorf("the ride accrued a bonus of %s, want none", ride.Bonus))
	}
	return stepOK(ctx)
}

func rideHasBankedOneWin(ctx context.Context) (context.Context, error) {
	w := worldFrom(ctx)
	ride, err := w.currentRide(ctx)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if ride.Streak != 1 {
		return stepFailed(ctx, fmt.Errorf("the ride banked %d wins, want one", ride.Streak))
	}
	return stepOK(ctx)
}

func rideHasAccruedBonus(ctx context.Context) (context.Context, error) {
	w := worldFrom(ctx)
	ride, err := w.currentRide(ctx)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if !ride.Bonus.GreaterThan(decimal.Zero) {
		return stepFailed(ctx, fmt.Errorf("the ride accrued no bonus"))
	}
	return stepOK(ctx)
}

func noRideExists(ctx context.Context, player, matchDay string) (context.Context, error) {
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
	var count int
	if err := w.app.Owner.QueryRow(ctx,
		`SELECT count(*) FROM game.rides WHERE player_id = $1 AND match_id = $2`,
		playerID, matchID).Scan(&count); err != nil {
		return stepFailed(ctx, err)
	}
	if count != 0 {
		return stepFailed(ctx, fmt.Errorf("%d ride(s) exist for %q on match day %d, want none", count, player, day))
	}
	return stepOK(ctx)
}

func rideWasNotCreated(ctx context.Context) (context.Context, error) {
	w := worldFrom(ctx)
	var count int
	if err := w.app.Owner.QueryRow(ctx, `SELECT count(*) FROM game.rides`).Scan(&count); err != nil {
		return stepFailed(ctx, err)
	}
	if count != 0 {
		return stepFailed(ctx, fmt.Errorf("%d ride(s) exist, want none", count))
	}
	return stepOK(ctx)
}

// --- refusals --------------------------------------------------------------

// The named refusals the game module's transport distinguishes for a ride
// command (internal/game/internal/adapter/inbound/http/errors.go). The feature
// names the business refusal; the message is what tells them apart on the wire.
const (
	illegalTransitionMessage = "this action is not allowed for a ride in current state"
	wrongPhaseMessage        = "this ride command is not allowed while the round is in current phase"
	roundNotOpenMessage      = "ride commands require an open round"
)

func commandIsRefused(ctx context.Context) (context.Context, error) {
	w := worldFrom(ctx)
	if w.last == nil {
		return stepFailed(ctx, fmt.Errorf("no command was issued"))
	}
	if w.last.Status < 400 {
		return stepFailed(ctx, fmt.Errorf("the command was allowed (%d %s), want a refusal",
			w.last.Status, w.last.message()))
	}
	return stepOK(ctx)
}

func commandRefusedAsIllegalTransition(ctx context.Context) (context.Context, error) {
	return commandRefusedAs(ctx, illegalTransitionMessage)
}

func commandRefusedAsWrongPhase(ctx context.Context) (context.Context, error) {
	return commandRefusedAs(ctx, wrongPhaseMessage)
}

func commandRefusedAsRoundNotOpen(ctx context.Context) (context.Context, error) {
	return commandRefusedAs(ctx, roundNotOpenMessage)
}

func commandRefusedAs(ctx context.Context, message string) (context.Context, error) {
	w := worldFrom(ctx)
	if w.last == nil {
		return stepFailed(ctx, fmt.Errorf("no command was issued"))
	}
	if w.last.Status != 409 || w.last.message() != message {
		return stepFailed(ctx, fmt.Errorf("the command was answered %d %q, want the refusal %q",
			w.last.Status, w.last.message(), message))
	}
	return stepOK(ctx)
}

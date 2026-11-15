package spec

// The settlement steps: the round's committed results, the bonus the ride
// accrues, the round completion's sweep and the settlement's own refusals.

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/cucumber/godog"
	"github.com/shopspring/decimal"

	"lt-api.aleksrdvn.com/internal/schedule"
)

func registerSettlementSteps(sc *godog.ScenarioContext) {
	// Money and streak facts on the ride.
	sc.Step(`^the ride has accrued a bonus of ([0-9.]+)$`, rideHasAccruedBonusOf)
	sc.Step(`^the ride keeps its bonus of ([0-9.]+)$`, rideKeepsBonusOf)
	sc.Step(`^the ride's bonus is still ([0-9.]+)$`, rideBonusStillIs)
	sc.Step(`^the ride carries no bonus$`, rideCarriesNoBonus)
	sc.Step(`^the ride waits on a decision with a bonus of ([0-9.]+)$`, rideWaitsWithBonus)
	sc.Step(`^the ride's team is priced at ([0-9.]+)$`, rideTeamPricedAt)
	sc.Step(`^the ride has banked (no earlier win|one earlier win)$`, rideHasBanked)

	// Committed results: with settlement ("ends") and without it ("ended").
	sc.Step(`^its match ends "([^"]+)"(?: to "([^"]+)")?$`, itsMatchEnds)
	sc.Step(`^match day ([0-9]+) ended "([^"]+)"(?: to "([^"]+)")?(?: without reaching settlement)?$`, matchDayEnded)
	sc.Step(`^the result of match day ([0-9]+) arrives a second time$`, resultArrivesSecondTime)

	// Round completion and the sweep.
	sc.Step(`^round ([0-9]+) completes$`, roundCompletes)
	sc.Step(`^the season's final round holds a match day for "([^"]+)"$`, finalRoundHoldsMatchDay)
	sc.Step(`^a ride waiting on a decision for "([^"]+)" in that match day with ([0-9]+) tokens$`, rideWaitingInFinalMatchDay)
	sc.Step(`^the season's final round completes$`, finalRoundCompletes)

	// The settlement's agreement refusal.
	sc.Step(`^the settlement reports match day ([0-9]+) as a win for "([^"]+)"$`, settlementReportsWin)
	sc.Step(`^the settlement reports match day ([0-9]+)'s odds for "([^"]+)" as ([0-9.]+)$`, settlementReportsOdds)
	sc.Step(`^the settlement is refused as an illegal transition$`, settlementRefusedAsIllegalTransition)
	sc.Step(`^the ride on "([^"]+)" waits on a decision$`, rideOnTeamWaitsOnDecision)
	sc.Step(`^the ride on "([^"]+)" is lost$`, rideOnTeamIsLost)
	sc.Step(`^the refusal is unnamed for the caller$`, refusalUnnamedForCaller)
}

// --- money and streak ------------------------------------------------------

func rideHasAccruedBonusOf(ctx context.Context, amount string) (context.Context, error) {
	return rideBonusIs(ctx, amount, "accrued")
}

func rideKeepsBonusOf(ctx context.Context, amount string) (context.Context, error) {
	return rideBonusIs(ctx, amount, "kept")
}

func rideBonusStillIs(ctx context.Context, amount string) (context.Context, error) {
	return rideBonusIs(ctx, amount, "still carries")
}

func rideBonusIs(ctx context.Context, amount, verb string) (context.Context, error) {
	w := worldFrom(ctx)
	want, err := decimal.NewFromString(amount)
	if err != nil {
		return stepFailed(ctx, fmt.Errorf("bonus %q: %w", amount, err))
	}
	ride, err := w.currentRide(ctx)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if !ride.Bonus.Equal(want) {
		return stepFailed(ctx, fmt.Errorf("the ride %s a bonus of %s, want %s", verb, ride.Bonus, want))
	}
	return stepOK(ctx)
}

func rideCarriesNoBonus(ctx context.Context) (context.Context, error) {
	w := worldFrom(ctx)
	ride, err := w.currentRide(ctx)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if !ride.Bonus.IsZero() {
		return stepFailed(ctx, fmt.Errorf("the ride carries a bonus of %s, want none", ride.Bonus))
	}
	return stepOK(ctx)
}

func rideWaitsWithBonus(ctx context.Context, amount string) (context.Context, error) {
	w := worldFrom(ctx)
	want, err := decimal.NewFromString(amount)
	if err != nil {
		return stepFailed(ctx, fmt.Errorf("bonus %q: %w", amount, err))
	}
	ride, err := w.currentRide(ctx)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if ride.State != "won_pending" {
		return stepFailed(ctx, fmt.Errorf("the ride reads %q, want %q", ride.State, "won_pending"))
	}
	if !ride.Bonus.Equal(want) {
		return stepFailed(ctx, fmt.Errorf("the ride accrued a bonus of %s, want %s", ride.Bonus, want))
	}
	return stepOK(ctx)
}

func rideTeamPricedAt(ctx context.Context, odds string) (context.Context, error) {
	w := worldFrom(ctx)
	priced, err := strconv.ParseFloat(odds, 64)
	if err != nil {
		return stepFailed(ctx, fmt.Errorf("odds %q: %w", odds, err))
	}
	ride, err := w.currentRide(ctx)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if err := w.priceTeam(ctx, ride.MatchID, w.teamName(ride.TeamID), priced); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

// rideHasBanked sets the ride's banked-wins counter. The counter is fixture
// state here: reaching it through the product needs the ride's whole prior
// round (a won ride continued onto this match), and the rule under test is the
// bonus arithmetic the streak scales — not the continuation that produced it
// (R-GAME-025 proves that one).
func rideHasBanked(ctx context.Context, banked string) (context.Context, error) {
	w := worldFrom(ctx)
	streak := 0
	if strings.HasPrefix(banked, "one") {
		streak = 1
	}
	ride, err := w.currentRide(ctx)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if err := w.exec(ctx, `UPDATE game.rides SET streak = $2 WHERE id = $1`, ride.ID, streak); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

// --- committed results -----------------------------------------------------

func itsMatchEnds(ctx context.Context, score, _ string) (context.Context, error) {
	w := worldFrom(ctx)
	ride, err := w.currentRide(ctx)
	if err != nil {
		return stepFailed(ctx, err)
	}
	home, away, err := parseScore(score)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if err := w.resolveMatchDay(ctx, ride.MatchID, home, away); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

// matchDayEnded commits the result WITHOUT settlement — the accepted gap the
// scenarios build their worlds in (the ride stays locked; the committed match is
// the fact the settlement is judged against).
func matchDayEnded(ctx context.Context, matchDay, score, _ string) (context.Context, error) {
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
	if err := w.closeMatch(ctx, matchID, home, away, 30*time.Minute); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

// resultArrivesSecondTime delivers the same result payload twice. The first
// delivery settles the match's locked rides; the second carries the same event
// id, so the settlement's claim makes it a no-op — the rule the scenario states.
func resultArrivesSecondTime(ctx context.Context, matchDay string) (context.Context, error) {
	w := worldFrom(ctx)
	day, err := strconv.Atoi(matchDay)
	if err != nil {
		return stepFailed(ctx, err)
	}
	matchID, ok := w.matchDays[day]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("match day %d was never built", day))
	}
	event, err := w.committedEvent(ctx, matchID)
	if err != nil {
		return stepFailed(ctx, err)
	}

	if err := w.app.publishResult(ctx, event); err != nil {
		return stepFailed(ctx, fmt.Errorf("deliver the result: %w", err))
	}
	if err := w.app.publishResult(ctx, event); err != nil {
		return stepFailed(ctx, fmt.Errorf("deliver the result a second time: %w", err))
	}
	return stepOK(ctx)
}

// committedEvent builds the payload the calendar would publish for a committed
// match — the same facts, a fresh event id.
func (w *world) committedEvent(ctx context.Context, matchID int) (schedule.MatchResultRecorded, error) {
	facts, err := w.matchFacts(ctx, matchID)
	if err != nil {
		return schedule.MatchResultRecorded{}, err
	}
	home, away := facts.HomeScore, facts.AwayScore

	var winner *int
	switch {
	case home > away:
		winner = &facts.HomeTeamID
	case away > home:
		winner = &facts.AwayTeamID
	}

	return schedule.MatchResultRecorded{
		EventID:      schedule.NewEventID(),
		MatchID:      facts.ID,
		RoundID:      facts.RoundID,
		SeasonID:     facts.SeasonID,
		HomeTeamID:   facts.HomeTeamID,
		AwayTeamID:   facts.AwayTeamID,
		Score:        schedule.Score{Home: &home, Away: &away},
		Odds:         schedule.Odds{Home: facts.HomeOdds, Away: facts.AwayOdds},
		WinnerTeamID: winner,
		EndedAt:      now(),
	}, nil
}

// --- round completion ------------------------------------------------------

func roundCompletes(ctx context.Context, roundNo string) (context.Context, error) {
	w := worldFrom(ctx)
	round, err := strconv.Atoi(roundNo)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if err := w.completeRound(ctx, round); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

func finalRoundHoldsMatchDay(ctx context.Context, team string) (context.Context, error) {
	w := worldFrom(ctx)
	if err := w.finalRoundWorld(ctx, team); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

func rideWaitingInFinalMatchDay(ctx context.Context, team, tokens string) (context.Context, error) {
	w := worldFrom(ctx)
	count, err := strconv.Atoi(tokens)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if w.finalMatchID == 0 {
		return stepFailed(ctx, fmt.Errorf("the season's final round has no match day yet"))
	}

	res, err := w.lockOnMatch(ctx, "Ana", team, w.finalMatchID, count)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if res.Status != 201 {
		return stepFailed(ctx, fmt.Errorf("lock in the final round: %d %s", res.Status, res.message()))
	}

	if err := w.startMatch(ctx, w.finalMatchID); err != nil {
		return stepFailed(ctx, err)
	}
	home, away, err := w.scoreFor(ctx, w.finalMatchID, team)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if err := w.resolveMatchDay(ctx, w.finalMatchID, home, away); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

func finalRoundCompletes(ctx context.Context) (context.Context, error) {
	w := worldFrom(ctx)
	if w.finalRoundID == 0 {
		return stepFailed(ctx, fmt.Errorf("the season's final round was never built"))
	}

	// Every match of the round must be settled before the close gate admits the
	// flip; the scenario's own match already is.
	if err := w.exec(ctx, `
		UPDATE schedule.matches SET ended_at = now() - interval '2 hours' WHERE round_id = $1
	`, w.finalRoundID); err != nil {
		return stepFailed(ctx, err)
	}

	res, err := w.app.call("PATCH",
		fmt.Sprintf("/v1/seasons/%d/rounds/%d", w.finalSeasonID, w.finalRoundID),
		map[string]any{"status": "closed"}, nil)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if res.Status != 200 {
		return stepFailed(ctx, fmt.Errorf("close the final round: %d %s", res.Status, res.message()))
	}
	return stepOK(ctx)
}

// --- the settlement's agreement refusal ------------------------------------

// settlementReportsWin delivers a payload that crowns the wrong team: the
// settlement must refuse it against the committed result.
func settlementReportsWin(ctx context.Context, matchDay, team string) (context.Context, error) {
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
	claimed, ok := w.teams[team]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("team %q was never built", team))
	}

	home, away := facts.HomeScore, facts.AwayScore
	event := schedule.MatchResultRecorded{
		EventID:      schedule.NewEventID(),
		MatchID:      facts.ID,
		RoundID:      facts.RoundID,
		SeasonID:     facts.SeasonID,
		HomeTeamID:   facts.HomeTeamID,
		AwayTeamID:   facts.AwayTeamID,
		Score:        schedule.Score{Home: &home, Away: &away},
		Odds:         schedule.Odds{Home: facts.HomeOdds, Away: facts.AwayOdds},
		WinnerTeamID: &claimed,
		EndedAt:      now(),
	}
	w.lastEventErr = w.app.publishResult(ctx, event)
	return stepOK(ctx)
}

// settlementReportsOdds delivers the committed result with one claim rewritten:
// the claimed team's rate, and nothing else. The destination agrees with the
// committed scores, so only the odds half of the agreement check can refuse it.
func settlementReportsOdds(ctx context.Context, matchDay, team, odds string) (context.Context, error) {
	w := worldFrom(ctx)
	day, err := strconv.Atoi(matchDay)
	if err != nil {
		return stepFailed(ctx, err)
	}
	matchID, ok := w.matchDays[day]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("match day %d was never built", day))
	}
	event, err := w.committedEvent(ctx, matchID)
	if err != nil {
		return stepFailed(ctx, err)
	}
	claimedTeam, ok := w.teams[team]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("team %q was never built", team))
	}
	claimed, err := strconv.ParseFloat(odds, 64)
	if err != nil {
		return stepFailed(ctx, fmt.Errorf("odds %q: %w", odds, err))
	}

	// Only the claimed side's rate moves; the other side keeps the committed
	// figure, so the refusal is about the reported rate, not a wholesale
	// payload the settlement could dismiss as a translation bug.
	switch claimedTeam {
	case event.HomeTeamID:
		event.Odds.Home = claimed
	case event.AwayTeamID:
		event.Odds.Away = claimed
	default:
		return stepFailed(ctx, fmt.Errorf("team %q plays no side of match day %d", team, day))
	}

	w.lastEventErr = w.app.publishResult(ctx, event)
	return stepOK(ctx)
}

func settlementRefusedAsIllegalTransition(ctx context.Context) (context.Context, error) {
	w := worldFrom(ctx)
	if w.lastEventErr == nil {
		return stepFailed(ctx, fmt.Errorf("the settlement accepted the payload, want an illegal-transition refusal"))
	}
	// The ride domain's ErrInvalidTransition is the refusal the whole ride
	// surface reports as the illegal transition; the game package does not
	// re-export it (only ErrNotFound and ErrRecordInUse cross the facade), so
	// the refusal is matched by the sentinel's own text.
	if !strings.Contains(w.lastEventErr.Error(), "invalid state transition") {
		return stepFailed(ctx, fmt.Errorf("the settlement refused with %q, want the illegal transition", w.lastEventErr))
	}
	return stepOK(ctx)
}

func rideOnTeamWaitsOnDecision(ctx context.Context, team string) (context.Context, error) {
	w := worldFrom(ctx)
	ride, err := w.rideForTeam(ctx, team)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if ride.State != "won_pending" {
		return stepFailed(ctx, fmt.Errorf("the ride on %q reads %q, want %q", team, ride.State, "won_pending"))
	}
	return stepOK(ctx)
}

func rideOnTeamIsLost(ctx context.Context, team string) (context.Context, error) {
	w := worldFrom(ctx)
	ride, err := w.rideForTeam(ctx, team)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if ride.State != "lost" {
		return stepFailed(ctx, fmt.Errorf("the ride on %q reads %q, want %q", team, ride.State, "lost"))
	}
	return stepOK(ctx)
}

// refusalUnnamedForCaller asserts the refusal is the opaque one: not one of the
// ride's named refusals, and not a field-level explanation either — the caller
// is told only that the server failed (the known-defect scenarios' subject).
func refusalUnnamedForCaller(ctx context.Context) (context.Context, error) {
	w := worldFrom(ctx)
	if w.last == nil {
		return stepFailed(ctx, fmt.Errorf("no command was issued"))
	}

	message := w.last.message()
	if w.last.Status == 409 && (message == illegalTransitionMessage ||
		message == wrongPhaseMessage || message == roundNotOpenMessage) {
		return stepFailed(ctx, fmt.Errorf("the refusal is named (%q), want the unnamed one", message))
	}
	if w.last.Status != 500 {
		return stepFailed(ctx, fmt.Errorf("the refusal is not opaque: %d %s", w.last.Status, message))
	}
	return stepOK(ctx)
}

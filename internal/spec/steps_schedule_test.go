package spec

// The calendar steps: the schedule module's own route table as the facade
// (style guide §7, family A) and the calendar's gates as sabotage subjects
// (§8, family B).
//
// The four schedule features build a world the calendar itself owns — a
// season, its rounds, the clubs, the fixtures — so where the pilot features
// plant those rows, these drive the routes the organiser's screen drives. The
// ride-side steps the resolve feature reuses (the lock, the phase windows, the
// burn) are the pilot's: the world maps round/matchDay/team/player are the
// same, so a calendar-built world and a fixture-built one are
// indistinguishable to them.

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/cucumber/godog"
)

func registerScheduleSteps(sc *godog.ScenarioContext) {
	// The world, built through the calendar (Background).
	sc.Step(`^a season is on the calendar$`, aSeasonIsOnTheCalendar)
	sc.Step(`^another season is on the calendar$`, anotherSeasonIsOnTheCalendar)
	sc.Step(`^round ([0-9]+) is on the calendar$`, roundIsOnTheCalendar)
	sc.Step(`^the teams "([^"]+)", "([^"]+)" and "([^"]+)" are on the calendar$`, teamsAreOnTheCalendar)
	sc.Step(`^the season's fixture list runs to thirty-eight rounds$`, fixtureListRunsToThirtyEightRounds)
	sc.Step(`^round ([0-9]+) is open$`, roundIsOpen)
	sc.Step(`^the organiser has declared the season open$`, organiserDeclaresSeasonOpen)
	sc.Step(`^rounds ([0-9]+) to ([0-9]+) have opened and closed in order$`, roundsOpenedAndClosedInOrder)

	// Registrations, as the game context holds them.
	sc.Step(`^the player "([^"]+)" is registered in the season with ([0-9]+) tokens$`, playerRegisteredInSeason)
	sc.Step(`^the player "([^"]+)" is registered in the season favouring "([^"]+)"$`, playerRegisteredFavouring)
	sc.Step(`^the player "([^"]+)" is still registered in the season$`, playerStillRegistered)
	sc.Step(`^the player "([^"]+)" is no longer registered in the season$`, playerNoLongerRegistered)

	// The clock states a fixture moves through.
	sc.Step(`^match day ([0-9]+) has kicked off$`, matchDayHasKickedOff)
	sc.Step(`^match day ([0-9]+) is under way$`, matchDayIsUnderWay)
	sc.Step(`^match day ([0-9]+) has been played out "([^"]+)"$`, matchDayHasBeenPlayedOut)
	sc.Step(`^match day ([0-9]+) was announced in round ([0-9]+) afterwards and is under way$`, matchDayAnnouncedAfterwardsAndUnderWay)

	// The organiser's commands.
	sc.Step(`^the organiser opens round ([0-9]+)$`, organiserOpensRound)
	sc.Step(`^the organiser closes round ([0-9]+)$`, organiserClosesRound)
	sc.Step(`^the organiser has closed round ([0-9]+)$`, organiserHasClosedRound)
	sc.Step(`^the organiser adds round ([0-9]+) to the season$`, organiserAddsRoundToSeason)
	sc.Step(`^the organiser adds round ([0-9]+) to the other season$`, organiserAddsRoundToOtherSeason)
	sc.Step(`^the organiser schedules match day ([0-9]+) in round ([0-9]+): "([^"]+)" against "([^"]+)"(?: kicking off (.+))?$`, organiserSchedulesMatchDay)
	sc.Step(`^the organiser postpones match day ([0-9]+)$`, organiserPostponesMatchDay)
	sc.Step(`^the organiser corrects match day ([0-9]+)'s kickoff to three hours ago$`, organiserCorrectsKickoff)
	sc.Step(`^the organiser corrects match day ([0-9]+)'s score to "([^"]+)"$`, organiserCorrectsScore)
	sc.Step(`^the organiser prices match day ([0-9]+) at ([0-9.]+) for the home side$`, organiserPricesMatchDay)
	sc.Step(`^the organiser resolves match day ([0-9]+) as "([^"]+)" ending (in an hour|at this instant|ten minutes ago|twenty minutes ago)$`, organiserResolvesMatchDay)
	sc.Step(`^another edit to match day ([0-9]+) has landed since the organiser read it$`, anotherEditToMatchDayLanded)
	sc.Step(`^the organiser deletes round ([0-9]+)$`, organiserDeletesRound)
	sc.Step(`^the organiser deletes the season$`, organiserDeletesSeason)
	sc.Step(`^the organiser deletes match day ([0-9]+)$`, organiserDeletesMatchDay)
	sc.Step(`^the organiser deletes the team "([^"]+)"$`, organiserDeletesTeam)

	// The team edits and their stale readings.
	sc.Step(`^the organiser has read the team "([^"]+)"$`, organiserReadsTeam)
	sc.Step(`^the organiser re-reads the team "([^"]+)"$`, organiserReadsTeam)
	sc.Step(`^another editor has since changed the description of the team "([^"]+)"$`, anotherEditorChangesTeam)
	sc.Step(`^the organiser saves a new description for the team "([^"]+)"$`, organiserSavesTeamDescription)
	sc.Step(`^the team "([^"]+)" keeps the other editor's description$`, teamKeepsOtherDescription)
	sc.Step(`^the team "([^"]+)" carries the organiser's description$`, teamCarriesOrganiserDescription)

	// The calendar's reads.
	sc.Step(`^match day ([0-9]+) is on the calendar$`, matchDayIsOnTheCalendar)
	sc.Step(`^match day ([0-9]+) is still on the calendar$`, matchDayIsOnTheCalendar)
	sc.Step(`^match day ([0-9]+) is no longer on the calendar$`, matchDayIsNoLongerOnTheCalendar)
	sc.Step(`^the calendar holds no match day ([0-9]+)$`, calendarHoldsNoMatchDay)
	sc.Step(`^match day ([0-9]+) reads ([a-z_ ]+)$`, matchDayReadsStatus)
	sc.Step(`^match day ([0-9]+) still reads ([a-z_ ]+)$`, matchDayReadsStatus)
	sc.Step(`^match day ([0-9]+) reads "([^"]+)"$`, matchDayReadsScore)
	sc.Step(`^match day ([0-9]+) keeps its score of "([^"]+)"$`, matchDayReadsScore)
	sc.Step(`^match day ([0-9]+) keeps a home price of ([0-9.]+)$`, matchDayKeepsHomePrice)
	sc.Step(`^match day ([0-9]+) is announced to kick off three hours ago$`, matchDayAnnouncedThreeHoursAgo)
	sc.Step(`^match day ([0-9]+) records the calendar's own end$`, matchDayRecordsCalendarEnd)
	sc.Step(`^match day ([0-9]+) keeps the end it was resolved with$`, matchDayKeepsResolvedEnd)
	sc.Step(`^round ([0-9]+) reads ([a-z_ ]+)$`, roundReadsStatus)
	sc.Step(`^round ([0-9]+) still reads ([a-z_ ]+)$`, roundReadsStatus)
	sc.Step(`^round ([0-9]+) is still on the calendar$`, roundIsStillOnTheCalendar)
	sc.Step(`^round ([0-9]+) is no longer on the calendar$`, roundIsNoLongerOnTheCalendar)
	sc.Step(`^the season holds one round ([0-9]+)$`, seasonHoldsOneRound)
	sc.Step(`^the other season holds a round ([0-9]+)$`, otherSeasonHoldsRound)
	sc.Step(`^the season reads ([a-z_ ]+)$`, seasonReadsStatus)
	sc.Step(`^the season still reads ([a-z_ ]+)$`, seasonReadsStatus)
	sc.Step(`^the season is still on the calendar$`, seasonIsStillOnTheCalendar)
	sc.Step(`^the season is no longer on the calendar$`, seasonIsNoLongerOnTheCalendar)
	sc.Step(`^the team "([^"]+)" is still on the calendar$`, teamIsStillOnTheCalendar)
	sc.Step(`^the team "([^"]+)" is no longer on the calendar$`, teamIsNoLongerOnTheCalendar)

	// The calendar's own refusals.
	sc.Step(`^the command is refused as (a kickoff that has already arrived|an end that has not happened yet|a price that is not above evens|a round number already taken|a stale reading|a started match|the same team on both sides)$`, commandRefusedAsCalendarRefusal)

	// Family B: the Owner pool's direct schedule writes.
	sc.Step(`^the Owner pool tries to send the closed match day ([0-9]+) back to "([^"]+)"$`, ownerSendsClosedMatchBackToState)
	sc.Step(`^the Owner pool tries to send round ([0-9]+) back to "([^"]+)"$`, ownerSendsRoundBackToState)
	sc.Step(`^the Owner pool tries to send the season back to "([^"]+)"$`, ownerSendsSeasonBackToState)
	sc.Step(`^the Owner pool rewrites the closed match day ([0-9]+) without changing its status$`, ownerRewritesClosedMatchWithoutTransition)
	sc.Step(`^the Owner pool tries to set (the season|round [0-9]+|match day [0-9]+) to the status "([^"]+)"$`, ownerSetsRecordToStatus)
	sc.Step(`^the Owner pool tries to postpone match day ([0-9]+)$`, ownerPostponesMatchDay)
	sc.Step(`^the Owner pool tries to open round ([0-9]+)$`, ownerOpensRound)
	sc.Step(`^the Owner pool tries to close round ([0-9]+) with match day ([0-9]+) still running$`, ownerClosesRoundOverRunningMatch)
	sc.Step(`^the Owner pool tries to store a match day that is (.+)$`, ownerStoresMatchDayShape)
	sc.Step(`^the Owner pool tries to close match day ([0-9]+) without an end time$`, ownerClosesMatchDayWithoutEnd)
	sc.Step(`^the Owner pool tries to price match day ([0-9]+) at ([0-9.]+) for the (home|away) side$`, ownerPricesMatchDay)
	sc.Step(`^the Owner pool tries to store a match day with "([^"]+)" on both sides$`, ownerStoresMatchDayWithTeamOnBothSides)
	sc.Step(`^the Owner pool tries to move match day ([0-9]+) into another season$`, ownerMovesMatchDayIntoAnotherSeason)
	sc.Step(`^the Owner pool tries to create a round numbered ([0-9]+)$`, ownerCreatesRoundNumbered)
	sc.Step(`^the Owner pool tries to duplicate round ([0-9]+) in the season$`, ownerDuplicatesRoundInSeason)
	sc.Step(`^the Owner pool tries to delete round ([0-9]+)$`, ownerDeletesRound)
	sc.Step(`^the Owner pool tries to delete the season$`, ownerDeletesSeason)
}

// --- the world -------------------------------------------------------------

func aSeasonIsOnTheCalendar(ctx context.Context) (context.Context, error) {
	w := worldFrom(ctx)
	seasonID, err := w.createSeason(ctx)
	if err != nil {
		return stepFailed(ctx, err)
	}
	w.seasonID = seasonID
	return stepOK(ctx)
}

func anotherSeasonIsOnTheCalendar(ctx context.Context) (context.Context, error) {
	w := worldFrom(ctx)
	seasonID, err := w.createSeason(ctx)
	if err != nil {
		return stepFailed(ctx, err)
	}
	w.otherSeasonID = seasonID
	return stepOK(ctx)
}

func roundIsOnTheCalendar(ctx context.Context, roundNo string) (context.Context, error) {
	w := worldFrom(ctx)
	number, err := strconv.Atoi(roundNo)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if w.seasonID == 0 {
		return stepFailed(ctx, fmt.Errorf("no season built: round %d needs it", number))
	}
	round, err := w.createRound(ctx, w.seasonID, number)
	if err != nil {
		return stepFailed(ctx, err)
	}
	w.rounds[number] = round.ID
	return stepOK(ctx)
}

func teamsAreOnTheCalendar(ctx context.Context, first, second, third string) (context.Context, error) {
	w := worldFrom(ctx)
	for _, name := range []string{first, second, third} {
		if _, ok := w.teams[name]; ok {
			continue
		}
		teamID, err := w.createTeam(ctx, name)
		if err != nil {
			return stepFailed(ctx, err)
		}
		w.teams[name] = teamID
		w.teamOrder = append(w.teamOrder, name)
	}
	return stepOK(ctx)
}

func fixtureListRunsToThirtyEightRounds(ctx context.Context) (context.Context, error) {
	w := worldFrom(ctx)
	const roundsPerSeason = 38
	for number := 1; number <= roundsPerSeason; number++ {
		if _, ok := w.rounds[number]; ok {
			continue
		}
		round, err := w.createRound(ctx, w.seasonID, number)
		if err != nil {
			return stepFailed(ctx, err)
		}
		w.rounds[number] = round.ID
	}
	return stepOK(ctx)
}

// roundIsOpen is the C4 author's phrasing: the season is declared open and then
// the round opens — a created season cannot host an open round
// (round_progress_season_not_live), so the two moves are one step.
func roundIsOpen(ctx context.Context, roundNo string) (context.Context, error) {
	w := worldFrom(ctx)
	number, err := strconv.Atoi(roundNo)
	if err != nil {
		return stepFailed(ctx, err)
	}
	roundID, ok := w.rounds[number]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("round %d was never built", number))
	}
	if err := w.openRound(ctx, w.seasonID, roundID); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

func organiserDeclaresSeasonOpen(ctx context.Context) (context.Context, error) {
	w := worldFrom(ctx)
	res, err := w.patchSeason(ctx, w.seasonID, "open", nil)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if err := calendarAccepted(res, "declare the season open"); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

func roundsOpenedAndClosedInOrder(ctx context.Context, first, last string) (context.Context, error) {
	w := worldFrom(ctx)
	from, err := strconv.Atoi(first)
	if err != nil {
		return stepFailed(ctx, err)
	}
	to, err := strconv.Atoi(last)
	if err != nil {
		return stepFailed(ctx, err)
	}

	// The first round of a fresh season needs a live season under it.
	season, status, err := w.readSeason(ctx, w.seasonID)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if status != 200 {
		return stepFailed(ctx, fmt.Errorf("read the season: %d", status))
	}
	if season.Status == "created" {
		res, err := w.patchSeason(ctx, w.seasonID, "open", nil)
		if err != nil {
			return stepFailed(ctx, err)
		}
		if err := calendarAccepted(res, "declare the season open"); err != nil {
			return stepFailed(ctx, err)
		}
	}

	for number := from; number <= to; number++ {
		roundID, ok := w.rounds[number]
		if !ok {
			return stepFailed(ctx, fmt.Errorf("round %d was never built", number))
		}
		res, err := w.patchRound(ctx, w.seasonID, roundID, map[string]any{"status": "open"}, nil)
		if err != nil {
			return stepFailed(ctx, err)
		}
		if err := calendarAccepted(res, fmt.Sprintf("open round %d", number)); err != nil {
			return stepFailed(ctx, err)
		}
		res, err = w.patchRound(ctx, w.seasonID, roundID, map[string]any{"status": "closed"}, nil)
		if err != nil {
			return stepFailed(ctx, err)
		}
		if err := calendarAccepted(res, fmt.Sprintf("close round %d", number)); err != nil {
			return stepFailed(ctx, err)
		}
	}
	return stepOK(ctx)
}

// --- registrations ---------------------------------------------------------

func playerRegisteredInSeason(ctx context.Context, player, _ string) (context.Context, error) {
	w := worldFrom(ctx)
	if err := w.registerPlayer(ctx, player); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

func playerRegisteredFavouring(ctx context.Context, player, team string) (context.Context, error) {
	w := worldFrom(ctx)
	if err := w.registerPlayerFavouring(ctx, player, team); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

func playerStillRegistered(ctx context.Context, player string) (context.Context, error) {
	return playerRegistrationCount(ctx, player, 1)
}

func playerNoLongerRegistered(ctx context.Context, player string) (context.Context, error) {
	return playerRegistrationCount(ctx, player, 0)
}

func playerRegistrationCount(ctx context.Context, player string, want int) (context.Context, error) {
	w := worldFrom(ctx)
	count, err := w.queryInt(ctx,
		`SELECT count(*) FROM game.players WHERE season_id = $1 AND name = $2`, w.seasonID, player)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if count != want {
		return stepFailed(ctx, fmt.Errorf("%q holds %d registration(s) in the season, want %d", player, count, want))
	}
	return stepOK(ctx)
}

// --- the clock states ------------------------------------------------------

func matchDayHasKickedOff(ctx context.Context, matchDay string) (context.Context, error) {
	w := worldFrom(ctx)
	day, err := strconv.Atoi(matchDay)
	if err != nil {
		return stepFailed(ctx, err)
	}
	matchID, err := w.ensureMatchDay(ctx, day)
	if err != nil {
		return stepFailed(ctx, err)
	}
	// The kickoff has passed while the fixture still reads created: the time
	// fact the started-freeze gate keys on (R-SCHEDULE-005). A created match
	// rewrites its own future kickoff without tripping the gate — the gate
	// reads the *stored* start, which is still ahead at this instant.
	if err := w.exec(ctx,
		`UPDATE schedule.matches SET starts_at = now() - interval '10 minutes' WHERE id = $1`, matchID); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

func matchDayIsUnderWay(ctx context.Context, matchDay string) (context.Context, error) {
	w := worldFrom(ctx)
	day, err := strconv.Atoi(matchDay)
	if err != nil {
		return stepFailed(ctx, err)
	}
	matchID, err := w.ensureMatchDay(ctx, day)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if err := w.startMatch(ctx, matchID); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

func matchDayHasBeenPlayedOut(ctx context.Context, matchDay, score string) (context.Context, error) {
	w := worldFrom(ctx)
	day, err := strconv.Atoi(matchDay)
	if err != nil {
		return stepFailed(ctx, err)
	}
	home, away, err := parseScore(score)
	if err != nil {
		return stepFailed(ctx, err)
	}
	matchID, err := w.ensureMatchDay(ctx, day)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if err := w.closeMatch(ctx, matchID, home, away, 30*time.Minute); err != nil {
		return stepFailed(ctx, err)
	}
	return rememberMatchEnd(ctx, w, matchID)
}

// rememberMatchEnd keeps the end the calendar stamped, so a later assertion can
// prove the end was written once and kept.
func rememberMatchEnd(ctx context.Context, w *world, matchID int) (context.Context, error) {
	match, status, err := w.readMatch(ctx, matchID)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if status != 200 || match.EndedAt == nil {
		return stepFailed(ctx, fmt.Errorf("match %d carries no end to remember (%d)", matchID, status))
	}
	w.lastMatchEnd = *match.EndedAt
	return stepOK(ctx)
}

func matchDayAnnouncedAfterwardsAndUnderWay(ctx context.Context, matchDay, roundNo string) (context.Context, error) {
	w := worldFrom(ctx)
	day, err := strconv.Atoi(matchDay)
	if err != nil {
		return stepFailed(ctx, err)
	}
	number, err := strconv.Atoi(roundNo)
	if err != nil {
		return stepFailed(ctx, err)
	}
	roundID, ok := w.rounds[number]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("round %d was never built", number))
	}
	res, err := w.announceMatchDay(ctx, w.seasonID, day, roundID,
		w.teams["Team A"], w.teams["Team B"], now().Add(4*time.Hour))
	if err != nil {
		return stepFailed(ctx, err)
	}
	// A fixture announced in a round that already closed is the accepted gap
	// the scenario builds: only the close transition is gated on the fixtures.
	if err := calendarAccepted(res, fmt.Sprintf("announce match day %d", day)); err != nil {
		return stepFailed(ctx, err)
	}
	if err := w.startMatch(ctx, w.matchDays[day]); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

// --- the organiser's commands ---------------------------------------------

func organiserOpensRound(ctx context.Context, roundNo string) (context.Context, error) {
	w := worldFrom(ctx)
	number, err := strconv.Atoi(roundNo)
	if err != nil {
		return stepFailed(ctx, err)
	}
	roundID, ok := w.rounds[number]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("round %d was never built", number))
	}
	res, err := w.patchRound(ctx, w.seasonID, roundID, map[string]any{"status": "open"}, nil)
	if err != nil {
		return stepFailed(ctx, err)
	}
	w.last = res
	return stepOK(ctx)
}

func organiserClosesRound(ctx context.Context, roundNo string) (context.Context, error) {
	w := worldFrom(ctx)
	number, err := strconv.Atoi(roundNo)
	if err != nil {
		return stepFailed(ctx, err)
	}
	roundID, ok := w.rounds[number]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("round %d was never built", number))
	}
	res, err := w.patchRound(ctx, w.seasonID, roundID, map[string]any{"status": "closed"}, nil)
	if err != nil {
		return stepFailed(ctx, err)
	}
	w.last = res
	return stepOK(ctx)
}

func organiserHasClosedRound(ctx context.Context, roundNo string) (context.Context, error) {
	w := worldFrom(ctx)
	number, err := strconv.Atoi(roundNo)
	if err != nil {
		return stepFailed(ctx, err)
	}
	roundID, ok := w.rounds[number]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("round %d was never built", number))
	}
	if err := w.closeRound(ctx, w.seasonID, roundID); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

func organiserAddsRoundToSeason(ctx context.Context, roundNo string) (context.Context, error) {
	w := worldFrom(ctx)
	number, err := strconv.Atoi(roundNo)
	if err != nil {
		return stepFailed(ctx, err)
	}
	res, err := w.calendar("POST", fmt.Sprintf("/v1/seasons/%d/rounds", w.seasonID),
		map[string]any{"number": number}, nil)
	if err != nil {
		return stepFailed(ctx, err)
	}
	w.last = res
	return stepOK(ctx)
}

func organiserAddsRoundToOtherSeason(ctx context.Context, roundNo string) (context.Context, error) {
	w := worldFrom(ctx)
	number, err := strconv.Atoi(roundNo)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if w.otherSeasonID == 0 {
		return stepFailed(ctx, fmt.Errorf("no second season was built"))
	}
	res, err := w.calendar("POST", fmt.Sprintf("/v1/seasons/%d/rounds", w.otherSeasonID),
		map[string]any{"number": number}, nil)
	if err != nil {
		return stepFailed(ctx, err)
	}
	w.last = res
	if res.Status == 201 {
		var round roundView
		if err := decodeEnvelope(res, "round", &round); err != nil {
			return stepFailed(ctx, err)
		}
		w.otherRounds[number] = round.ID
	}
	return stepOK(ctx)
}

func organiserSchedulesMatchDay(ctx context.Context, matchDay, roundNo, home, away, when string) (context.Context, error) {
	w := worldFrom(ctx)
	day, err := strconv.Atoi(matchDay)
	if err != nil {
		return stepFailed(ctx, err)
	}
	number, err := strconv.Atoi(roundNo)
	if err != nil {
		return stepFailed(ctx, err)
	}
	roundID, ok := w.rounds[number]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("round %d was never built", number))
	}
	homeID, ok := w.teams[home]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("team %q was never built", home))
	}
	awayID, ok := w.teams[away]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("team %q was never built", away))
	}

	startsAt := now().Add(2 * time.Hour)
	if when != "" {
		startsAt, err = kickoffInstant(when)
		if err != nil {
			return stepFailed(ctx, err)
		}
	}

	res, err := w.announceMatchDay(ctx, w.seasonID, day, roundID, homeID, awayID, startsAt)
	if err != nil {
		return stepFailed(ctx, err)
	}
	w.last = res
	return stepOK(ctx)
}

// kickoffInstant reads the feature's clock phrase for an announcement.
func kickoffInstant(phrase string) (time.Time, error) {
	switch phrase {
	case "an hour ago":
		return now().Add(-time.Hour), nil
	case "at this instant":
		// The calendar reads its own clock a moment after the body is built, so
		// an instant taken here is already behind it — which is what "this
		// instant has arrived" means for the rule.
		return now(), nil
	case "in two hours":
		return now().Add(2 * time.Hour), nil
	default:
		return time.Time{}, fmt.Errorf("no kickoff instant for %q", phrase)
	}
}

func organiserPostponesMatchDay(ctx context.Context, matchDay string) (context.Context, error) {
	w := worldFrom(ctx)
	day, err := strconv.Atoi(matchDay)
	if err != nil {
		return stepFailed(ctx, err)
	}
	matchID, ok := w.matchDays[day]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("match day %d was never built", day))
	}
	res, err := w.patchMatch(ctx, w.seasonID, matchID, map[string]any{"status": "postponed"}, nil)
	if err != nil {
		return stepFailed(ctx, err)
	}
	w.last = res
	return stepOK(ctx)
}

func organiserCorrectsKickoff(ctx context.Context, matchDay string) (context.Context, error) {
	w := worldFrom(ctx)
	day, err := strconv.Atoi(matchDay)
	if err != nil {
		return stepFailed(ctx, err)
	}
	matchID, ok := w.matchDays[day]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("match day %d was never built", day))
	}
	res, err := w.patchMatch(ctx, w.seasonID, matchID,
		map[string]any{"starts_at": now().Add(-3 * time.Hour).UTC()}, nil)
	if err != nil {
		return stepFailed(ctx, err)
	}
	w.last = res
	return stepOK(ctx)
}

func organiserCorrectsScore(ctx context.Context, matchDay, score string) (context.Context, error) {
	w := worldFrom(ctx)
	day, err := strconv.Atoi(matchDay)
	if err != nil {
		return stepFailed(ctx, err)
	}
	home, away, err := parseScore(score)
	if err != nil {
		return stepFailed(ctx, err)
	}
	matchID, ok := w.matchDays[day]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("match day %d was never built", day))
	}
	res, err := w.patchMatch(ctx, w.seasonID, matchID,
		map[string]any{"score": map[string]any{"home": home, "away": away}}, nil)
	if err != nil {
		return stepFailed(ctx, err)
	}
	w.last = res
	return stepOK(ctx)
}

func organiserPricesMatchDay(ctx context.Context, matchDay, price string) (context.Context, error) {
	w := worldFrom(ctx)
	day, err := strconv.Atoi(matchDay)
	if err != nil {
		return stepFailed(ctx, err)
	}
	value, err := strconv.ParseFloat(price, 64)
	if err != nil {
		return stepFailed(ctx, fmt.Errorf("price %q: %w", price, err))
	}
	matchID, ok := w.matchDays[day]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("match day %d was never built", day))
	}
	res, err := w.patchMatch(ctx, w.seasonID, matchID,
		map[string]any{"odds": map[string]any{"home": value, "away": 2.50}}, nil)
	if err != nil {
		return stepFailed(ctx, err)
	}
	w.last = res
	return stepOK(ctx)
}

// organiserResolvesMatchDay is the calendar's resolve: the scoreline, the end
// the organiser observed and the version the organiser held.
func organiserResolvesMatchDay(ctx context.Context, matchDay, score, when string) (context.Context, error) {
	w := worldFrom(ctx)
	day, err := strconv.Atoi(matchDay)
	if err != nil {
		return stepFailed(ctx, err)
	}
	home, away, err := parseScore(score)
	if err != nil {
		return stepFailed(ctx, err)
	}
	end, err := resolveEnd(when)
	if err != nil {
		return stepFailed(ctx, err)
	}
	matchID, ok := w.matchDays[day]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("match day %d was never built", day))
	}

	version, ok := w.matchReadVersion[matchID]
	if !ok {
		match, status, err := w.readMatch(ctx, matchID)
		if err != nil {
			return stepFailed(ctx, err)
		}
		if status != 200 {
			return stepFailed(ctx, fmt.Errorf("read match day %d: %d", day, status))
		}
		version = match.Version
	}

	res, err := w.calendar("PATCH",
		fmt.Sprintf("/v1/seasons/%d/matches/%d/resolve", w.seasonID, matchID),
		map[string]any{
			"ended_at": end.UTC(),
			"score":    map[string]any{"home": home, "away": away},
			"version":  version,
		}, nil)
	if err != nil {
		return stepFailed(ctx, err)
	}
	w.last = res
	if res.Status == 200 {
		w.lastMatchEnd = end
	}
	return stepOK(ctx)
}

// resolveEnd reads the feature's clock phrase for a result's end time.
func resolveEnd(phrase string) (time.Time, error) {
	switch phrase {
	case "in an hour":
		return now().Add(time.Hour), nil
	case "at this instant":
		// The resolve use case compares the end against its own clock a moment
		// after this body is built, so "this instant" is pinned a hair ahead: the
		// boundary the scenario proves is "an end that has not happened yet",
		// not scheduling jitter.
		return now().Add(10 * time.Millisecond), nil
	case "ten minutes ago":
		return now().Add(-10 * time.Minute), nil
	case "twenty minutes ago":
		return now().Add(-20 * time.Minute), nil
	default:
		return time.Time{}, fmt.Errorf("no end instant for %q", phrase)
	}
}

func anotherEditToMatchDayLanded(ctx context.Context, matchDay string) (context.Context, error) {
	w := worldFrom(ctx)
	day, err := strconv.Atoi(matchDay)
	if err != nil {
		return stepFailed(ctx, err)
	}
	matchID, ok := w.matchDays[day]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("match day %d was never built", day))
	}
	read, status, err := w.readMatch(ctx, matchID)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if status != 200 {
		return stepFailed(ctx, fmt.Errorf("read match day %d: %d", day, status))
	}
	// The version the organiser holds; the next resolve is written against it.
	w.matchReadVersion[matchID] = read.Version

	res, err := w.patchMatch(ctx, w.seasonID, matchID,
		map[string]any{"odds": map[string]any{"home": 1.90, "away": 2.60}}, nil)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if err := calendarAccepted(res, fmt.Sprintf("another edit to match day %d", day)); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

func organiserDeletesRound(ctx context.Context, roundNo string) (context.Context, error) {
	w := worldFrom(ctx)
	number, err := strconv.Atoi(roundNo)
	if err != nil {
		return stepFailed(ctx, err)
	}
	roundID, ok := w.rounds[number]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("round %d was never built", number))
	}
	res, err := w.calendar("DELETE", fmt.Sprintf("/v1/seasons/%d/rounds/%d", w.seasonID, roundID), nil, nil)
	if err != nil {
		return stepFailed(ctx, err)
	}
	w.last = res
	return stepOK(ctx)
}

func organiserDeletesSeason(ctx context.Context) (context.Context, error) {
	w := worldFrom(ctx)
	res, err := w.calendar("DELETE", fmt.Sprintf("/v1/seasons/%d", w.seasonID), nil, nil)
	if err != nil {
		return stepFailed(ctx, err)
	}
	w.last = res
	return stepOK(ctx)
}

func organiserDeletesMatchDay(ctx context.Context, matchDay string) (context.Context, error) {
	w := worldFrom(ctx)
	day, err := strconv.Atoi(matchDay)
	if err != nil {
		return stepFailed(ctx, err)
	}
	matchID, ok := w.matchDays[day]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("match day %d was never built", day))
	}
	res, err := w.calendar("DELETE",
		fmt.Sprintf("/v1/seasons/%d/matches/%d", w.seasonID, matchID), nil, nil)
	if err != nil {
		return stepFailed(ctx, err)
	}
	w.last = res
	return stepOK(ctx)
}

func organiserDeletesTeam(ctx context.Context, team string) (context.Context, error) {
	w := worldFrom(ctx)
	teamID, ok := w.teams[team]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("team %q was never built", team))
	}
	res, err := w.calendar("DELETE", fmt.Sprintf("/v1/teams/%d", teamID), nil, nil)
	if err != nil {
		return stepFailed(ctx, err)
	}
	w.last = res
	return stepOK(ctx)
}

// --- the team edits --------------------------------------------------------

// The two descriptions the stale-reading scenarios read back: the other
// editor's rewrite, and the one the organiser supplies afterwards.
const (
	otherEditorDescription = "a description the other editor wrote"
	organiserDescription   = "a description the organiser wrote"
)

// organiserReadsTeam records the version the organiser's screen is showing.
func organiserReadsTeam(ctx context.Context, team string) (context.Context, error) {
	w := worldFrom(ctx)
	teamID, ok := w.teams[team]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("team %q was never built", team))
	}
	read, status, err := w.readTeam(ctx, teamID)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if status != 200 {
		return stepFailed(ctx, fmt.Errorf("read team %q: %d", team, status))
	}
	w.teamReadVersion[team] = read.Version
	return stepOK(ctx)
}

func anotherEditorChangesTeam(ctx context.Context, team string) (context.Context, error) {
	w := worldFrom(ctx)
	teamID, ok := w.teams[team]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("team %q was never built", team))
	}
	res, err := w.calendar("PATCH", fmt.Sprintf("/v1/teams/%d", teamID),
		map[string]any{"description": otherEditorDescription}, nil)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if err := calendarAccepted(res, "change the team's description"); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

func organiserSavesTeamDescription(ctx context.Context, team string) (context.Context, error) {
	w := worldFrom(ctx)
	teamID, ok := w.teams[team]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("team %q was never built", team))
	}
	headers := map[string]string{}
	if version, ok := w.teamReadVersion[team]; ok {
		headers["X-Expected-Version"] = strconv.Itoa(version)
	}
	res, err := w.calendar("PATCH", fmt.Sprintf("/v1/teams/%d", teamID),
		map[string]any{"description": organiserDescription}, headers)
	if err != nil {
		return stepFailed(ctx, err)
	}
	w.last = res
	return stepOK(ctx)
}

func teamKeepsOtherDescription(ctx context.Context, team string) (context.Context, error) {
	return teamDescriptionIs(ctx, team, otherEditorDescription, "keeps the other editor's description")
}

func teamCarriesOrganiserDescription(ctx context.Context, team string) (context.Context, error) {
	return teamDescriptionIs(ctx, team, organiserDescription, "carries the organiser's description")
}

func teamDescriptionIs(ctx context.Context, team, want, claim string) (context.Context, error) {
	w := worldFrom(ctx)
	teamID, ok := w.teams[team]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("team %q was never built", team))
	}
	read, status, err := w.readTeam(ctx, teamID)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if status != 200 {
		return stepFailed(ctx, fmt.Errorf("read team %q: %d", team, status))
	}
	if read.Description != want {
		return stepFailed(ctx, fmt.Errorf("the team %q %s: it reads %q, want %q",
			team, claim, read.Description, want))
	}
	return stepOK(ctx)
}

// --- the calendar's reads --------------------------------------------------

func matchDayIsOnTheCalendar(ctx context.Context, matchDay string) (context.Context, error) {
	w := worldFrom(ctx)
	day, err := strconv.Atoi(matchDay)
	if err != nil {
		return stepFailed(ctx, err)
	}
	matchID, ok := w.matchDays[day]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("match day %d was never announced", day))
	}
	_, status, err := w.readMatch(ctx, matchID)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if status != 200 {
		return stepFailed(ctx, fmt.Errorf("match day %d reads %d, want it on the calendar", day, status))
	}
	return stepOK(ctx)
}

func matchDayIsNoLongerOnTheCalendar(ctx context.Context, matchDay string) (context.Context, error) {
	w := worldFrom(ctx)
	day, err := strconv.Atoi(matchDay)
	if err != nil {
		return stepFailed(ctx, err)
	}
	matchID, ok := w.matchDays[day]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("match day %d was never announced", day))
	}
	_, status, err := w.readMatch(ctx, matchID)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if status != 404 {
		return stepFailed(ctx, fmt.Errorf("match day %d reads %d, want it gone", day, status))
	}
	return stepOK(ctx)
}

func calendarHoldsNoMatchDay(ctx context.Context, matchDay string) (context.Context, error) {
	w := worldFrom(ctx)
	day, err := strconv.Atoi(matchDay)
	if err != nil {
		return stepFailed(ctx, err)
	}
	count, err := w.countMatchesInSeason(ctx, w.seasonID)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if count != 0 {
		return stepFailed(ctx, fmt.Errorf("the calendar holds %d fixture(s), want no match day %d", count, day))
	}
	return stepOK(ctx)
}

func matchDayReadsStatus(ctx context.Context, matchDay, want string) (context.Context, error) {
	w := worldFrom(ctx)
	day, err := strconv.Atoi(matchDay)
	if err != nil {
		return stepFailed(ctx, err)
	}
	matchID, ok := w.matchDays[day]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("match day %d was never built", day))
	}
	match, status, err := w.readMatch(ctx, matchID)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if status != 200 {
		return stepFailed(ctx, fmt.Errorf("match day %d reads %d, want status %q", day, status, statusWord(want)))
	}
	if match.Status != statusWord(want) {
		return stepFailed(ctx, fmt.Errorf("match day %d reads %q, want %q", day, match.Status, statusWord(want)))
	}
	return stepOK(ctx)
}

func matchDayReadsScore(ctx context.Context, matchDay, want string) (context.Context, error) {
	w := worldFrom(ctx)
	day, err := strconv.Atoi(matchDay)
	if err != nil {
		return stepFailed(ctx, err)
	}
	home, away, err := parseScore(want)
	if err != nil {
		return stepFailed(ctx, err)
	}
	matchID, ok := w.matchDays[day]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("match day %d was never built", day))
	}
	match, status, err := w.readMatch(ctx, matchID)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if status != 200 {
		return stepFailed(ctx, fmt.Errorf("match day %d reads %d, want the score %s", day, status, want))
	}
	if match.Score.Home == nil || match.Score.Away == nil {
		return stepFailed(ctx, fmt.Errorf("match day %d carries no score, want %s", day, want))
	}
	if *match.Score.Home != home || *match.Score.Away != away {
		return stepFailed(ctx, fmt.Errorf("match day %d reads %d-%d, want %s",
			day, *match.Score.Home, *match.Score.Away, want))
	}
	return stepOK(ctx)
}

func matchDayKeepsHomePrice(ctx context.Context, matchDay, price string) (context.Context, error) {
	w := worldFrom(ctx)
	day, err := strconv.Atoi(matchDay)
	if err != nil {
		return stepFailed(ctx, err)
	}
	want, err := strconv.ParseFloat(price, 64)
	if err != nil {
		return stepFailed(ctx, fmt.Errorf("price %q: %w", price, err))
	}
	matchID, ok := w.matchDays[day]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("match day %d was never built", day))
	}
	match, status, err := w.readMatch(ctx, matchID)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if status != 200 {
		return stepFailed(ctx, fmt.Errorf("match day %d reads %d, want the home price %s", day, status, price))
	}
	if match.Odds.Home != want {
		return stepFailed(ctx, fmt.Errorf("match day %d carries a home price of %v, want %v", day, match.Odds.Home, want))
	}
	return stepOK(ctx)
}

func matchDayAnnouncedThreeHoursAgo(ctx context.Context, matchDay string) (context.Context, error) {
	w := worldFrom(ctx)
	day, err := strconv.Atoi(matchDay)
	if err != nil {
		return stepFailed(ctx, err)
	}
	matchID, ok := w.matchDays[day]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("match day %d was never built", day))
	}
	match, status, err := w.readMatch(ctx, matchID)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if status != 200 {
		return stepFailed(ctx, fmt.Errorf("match day %d reads %d, want its corrected kickoff", day, status))
	}
	if delta := now().Sub(match.StartsAt); delta < 170*time.Minute || delta > 190*time.Minute {
		return stepFailed(ctx, fmt.Errorf("match day %d kicks off %s from now, want about three hours ago",
			day, delta.Round(time.Minute)))
	}
	return stepOK(ctx)
}

func matchDayRecordsCalendarEnd(ctx context.Context, matchDay string) (context.Context, error) {
	w := worldFrom(ctx)
	day, err := strconv.Atoi(matchDay)
	if err != nil {
		return stepFailed(ctx, err)
	}
	matchID, ok := w.matchDays[day]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("match day %d was never built", day))
	}
	match, status, err := w.readMatch(ctx, matchID)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if status != 200 || match.EndedAt == nil {
		return stepFailed(ctx, fmt.Errorf("match day %d carries no end (%d), want the calendar's own stamp", day, status))
	}
	if delta := now().Sub(*match.EndedAt); delta < -time.Minute || delta > time.Minute {
		return stepFailed(ctx, fmt.Errorf("match day %d ended %s from now, want the calendar's own clock",
			day, delta.Round(time.Second)))
	}
	return stepOK(ctx)
}

func matchDayKeepsResolvedEnd(ctx context.Context, matchDay string) (context.Context, error) {
	w := worldFrom(ctx)
	day, err := strconv.Atoi(matchDay)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if w.lastMatchEnd.IsZero() {
		return stepFailed(ctx, fmt.Errorf("no end was recorded for match day %d to keep", day))
	}
	matchID, ok := w.matchDays[day]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("match day %d was never built", day))
	}
	match, status, err := w.readMatch(ctx, matchID)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if status != 200 || match.EndedAt == nil {
		return stepFailed(ctx, fmt.Errorf("match day %d carries no end (%d), want the one it was given", day, status))
	}
	// The calendar stores whole seconds, so the comparison allows the cast.
	if delta := match.EndedAt.Sub(w.lastMatchEnd); delta > time.Second || delta < -time.Second {
		return stepFailed(ctx, fmt.Errorf("match day %d ended at %s, want the recorded %s",
			day, match.EndedAt.UTC(), w.lastMatchEnd.UTC()))
	}
	return stepOK(ctx)
}

func roundReadsStatus(ctx context.Context, roundNo, want string) (context.Context, error) {
	w := worldFrom(ctx)
	number, err := strconv.Atoi(roundNo)
	if err != nil {
		return stepFailed(ctx, err)
	}
	roundID, ok := w.rounds[number]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("round %d was never built", number))
	}
	round, status, err := w.readRound(ctx, roundID)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if status != 200 {
		return stepFailed(ctx, fmt.Errorf("round %d reads %d, want %q", number, status, statusWord(want)))
	}
	if round.Status != statusWord(want) {
		return stepFailed(ctx, fmt.Errorf("round %d reads %q, want %q", number, round.Status, statusWord(want)))
	}
	return stepOK(ctx)
}

func roundIsStillOnTheCalendar(ctx context.Context, roundNo string) (context.Context, error) {
	w := worldFrom(ctx)
	number, err := strconv.Atoi(roundNo)
	if err != nil {
		return stepFailed(ctx, err)
	}
	roundID, ok := w.rounds[number]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("round %d was never built", number))
	}
	_, status, err := w.readRound(ctx, roundID)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if status != 200 {
		return stepFailed(ctx, fmt.Errorf("round %d reads %d, want it on the calendar", number, status))
	}
	return stepOK(ctx)
}

func roundIsNoLongerOnTheCalendar(ctx context.Context, roundNo string) (context.Context, error) {
	w := worldFrom(ctx)
	number, err := strconv.Atoi(roundNo)
	if err != nil {
		return stepFailed(ctx, err)
	}
	roundID, ok := w.rounds[number]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("round %d was never built", number))
	}
	_, status, err := w.readRound(ctx, roundID)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if status != 404 {
		return stepFailed(ctx, fmt.Errorf("round %d reads %d, want it gone", number, status))
	}
	return stepOK(ctx)
}

func seasonHoldsOneRound(ctx context.Context, roundNo string) (context.Context, error) {
	w := worldFrom(ctx)
	number, err := strconv.Atoi(roundNo)
	if err != nil {
		return stepFailed(ctx, err)
	}
	count, err := w.queryInt(ctx,
		`SELECT count(*) FROM schedule.rounds WHERE season_id = $1 AND number = $2`, w.seasonID, number)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if count != 1 {
		return stepFailed(ctx, fmt.Errorf("the season holds %d round(s) numbered %d, want one", count, number))
	}
	return stepOK(ctx)
}

func otherSeasonHoldsRound(ctx context.Context, roundNo string) (context.Context, error) {
	w := worldFrom(ctx)
	number, err := strconv.Atoi(roundNo)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if w.otherSeasonID == 0 {
		return stepFailed(ctx, fmt.Errorf("no second season was built"))
	}
	roundID, ok := w.otherRounds[number]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("the other season holds no round %d", number))
	}
	round, status, err := w.readRound(ctx, roundID)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if status != 200 {
		return stepFailed(ctx, fmt.Errorf("the other season's round %d reads %d, want it on the calendar", number, status))
	}
	if round.SeasonID != w.otherSeasonID || round.Number != number {
		return stepFailed(ctx, fmt.Errorf("the other season holds round %d of season %d numbered %d",
			round.ID, round.SeasonID, round.Number))
	}
	return stepOK(ctx)
}

func seasonReadsStatus(ctx context.Context, want string) (context.Context, error) {
	w := worldFrom(ctx)
	season, status, err := w.readSeason(ctx, w.seasonID)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if status != 200 {
		return stepFailed(ctx, fmt.Errorf("the season reads %d, want %q", status, statusWord(want)))
	}
	if season.Status != statusWord(want) {
		return stepFailed(ctx, fmt.Errorf("the season reads %q, want %q", season.Status, statusWord(want)))
	}
	return stepOK(ctx)
}

func seasonIsStillOnTheCalendar(ctx context.Context) (context.Context, error) {
	w := worldFrom(ctx)
	_, status, err := w.readSeason(ctx, w.seasonID)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if status != 200 {
		return stepFailed(ctx, fmt.Errorf("the season reads %d, want it on the calendar", status))
	}
	return stepOK(ctx)
}

func seasonIsNoLongerOnTheCalendar(ctx context.Context) (context.Context, error) {
	w := worldFrom(ctx)
	_, status, err := w.readSeason(ctx, w.seasonID)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if status != 404 {
		return stepFailed(ctx, fmt.Errorf("the season reads %d, want it gone", status))
	}
	return stepOK(ctx)
}

func teamIsStillOnTheCalendar(ctx context.Context, team string) (context.Context, error) {
	w := worldFrom(ctx)
	teamID, ok := w.teams[team]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("team %q was never built", team))
	}
	_, status, err := w.readTeam(ctx, teamID)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if status != 200 {
		return stepFailed(ctx, fmt.Errorf("the team %q reads %d, want it on the calendar", team, status))
	}
	return stepOK(ctx)
}

func teamIsNoLongerOnTheCalendar(ctx context.Context, team string) (context.Context, error) {
	w := worldFrom(ctx)
	teamID, ok := w.teams[team]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("team %q was never built", team))
	}
	_, status, err := w.readTeam(ctx, teamID)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if status != 404 {
		return stepFailed(ctx, fmt.Errorf("the team %q reads %d, want it gone", team, status))
	}
	return stepOK(ctx)
}

// statusWord turns the feature's "in progress" into the stored value.
func statusWord(phrase string) string {
	return strings.ReplaceAll(strings.TrimSpace(phrase), " ", "_")
}

// --- the calendar's own refusals ------------------------------------------

// The three messages the calendar's conflict answers carry
// (internal/schedule/internal/handler/errors.go). A validation refusal is a
// field map instead, so those assert the field the organiser must fix.
const (
	duplicateRecordMessage = "a record with these unique values already exists"
	editConflictMessage    = "unable to update the record due to an edit conflict, please try again"
	frozenMessage          = "a match has already started, so the hierarchy cannot move to an earlier stage"
)

func commandRefusedAsCalendarRefusal(ctx context.Context, phrase string) (context.Context, error) {
	switch phrase {
	case "a kickoff that has already arrived":
		return refusedOnField(ctx, "starts_at")
	case "an end that has not happened yet":
		return refusedOnField(ctx, "ended_at")
	case "a price that is not above evens":
		return refusedOnField(ctx, "odds")
	case "the same team on both sides":
		return refusedOnField(ctx, "home_team_id")
	case "a round number already taken":
		return refusedWithMessage(ctx, duplicateRecordMessage)
	case "a stale reading":
		return refusedWithMessage(ctx, editConflictMessage)
	case "a started match":
		return refusedWithMessage(ctx, frozenMessage)
	default:
		return stepFailed(ctx, fmt.Errorf("no calendar refusal for %q", phrase))
	}
}

// refusedOnField asserts the calendar refused the body's field: a 422 whose
// answer names the field the organiser has to change.
func refusedOnField(ctx context.Context, field string) (context.Context, error) {
	w := worldFrom(ctx)
	if w.last == nil {
		return stepFailed(ctx, fmt.Errorf("no command was issued"))
	}
	if w.last.Status != 422 {
		return stepFailed(ctx, fmt.Errorf("the command was answered %d %q, want a refused %s",
			w.last.Status, w.last.message(), field))
	}
	if !strings.Contains(string(w.last.Body), `"`+field+`"`) {
		return stepFailed(ctx, fmt.Errorf("the refusal does not name %q: %s", field, w.last.message()))
	}
	return stepOK(ctx)
}

// refusedWithMessage asserts the calendar answered with one of its named
// conflicts.
func refusedWithMessage(ctx context.Context, message string) (context.Context, error) {
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

// --- family B: the Owner pool's direct schedule writes ---------------------

func matchDayID(w *world, matchDay string) (int, error) {
	day, err := strconv.Atoi(matchDay)
	if err != nil {
		return 0, err
	}
	matchID, ok := w.matchDays[day]
	if !ok {
		return 0, fmt.Errorf("match day %d was never built", day)
	}
	return matchID, nil
}

func ownerSendsClosedMatchBackToState(ctx context.Context, matchDay, state string) (context.Context, error) {
	w := worldFrom(ctx)
	matchID, err := matchDayID(w, matchDay)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if err := w.ownerWrite(ctx,
		`UPDATE schedule.matches SET status = $2 WHERE id = $1`, matchID, state); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

func ownerSendsRoundBackToState(ctx context.Context, roundNo, state string) (context.Context, error) {
	w := worldFrom(ctx)
	number, err := strconv.Atoi(roundNo)
	if err != nil {
		return stepFailed(ctx, err)
	}
	roundID, ok := w.rounds[number]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("round %d was never built", number))
	}
	if err := w.ownerWrite(ctx,
		`UPDATE schedule.rounds SET status = $2 WHERE id = $1`, roundID, state); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

func ownerSendsSeasonBackToState(ctx context.Context, state string) (context.Context, error) {
	w := worldFrom(ctx)
	if err := w.ownerWrite(ctx,
		`UPDATE schedule.seasons SET status = $2 WHERE id = $1`, w.seasonID, state); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

// ownerRewritesClosedMatchWithoutTransition is the carve-out: a full-row
// rewrite that keeps the status is not a transition, so the gate allows it.
func ownerRewritesClosedMatchWithoutTransition(ctx context.Context, matchDay string) (context.Context, error) {
	w := worldFrom(ctx)
	matchID, err := matchDayID(w, matchDay)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if err := w.ownerWrite(ctx, `
		UPDATE schedule.matches
		SET status = status, home_score = home_score, away_score = away_score, version = version + 1
		WHERE id = $1
	`, matchID); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

// ownerSetsRecordToStatus writes one status word the vocabulary does not carry
// onto whichever record the scenario names.
func ownerSetsRecordToStatus(ctx context.Context, record, status string) (context.Context, error) {
	w := worldFrom(ctx)
	switch {
	case record == "the season":
		if err := w.ownerWrite(ctx,
			`UPDATE schedule.seasons SET status = $2 WHERE id = $1`, w.seasonID, status); err != nil {
			return stepFailed(ctx, err)
		}
	case strings.HasPrefix(record, "round "):
		number, err := strconv.Atoi(strings.TrimPrefix(record, "round "))
		if err != nil {
			return stepFailed(ctx, err)
		}
		roundID, ok := w.rounds[number]
		if !ok {
			return stepFailed(ctx, fmt.Errorf("round %d was never built", number))
		}
		if err := w.ownerWrite(ctx,
			`UPDATE schedule.rounds SET status = $2 WHERE id = $1`, roundID, status); err != nil {
			return stepFailed(ctx, err)
		}
	case strings.HasPrefix(record, "match day "):
		matchID, err := matchDayID(w, strings.TrimPrefix(record, "match day "))
		if err != nil {
			return stepFailed(ctx, err)
		}
		if err := w.ownerWrite(ctx,
			`UPDATE schedule.matches SET status = $2 WHERE id = $1`, matchID, status); err != nil {
			return stepFailed(ctx, err)
		}
	default:
		return stepFailed(ctx, fmt.Errorf("no record %q in the calendar", record))
	}
	return stepOK(ctx)
}

func ownerPostponesMatchDay(ctx context.Context, matchDay string) (context.Context, error) {
	w := worldFrom(ctx)
	matchID, err := matchDayID(w, matchDay)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if err := w.ownerWrite(ctx,
		`UPDATE schedule.matches SET status = 'postponed' WHERE id = $1`, matchID); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

func ownerOpensRound(ctx context.Context, roundNo string) (context.Context, error) {
	w := worldFrom(ctx)
	number, err := strconv.Atoi(roundNo)
	if err != nil {
		return stepFailed(ctx, err)
	}
	roundID, ok := w.rounds[number]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("round %d was never built", number))
	}
	if err := w.ownerWrite(ctx,
		`UPDATE schedule.rounds SET status = 'open' WHERE id = $1`, roundID); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

func ownerClosesRoundOverRunningMatch(ctx context.Context, roundNo, matchDay string) (context.Context, error) {
	w := worldFrom(ctx)
	number, err := strconv.Atoi(roundNo)
	if err != nil {
		return stepFailed(ctx, err)
	}
	roundID, ok := w.rounds[number]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("round %d was never built", number))
	}
	if _, err := matchDayID(w, matchDay); err != nil {
		return stepFailed(ctx, err)
	}
	if err := w.ownerWrite(ctx,
		`UPDATE schedule.rounds SET status = 'closed' WHERE id = $1`, roundID); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

// matchShapeDraft is one forbidden match row: the lifecycle shape the scenario
// names, so exactly one CHECK can refuse it.
type matchShapeDraft struct {
	status   string
	home     *int
	away     *int
	startsAt time.Time
	endedAt  *time.Time
}

func score(home, away int) (*int, *int) { return &home, &away }

// matchShapeDraftFor maps the feature's shape onto the row that carries the
// violation (only the named CHECK is tripped).
func matchShapeDraftFor(shape string) (matchShapeDraft, error) {
	home2, away1 := score(2, 1)
	homeNeg, away2a := score(-1, 2)
	home2c, awayNeg := score(2, -2)
	future := now().Add(3 * time.Hour)
	past := now().Add(-3 * time.Hour)
	endedAt := now().Add(-time.Hour)

	switch shape {
	case "created with a full-time score of 2–1":
		return matchShapeDraft{status: "created", home: home2, away: away1, startsAt: future}, nil
	case "postponed with a full-time score of 2–1":
		return matchShapeDraft{status: "postponed", home: home2, away: away1, startsAt: future}, nil
	case "in progress with no score at all":
		return matchShapeDraft{status: "in_progress", startsAt: past}, nil
	case "in progress with only the home half of a score":
		return matchShapeDraft{status: "in_progress", home: home2, startsAt: past}, nil
	case "in progress with a home score of minus one":
		return matchShapeDraft{status: "in_progress", home: homeNeg, away: away2a, startsAt: past}, nil
	case "in progress with an away score of minus two":
		return matchShapeDraft{status: "in_progress", home: home2c, away: awayNeg, startsAt: past}, nil
	case "closed with no end at all":
		return matchShapeDraft{status: "closed", home: home2, away: away1, startsAt: past}, nil
	case "in progress with an end time":
		return matchShapeDraft{status: "in_progress", home: home2, away: away1, startsAt: past, endedAt: &endedAt}, nil
	case "closed with an end that falls before its kickoff":
		kickoff := now().Add(-time.Hour)
		endBefore := now().Add(-2 * time.Hour)
		return matchShapeDraft{status: "closed", home: home2, away: away1, startsAt: kickoff, endedAt: &endBefore}, nil
	default:
		return matchShapeDraft{}, fmt.Errorf("no match day shape for %q", shape)
	}
}

func ownerStoresMatchDayShape(ctx context.Context, shape string) (context.Context, error) {
	w := worldFrom(ctx)
	draft, err := matchShapeDraftFor(shape)
	if err != nil {
		return stepFailed(ctx, err)
	}
	roundID, ok := w.rounds[1]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("round 1 was never built"))
	}
	home, ok := w.teams["Team A"]
	if !ok {
		return stepFailed(ctx, fmt.Errorf(`team "Team A" was never built`))
	}
	away, ok := w.teams["Team B"]
	if !ok {
		return stepFailed(ctx, fmt.Errorf(`team "Team B" was never built`))
	}

	err = w.ownerWrite(ctx, `
		INSERT INTO schedule.matches
			(season_id, round_id, home_team_id, away_team_id, home_odds, away_odds,
			 home_score, away_score, status, starts_at, ended_at)
		VALUES ($1, $2, $3, $4, 1.80, 2.50, $5, $6, $7, $8, $9)
	`, w.seasonID, roundID, home, away, draft.home, draft.away, draft.status, draft.startsAt, draft.endedAt)
	if err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

func ownerClosesMatchDayWithoutEnd(ctx context.Context, matchDay string) (context.Context, error) {
	w := worldFrom(ctx)
	matchID, err := matchDayID(w, matchDay)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if err := w.ownerWrite(ctx, `
		UPDATE schedule.matches SET status = 'closed', home_score = 2, away_score = 1 WHERE id = $1
	`, matchID); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

func ownerPricesMatchDay(ctx context.Context, matchDay, price, side string) (context.Context, error) {
	w := worldFrom(ctx)
	matchID, err := matchDayID(w, matchDay)
	if err != nil {
		return stepFailed(ctx, err)
	}
	column := "home_odds"
	if side == "away" {
		column = "away_odds"
	}
	if err := w.ownerWrite(ctx,
		fmt.Sprintf(`UPDATE schedule.matches SET %s = $2 WHERE id = $1`, column), matchID, price); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

func ownerStoresMatchDayWithTeamOnBothSides(ctx context.Context, team string) (context.Context, error) {
	w := worldFrom(ctx)
	roundID, ok := w.rounds[1]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("round 1 was never built"))
	}
	teamID, ok := w.teams[team]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("team %q was never built", team))
	}
	err := w.ownerWrite(ctx, `
		INSERT INTO schedule.matches
			(season_id, round_id, home_team_id, away_team_id, home_odds, away_odds, starts_at, status)
		VALUES ($1, $2, $3, $3, 1.80, 2.50, $4, 'created')
	`, w.seasonID, roundID, teamID, now().Add(3*time.Hour))
	if err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

func ownerMovesMatchDayIntoAnotherSeason(ctx context.Context, matchDay string) (context.Context, error) {
	w := worldFrom(ctx)
	matchID, err := matchDayID(w, matchDay)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if w.otherSeasonID == 0 {
		return stepFailed(ctx, fmt.Errorf("no second season was built"))
	}
	if err := w.ownerWrite(ctx,
		`UPDATE schedule.matches SET season_id = $2 WHERE id = $1`, matchID, w.otherSeasonID); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

func ownerCreatesRoundNumbered(ctx context.Context, number string) (context.Context, error) {
	w := worldFrom(ctx)
	if err := w.ownerWrite(ctx,
		`INSERT INTO schedule.rounds (season_id, number) VALUES ($1, $2)`, w.seasonID, number); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

func ownerDuplicatesRoundInSeason(ctx context.Context, roundNo string) (context.Context, error) {
	w := worldFrom(ctx)
	if err := w.ownerWrite(ctx,
		`INSERT INTO schedule.rounds (season_id, number) VALUES ($1, $2)`, w.seasonID, roundNo); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

func ownerDeletesRound(ctx context.Context, roundNo string) (context.Context, error) {
	w := worldFrom(ctx)
	number, err := strconv.Atoi(roundNo)
	if err != nil {
		return stepFailed(ctx, err)
	}
	roundID, ok := w.rounds[number]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("round %d was never built", number))
	}
	if err := w.ownerWrite(ctx,
		`DELETE FROM schedule.rounds WHERE id = $1`, roundID); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

func ownerDeletesSeason(ctx context.Context) (context.Context, error) {
	w := worldFrom(ctx)
	if err := w.ownerWrite(ctx,
		`DELETE FROM schedule.seasons WHERE id = $1`, w.seasonID); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

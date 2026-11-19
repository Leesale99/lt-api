package spec

// The fixture DSL: the world a scenario runs in, built from the repository's
// own vocabulary (season, round, match day, team, player, ride) and driven
// through the modules' entry points wherever the product has one.
//
// World rows (season, rounds, teams, match days, players) are planted with the
// Owner pool: neither module exposes a create entry point for them, and no
// scenario here proves a calendar rule — the world is the backdrop, never the
// subject (see doc.go). Ride rows are always produced the way the product
// produces them.
//
// The clock is fixture state: a scenario says "3 hours before round 1's first
// match" by moving the round's match rows, and the product derives the phase
// from them, exactly as it does in production.

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/shopspring/decimal"

	"lt-api.aleksrdvn.com/internal/schedule"
)

// resetSQL empties the tables a scenario builds and restarts their identities,
// so every scenario addresses its own world by the ids it just created —
// scenarios are order-independent and nothing leaks between them.
//
// identity.users and identity.user_tokens are reset with the rest: the identity
// scenarios seed accounts through the facade, and game.players.user_id carries
// no foreign key (000016), so the two contexts empty independently. roles,
// permissions and roles_permissions are NOT touched — they are the seeded
// reference data (R-IDENTITY-014) every scenario reads.
const resetSQL = `
	TRUNCATE game.rides, game.players, game.idempotency_keys,
	         schedule.matches, schedule.rounds, schedule.seasons, schedule.teams,
	         identity.users, identity.user_tokens
	RESTART IDENTITY
`

// world is one scenario's built world plus the facts the steps thread between
// themselves.
type world struct {
	app *specApp

	seasonID   int
	seasonName string

	rounds    map[int]int // round number → round id
	teams     map[string]int
	teamOrder []string
	players   map[string]int
	matchDays map[int]int // match day → match id

	// otherSeasonID is the second season the cross-season scenarios build;
	// otherRounds maps its round numbers the way rounds does for the world's own
	// season.
	otherSeasonID int
	otherRounds   map[int]int

	// lastMatchEnd is the end instant a closed match was given ("has been
	// played out", a resolve), so a later assertion can prove the calendar kept
	// it. matchReadVersion is the version the organiser read before another
	// writer moved the row — the version a stale edit is written against.
	lastMatchEnd     time.Time
	matchReadVersion map[int]int
	teamReadVersion  map[string]int

	// finalSeason is the separately built season whose last round (38) the
	// final-round scenarios use.
	finalSeasonID int
	finalRoundID  int
	finalMatchID  int

	rides    []int // in creation order
	lastRide int

	// --- identity (steps_identity_test.go) --------------------------------
	//
	// members are the accounts a scenario signs in as, keyed by the name the
	// feature gives them; every one of them was seeded through the facade
	// (register → activation mail → activate), never written by hand.
	members    map[string]*member
	lastSignIn *signIn

	// listedCodes is the permission matrix the last "the standard user role
	// carries:" table listed, so "carries no other code" can compare the role's
	// real set against it.
	listedCodes []string

	// mailAsked is the address the last "asks for a fresh activation mail" step
	// asked about, and mailCount the number of mails held for it just before the
	// request ("a fresh activation token arrives in her mail" compares against
	// the count, "no activation mail arrives" against the same count).
	mailAsked string
	mailCount int

	// teamsRecorded is the number of teams on the calendar just before a page
	// tried to record one ("no team was recorded").
	teamsRecorded int

	// --- registrations (steps_players_test.go) -----------------------------
	//
	// ownedPlayer is the registration the players feature makes through the
	// create route as the signed-in member named after her, and ownedOwner is
	// that member — the principal the row's user_id carries (R-GAME-012).
	// lastPlayer is the registration the scenario's last step created or acted
	// on ("the registration …" assertions read it), and playerSeen is the
	// version the scenario's reader last saw ("the version as it read when she
	// last looked").
	ownedPlayer string
	ownedOwner  *member
	lastPlayer  string
	playerSeen  map[string]int
	owners      map[int]*int

	// --- idempotency (steps_players_test.go) -------------------------------
	//
	// keyed is the answer each idempotency key recorded — the first execution's
	// answer, which a retry under the same key must come back with byte for
	// byte — and lastKey is the key the scenario's last command travelled under.
	keyed   map[string]wireResponse
	lastKey string

	// last is the most recent module answer a "the command is refused" step
	// reads.
	last *wireResponse

	lastPgError  *pgconn.PgError
	lastEventErr error
}

type worldKey struct{}

func worldFrom(ctx context.Context) *world {
	w, _ := ctx.Value(worldKey{}).(*world)
	return w
}

// newWorld wipes the database and returns the empty world a scenario starts
// from.
func newWorld(ctx context.Context) (*world, error) {
	if app == nil {
		return nil, fmt.Errorf("spec application is not composed")
	}
	if _, err := app.Owner.Exec(ctx, resetSQL); err != nil {
		return nil, fmt.Errorf("reset the spec database: %w", err)
	}
	// The held mail is the per-scenario world's other half: a scenario reads
	// only what its own run of the service mailed.
	app.Mail.reset()

	return &world{
		app:              app,
		rounds:           map[int]int{},
		otherRounds:      map[int]int{},
		teams:            map[string]int{},
		players:          map[string]int{},
		matchDays:        map[int]int{},
		matchReadVersion: map[int]int{},
		teamReadVersion:  map[string]int{},
		members:          map[string]*member{},
		playerSeen:       map[string]int{},
		owners:           map[int]*int{},
		keyed:            map[string]wireResponse{},
	}, nil
}

// --- SQL helpers -----------------------------------------------------------

func (w *world) exec(ctx context.Context, sql string, args ...any) error {
	if _, err := w.app.Owner.Exec(ctx, sql, args...); err != nil {
		return fmt.Errorf("%s: %w", firstLine(sql), err)
	}
	return nil
}

func (w *world) queryInt(ctx context.Context, sql string, args ...any) (int, error) {
	var id int
	if err := w.app.Owner.QueryRow(ctx, sql, args...).Scan(&id); err != nil {
		return 0, fmt.Errorf("%s: %w", firstLine(sql), err)
	}
	return id, nil
}

func firstLine(sql string) string {
	for i, r := range sql {
		if r == '\n' {
			return sql[:i]
		}
	}
	return sql
}

// --- the calendar world ----------------------------------------------------

// seasonWithRounds builds a live season with n rounds. Round 1 is opened the
// calendar's own way as far as the fixtures go: the rounds are inserted
// created and then opened in sequence, so rounds_progress_gate sees the
// unbroken sequence it demands (a round 38 needs its 37 predecessors).
func (w *world) seasonWithRounds(ctx context.Context, name string, n int) error {
	seasonID, err := w.queryInt(ctx, `INSERT INTO schedule.seasons (status) VALUES ('open') RETURNING id`)
	if err != nil {
		return err
	}
	w.seasonID = seasonID
	w.seasonName = name

	for number := 1; number <= n; number++ {
		if err := w.insertRound(ctx, seasonID, number); err != nil {
			return err
		}
	}

	return w.exec(ctx, `UPDATE schedule.rounds SET status = 'open' WHERE id = $1`, w.rounds[1])
}

func (w *world) insertRound(ctx context.Context, seasonID, number int) error {
	roundID, err := w.queryInt(ctx,
		`INSERT INTO schedule.rounds (season_id, number, status) VALUES ($1, $2, 'created') RETURNING id`,
		seasonID, number)
	if err != nil {
		return err
	}
	w.rounds[number] = roundID
	return nil
}

// teamsInSeason registers the named teams.
func (w *world) teamsInSeason(ctx context.Context, names ...string) error {
	for _, name := range names {
		teamID, err := w.queryInt(ctx, `
			INSERT INTO schedule.teams (name, logo, description)
			VALUES ($1, $2, $3) RETURNING id
		`, name, "https://spec.example/"+name+".png", "spec fixture team "+name)
		if err != nil {
			return err
		}
		w.teams[name] = teamID
		w.teamOrder = append(w.teamOrder, name)
	}
	return nil
}

// registerPlayer registers a player in the season, favouring the season's
// first team — the registration's only calendar reference.
func (w *world) registerPlayer(ctx context.Context, name string) error {
	if w.seasonID == 0 {
		return fmt.Errorf("no season built: player %q registration needs the world's season", name)
	}
	favorite := w.teams[w.teamOrder[0]]

	playerID, err := w.queryInt(ctx, `
		INSERT INTO game.players (season_id, favorite_team_id, name)
		VALUES ($1, $2, $3) RETURNING id
	`, w.seasonID, favorite, name)
	if err != nil {
		return err
	}
	w.players[name] = playerID
	return nil
}

// holdMatchDay places one match in the round — "round 1 holds match day 1".
// matches are created in the future (ActionPhase) unless a clock step moves
// them.
func (w *world) holdMatchDay(ctx context.Context, roundNo, matchDay int, home, away string, odds *float64) error {
	roundID, ok := w.rounds[roundNo]
	if !ok {
		return fmt.Errorf("round %d was never built", roundNo)
	}
	homeID, ok := w.teams[home]
	if !ok {
		return fmt.Errorf("team %q was never built", home)
	}
	awayID, ok := w.teams[away]
	if !ok {
		return fmt.Errorf("team %q was never built", away)
	}

	homeOdds := 1.80
	awayOdds := 2.50
	if odds != nil {
		homeOdds = *odds
	}

	startsAt := now().Add(3*time.Hour + time.Duration(matchDay)*time.Hour)

	matchID, err := w.queryInt(ctx, `
		INSERT INTO schedule.matches
			(season_id, round_id, home_team_id, away_team_id, home_odds, away_odds, starts_at, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'created')
		RETURNING id
	`, w.seasonID, roundID, homeID, awayID, homeOdds, awayOdds, startsAt)
	if err != nil {
		return err
	}
	w.matchDays[matchDay] = matchID
	return nil
}

// clockBeforeFirstMatch moves the round's matches so that now sits lead before
// the round's first kickoff — the ActionPhase window (or, at 30 minutes, the
// match phase).
//
// A match that is still created with a kickoff ahead takes the UPDATE (a
// same-status rewrite, rank 0 → 0). A match that has already been started or
// closed, or whose kickoff has already passed, is planted at created instead:
// 000019 refuses the backward UPDATE (match_status_regression) and 000001
// refuses a created rewrite over a past kickoff
// (started_match_blocks_status_regression).
func (w *world) clockBeforeFirstMatch(ctx context.Context, roundNo int, lead time.Duration) error {
	matches, err := w.roundMatches(ctx, roundNo)
	if err != nil {
		return err
	}
	for i, matchID := range matches {
		// Whole seconds: schedule's instants are stored as timestamp(0), which
		// ROUNDS a fractional second — a placement a hair below the boundary
		// would land a hair above it, and a scenario stating the edge exactly
		// ("60 minutes before kickoff") would then flip by chance. Flooring the
		// fixture's clock keeps every placement on the near side of its edge.
		startsAt := now().Add(lead + time.Duration(i)*time.Hour).Truncate(time.Second)
		status, kickoff, err := w.matchLifecycle(ctx, matchID)
		if err != nil {
			return err
		}
		if status != "created" || !kickoff.After(now()) {
			if err := w.replantMatch(ctx, matchID, matchShape{
				status:   "created",
				startsAt: startsAt,
			}); err != nil {
				return err
			}
			continue
		}
		err = w.exec(ctx, `
			UPDATE schedule.matches
			SET starts_at = $2, status = 'created', home_score = NULL, away_score = NULL
			WHERE id = $1
		`, matchID, startsAt)
		if err != nil {
			return err
		}
	}
	return nil
}

// matchLifecycle reads the two facts a fixture's rewind decision needs: the
// match's lifecycle status and its kickoff.
func (w *world) matchLifecycle(ctx context.Context, matchID int) (string, time.Time, error) {
	var (
		status   string
		startsAt time.Time
	)
	if err := w.app.Owner.QueryRow(ctx,
		`SELECT status, starts_at FROM schedule.matches WHERE id = $1`, matchID).
		Scan(&status, &startsAt); err != nil {
		return "", time.Time{}, fmt.Errorf("read match %d lifecycle: %w", matchID, err)
	}
	return status, startsAt, nil
}

// matchShape is a match's lifecycle shape as a fixture plants it: the columns
// the status/score/end CHECKs couple. A nil score or a nil end is a SQL NULL.
type matchShape struct {
	status   string
	startsAt time.Time
	home     *int
	away     *int
	endedAt  *time.Time
}

// replantMatch writes a match into the given lifecycle shape by planting the
// row — DELETE, then INSERT with the same id — instead of updating it.
//
// Why: 000019's status-rank gates make a closed match final for UPDATE, and the
// fixtures do need to walk one backwards (several scenarios re-resolve a match
// whose committed result they are about to rewrite; "the last match has not
// ended" un-closes one). INSERT is not rank-gated — a fresh row has no earlier
// stage — and every CHECK is satisfied because the caller supplies a complete
// shape: in_progress/closed carry both scores, closed carries ended_at >
// starts_at, created/postponed carry neither.
//
// The id is re-supplied explicitly (OVERRIDING SYSTEM VALUE), so the planted
// row keeps its identity: rides already planted against the match keep reading
// through their match_id (rides.match_id has carried no FK since 000016, so
// nothing cascades) and the world's id maps stay valid.
func (w *world) replantMatch(ctx context.Context, matchID int, shape matchShape) error {
	var (
		seasonID, roundID, homeTeamID, awayTeamID int
		homeOdds, awayOdds                        string
		createdAt                                 time.Time
		version                                   int
	)
	err := w.app.Owner.QueryRow(ctx, `
		SELECT season_id, round_id, home_team_id, away_team_id,
		       home_odds::text, away_odds::text, created_at, version
		FROM schedule.matches WHERE id = $1
	`, matchID).Scan(&seasonID, &roundID, &homeTeamID, &awayTeamID,
		&homeOdds, &awayOdds, &createdAt, &version)
	if err != nil {
		return fmt.Errorf("read match %d to replant it: %w", matchID, err)
	}

	tx, err := w.app.Owner.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin replanting match %d: %w", matchID, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `DELETE FROM schedule.matches WHERE id = $1`, matchID); err != nil {
		return fmt.Errorf("unplant match %d: %w", matchID, err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO schedule.matches
			(id, created_at, starts_at, season_id, round_id, home_team_id, away_team_id,
			 home_odds, away_odds, home_score, away_score, status, ended_at, version)
		OVERRIDING SYSTEM VALUE
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
	`, matchID, createdAt, shape.startsAt, seasonID, roundID, homeTeamID, awayTeamID,
		homeOdds, awayOdds, shape.home, shape.away, shape.status, shape.endedAt, version); err != nil {
		return fmt.Errorf("replant match %d as %s: %w", matchID, shape.status, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit replanting match %d: %w", matchID, err)
	}
	return nil
}

// clockAfterLastMatch closes whatever the round still has open and stamps every
// match's end ago before now — the DecisionPhase window (it opens one hour
// after the round's last match).
func (w *world) clockAfterLastMatch(ctx context.Context, roundNo int, ago time.Duration) error {
	matches, err := w.roundMatches(ctx, roundNo)
	if err != nil {
		return err
	}
	for _, matchID := range matches {
		status, err := w.matchStatus(ctx, matchID)
		if err != nil {
			return err
		}
		if status != "closed" {
			if err := w.closeMatch(ctx, matchID, 1, 0, ago); err != nil {
				return err
			}
			continue
		}
		// A committed result keeps its scoreline: the clock only moves the end.
		if err := w.stampMatchEnd(ctx, matchID, ago); err != nil {
			return err
		}
	}
	return nil
}

// stampMatchEnd places a closed match's end where the scenario's clock needs it.
//
// The instant is floored to the whole second: ended_at is timestamp(0) and
// PostgreSQL rounds the fractional part, which would push an exact-edge
// placement ("60 minutes after the last match") past its own boundary half the
// time.
func (w *world) stampMatchEnd(ctx context.Context, matchID int, ago time.Duration) error {
	return w.exec(ctx,
		`UPDATE schedule.matches
		 SET ended_at = date_trunc('second', now() - make_interval(secs => $2))
		 WHERE id = $1`,
		matchID, ago.Seconds())
}

// roundMatches lists a round's match ids in creation order.
func (w *world) roundMatches(ctx context.Context, roundNo int) ([]int, error) {
	roundID, ok := w.rounds[roundNo]
	if !ok {
		return nil, fmt.Errorf("round %d was never built", roundNo)
	}
	rows, err := w.app.Owner.Query(ctx,
		`SELECT id FROM schedule.matches WHERE round_id = $1 ORDER BY id`, roundID)
	if err != nil {
		return nil, fmt.Errorf("list round %d matches: %w", roundNo, err)
	}
	defer rows.Close()

	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// roundStatus reports the round's lifecycle status through the calendar's own
// read facade.
func (w *world) roundStatus(ctx context.Context, roundNo int) (schedule.RoundStatus, error) {
	matches, err := w.roundMatches(ctx, roundNo)
	if err != nil || len(matches) == 0 {
		return "", fmt.Errorf("round %d has no matches to derive a status from: %w", roundNo, err)
	}
	status, _, _, err := w.app.Schedule.PhaseForMatch(ctx, matches[0])
	return status, err
}

// matchFacts is the committed snapshot the settlement payload carries.
type matchFacts struct {
	ID, SeasonID, RoundID, HomeTeamID, AwayTeamID int
	HomeOdds, AwayOdds                            float64
	HomeScore, AwayScore                          int
}

func (w *world) matchFacts(ctx context.Context, matchID int) (matchFacts, error) {
	var facts matchFacts
	err := w.app.Owner.QueryRow(ctx, `
		SELECT id, season_id, round_id, home_team_id, away_team_id, home_odds, away_odds,
		       COALESCE(home_score, 0), COALESCE(away_score, 0)
		FROM schedule.matches WHERE id = $1
	`, matchID).Scan(&facts.ID, &facts.SeasonID, &facts.RoundID,
		&facts.HomeTeamID, &facts.AwayTeamID, &facts.HomeOdds, &facts.AwayOdds,
		&facts.HomeScore, &facts.AwayScore)
	if err != nil {
		return matchFacts{}, fmt.Errorf("read match %d: %w", matchID, err)
	}
	return facts, nil
}

// teamName is the street name of a team id (the features name teams, the rows
// carry ids).
func (w *world) teamName(teamID int) string {
	for name, id := range w.teams {
		if id == teamID {
			return name
		}
	}
	return fmt.Sprintf("team %d", teamID)
}

// startMatch puts a match in play: started, scored (the schema's in-progress
// shape), not yet ended — the state ResolveTx demands. It is also how the
// fixture rewinds a match whose committed result the scenario is about to
// rewrite.
//
// A created/postponed match walks forward and an in-progress one is rewritten
// in place (same rank) — both legal UPDATEs. A closed match is planted at
// in_progress instead: 000019 makes it final for UPDATE (match_status_
// regression), while an INSERT is not rank-gated.
func (w *world) startMatch(ctx context.Context, matchID int) error {
	status, _, err := w.matchLifecycle(ctx, matchID)
	if err != nil {
		return err
	}
	if status == "closed" {
		home, away := 0, 0
		return w.replantMatch(ctx, matchID, matchShape{
			status:   "in_progress",
			startsAt: now().Add(-6 * time.Hour),
			home:     &home,
			away:     &away,
		})
	}
	return w.exec(ctx, `
		UPDATE schedule.matches
		SET starts_at = now() - interval '6 hours', status = 'in_progress',
		    home_score = 0, away_score = 0, ended_at = NULL
		WHERE id = $1
	`, matchID)
}

// matchStatus reads one match's lifecycle status.
func (w *world) matchStatus(ctx context.Context, matchID int) (string, error) {
	var status string
	if err := w.app.Owner.QueryRow(ctx,
		`SELECT status FROM schedule.matches WHERE id = $1`, matchID).Scan(&status); err != nil {
		return "", fmt.Errorf("read match %d status: %w", matchID, err)
	}
	return status, nil
}

// closeMatch ends a match without any settlement running: the committed result
// exists, no MatchResultRecorded was published — the accepted settlement gap the
// features use to build "the result never arrived" worlds. An already-closed
// match only has its end time placed.
func (w *world) closeMatch(ctx context.Context, matchID, home, away int, ago time.Duration) error {
	if err := w.startMatch(ctx, matchID); err != nil {
		return err
	}
	if err := w.exec(ctx, `
		UPDATE schedule.matches SET status = 'closed', home_score = $2, away_score = $3 WHERE id = $1
	`, matchID, home, away); err != nil {
		return err
	}
	// matches_set_ended_at_on_close stamps now() on the transition; the
	// fixture then places the end where the scenario's clock needs it.
	return w.stampMatchEnd(ctx, matchID, ago)
}

// resolveMatchDay commits a result the product's way: the calendar's resolve use
// case, which publishes MatchResultRecorded and settles the match's rides.
func (w *world) resolveMatchDay(ctx context.Context, matchID, home, away int) error {
	if err := w.startMatch(ctx, matchID); err != nil {
		return err
	}

	var version int
	if err := w.app.Owner.QueryRow(ctx,
		`SELECT version FROM schedule.matches WHERE id = $1`, matchID).Scan(&version); err != nil {
		return fmt.Errorf("read match %d version: %w", matchID, err)
	}

	_, err := w.app.Schedule.ResolveMatch(ctx, matchID, schedule.Score{Home: &home, Away: &away},
		now().Add(-time.Minute), version)
	if err != nil {
		return fmt.Errorf("resolve match %d as %d-%d: %w", matchID, home, away, err)
	}
	return nil
}

// placeRoundEndBehindLastMatch moves the derived phase window past the round's
// last match when the match just resolved was the round's last live one.
//
// The phase is derived from the round's facts (min starts_at, max ended_at), so
// "the round's matches have all ended" and "the decision window is open" are
// one fact only once the ends sit the decision lag in the past. A scenario that
// says its last result has arrived expects the window the previous refusal was
// waiting on to be open, without a separate clock step saying so. A round that
// still carries a live match is left untouched: its phase is the match phase.
func (w *world) placeRoundEndBehindLastMatch(ctx context.Context, matchID int, ago time.Duration) error {
	var roundID int
	if err := w.app.Owner.QueryRow(ctx,
		`SELECT round_id FROM schedule.matches WHERE id = $1`, matchID).Scan(&roundID); err != nil {
		return fmt.Errorf("read match %d's round: %w", matchID, err)
	}

	live, err := w.queryInt(ctx,
		`SELECT count(*) FROM schedule.matches WHERE round_id = $1 AND status <> 'closed'`, roundID)
	if err != nil {
		return err
	}
	if live > 0 {
		return nil
	}

	// Every end moves, not only the resolved match's: the window reads the
	// round's LAST end, and an earlier result left where it was committed would
	// still be the latest instant the round carries.
	return w.exec(ctx, `
		UPDATE schedule.matches
		SET ended_at = date_trunc('second', now() - make_interval(secs => $2))
		WHERE round_id = $1
	`, roundID, ago.Seconds())
}

// scoreFor picks the scoreline that crowns the given team in this match.
func (w *world) scoreFor(ctx context.Context, matchID int, team string) (home, away int, err error) {
	facts, err := w.matchFacts(ctx, matchID)
	if err != nil {
		return 0, 0, err
	}
	if facts.HomeTeamID == w.teams[team] {
		return 2, 1, nil
	}
	return 1, 2, nil
}

// reopenLastMatch makes the round's last match live again — "the last match of
// round 1 has not ended". It is a rewind out of closed, so the match is planted
// at in_progress (000019 refuses the backward UPDATE; INSERT is not gated); the
// row keeps its id and its kickoff.
func (w *world) reopenLastMatch(ctx context.Context, roundNo int) error {
	matches, err := w.roundMatches(ctx, roundNo)
	if err != nil {
		return err
	}
	if len(matches) == 0 {
		return fmt.Errorf("round %d has no matches", roundNo)
	}
	matchID := matches[len(matches)-1]
	status, kickoff, err := w.matchLifecycle(ctx, matchID)
	if err != nil {
		return err
	}
	if status == "closed" {
		home, away := 0, 0
		return w.replantMatch(ctx, matchID, matchShape{
			status:   "in_progress",
			startsAt: kickoff,
			home:     &home,
			away:     &away,
		})
	}
	return w.exec(ctx, `
		UPDATE schedule.matches
		SET status = 'in_progress', home_score = 0, away_score = 0, ended_at = NULL
		WHERE id = $1
	`, matchID)
}

// closeRoundWithoutSettlement closes a round the way a crashed close would leave
// it: every match settled, the round flipped, no RoundCompleted published — the
// gap the rides keep waiting in.
func (w *world) closeRoundWithoutSettlement(ctx context.Context, roundNo int) error {
	if err := w.clockAfterLastMatch(ctx, roundNo, 2*time.Hour); err != nil {
		return err
	}
	return w.exec(ctx, `UPDATE schedule.rounds SET status = 'closed' WHERE id = $1`, w.rounds[roundNo])
}

// completeRound closes the round the product's way: every match settled, then
// the lifecycle transition through the calendar's route table, which publishes
// RoundCompleted and lets the game context settle the round's undecided rides.
func (w *world) completeRound(ctx context.Context, roundNo int) error {
	return w.completeRoundID(ctx, roundNo, w.rounds[roundNo], w.seasonID)
}

func (w *world) completeRoundID(ctx context.Context, roundNo, roundID, seasonID int) error {
	if err := w.clockAfterLastMatch(ctx, roundNo, 2*time.Hour); err != nil {
		return err
	}

	res, err := w.app.call("PATCH", fmt.Sprintf("/v1/seasons/%d/rounds/%d", seasonID, roundID),
		map[string]any{"status": "closed"}, nil)
	if err != nil {
		return err
	}
	if res.Status != 200 {
		return fmt.Errorf("close round %d: %d %s", roundNo, res.Status, res.message())
	}
	return nil
}

// finalRoundWorld builds the season whose last round (38) is open and holds one
// match day for team: the fixture the final-round scenarios need instead of
// walking thirty-eight rounds by hand.
func (w *world) finalRoundWorld(ctx context.Context, team string) error {
	const roundsPerSeason = 38

	seasonID, err := w.queryInt(ctx, `INSERT INTO schedule.seasons (status) VALUES ('in_progress') RETURNING id`)
	if err != nil {
		return err
	}
	w.finalSeasonID = seasonID

	roundIDs := map[int]int{}
	for number := 1; number <= roundsPerSeason; number++ {
		roundID, err := w.queryInt(ctx,
			`INSERT INTO schedule.rounds (season_id, number, status) VALUES ($1, $2, 'created') RETURNING id`,
			seasonID, number)
		if err != nil {
			return err
		}
		roundIDs[number] = roundID
	}
	// Close 1..37 in order (the sequence gate wants each predecessor closed),
	// then open the last round: the season's final round.
	for number := 1; number < roundsPerSeason; number++ {
		if err := w.exec(ctx, `UPDATE schedule.rounds SET status = 'closed' WHERE id = $1`, roundIDs[number]); err != nil {
			return err
		}
	}
	if err := w.exec(ctx, `UPDATE schedule.rounds SET status = 'open' WHERE id = $1`,
		roundIDs[roundsPerSeason]); err != nil {
		return err
	}
	w.finalRoundID = roundIDs[roundsPerSeason]

	homeID, ok := w.teams[team]
	if !ok {
		return fmt.Errorf("team %q was never built", team)
	}
	otherID := w.teams[w.teamOrder[1]]

	matchID, err := w.queryInt(ctx, `
		INSERT INTO schedule.matches
			(season_id, round_id, home_team_id, away_team_id, home_odds, away_odds, starts_at, status)
		VALUES ($1, $2, $3, $4, 1.80, 2.50, $5, 'created')
		RETURNING id
	`, seasonID, w.finalRoundID, homeID, otherID, now().Add(3*time.Hour))
	if err != nil {
		return err
	}
	w.finalMatchID = matchID
	return nil
}

// --- the calendar's own writes -------------------------------------------
//
// The schedule features drive the calendar through its route table (style
// guide §7, family A): a season, a round, a team and a fixture are created the
// way the product creates them, and every lifecycle move is the PATCH the
// organiser's screen sends. Reads go the same way, so an assertion never
// inspects a row the caller could not see.

// The calendar's read shapes: only the members a scenario asserts travel here.
type seasonView struct {
	ID      int    `json:"id"`
	Status  string `json:"status"`
	Version int    `json:"version"`
}

type roundView struct {
	ID       int    `json:"id"`
	SeasonID int    `json:"season_id"`
	Number   int    `json:"number"`
	Status   string `json:"status"`
	Version  int    `json:"version"`
}

type sideOdds struct {
	Home float64 `json:"home"`
	Away float64 `json:"away"`
}

type sideScore struct {
	Home *int `json:"home"`
	Away *int `json:"away"`
}

type matchView struct {
	ID         int        `json:"id"`
	SeasonID   int        `json:"season_id"`
	RoundID    int        `json:"round_id"`
	HomeTeamID int        `json:"home_team_id"`
	AwayTeamID int        `json:"away_team_id"`
	Status     string     `json:"status"`
	StartsAt   time.Time  `json:"starts_at"`
	EndedAt    *time.Time `json:"ended_at"`
	Odds       sideOdds   `json:"odds"`
	Score      sideScore  `json:"score"`
	Version    int        `json:"version"`
}

type teamView struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     int    `json:"version"`
}

// calendar drives one calendar route in-process, keeping the answer for the
// refusal steps (the same w.last the ride steps read).
func (w *world) calendar(method, path string, body any, headers map[string]string) (*wireResponse, error) {
	res, err := w.app.call(method, path, body, headers)
	if err != nil {
		return nil, err
	}
	w.last = res
	return res, nil
}

// calendarAccepted fails a world-building step whose write the calendar
// refused: the scenarios that expect a refusal assert it in their own Then.
func calendarAccepted(res *wireResponse, what string) error {
	if res.Status < 200 || res.Status >= 300 {
		return fmt.Errorf("%s: %d %s", what, res.Status, res.message())
	}
	return nil
}

// createSeason creates the world's season through the calendar's route.
func (w *world) createSeason(ctx context.Context) (int, error) {
	res, err := w.calendar("POST", "/v1/seasons", map[string]any{}, nil)
	if err != nil {
		return 0, err
	}
	if err := calendarAccepted(res, "create a season"); err != nil {
		return 0, err
	}
	var season seasonView
	if err := decodeEnvelope(res, "season", &season); err != nil {
		return 0, err
	}
	return season.ID, nil
}

// createRound adds a round to the season the way the calendar does.
func (w *world) createRound(ctx context.Context, seasonID, number int) (roundView, error) {
	res, err := w.calendar("POST", fmt.Sprintf("/v1/seasons/%d/rounds", seasonID),
		map[string]any{"number": number}, nil)
	if err != nil {
		return roundView{}, err
	}
	if err := calendarAccepted(res, fmt.Sprintf("create round %d", number)); err != nil {
		return roundView{}, err
	}
	var round roundView
	if err := decodeEnvelope(res, "round", &round); err != nil {
		return roundView{}, err
	}
	return round, nil
}

// createTeam registers a club on the calendar.
func (w *world) createTeam(ctx context.Context, name string) (int, error) {
	res, err := w.calendar("POST", "/v1/teams", map[string]any{
		"name":        name,
		"logo":        "https://spec.example/" + name + ".png",
		"description": "spec fixture team " + name,
	}, nil)
	if err != nil {
		return 0, err
	}
	if err := calendarAccepted(res, "create team "+name); err != nil {
		return 0, err
	}
	var team teamView
	if err := decodeEnvelope(res, "team", &team); err != nil {
		return 0, err
	}
	return team.ID, nil
}

// fixtureOdds is the price a fixture is announced at when a scenario does not
// say otherwise: both sides above evens, as the calendar demands.
var fixtureOdds = sideOdds{Home: 1.80, Away: 2.50}

// announceMatchDay announces the fixture through the calendar's create route
// and, when it lands, records it as the scenario's match day.
func (w *world) announceMatchDay(ctx context.Context, seasonID, day, roundID, homeTeamID, awayTeamID int, startsAt time.Time) (*wireResponse, error) {
	res, err := w.calendar("POST", fmt.Sprintf("/v1/seasons/%d/matches", seasonID), map[string]any{
		"round_id":     roundID,
		"home_team_id": homeTeamID,
		"away_team_id": awayTeamID,
		"starts_at":    startsAt.UTC(),
		"odds":         fixtureOdds,
	}, nil)
	if err != nil {
		return nil, err
	}
	if res.Status == 201 {
		var match matchView
		if err := decodeEnvelope(res, "match", &match); err != nil {
			return nil, err
		}
		w.matchDays[day] = match.ID
	}
	return res, nil
}

// ensureMatchDay plants the fixture a clock state needs when the scenario did
// not announce one: the calendar's own create route, Team A against Team B,
// kicking off with room to spare before the action window closes.
func (w *world) ensureMatchDay(ctx context.Context, day int) (int, error) {
	if matchID, ok := w.matchDays[day]; ok {
		return matchID, nil
	}
	roundID, ok := w.rounds[1]
	if !ok {
		return 0, fmt.Errorf("round 1 was never built")
	}
	home, ok := w.teams["Team A"]
	if !ok {
		return 0, fmt.Errorf(`team "Team A" was never built`)
	}
	away, ok := w.teams["Team B"]
	if !ok {
		return 0, fmt.Errorf(`team "Team B" was never built`)
	}
	res, err := w.announceMatchDay(ctx, w.seasonID, day, roundID, home, away, now().Add(4*time.Hour))
	if err != nil {
		return 0, err
	}
	if err := calendarAccepted(res, fmt.Sprintf("announce match day %d", day)); err != nil {
		return 0, err
	}
	return w.matchDays[day], nil
}

// patchSeason sends one season lifecycle move.
func (w *world) patchSeason(ctx context.Context, seasonID int, status string, expectedVersion *int) (*wireResponse, error) {
	headers := map[string]string{}
	if expectedVersion != nil {
		headers["X-Expected-Version"] = strconv.Itoa(*expectedVersion)
	}
	return w.calendar("PATCH", fmt.Sprintf("/v1/seasons/%d", seasonID),
		map[string]any{"status": status}, headers)
}

// patchRound sends one round edit (the lifecycle moves and the plain edits).
func (w *world) patchRound(ctx context.Context, seasonID, roundID int, body map[string]any, expectedVersion *int) (*wireResponse, error) {
	headers := map[string]string{}
	if expectedVersion != nil {
		headers["X-Expected-Version"] = strconv.Itoa(*expectedVersion)
	}
	return w.calendar("PATCH", fmt.Sprintf("/v1/seasons/%d/rounds/%d", seasonID, roundID), body, headers)
}

// patchMatch sends one match edit.
func (w *world) patchMatch(ctx context.Context, seasonID, matchID int, body map[string]any, expectedVersion *int) (*wireResponse, error) {
	headers := map[string]string{}
	if expectedVersion != nil {
		headers["X-Expected-Version"] = strconv.Itoa(*expectedVersion)
	}
	return w.calendar("PATCH", fmt.Sprintf("/v1/seasons/%d/matches/%d", seasonID, matchID), body, headers)
}

// openRound is the calendar's own way to a live round: the season is declared
// open first (a created season cannot host an open round —
// round_progress_season_not_live), then the round opens.
func (w *world) openRound(ctx context.Context, seasonID, roundID int) error {
	season, status, err := w.readSeason(ctx, seasonID)
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("read season %d: %d", seasonID, status)
	}
	if season.Status == "created" {
		res, err := w.patchSeason(ctx, seasonID, "open", nil)
		if err != nil {
			return err
		}
		if err := calendarAccepted(res, "declare the season open"); err != nil {
			return err
		}
	}
	res, err := w.patchRound(ctx, seasonID, roundID, map[string]any{"status": "open"}, nil)
	if err != nil {
		return err
	}
	return calendarAccepted(res, fmt.Sprintf("open round %d", roundID))
}

// closeRound flips a live round to closed through the calendar's route.
func (w *world) closeRound(ctx context.Context, seasonID, roundID int) error {
	res, err := w.patchRound(ctx, seasonID, roundID, map[string]any{"status": "closed"}, nil)
	if err != nil {
		return err
	}
	return calendarAccepted(res, fmt.Sprintf("close round %d", roundID))
}

// readSeason, readRound, readMatch and readTeam read one calendar record back
// through its own read route; the status is the route's answer (404 when the
// record is gone), so a scenario can assert absence as well as content.
func (w *world) readSeason(ctx context.Context, id int) (seasonView, int, error) {
	res, err := w.calendar("GET", fmt.Sprintf("/v1/seasons/%d", id), nil, nil)
	if err != nil {
		return seasonView{}, 0, err
	}
	if res.Status != 200 {
		return seasonView{}, res.Status, nil
	}
	var season seasonView
	if err := decodeEnvelope(res, "season", &season); err != nil {
		return seasonView{}, res.Status, err
	}
	return season, res.Status, nil
}

func (w *world) readRound(ctx context.Context, id int) (roundView, int, error) {
	res, err := w.calendar("GET", fmt.Sprintf("/v1/rounds/%d", id), nil, nil)
	if err != nil {
		return roundView{}, 0, err
	}
	if res.Status != 200 {
		return roundView{}, res.Status, nil
	}
	var round roundView
	if err := decodeEnvelope(res, "round", &round); err != nil {
		return roundView{}, res.Status, err
	}
	return round, res.Status, nil
}

func (w *world) readMatch(ctx context.Context, id int) (matchView, int, error) {
	res, err := w.calendar("GET", fmt.Sprintf("/v1/matches/%d", id), nil, nil)
	if err != nil {
		return matchView{}, 0, err
	}
	if res.Status != 200 {
		return matchView{}, res.Status, nil
	}
	var match matchView
	if err := decodeEnvelope(res, "match", &match); err != nil {
		return matchView{}, res.Status, err
	}
	return match, res.Status, nil
}

func (w *world) readTeam(ctx context.Context, id int) (teamView, int, error) {
	res, err := w.calendar("GET", fmt.Sprintf("/v1/teams/%d", id), nil, nil)
	if err != nil {
		return teamView{}, 0, err
	}
	if res.Status != 200 {
		return teamView{}, res.Status, nil
	}
	var team teamView
	if err := decodeEnvelope(res, "team", &team); err != nil {
		return teamView{}, res.Status, err
	}
	return team, res.Status, nil
}

// countMatchesInSeason is the calendar's own answer to "how many fixtures does
// this season carry?" — the list route's total.
func (w *world) countMatchesInSeason(ctx context.Context, seasonID int) (int, error) {
	res, err := w.calendar("GET", fmt.Sprintf("/v1/matches?season_id=%d", seasonID), nil, nil)
	if err != nil {
		return 0, err
	}
	if res.Status != 200 {
		return 0, fmt.Errorf("list the season's matches: %d %s", res.Status, res.message())
	}
	var envelope struct {
		Metadata struct {
			TotalRecords int `json:"total_records"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(res.Body, &envelope); err != nil {
		return 0, fmt.Errorf("read the match list metadata: %w", err)
	}
	return envelope.Metadata.TotalRecords, nil
}

// registerPlayerFavouring registers a player whose favourite club is named
// (the pilot's registerPlayer favours the season's first club).
func (w *world) registerPlayerFavouring(ctx context.Context, name, team string) error {
	if w.seasonID == 0 {
		return fmt.Errorf("no season built: player %q registration needs the world's season", name)
	}
	teamID, ok := w.teams[team]
	if !ok {
		return fmt.Errorf("team %q was never built", team)
	}

	playerID, err := w.queryInt(ctx, `
		INSERT INTO game.players (season_id, favorite_team_id, name)
		VALUES ($1, $2, $3) RETURNING id
	`, w.seasonID, teamID, name)
	if err != nil {
		return err
	}
	w.players[name] = playerID
	return nil
}

// --- the ride world --------------------------------------------------------

// rideRow is one ride as the suite reads it back. The frozen wire shape omits
// the accrual and the streak, so those two are read from the row; everything a
// caller can see is asserted through the module's read route.
type rideRow struct {
	ID       int
	PlayerID int
	TeamID   int
	MatchID  int
	State    string
	Tokens   int64
	Base     decimal.Decimal
	Bonus    decimal.Decimal
	Streak   int
	Version  int
}

func (w *world) rideByID(ctx context.Context, rideID int) (rideRow, error) {
	var (
		row   rideRow
		base  string
		bonus string
	)
	err := w.app.Owner.QueryRow(ctx, `
		SELECT id, player_id, team_id, match_id, state, tokens_locked,
		       base_at_lock::text, bonus_acc::text, streak, version
		FROM game.rides WHERE id = $1
	`, rideID).Scan(&row.ID, &row.PlayerID, &row.TeamID, &row.MatchID, &row.State,
		&row.Tokens, &base, &bonus, &row.Streak, &row.Version)
	if err != nil {
		return rideRow{}, fmt.Errorf("read ride %d: %w", rideID, err)
	}
	if row.Base, err = decimal.NewFromString(base); err != nil {
		return rideRow{}, fmt.Errorf("parse ride %d base %q: %w", rideID, base, err)
	}
	if row.Bonus, err = decimal.NewFromString(bonus); err != nil {
		return rideRow{}, fmt.Errorf("parse ride %d bonus %q: %w", rideID, bonus, err)
	}
	return row, nil
}

// currentRide is the ride the scenario is acting on.
func (w *world) currentRide(ctx context.Context) (rideRow, error) {
	if w.lastRide == 0 {
		return rideRow{}, fmt.Errorf("no ride has been built or locked in this scenario")
	}
	return w.rideByID(ctx, w.lastRide)
}

// rideForTeam finds the scenario's ride on the given team.
func (w *world) rideForTeam(ctx context.Context, team string) (rideRow, error) {
	teamID, ok := w.teams[team]
	if !ok {
		return rideRow{}, fmt.Errorf("team %q was never built", team)
	}
	var rideID int
	err := w.app.Owner.QueryRow(ctx,
		`SELECT id FROM game.rides WHERE team_id = $1 ORDER BY id DESC LIMIT 1`, teamID).Scan(&rideID)
	if err != nil {
		return rideRow{}, fmt.Errorf("find the ride on %q: %w", team, err)
	}
	return w.rideByID(ctx, rideID)
}

// lockTokens is the player's lock on a match day: the ride's create command
// through the game module's own route.
func (w *world) lockTokens(ctx context.Context, player, team string, matchDay, tokens int) (*wireResponse, error) {
	matchID, ok := w.matchDays[matchDay]
	if !ok {
		return nil, fmt.Errorf("match day %d was never built", matchDay)
	}
	return w.lockOnMatch(ctx, player, team, matchID, tokens)
}

// lockOnMatch is the same lock addressed by match id — the final-round
// scenarios' match day is not one of the season's numbered days.
func (w *world) lockOnMatch(ctx context.Context, player, team string, matchID, tokens int) (*wireResponse, error) {
	playerID, ok := w.players[player]
	if !ok {
		return nil, fmt.Errorf("player %q was never registered", player)
	}
	teamID, ok := w.teams[team]
	if !ok {
		return nil, fmt.Errorf("team %q was never built", team)
	}

	body := map[string]any{
		"player_id":     playerID,
		"team_id":       teamID,
		"match_id":      matchID,
		"tokens_locked": tokens,
	}
	res, err := w.app.call("POST", "/v1/rides", body,
		map[string]string{"Idempotency-Key": w.app.seq.next("create")})
	if err != nil {
		return nil, err
	}
	w.last = res
	if res.Status == 201 {
		var stored struct {
			Ride struct {
				ID int `json:"id"`
			} `json:"ride"`
		}
		if err := decodeEnvelope(res, "ride", &stored.Ride); err != nil {
			return nil, err
		}
		w.rides = append(w.rides, stored.Ride.ID)
		w.lastRide = stored.Ride.ID
	}
	return res, nil
}

// rideCommand issues one decision command (burn, unlock) or the continuation
// (lock) through the game module's route.
func (w *world) rideCommand(ctx context.Context, rideID int, command string) (*wireResponse, error) {
	res, err := w.app.call("POST", fmt.Sprintf("/v1/rides/%d/%s", rideID, command), nil,
		map[string]string{"Idempotency-Key": w.app.seq.next(command)})
	if err != nil {
		return nil, err
	}
	w.last = res
	return res, nil
}

// createLockedRide locks tokens and returns the ride's id, failing the step when
// the lock was refused.
func (w *world) createLockedRide(ctx context.Context, player, team string, matchDay, tokens int, odds *float64) (int, error) {
	if odds != nil {
		matchID, ok := w.matchDays[matchDay]
		if !ok {
			return 0, fmt.Errorf("match day %d was never built", matchDay)
		}
		if err := w.priceTeam(ctx, matchID, team, *odds); err != nil {
			return 0, err
		}
	}

	res, err := w.lockTokens(ctx, player, team, matchDay, tokens)
	if err != nil {
		return 0, err
	}
	if res.Status != 201 {
		return 0, fmt.Errorf("lock %d tokens on %q for match day %d: %d %s",
			tokens, team, matchDay, res.Status, res.message())
	}
	return w.lastRide, nil
}

// priceTeam sets the odds the ride's team is priced at.
func (w *world) priceTeam(ctx context.Context, matchID int, team string, odds float64) error {
	facts, err := w.matchFacts(ctx, matchID)
	if err != nil {
		return err
	}
	if facts.HomeTeamID == w.teams[team] {
		return w.exec(ctx, `UPDATE schedule.matches SET home_odds = $2 WHERE id = $1`, matchID, odds)
	}
	return w.exec(ctx, `UPDATE schedule.matches SET away_odds = $2 WHERE id = $1`, matchID, odds)
}

// lockedRideWorld builds "a ride locked by <player> on <team> for match day
// <n>": the lock through the product, then the ride's match committed without
// settlement — the accepted gap (the result exists; no MatchResultRecorded
// reached the ride). That is the world in which burn/unlock/continue are legal
// moves by phase but illegal by state, so the state graph is the guard the
// scenario is about; a later "match day n ends" replays the match and commits
// the scenario's scoreline.
//
// A second ride on the same match (one scenario arranges two players' rides on
// one match day) cannot be locked this way: the create window closed when the
// first ride's world advanced. That ride is planted as a locked row through the
// insert gate — the state the product would have created (R-GAME-001 proves the
// birth itself).
func (w *world) lockedRideWorld(ctx context.Context, player, team string, matchDay, tokens int, odds *float64) error {
	if len(w.rides) > 0 {
		return w.plantLockedRide(ctx, player, team, matchDay, tokens)
	}

	if _, err := w.createLockedRide(ctx, player, team, matchDay, tokens, odds); err != nil {
		return err
	}

	return w.closeMatch(ctx, w.matchDays[matchDay], 1, 0, 2*time.Hour)
}

// plantLockedRide inserts a locked ride — the second player's ride on a match
// day whose create window has already closed. The row is born the way
// RideCreate would have written it (locked, no accrual, no streak) and still
// passes the insert gate.
func (w *world) plantLockedRide(ctx context.Context, player, team string, matchDay, tokens int) error {
	playerID, ok := w.players[player]
	if !ok {
		return fmt.Errorf("player %q was never registered", player)
	}
	teamID, ok := w.teams[team]
	if !ok {
		return fmt.Errorf("team %q was never built", team)
	}
	matchID, ok := w.matchDays[matchDay]
	if !ok {
		return fmt.Errorf("match day %d was never built", matchDay)
	}

	rideID, err := w.queryInt(ctx, `
		INSERT INTO game.rides (player_id, team_id, match_id, state, tokens_locked, base_at_lock)
		VALUES ($1, $2, $3, 'locked', $4, 1.95)
		RETURNING id
	`, playerID, teamID, matchID, tokens)
	if err != nil {
		return err
	}
	w.rides = append(w.rides, rideID)
	w.lastRide = rideID
	return nil
}

// waitingRideWorld builds "a ride waiting on a decision": locked, then its match
// resolved in its favour — the product's own path to won_pending.
func (w *world) waitingRideWorld(ctx context.Context, player, team string, matchDay, tokens int, odds *float64) error {
	if _, err := w.createLockedRide(ctx, player, team, matchDay, tokens, odds); err != nil {
		return err
	}

	matchID := w.matchDays[matchDay]
	home, away, err := w.scoreFor(ctx, matchID, team)
	if err != nil {
		return err
	}
	return w.resolveMatchDay(ctx, matchID, home, away)
}

// decide sends a decision command (burn|unlock) on the scenario's current ride.
func (w *world) decide(ctx context.Context, command string) (*wireResponse, error) {
	ride, err := w.currentRide(ctx)
	if err != nil {
		return nil, err
	}
	return w.rideCommand(ctx, ride.ID, command)
}

func decodeEnvelope(res *wireResponse, key string, target any) error {
	envelope := res.envelope()
	raw, ok := envelope[key]
	if !ok {
		return fmt.Errorf("response carries no %q member: %s", key, string(res.Body))
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("decode %q: %w", key, err)
	}
	return nil
}

// --- sabotage writes -------------------------------------------------------

// ownerWrite runs one direct, unrestricted write and keeps the driver's refusal
// for the assertion step.
func (w *world) ownerWrite(ctx context.Context, sql string, args ...any) error {
	w.lastPgError = nil
	_, err := w.app.Owner.Exec(ctx, sql, args...)
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errorsAsPgError(err, &pgErr) {
		w.lastPgError = pgErr
		return nil
	}
	return fmt.Errorf("%s: %w", firstLine(sql), err)
}

func errorsAsPgError(err error, target **pgconn.PgError) bool {
	for err != nil {
		if pgErr, ok := err.(*pgconn.PgError); ok {
			*target = pgErr
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

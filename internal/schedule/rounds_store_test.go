package schedule

// RoundStore.Close integration tests: the close transaction's status flip,
// its gates (000008), and the RoundCompleted event seam (Phase C).
//
// Since the Phase C split the close no longer resolves rides: Schedule
// closes on feed completion and publishes RoundCompleted post-commit; the
// game context settles the undecided rides from the event payload. The
// ride side is still planted/read through raw SQL, deliberately: schedule
// must not import game, so the assertions go to the tables themselves.
// The ride-side settlement coverage (what used to be the auto-unlock/burn
// half of this transaction) lives in internal/game's event-handler tests.

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/jackc/pgx/v5"

	"lt-api.aleksrdvn.com/internal/eventbus"
	"lt-api.aleksrdvn.com/internal/store"
)

// closeRoundSQL is the direct-writer close statement the gate-backstop
// test probes with. The unclosed-matches half of rounds_close_gate holds
// for any writer.
const closeRoundSQL = `UPDATE schedule.rounds SET status = 'closed' WHERE id = $1`

// rideRow is the raw read of a ride row the close-side assertions check:
// state, acc and version — the columns a settlement would move.
type rideRow struct {
	state   string
	acc     string // numeric read as text, scale-trimmed to "0"/"90" shape
	version int
}

// getRideRow reads a ride row directly (no game store — see the package
// comment above).
func getRideRow(ctx context.Context, t *testing.T, rideID int) rideRow {
	t.Helper()

	var row rideRow
	err := pool.QueryRow(ctx,
		`SELECT state::text, trim(trailing '.' from trim(trailing '0' from bonus_acc::text)), version
		FROM game.rides WHERE id = $1`, rideID).Scan(
		&row.state, &row.acc, &row.version)
	if err != nil {
		t.Fatalf("read ride %d: %v", rideID, err)
	}

	return row
}

// settleRound closes every match of the round directly (the shape the API
// round tests use): starts_at moves into the past so the score
// constraints and the freeze gate accept it, and the status transition
// stamps ended_at.
func settleRound(ctx context.Context, t *testing.T, roundID int) {
	t.Helper()

	if _, err := pool.Exec(ctx, `
		UPDATE schedule.matches SET starts_at = now() - interval '3 hours',
			status = 'closed', home_score = 2, away_score = 1
		WHERE round_id = $1`, roundID); err != nil {
		t.Fatalf("settle round matches: %v", err)
	}
}

// countRidesInState counts the rides of a round (reached through matches —
// no round_id on rides, ADR-018) in a given state.
func countRidesInState(ctx context.Context, t *testing.T, roundID int, state string) int {
	t.Helper()

	var n int
	err := pool.QueryRow(ctx, `
		SELECT count(*) FROM game.rides rd
		JOIN schedule.matches m ON m.id = rd.match_id
		WHERE m.round_id = $1 AND rd.state = $2`, roundID, state).Scan(&n)
	if err != nil {
		t.Fatalf("count rides in state %q: %v", state, err)
	}

	return n
}

// plantPlayer inserts a player row on the fixture season (id 1 after
// cleanup) to satisfy rides_player_id_fkey (rides RESTRICT-reference
// players, ADR-018).
func plantPlayer(ctx context.Context, t *testing.T, teamID int) int {
	t.Helper()

	var playerID int
	err := pool.QueryRow(ctx, `
		INSERT INTO game.players (season_id, favorite_team_id, name)
		VALUES (1, $1, 'ride player')
		RETURNING id
	`, teamID).Scan(&playerID)
	if err != nil {
		t.Fatalf("plant player: %v", err)
	}

	return playerID
}

// plantRide stores a locked ride for the given team on the match and walks
// it to the requested state through the same UPDATE gates every writer
// faces (locked → won_pending → burned|unlocked; locked → lost). Since
// 000012 the INSERT gate admits locked only, so the walk is the only way
// to reach the other states. The result-agreement check (000011) applies to
// the walk — planting a state the fixture's scores disagree with fails
// loudly, which is the gate doing its job. The walk's own transitions are
// incidental to what tests arrange, so the final UPDATE resets version to
// 1: version assertions count from the planted state.
func plantRide(ctx context.Context, t *testing.T, matchID, teamID int, state string, acc string, streak int) int {
	t.Helper()

	var rideID int
	err := pool.QueryRow(ctx, `
		INSERT INTO game.rides (player_id, team_id, match_id, state, tokens_locked, base_at_lock, bonus_acc, streak)
		VALUES ($1, $2, $3, 'locked', 100, 100, 0, 0)
		RETURNING id
	`, plantPlayer(ctx, t, teamID), teamID, matchID).Scan(&rideID)
	if err != nil {
		t.Fatalf("plant ride: %v", err)
	}

	if state != "locked" {
		// burned/unlocked arrive only via won_pending (gate check 3).
		if state == "burned" || state == "unlocked" {
			if _, err := pool.Exec(ctx, `UPDATE game.rides SET state = 'won_pending' WHERE id = $1`, rideID); err != nil {
				t.Fatalf("plant ride walk (locked → won_pending): %v", err)
			}
		}

		// The arrival writes state and acc in one statement: the money-
		// state coupling reads NEW.bonus_acc on the lost/unlocked arrivals
		// — the declarative rides_money_state_coupling CHECK (000013).
		if _, err := pool.Exec(ctx, `
			UPDATE game.rides SET state = $2, bonus_acc = $3::numeric, streak = $4, version = 1 WHERE id = $1
		`, rideID, state, acc, streak); err != nil {
			t.Fatalf("plant ride walk (→ %s): %v", state, err)
		}
	}

	return rideID
}

// insertFutureMatch adds a pre-start match starting `lead` from now and
// returns its id.
func insertFutureMatch(ctx context.Context, t *testing.T, seasonID, roundID, homeID, awayID int, lead string) int {
	t.Helper()

	var id int
	err := pool.QueryRow(ctx, `
		INSERT INTO schedule.matches (season_id, round_id, home_team_id, away_team_id, home_odds, away_odds, status, starts_at)
		VALUES ($1, $2, $3, $4, 1.75, 2.20, 'created', now() + $5::interval)
		RETURNING id
	`, seasonID, roundID, homeID, awayID, lead).Scan(&id)
	if err != nil {
		t.Fatalf("insert future match: %v", err)
	}

	return id
}

// seedFixture re-seeds the base hierarchy (open round 1, teams 1 & 2) and
// returns (seasonID, roundID, homeID, awayID). Call at the top of every
// subtest — cleanup first, as the gate tests do, so IDs restart from a
// known point.
func seedFixture(ctx context.Context, t *testing.T) (int, int, int, int) {
	t.Helper()

	cleanup(ctx, t)
	return seed(ctx, t)
}

// TestMatchDeleteRestricted pins the Phase D shape of the ADR-018 rule:
// rides_match_id_fkey's RESTRICT is gone with the cross-context FKs
// (migration 000016), so the store alone no longer refuses a delete over
// rides — the "match still has rides" check is the api delete handler's,
// an api-level composition of schedule's and game's facts (same 409; the
// probe lives in internal/api/matches_test.go). This store-level case now
// documents the boundary: the delete goes through (rides_player_id RESTRICT
// is game-internal and stays, but nothing on THIS side references the
// match anymore).
func TestMatchDeleteRestricted(t *testing.T) {
	requireDB(t)

	ctx := context.Background()

	_, roundID, homeID, awayID := seedFixture(ctx, t)
	matchID := insertFutureMatch(ctx, t, 1, roundID, homeID, awayID, "30 days")
	plantRide(ctx, t, matchID, homeID, "locked", "0", 0)

	err := NewStore(schedulePool).Matches.Delete(ctx, matchID, 1)

	if err != nil {
		t.Fatalf("Delete() = %v, want nil (rides RESTRICT moved to the api layer, Phase D)", err)
	}

	// The delete actually deleted: the match row is gone.
	if _, err := NewStore(schedulePool).Matches.Get(ctx, matchID); !errors.Is(err, store.ErrRecordNotFound) {
		t.Fatalf("Get() after delete = %v, want ErrRecordNotFound", err)
	}
}

// closeService builds the wired stack the close-side tests run against:
// the schedule service publishing on a capture bus, so the RoundCompleted
// payload can be asserted end-to-end (the reverse-pyramid top for the
// event seam is the api harness; this pins the store-level publish).
func closeService(t *testing.T) (*Service, *[]RoundCompleted) {
	t.Helper()

	bus := eventbus.New()
	var received []RoundCompleted
	eventbus.Subscribe(bus, func(ctx context.Context, e RoundCompleted) error {
		received = append(received, e)
		return nil
	})

	svc := NewService(NewStore(schedulePool), bus, slog.New(slog.NewTextHandler(testWriter{}, nil)))
	return svc, &received
}

// testWriter adapts io.Writer from os.Stderr without another import.
type testWriter struct{}

func (testWriter) Write(p []byte) (int, error) { return len(p), nil }

// seedClosedRound hands back an open round whose matches are all settled
// (rounds_close_gate's precondition) with one undecided won_pending ride
// planted on its match, and the ride's owning player.
func seedClosedRound(ctx context.Context, t *testing.T) (int, int, int) {
	t.Helper()

	seasonID, roundID, homeID, awayID := seedFixture(ctx, t)
	// The fixture round has no matches yet: plant one, then settle it —
	// rounds_close_gate requires every match of the closing round closed.
	insertFutureMatch(ctx, t, seasonID, roundID, homeID, awayID, "3 days")
	settleRound(ctx, t, roundID)

	var matchID int
	if err := pool.QueryRow(ctx, `SELECT id FROM schedule.matches WHERE round_id = $1 LIMIT 1`, roundID).Scan(&matchID); err != nil {
		t.Fatalf("fetch match id: %v", err)
	}

	rideID := plantRide(ctx, t, matchID, homeID, "won_pending", "90", 1)

	return seasonID, roundID, rideID
}

// closeableRound hands back the stored round with the close transition
// applied — the handler contract: Close receives the round as currently
// stored plus the requested target status, and updateRound's version
// check still guards the write.
func closeableRound(ctx context.Context, t *testing.T, roundID int) Round {
	t.Helper()

	round, err := NewStore(schedulePool).Rounds.Get(ctx, roundID)
	if err != nil {
		t.Fatalf("round Get() = %v, want nil", err)
	}
	round.Status = RoundClosed

	return round
}

func TestRoundStore_Close(t *testing.T) {
	requireDB(t)

	ctx := context.Background()

	t.Run("closes and publishes RoundCompleted with the round's results", func(t *testing.T) {
		seasonID, roundID, homeID, awayID := seedFixture(ctx, t)
		matchID := insertFutureMatch(ctx, t, seasonID, roundID, homeID, awayID, "3 days")
		settleRound(ctx, t, roundID)

		svc, received := closeService(t)

		got, err := svc.Rounds.Close(ctx, closeableRound(ctx, t, roundID))
		if err != nil {
			t.Fatalf("Close() = %v, want nil", err)
		}
		if got.Status != RoundClosed {
			t.Errorf("status = %q, want closed", got.Status)
		}

		if len(*received) != 1 {
			t.Fatalf("RoundCompleted events = %d, want 1", len(*received))
		}
		event := (*received)[0]
		if event.RoundID != roundID || event.SeasonID != seasonID {
			t.Errorf("event ids = (%d, %d), want (%d, %d)", event.RoundID, event.SeasonID, roundID, seasonID)
		}
		if event.IsFinal {
			t.Error("IsFinal = true, want false (round 1 of 38)")
		}
		if event.EventID == "" {
			t.Error("EventID empty, want a dedup identity")
		}
		if len(event.Results) != 1 {
			t.Fatalf("results = %d, want 1 (the round's only match)", len(event.Results))
		}
		result := event.Results[0]
		if result.MatchID != matchID {
			t.Errorf("result match = %d, want %d", result.MatchID, matchID)
		}
		// The fixture settles 2:1 home — the winner rides in the payload.
		if result.WinnerTeamID == nil || *result.WinnerTeamID != homeID {
			t.Errorf("winner = %v, want home team %d", result.WinnerTeamID, homeID)
		}
	})

	t.Run("the season's last round publishes IsFinal", func(t *testing.T) {
		seasonID, roundID, homeID, awayID := seedFixture(ctx, t)
		renumberRoundForFixture(ctx, t, roundID, 38)
		insertFutureMatch(ctx, t, seasonID, roundID, homeID, awayID, "3 days")
		settleRound(ctx, t, roundID)

		// A live round 38 needs 37 closed rounds (the unbroken-sequence
		// gate); no realistic fixture builds that, so the close itself runs
		// with the progress gate suspended — the IsFinal predicate under
		// test is the app-side last-round check, not the sequence.
		if _, err := pool.Exec(ctx, `ALTER TABLE schedule.rounds DISABLE TRIGGER rounds_progress_gate`); err != nil {
			t.Fatalf("suspend progress gate: %v", err)
		}
		svc, received := closeService(t)

		if _, err := svc.Rounds.Close(ctx, closeableRound(ctx, t, roundID)); err != nil {
			t.Fatalf("Close() = %v, want nil", err)
		}
		if _, err := pool.Exec(ctx, `ALTER TABLE schedule.rounds ENABLE TRIGGER rounds_progress_gate`); err != nil {
			t.Fatalf("restore progress gate: %v", err)
		}

		if len(*received) != 1 || !(*received)[0].IsFinal {
			t.Fatalf("RoundCompleted events = %+v, want exactly one with IsFinal", *received)
		}
		// The close also flipped the season: the mirror of Open's flip.
		var status string
		if err := pool.QueryRow(ctx, `SELECT status FROM schedule.seasons WHERE id = $1`, seasonID).Scan(&status); err != nil {
			t.Fatalf("read season: %v", err)
		}
		if status != "closed" {
			t.Errorf("season status = %q, want closed", status)
		}
	})

	t.Run("close over undecided rides commits — settlement is eventual (000014)", func(t *testing.T) {
		_, roundID, rideID := seedClosedRound(ctx, t)

		// The relaxed rounds_close_gate (000014): undecided won_pending
		// rides no longer refuse the close. The close publishes the round's
		// results; the game context settles the ride afterwards, in its own
		// transaction. Here (no subscriber wired beyond the capture bus,
		// which settles nothing) the ride must be untouched by the close:
		// the transaction no longer writes game's table.
		if _, err := NewStore(schedulePool).Rounds.Close(ctx, closeableRound(ctx, t, roundID)); err != nil {
			t.Fatalf("Close() = %v, want nil (the undecided-rides gate is relaxed)", err)
		}

		persisted := getRideRow(ctx, t, rideID)
		if persisted.state != "won_pending" || persisted.version != 1 || persisted.acc != "90" {
			t.Errorf("ride after close = %+v, want untouched won_pending/1/90 (the close owns no ride writes)", persisted)
		}
	})

	t.Run("gate refuses a direct-writer close over unclosed matches", func(t *testing.T) {
		seasonID, roundID, homeID, awayID := seedFixture(ctx, t)
		_ = seasonID
		insertFutureMatch(ctx, t, 1, roundID, homeID, awayID, "3 days")

		// The writer that skips the settle step: rounds_close_gate's
		// unclosed-matches half (000008, kept in 000014) is the backstop —
		// P0001, not a silent close over live schedule.
		_, err := pool.Exec(ctx, closeRoundSQL, roundID)
		if err == nil {
			t.Fatal("direct close over unclosed matches = nil, want refusal")
		}
		if !store.IsTriggerViolation(err) {
			t.Fatalf("direct close = %v, want the rounds_close_gate P0001", err)
		}

		// The refusal actually refused.
		round, err := NewStore(schedulePool).Rounds.Get(ctx, roundID)
		if err != nil {
			t.Fatalf("round Get() = %v, want nil", err)
		}
		if round.Status != RoundOpen {
			t.Errorf("round status = %q, want open", round.Status)
		}
	})
}

// renumberRoundForFixture moves a live round to another number with the
// progress gate suspended: rounds_progress_gate requires an unbroken
// closed sequence (37 closed rounds before a live 38), which no realistic
// fixture builds. Fixture tooling only — production renumbering stays
// gated.
func renumberRoundForFixture(ctx context.Context, t *testing.T, roundID, number int) {
	t.Helper()

	if _, err := pool.Exec(ctx, `ALTER TABLE schedule.rounds DISABLE TRIGGER rounds_progress_gate`); err != nil {
		t.Fatalf("suspend progress gate: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE schedule.rounds SET number = $2 WHERE id = $1`, roundID, number); err != nil {
		t.Fatalf("renumber round: %v", err)
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE schedule.rounds ENABLE TRIGGER rounds_progress_gate`); err != nil {
		t.Fatalf("restore progress gate: %v", err)
	}
}

// TestRoundStore_Close_MidTxFailureLeavesNoTrace is the phase done-when:
// the season flip fails after the round flip succeeded, and nothing of the
// transaction survives.
//
// The failure is not injected by hacking the store: the fixture aims the
// close at a round whose parent season is closed, so rounds_progress_gate
// refuses the round flip (round_progress_season_not_live) — the first
// statement fails.
func TestRoundStore_Close_MidTxFailureLeavesNoTrace(t *testing.T) {
	requireDB(t)

	ctx := context.Background()

	seasonID, roundID, rideID := seedClosedRound(ctx, t)

	// Close the season — a legal forward transition (the freeze gate only
	// refuses created/open regressions) — so rounds_progress_gate refuses
	// the round's status flip: the season must be live while a round
	// closes.
	if _, err := pool.Exec(ctx, `UPDATE schedule.seasons SET status = 'closed' WHERE id = $1`, seasonID); err != nil {
		t.Fatalf("deactivate season: %v", err)
	}

	_, err := NewStore(schedulePool).Rounds.Close(ctx, closeableRound(ctx, t, roundID))
	if err == nil {
		t.Fatal("Close() = nil, want the season-not-live refusal")
	}
	// updateRound maps the rounds-gate P0001 to ErrRecordInUse (409),
	// ADR-007/008 convention.
	if !errors.Is(err, store.ErrRecordInUse) {
		t.Fatalf("Close() = %v, want ErrRecordInUse", err)
	}

	// The ride is untouched: state, acc and version all read back as
	// planted. (The close owns no ride writes since Phase C — this pins
	// that the refusal left the whole world as it was.)
	persisted := getRideRow(ctx, t, rideID)
	if persisted.state != "won_pending" {
		t.Errorf("state = %q, want won_pending (transaction rolled back)", persisted.state)
	}
	if persisted.acc != "90" {
		t.Errorf("acc = %s, want the planted 90", persisted.acc)
	}
	if persisted.version != 1 {
		t.Errorf("version = %d, want 1 (no bump)", persisted.version)
	}

	// And the round is still open.
	round, err := NewStore(schedulePool).Rounds.Get(ctx, roundID)
	if err != nil {
		t.Fatalf("round Get() = %v, want nil", err)
	}
	if round.Status != RoundOpen {
		t.Errorf("round status = %q, want open", round.Status)
	}
}

// TestRoundStore_Close_ProvesPostgresRollsBack runs Close's statement
// sequence inside a manual transaction that fails at the flip, purely to
// pin the DB-level mechanism the store's shape relies on: a write inside
// the transaction is visible on the connection's own snapshot before the
// flip fails, and vanishes with the rollback.
func TestRoundStore_Close_ProvesPostgresRollsBack(t *testing.T) {
	requireDB(t)

	ctx := context.Background()

	seasonID, roundID, rideID := seedClosedRound(ctx, t)

	if _, err := pool.Exec(ctx, `UPDATE schedule.seasons SET status = 'closed' WHERE id = $1`, seasonID); err != nil {
		t.Fatalf("deactivate season: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin() = %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `UPDATE game.rides SET state = 'unlocked', bonus_acc = 0, version = version + 1 WHERE id = $1`, rideID); err != nil {
		t.Fatalf("ride write inside manual tx: %v", err)
	}

	// Observable within the tx: the write has taken effect on the
	// connection's own snapshot.
	if n := countRidesInStateInTx(ctx, t, tx, roundID, "unlocked"); n != 1 {
		t.Fatalf("in-tx unlocked count = %d, want 1", n)
	}

	// The flip under a dead season aborts the statement (and the tx).
	if _, err := tx.Exec(ctx, `UPDATE schedule.rounds SET status = 'closed', version = version + 1 WHERE id = $1 AND version = 1`, roundID); err == nil {
		t.Fatal("flip in dead-season tx = nil, want the progress-gate refusal")
	} else if !store.IsTriggerViolation(err) {
		t.Fatalf("flip error = %v, want P0001", err)
	}

	if err := tx.Commit(ctx); err == nil {
		t.Fatal("Commit() after an aborted statement = nil, want an error")
	}

	// Outside the tx: nothing survived.
	persisted := getRideRow(ctx, t, rideID)
	if persisted.state != "won_pending" || persisted.version != 1 {
		t.Errorf("ride after rollback = state %q version %d, want untouched won_pending/1",
			persisted.state, persisted.version)
	}
}

// countRidesInStateInTx is countRidesInState against a transaction — the
// in-tx visibility probe for the rollback semantics test.
func countRidesInStateInTx(ctx context.Context, t *testing.T, tx pgx.Tx, roundID int, state string) int {
	t.Helper()

	var n int
	err := tx.QueryRow(ctx, `
		SELECT count(*) FROM game.rides rd
		JOIN schedule.matches m ON m.id = rd.match_id
		WHERE m.round_id = $1 AND rd.state = $2`, roundID, state).Scan(&n)
	if err != nil {
		t.Fatalf("count rides in tx: %v", err)
	}

	return n
}

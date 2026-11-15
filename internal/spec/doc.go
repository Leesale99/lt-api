// Package spec is the executable specification suite: the godog scenarios in
// docs/spec/features run against real PostgreSQL, through the real modules.
//
// # What the suite drives
//
// Every scenario runs against a database built from the repository's own
// migrations, opened with the per-context role-restricted pools
// internal/testdb hands out — the same harness the E2E suite uses
// (testmain_test.go mirrors it). The modules are composed exactly like
// cmd/api/main.go: one event bus, the calendar module over the schedule pool,
// the ledger, the identity module (no-op mailer) and the game module over the
// game pool, with game's identity directory and the calendar's game-reference
// adapter installed.
//
// # Facade steps over the modules' own entry points
//
// A facade step drives the module's own entry point; it never writes the rows
// under test. For the calendar and the game context those entry points are the
// modules' registered route tables: the game module's ride commands (create,
// lock/burn/unlock) and the calendar's writes exist ONLY as transport
// handlers (game.Module and schedule.Module expose reads plus the resolve use
// case — no ride command and no calendar create is on either facade), and Go's
// internal-package rule keeps the use cases themselves out of reach from this
// tree. The suite therefore registers each module's own route table on an
// in-process router and calls it with httptest recorders: no server, no
// listener, no middleware chain, no authorization gate — one handler call per
// step, straight into the product code.
//
// The calendar's read facade (PhaseForMatch, Match, Team, Season) and its
// resolve use case (ResolveMatch, which publishes MatchResultRecorded and
// RoundCompleted on the bus — the settlement seam) are called directly as
// facade methods.
//
// # Fixtures
//
// The world a scenario needs (season, rounds, teams, match days, players) is
// planted with the Owner pool, the same role internal/e2e's seed fixture
// plants with: neither module exposes a create entry point for those rows, and
// a scenario never proves a calendar rule — it proves a ride rule over a
// calendar world. Ride rows are ALWAYS produced the way the product produces
// them (create → settle → decision), never inserted in a state: the one
// exception is a sabotage scenario, whose subject is precisely a direct
// writer.
//
// The clock a scenario reads ("3 hours before round 1's first match", "2
// hours after the last one") is fixture state too: the rounds' match rows
// carry the times, so the suite moves those rows and lets the product derive
// the phase from them, exactly as it does in production.
//
// # Assertions
//
// Ride state, stake and match are read back through the module's own read
// route; the accrued bonus and the banked streak are not on the wire (the
// frozen DTO omits them), so those two are read from the ride's row. Database
// sabotage refusals are read from the driver's *pgconn.PgError: SQLSTATE plus
// the constraint or RAISE name the feature names.
//
// # @known-defect scenarios
//
// A scenario tagged @known-defect:<item> is red because of a catalogued
// misalignment-report §6 defect. The default run excludes those scenarios and
// prints a loud note for each one, so a green run never claims the behaviour is
// correct; SPEC_KNOWN_DEFECTS=1 clears the filter and runs them (the harness's
// findings).
package spec

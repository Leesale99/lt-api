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
// the ledger, the identity module (its mail transport is a capturing one —
// what the service mails is held, not delivered) and the game module over the
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
// The identity scenarios prove exactly that gate, so they drive the same route
// tables on a SECOND router wrapped in the chain cmd/api/routes.go builds:
// httpx.Authenticate resolving the presented bearer token through the identity
// module (httpx.UserLookup), then each route's own httpx.RequirePermission
// resolving the principal's role codes through the same module
// (httpx.PermissionLookup). Those steps register, activate and sign in through
// the identity module's own pre-authentication route table, so the credential
// they present is a real one the service minted; nothing is stubbed and no
// principal is injected. Both routers hang off one module instance, so the
// identity module the gate reads is the module the routes write through (and
// the one the game module holds as its identity directory).
//
// The calendar's read facade (PhaseForMatch, Match, Team, Season) and its
// resolve use case (ResolveMatch, which publishes MatchResultRecorded and
// RoundCompleted on the bus — the settlement seam) are called directly as
// facade methods.
//
// # Fixtures
//
// The calendar world the ride features need (season, rounds, teams, match days,
// players) is planted with the Owner pool, the same role internal/e2e's seed
// fixture plants with: the ride features prove ride rules over a calendar, not
// calendar rules. The calendar features build the same world through the
// calendar's own route table instead (a season, a round, a club and a fixture
// are created the way the organiser's screen creates them), so the world is
// the product's own either way and the two halves share the same maps — a
// calendar-built match day and a planted one are indistinguishable to the ride
// steps. Ride rows are ALWAYS produced the way the product produces them
// (create → settle → decision), never inserted in a state: the one exception
// is a sabotage scenario, whose subject is precisely a direct writer.
//
// A registration that is somebody's is made the way the product makes it too:
// the players feature's Background registers the player through the game
// module's create route as a signed-in member (steps_players_test.go), so the
// ownership its scenarios refuse or obey is a real principal. The pilot's
// planted registrations stay planted — those features prove ride rules and
// carry no principals — because neither module exposes a create entry point for
// the calendar world they sit in.
//
// The clock a scenario reads ("3 hours before round 1's first match", "2
// hours after the last one") is fixture state: the rounds' match rows carry
// the times, so the suite moves those rows and lets the product derive the
// phase from them, exactly as it does in production. The same holds for the
// calendar's own clock states ("has kicked off", "is under way", "has been
// played out"): they are row facts, never a stubbed clock.
//
// # Assertions
//
// Ride state, stake and match are read back through the module's own read
// route; the accrued bonus and the banked streak are not on the wire (the
// frozen DTO omits them), so those two are read from the ride's row, and a
// registration's owner is read the same way (the player DTO omits user_id)
// while the page still says which registrations exist. Database
// sabotage refusals are read from the driver's *pgconn.PgError: SQLSTATE plus
// the constraint or RAISE name the feature names.
//
// A refusal the kernel writes (unauthenticated, unactivated, missing code,
// invalid credential) is asserted by rendering the kernel's own writer, so the
// feature's name for the refusal and the sentence the service sends stay one
// fact; a refusal a handler writes (duplicate, spent activation token, already
// activated account) is asserted on the field or the answer it names. A
// credential's lifetime is read from the token's row — what was minted is the
// fact — and the same holds for an aged credential, which the fixture ages by
// moving the minted expiry (never by patching a clock the product reads).
//
// # @known-defect scenarios
//
// A scenario tagged @known-defect:<item> is red because of a catalogued
// misalignment-report §6 defect. The default run excludes those scenarios and
// prints a loud note for each one, so a green run never claims the behaviour is
// correct; SPEC_KNOWN_DEFECTS=1 clears the filter and runs them (the harness's
// findings).
package spec

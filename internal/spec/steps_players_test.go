package spec

// The registration steps (docs/spec/features/game/players.feature) and the ride
// commands' key promise (docs/spec/features/game/idempotency.feature).
//
// A registration is somebody's, so these scenarios act as real people: the
// identity worker's machinery (steps_identity_test.go) registers, activates and
// signs each of them in, and every registration is made through the game
// module's own create route on the authenticated router — never planted — so
// the user_id on the row is the principal that made it (R-GAME-012). That is
// what makes an ownership refusal a refusal and an administrator's write a
// write (R-GAME-011).
//
// Three actors share the world:
//
//	the owner          the member the players feature's Background registration
//	                   belongs to — the member named after the player
//	the second member  an activated standard user who is not the owner (Bruno)
//	the administrator  an activated administrator — the identity worker's "Admin"
//
// Assertions read registrations back through the game module's read routes as
// the administrator, so a read is never the refusal a scenario is about; "does
// this registration exist?" is the list route's answer, which is the caller's
// own view of the world.
//
// The idempotency steps send the caller's literal key, because the key is what
// the scenarios are about: the same key re-sent must replay the recorded answer
// byte for byte (ADR-024, R-GAME-018), a refused command leaves no record so its
// retry executes (R-KERNEL-004's transactional coupling), and the same key on a
// different command is a misuse the game refuses, never a replay
// (R-KERNEL-003).

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/cucumber/godog"

	"lt-api.aleksrdvn.com/internal/kernel/httpx"
)

// secondMemberName is the account the players scenarios act as when they are
// not the owner. The feature says "the second member" and never names one, so
// the fixture names the account (and its registration) after the pilot's second
// player.
const secondMemberName = "Bruno"

// idempotencyConflictMessage is the game transport's refusal for a key already
// used with a different request (adapter/inbound/http/errors.go). The feature
// names the business refusal; this sentence is what tells it apart on the wire,
// exactly as the ride steps do for their refusals.
const idempotencyConflictMessage = "this idempotency key was already used with a different request"

// The refusals a registration write or a ride command produces, rendered by the
// writers that produce them (steps_identity_test.go's refusalOf/refusedAs), so
// the feature's name for a refusal and the sentence the caller reads stay one
// fact.
var (
	notPermitted = refusalOf(httpx.Forbidden)
	absentRecord = refusalOf(httpx.NotFound)
)

func registerGamePlayerSteps(sc *godog.ScenarioContext) {
	// The world the registration scenarios run in.
	sc.Step(`^the player "([^"]+)" is registered by her member in "([^"]+)" with ([0-9]+) tokens$`, thePlayerIsRegisteredByHerMember)
	sc.Step(`^the second member registers in "([^"]+)" favouring "([^"]+)"$`, theSecondMemberRegistersFavouring)
	sc.Step(`^the second member registers in "([^"]+)" favouring a team the calendar does not carry$`, theSecondMemberRegistersFavouringAnAbsentTeam)
	sc.Step(`^the second member registers in a season the calendar does not carry$`, theSecondMemberRegistersInAnAbsentSeason)
	sc.Step(`^the owner registers again in "([^"]+)" favouring "([^"]+)"$`, theOwnerRegistersAgain)

	// What a person may do to a registration.
	sc.Step(`^the (owner|second member|administrator) changes (?:her registration|"([^"]+)"'s registration) to favour "([^"]+)"$`, actorChangesRegistration)
	sc.Step(`^the (owner|second member|administrator) changes (?:her registration|"([^"]+)"'s registration) to favour "([^"]+)", claiming the version she last read$`, actorChangesRegistrationClaimingVersion)
	sc.Step(`^the (owner|second member|administrator) withdraws (?:her registration|"([^"]+)"'s registration)$`, actorWithdrawsRegistration)
	sc.Step(`^the owner's registration has moved to "([^"]+)" since she last read it$`, theOwnersRegistrationMoved)

	// What a registration reads.
	sc.Step(`^the registration belongs to the second member$`, registrationBelongsToSecondMember)
	sc.Step(`^the registration sits in "([^"]+)"$`, registrationSitsIn)
	sc.Step(`^the registration (?:has|still has) "([^"]+)" as its favourite team$`, registrationHasFavouriteTeam)
	sc.Step(`^no registration exists for "([^"]+)" in "([^"]+)"$`, noRegistrationExistsFor)
	sc.Step(`^no registration exists for the second member in "([^"]+)"$`, noRegistrationExistsForSecondMember)
	sc.Step(`^the second member holds no registration$`, secondMemberHoldsNoRegistration)
	sc.Step(`^"([^"]+)" still holds one registration in "([^"]+)"$`, stillHoldsOneRegistration)
	sc.Step(`^the registration still exists for "([^"]+)" in "([^"]+)"$`, registrationStillExistsFor)

	// The ride command that reaches outside the calendar.
	sc.Step(`^the player locks ([0-9]+) tokens on a team the calendar does not carry for match day ([0-9]+)$`, playerLocksOnAbsentTeam)

	// The refusals. A stale version claim is named the same way here as on the
	// calendar ("the command is refused as a stale reading"), so the scenario
	// uses steps_schedule_test.go's named-conflict step rather than a second
	// spelling of the same refusal.
	sc.Step(`^the refusal names the favourite team as the field to change$`, refusalNamesFavouriteTeamAsTheFieldToChange)
	sc.Step(`^the registration is refused as an unknown season$`, registrationRefusedAsUnknownSeason)
	sc.Step(`^the command is refused as someone else's registration$`, commandRefusedAsSomeoneElsesRegistration)
	sc.Step(`^the command is refused as an unknown team$`, commandRefusedAsUnknownTeam)
	sc.Step(`^the withdrawal is refused as a registration still in use$`, withdrawalRefusedAsRegistrationInUse)

	// The key a command travels under.
	sc.Step(`^the player locks ([0-9]+) tokens on "([^"]+)" for match day ([0-9]+) under her key "([^"]+)"$`, playerLocksUnderHerKey)
	sc.Step(`^the second member locks ([0-9]+) tokens on "([^"]+)" for match day ([0-9]+) under her key "([^"]+)"$`, secondMemberLocksUnderHerKey)
	sc.Step(`^the player (burns|unlocks|continues) the ride under her key "([^"]+)"$`, playerDecidesUnderHerKey)
	sc.Step(`^the recorded answer comes back$`, theRecordedAnswerComesBack)
	sc.Step(`^the command is refused as a key that already names another command$`, commandRefusedAsKeyReuse)
	sc.Step(`^the key "([^"]+)" was claimed ([0-9]+) hours ago$`, theKeyWasClaimedHoursAgo)
	sc.Step(`^no second ride is born for "([^"]+)" on match day ([0-9]+)$`, noSecondRideIsBorn)
	sc.Step(`^a ride is born for "([^"]+)" on match day ([0-9]+)$`, aRideIsBorn)
	sc.Step(`^a second ride is born for "([^"]+)" on match day ([0-9]+)$`, aSecondRideIsBorn)
}

// --- who is acting ---------------------------------------------------------

// activatedMember is an activated standard user seeded through the facade.
func (w *world) activatedMember(name string) (*member, error) {
	m, err := w.ensureMember(name)
	if err != nil {
		return nil, err
	}
	if err := w.activateMember(m); err != nil {
		return nil, err
	}
	return m, nil
}

// secondMember is the account that acts beside the owner.
func (w *world) secondMember() (*member, error) {
	return w.activatedMember(secondMemberName)
}

// owner is the member the world's owned registration belongs to.
func (w *world) owner() (*member, error) {
	if w.ownedOwner == nil {
		return nil, fmt.Errorf("no registration was made by its member in this scenario")
	}
	return w.ownedOwner, nil
}

// actorOfRole maps the feature's words for a person onto the account behind
// them.
func (w *world) actorOfRole(ctx context.Context, role string) (*member, error) {
	switch role {
	case "owner":
		return w.owner()
	case "second member":
		return w.secondMember()
	case "administrator":
		return w.ensureAdministrator(ctx)
	}
	return nil, fmt.Errorf("no account is named %q", role)
}

// registrationFor finds the registration a step names: the empty name is the
// owner's own ("her registration").
func (w *world) registrationFor(name string) (string, int, error) {
	if name == "" {
		name = w.ownedPlayer
	}
	if name == "" {
		return "", 0, fmt.Errorf("no registration was made by its member in this scenario")
	}
	id, ok := w.players[name]
	if !ok {
		return "", 0, fmt.Errorf("no registration for %q was made in this scenario", name)
	}
	return name, id, nil
}

// favouriteTeam is the season's first club — the favourite the pilot's
// registration carries.
func (w *world) favouriteTeam() (int, error) {
	if len(w.teamOrder) == 0 {
		return 0, fmt.Errorf("no team was built in the season")
	}
	return w.teams[w.teamOrder[0]], nil
}

// absentTeamID and absentSeasonID are ids no calendar row holds: a thousand past
// the highest one the fixture planted.
func (w *world) absentTeamID(ctx context.Context) (int, error) {
	return w.queryInt(ctx, `SELECT COALESCE(max(id), 0) + 1000 FROM schedule.teams`)
}

func (w *world) absentSeasonID(ctx context.Context) (int, error) {
	return w.queryInt(ctx, `SELECT COALESCE(max(id), 0) + 1000 FROM schedule.seasons`)
}

// --- registrations through the create route --------------------------------

// registerPlayerAs makes a registration the way the product makes it: the
// member is the caller, so her principal is the row's user_id.
func (w *world) registerPlayerAs(ctx context.Context, m *member, seasonID int, name string, teamID int) (*wireResponse, error) {
	return w.asMember(m, "POST", fmt.Sprintf("/v1/seasons/%d/players", seasonID), map[string]any{
		"name":             name,
		"favorite_team_id": teamID,
	}, nil)
}

// recordPlayer keeps the registration a create answer carries.
func (w *world) recordPlayer(res *wireResponse, name string) (int, error) {
	if res.Status != http.StatusCreated {
		return 0, fmt.Errorf("register %q: %d %s", name, res.Status, res.message())
	}
	var stored struct {
		ID int `json:"id"`
	}
	if err := decodeEnvelope(res, "player", &stored); err != nil {
		return 0, err
	}
	w.players[name] = stored.ID
	return stored.ID, nil
}

// thePlayerIsRegisteredByHerMember is the players feature's Background: the
// registration is made by the member named after the player, through the create
// route, so the row belongs to a principal the ownership scenarios can obey or
// refuse (R-GAME-011, R-GAME-012). The pilot's own Background step
// (`the player "<name>" is registered in "<season>" with <n> tokens`) is
// untouched: those features prove ride rules over a world that carries no
// principals.
func thePlayerIsRegisteredByHerMember(ctx context.Context, player, season, _ string) (context.Context, error) {
	w := worldFrom(ctx)
	if w.seasonName != season {
		return stepFailed(ctx, fmt.Errorf("no season %q was built (have %q)", season, w.seasonName))
	}
	teamID, err := w.favouriteTeam()
	if err != nil {
		return stepFailed(ctx, err)
	}
	m, err := w.activatedMember(player)
	if err != nil {
		return stepFailed(ctx, err)
	}
	res, err := w.registerPlayerAs(ctx, m, w.seasonID, player, teamID)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if _, err := w.recordPlayer(res, player); err != nil {
		return stepFailed(ctx, err)
	}
	w.ownedPlayer = player
	w.ownedOwner = m
	w.lastPlayer = player
	return stepOK(ctx)
}

func theSecondMemberRegistersFavouring(ctx context.Context, season, team string) (context.Context, error) {
	w := worldFrom(ctx)
	if w.seasonName != season {
		return stepFailed(ctx, fmt.Errorf("no season %q was built (have %q)", season, w.seasonName))
	}
	teamID, ok := w.teams[team]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("team %q was never built", team))
	}
	m, err := w.secondMember()
	if err != nil {
		return stepFailed(ctx, err)
	}
	res, err := w.registerPlayerAs(ctx, m, w.seasonID, secondMemberName, teamID)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if _, err := w.recordPlayer(res, secondMemberName); err != nil {
		return stepFailed(ctx, err)
	}
	w.lastPlayer = secondMemberName
	return stepOK(ctx)
}

func theSecondMemberRegistersFavouringAnAbsentTeam(ctx context.Context, season string) (context.Context, error) {
	w := worldFrom(ctx)
	if w.seasonName != season {
		return stepFailed(ctx, fmt.Errorf("no season %q was built (have %q)", season, w.seasonName))
	}
	teamID, err := w.absentTeamID(ctx)
	if err != nil {
		return stepFailed(ctx, err)
	}
	m, err := w.secondMember()
	if err != nil {
		return stepFailed(ctx, err)
	}
	// The refusal is the scenario's subject: a refused registration records
	// nothing, so the answer is kept for the Then and nothing is tracked.
	if _, err := w.registerPlayerAs(ctx, m, w.seasonID, secondMemberName, teamID); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

func theSecondMemberRegistersInAnAbsentSeason(ctx context.Context) (context.Context, error) {
	w := worldFrom(ctx)
	seasonID, err := w.absentSeasonID(ctx)
	if err != nil {
		return stepFailed(ctx, err)
	}
	teamID, err := w.favouriteTeam()
	if err != nil {
		return stepFailed(ctx, err)
	}
	m, err := w.secondMember()
	if err != nil {
		return stepFailed(ctx, err)
	}
	if _, err := w.registerPlayerAs(ctx, m, seasonID, secondMemberName, teamID); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

func theOwnerRegistersAgain(ctx context.Context, season, team string) (context.Context, error) {
	w := worldFrom(ctx)
	if w.seasonName != season {
		return stepFailed(ctx, fmt.Errorf("no season %q was built (have %q)", season, w.seasonName))
	}
	teamID, ok := w.teams[team]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("team %q was never built", team))
	}
	m, err := w.owner()
	if err != nil {
		return stepFailed(ctx, err)
	}
	// The second registration is the refusal the Then asserts.
	if _, err := w.registerPlayerAs(ctx, m, w.seasonID, w.ownedPlayer, teamID); err != nil {
		return stepFailed(ctx, err)
	}
	w.lastPlayer = w.ownedPlayer
	return stepOK(ctx)
}

// --- changing and withdrawing ---------------------------------------------

func actorChangesRegistration(ctx context.Context, role, named, team string) (context.Context, error) {
	w := worldFrom(ctx)
	if err := w.changeRegistration(ctx, role, named, team, false); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

func actorChangesRegistrationClaimingVersion(ctx context.Context, role, named, team string) (context.Context, error) {
	w := worldFrom(ctx)
	if err := w.changeRegistration(ctx, role, named, team, true); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

// changeRegistration sends one favourite-team patch as the actor: one action,
// the use case's own command. claim sends the version she read when she last
// looked (X-Expected-Version), which is how a stale reading is refused instead
// of overwriting the newer one (R-GAME-017).
func (w *world) changeRegistration(ctx context.Context, role, named, team string, claim bool) error {
	m, err := w.actorOfRole(ctx, role)
	if err != nil {
		return err
	}
	name, playerID, err := w.registrationFor(named)
	if err != nil {
		return err
	}
	teamID, ok := w.teams[team]
	if !ok {
		return fmt.Errorf("team %q was never built", team)
	}

	headers := map[string]string{}
	if claim {
		version, err := w.lastSeenVersion(ctx, name, playerID)
		if err != nil {
			return err
		}
		headers["X-Expected-Version"] = strconv.Itoa(version)
	}

	if _, err := w.asMember(m, "PATCH",
		fmt.Sprintf("/v1/seasons/%d/players/%d", w.seasonID, playerID),
		map[string]any{"favorite_team_id": teamID}, headers); err != nil {
		return err
	}
	w.lastPlayer = name
	return nil
}

// lastSeenVersion is the version she read when she last looked at the
// registration: the recorded read when the scenario made one ("has moved …
// since she last looked at it"), otherwise the read this claim makes now.
func (w *world) lastSeenVersion(ctx context.Context, name string, playerID int) (int, error) {
	if version, ok := w.playerSeen[name]; ok {
		return version, nil
	}
	view, err := w.readRegistration(ctx, playerID)
	if err != nil {
		return 0, err
	}
	w.playerSeen[name] = view.Version
	return view.Version, nil
}

func actorWithdrawsRegistration(ctx context.Context, role, named string) (context.Context, error) {
	w := worldFrom(ctx)
	m, err := w.actorOfRole(ctx, role)
	if err != nil {
		return stepFailed(ctx, err)
	}
	name, playerID, err := w.registrationFor(named)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if _, err := w.asMember(m, "DELETE",
		fmt.Sprintf("/v1/seasons/%d/players/%d", w.seasonID, playerID), nil, nil); err != nil {
		return stepFailed(ctx, err)
	}
	w.lastPlayer = name
	return stepOK(ctx)
}

// theOwnersRegistrationMoved is the stale-read world: she looks (her read is the
// version she will claim), then another hand moves the registration through the
// same route, so her claim names a version the row has moved past.
func theOwnersRegistrationMoved(ctx context.Context, team string) (context.Context, error) {
	w := worldFrom(ctx)
	name, playerID, err := w.registrationFor("")
	if err != nil {
		return stepFailed(ctx, err)
	}
	view, err := w.readRegistration(ctx, playerID)
	if err != nil {
		return stepFailed(ctx, err)
	}
	w.playerSeen[name] = view.Version

	teamID, ok := w.teams[team]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("team %q was never built", team))
	}
	mover, err := w.ensureAdministrator(ctx)
	if err != nil {
		return stepFailed(ctx, err)
	}
	res, err := w.asMember(mover, "PATCH",
		fmt.Sprintf("/v1/seasons/%d/players/%d", w.seasonID, playerID),
		map[string]any{"favorite_team_id": teamID}, nil)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if res.Status != http.StatusOK {
		return stepFailed(ctx, fmt.Errorf("move the registration to %q: %d %s",
			team, res.Status, res.message()))
	}
	w.lastPlayer = name
	return stepOK(ctx)
}

// --- reading a registration ------------------------------------------------

// registrationView is a registration as the game module's read routes carry it.
type registrationView struct {
	ID             int    `json:"id"`
	Name           string `json:"name"`
	UserID         *int   `json:"user_id"`
	SeasonID       int    `json:"season_id"`
	FavoriteTeamID int    `json:"favorite_team_id"`
	Version        int    `json:"version"`
}

// readRegistration reads one registration through its read route, as the
// administrator — a reader the ownership under test never refuses, so the read
// itself is never the refusal a scenario is about.
func (w *world) readRegistration(ctx context.Context, playerID int) (registrationView, error) {
	reader, err := w.ensureAdministrator(ctx)
	if err != nil {
		return registrationView{}, err
	}
	res, err := w.asMember(reader, "GET", fmt.Sprintf("/v1/players/%d", playerID), nil, nil)
	if err != nil {
		return registrationView{}, err
	}
	if res.Status != http.StatusOK {
		return registrationView{}, fmt.Errorf("read registration %d: %d %s",
			playerID, res.Status, res.message())
	}
	var view registrationView
	if err := decodeEnvelope(res, "player", &view); err != nil {
		return registrationView{}, err
	}
	if err := w.fillOwners(ctx, []registrationView{view}); err != nil {
		return registrationView{}, err
	}
	view.UserID = w.owners[view.ID]
	return view, nil
}

// owners is the owner each registration read in this scenario carries, as the
// row holds it (the frozen player DTO omits user_id, like it omits the ride's
// accrual: the fact is read from the row, the caller's page still says which
// registrations exist).
func (w *world) fillOwners(ctx context.Context, listed []registrationView) error {
	if len(listed) == 0 {
		return nil
	}
	ids := make([]int32, 0, len(listed))
	for _, view := range listed {
		ids = append(ids, int32(view.ID))
	}
	rows, err := w.app.Owner.Query(ctx,
		`SELECT id, user_id FROM game.players WHERE id = ANY($1)`, ids)
	if err != nil {
		return fmt.Errorf("read the registrations' owners: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			id     int
			userID *int
		)
		if err := rows.Scan(&id, &userID); err != nil {
			return err
		}
		w.owners[id] = userID
	}
	return rows.Err()
}

// lastRegistration reads the registration the scenario's last step created or
// acted on.
func (w *world) lastRegistration(ctx context.Context) (registrationView, error) {
	_, playerID, err := w.registrationFor(w.lastPlayer)
	if err != nil {
		return registrationView{}, err
	}
	return w.readRegistration(ctx, playerID)
}

// registrations lists the registrations the caller can see, through the list
// route: "does a registration exist?" is the page's answer, not a row count.
func (w *world) registrations(ctx context.Context, name string) ([]registrationView, error) {
	reader, err := w.ensureAdministrator(ctx)
	if err != nil {
		return nil, err
	}
	path := "/v1/players?page_size=100"
	if name != "" {
		path += "&name=" + url.QueryEscape(name)
	}
	res, err := w.asMember(reader, "GET", path, nil, nil)
	if err != nil {
		return nil, err
	}
	if res.Status != http.StatusOK {
		return nil, fmt.Errorf("list the registrations: %d %s", res.Status, res.message())
	}
	var listed []registrationView
	if err := decodeEnvelope(res, "players", &listed); err != nil {
		return nil, err
	}
	if err := w.fillOwners(ctx, listed); err != nil {
		return nil, err
	}
	for i := range listed {
		listed[i].UserID = w.owners[listed[i].ID]
	}
	return listed, nil
}

func registrationBelongsToSecondMember(ctx context.Context) (context.Context, error) {
	w := worldFrom(ctx)
	second, err := w.secondMember()
	if err != nil {
		return stepFailed(ctx, err)
	}
	view, err := w.lastRegistration(ctx)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if view.UserID == nil {
		return stepFailed(ctx, fmt.Errorf("the registration belongs to nobody, want the second member"))
	}
	if *view.UserID != second.UserID {
		return stepFailed(ctx, fmt.Errorf("the registration belongs to user %d, want the second member (%d)",
			*view.UserID, second.UserID))
	}
	return stepOK(ctx)
}

func registrationSitsIn(ctx context.Context, season string) (context.Context, error) {
	w := worldFrom(ctx)
	if w.seasonName != season {
		return stepFailed(ctx, fmt.Errorf("no season %q was built (have %q)", season, w.seasonName))
	}
	view, err := w.lastRegistration(ctx)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if view.SeasonID != w.seasonID {
		return stepFailed(ctx, fmt.Errorf("the registration sits in season %d, want %q (%d)",
			view.SeasonID, season, w.seasonID))
	}
	return stepOK(ctx)
}

func registrationHasFavouriteTeam(ctx context.Context, team string) (context.Context, error) {
	w := worldFrom(ctx)
	teamID, ok := w.teams[team]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("team %q was never built", team))
	}
	view, err := w.lastRegistration(ctx)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if view.FavoriteTeamID != teamID {
		return stepFailed(ctx, fmt.Errorf("the registration favours team %d, want %q (%d)",
			view.FavoriteTeamID, team, teamID))
	}
	return stepOK(ctx)
}

func noRegistrationExistsFor(ctx context.Context, name, season string) (context.Context, error) {
	w := worldFrom(ctx)
	if w.seasonName != season {
		return stepFailed(ctx, fmt.Errorf("no season %q was built (have %q)", season, w.seasonName))
	}
	held, err := w.registrationsHeldBy(ctx, "", name, true)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if held > 0 {
		return stepFailed(ctx, fmt.Errorf("%q holds %d registration(s) in %q, want none",
			name, held, season))
	}
	return stepOK(ctx)
}

func noRegistrationExistsForSecondMember(ctx context.Context, season string) (context.Context, error) {
	w := worldFrom(ctx)
	if w.seasonName != season {
		return stepFailed(ctx, fmt.Errorf("no season %q was built (have %q)", season, w.seasonName))
	}
	second, err := w.secondMember()
	if err != nil {
		return stepFailed(ctx, err)
	}
	held, err := w.registrationsHeldBy(ctx, second.Name, secondMemberName, true)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if held > 0 {
		return stepFailed(ctx, fmt.Errorf("the second member holds %d registration(s) in %q, want none",
			held, season))
	}
	return stepOK(ctx)
}

func secondMemberHoldsNoRegistration(ctx context.Context) (context.Context, error) {
	w := worldFrom(ctx)
	second, err := w.secondMember()
	if err != nil {
		return stepFailed(ctx, err)
	}
	held, err := w.registrationsHeldBy(ctx, second.Name, secondMemberName, false)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if held > 0 {
		return stepFailed(ctx, fmt.Errorf("the second member holds %d registration(s), want none", held))
	}
	return stepOK(ctx)
}

func stillHoldsOneRegistration(ctx context.Context, name, season string) (context.Context, error) {
	w := worldFrom(ctx)
	if w.seasonName != season {
		return stepFailed(ctx, fmt.Errorf("no season %q was built (have %q)", season, w.seasonName))
	}
	held, err := w.registrationsHeldBy(ctx, "", name, true)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if held != 1 {
		return stepFailed(ctx, fmt.Errorf("%q holds %d registration(s) in %q, want one", name, held, season))
	}
	return stepOK(ctx)
}

func registrationStillExistsFor(ctx context.Context, name, season string) (context.Context, error) {
	return stillHoldsOneRegistration(ctx, name, season)
}

// registrationsHeldBy counts the caller's page entries that belong to one
// person in the scenario's season: by name, or (byMember) by the principal's
// user id — a registration the refused create never made has no name to list by
// beyond the account's own, so the by-member count asks for the whole page.
func (w *world) registrationsHeldBy(ctx context.Context, memberName, name string, inSeasonOnly bool) (int, error) {
	listName := name
	var userID int
	if memberName != "" {
		listName = ""
		m, err := w.memberOf(memberName)
		if err != nil {
			return 0, err
		}
		userID = m.UserID
	}

	listed, err := w.registrations(ctx, listName)
	if err != nil {
		return 0, err
	}

	held := 0
	for _, registration := range listed {
		if inSeasonOnly && registration.SeasonID != w.seasonID {
			continue
		}
		switch {
		case memberName != "":
			if registration.UserID != nil && *registration.UserID == userID {
				held++
			}
		default:
			if registration.Name == name {
				held++
			}
		}
	}
	return held, nil
}

// --- the refusals ----------------------------------------------------------

func refusalNamesFavouriteTeamAsTheFieldToChange(ctx context.Context) (context.Context, error) {
	w := worldFrom(ctx)
	if w.last == nil {
		return stepFailed(ctx, fmt.Errorf("no request has been made in this scenario"))
	}
	if w.last.Status != http.StatusUnprocessableEntity {
		return stepFailed(ctx, fmt.Errorf("the registration was not refused on a field: %d %s",
			w.last.Status, w.last.message()))
	}
	if message, ok := w.last.refusalFields()["favorite_team_id"]; !ok || message == "" {
		return stepFailed(ctx, fmt.Errorf("the refusal names no favourite team: %s", string(w.last.Body)))
	}
	return stepOK(ctx)
}

func registrationRefusedAsUnknownSeason(ctx context.Context) (context.Context, error) {
	return refusalAsserted(ctx, absentRecord, "an unknown season")
}

func commandRefusedAsSomeoneElsesRegistration(ctx context.Context) (context.Context, error) {
	return refusalAsserted(ctx, notPermitted, "someone else's registration")
}

func commandRefusedAsUnknownTeam(ctx context.Context) (context.Context, error) {
	return refusalAsserted(ctx, absentRecord, "an unknown team")
}

// recordInUseMessage is the game transport's refusal for a delete the database
// refuses because other rows still reference the record
// (internal/game/internal/adapter/inbound/http/errors.go::recordInUseResponse).
// The feature names the business refusal; this sentence is what tells it apart
// on the wire, exactly as the ride and key-misuse refusals are asserted.
const recordInUseMessage = "the record is referenced by other records and cannot be deleted"

// withdrawalRefusedAsRegistrationInUse asserts the withdrawal was refused as a
// record still in use: the database refused the delete because the player's
// rides still name the registration (rides_player_id_fkey, R-GAME-035), and the
// transport names that cause instead of failing opaquely.
func withdrawalRefusedAsRegistrationInUse(ctx context.Context) (context.Context, error) {
	w := worldFrom(ctx)
	if w.last == nil {
		return stepFailed(ctx, fmt.Errorf("no request has been made in this scenario"))
	}
	if w.last.Status != http.StatusConflict || w.last.message() != recordInUseMessage {
		return stepFailed(ctx, fmt.Errorf("the withdrawal was answered %d %q, want the record-in-use refusal %q",
			w.last.Status, w.last.message(), recordInUseMessage))
	}
	return stepOK(ctx)
}

func refusalAsserted(ctx context.Context, name namedRefusal, what string) (context.Context, error) {
	w := worldFrom(ctx)
	if w.last == nil {
		return stepFailed(ctx, fmt.Errorf("no request has been made in this scenario"))
	}
	if !refusedAs(w.last, name) {
		return stepFailed(ctx, fmt.Errorf("the command was not refused as %s: %d %s",
			what, w.last.Status, w.last.message()))
	}
	return stepOK(ctx)
}

// --- the ride command that reaches outside the calendar --------------------

func playerLocksOnAbsentTeam(ctx context.Context, tokens, matchDay string) (context.Context, error) {
	w := worldFrom(ctx)
	count, err := strconv.Atoi(tokens)
	if err != nil {
		return stepFailed(ctx, err)
	}
	day, err := strconv.Atoi(matchDay)
	if err != nil {
		return stepFailed(ctx, err)
	}
	matchID, ok := w.matchDays[day]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("match day %d was never built", day))
	}
	playerID, ok := w.players["Ana"]
	if !ok {
		return stepFailed(ctx, fmt.Errorf(`player "Ana" was never registered`))
	}
	teamID, err := w.absentTeamID(ctx)
	if err != nil {
		return stepFailed(ctx, err)
	}

	res, err := w.app.call("POST", "/v1/rides", map[string]any{
		"player_id":     playerID,
		"team_id":       teamID,
		"match_id":      matchID,
		"tokens_locked": count,
	}, map[string]string{"Idempotency-Key": w.app.seq.next("create")})
	if err != nil {
		return stepFailed(ctx, err)
	}
	w.last = res
	return stepOK(ctx)
}

// --- commands under the caller's own key -----------------------------------

// lockUnderKey is the lock sent with the caller's literal key: a retry of the
// identical request must come back with the recorded answer instead of
// executing again (ADR-024). The first executed answer is what the key holds;
// the request's bytes are identical because the body is the same map.
func (w *world) lockUnderKey(ctx context.Context, player, team string, matchDay, tokens int, key string) (*wireResponse, error) {
	return w.lockUnderKeyAs(ctx, nil, player, team, matchDay, tokens, key)
}

// lockUnderKeyAs is the same command sent as a caller: nil is the suite's
// fixture principal on the bare router, a member is that person herself — her
// own credential on the real chain, her own user id on the claim. The
// two-caller scenario (R-KERNEL-003) is the only caller of the second form.
func (w *world) lockUnderKeyAs(ctx context.Context, caller *member, player, team string, matchDay, tokens int, key string) (*wireResponse, error) {
	matchID, ok := w.matchDays[matchDay]
	if !ok {
		return nil, fmt.Errorf("match day %d was never built", matchDay)
	}
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
	headers := map[string]string{"Idempotency-Key": key}

	var (
		res *wireResponse
		err error
	)
	if caller == nil {
		res, err = w.app.call("POST", "/v1/rides", body, headers)
	} else {
		res, err = w.asMember(caller, "POST", "/v1/rides", body, headers)
	}
	if err != nil {
		return nil, err
	}
	w.last = res
	w.lastKey = key

	if res.Status == http.StatusCreated {
		var stored struct {
			Ride struct {
				ID int `json:"id"`
			} `json:"ride"`
		}
		if err := decodeEnvelope(res, "ride", &stored.Ride); err != nil {
			return nil, err
		}
		if !w.holdsRide(stored.Ride.ID) {
			w.rides = append(w.rides, stored.Ride.ID)
		}
		w.lastRide = stored.Ride.ID
		if _, recorded := w.keyed[key]; !recorded {
			w.keyed[key] = *res
		}
	}
	return res, nil
}

// holdsRide reports whether the ride is already one of the scenario's.
func (w *world) holdsRide(rideID int) bool {
	for _, id := range w.rides {
		if id == rideID {
			return true
		}
	}
	return false
}

// rideCommandUnderKey issues one decision command under the caller's literal
// key — the shape a retry of an executed command arrives in.
func (w *world) rideCommandUnderKey(ctx context.Context, rideID int, command, key string) (*wireResponse, error) {
	res, err := w.app.call("POST", fmt.Sprintf("/v1/rides/%d/%s", rideID, command), nil,
		map[string]string{"Idempotency-Key": key})
	if err != nil {
		return nil, err
	}
	w.last = res
	w.lastKey = key
	return res, nil
}

func playerLocksUnderHerKey(ctx context.Context, tokens, team, matchDay, key string) (context.Context, error) {
	w := worldFrom(ctx)
	count, err := strconv.Atoi(tokens)
	if err != nil {
		return stepFailed(ctx, err)
	}
	day, err := strconv.Atoi(matchDay)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if _, err := w.lockUnderKey(ctx, "Ana", team, day, count, key); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

func playerDecidesUnderHerKey(ctx context.Context, command, key string) (context.Context, error) {
	w := worldFrom(ctx)
	verb := map[string]string{"burns": "burn", "unlocks": "unlock", "continues": "lock"}[command]
	ride, err := w.currentRide(ctx)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if _, err := w.rideCommandUnderKey(ctx, ride.ID, verb, key); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

// theRecordedAnswerComesBack asserts the replay: the answer the key recorded,
// status and body byte for byte — the caller's retry learns what the first
// attempt answered, and nothing was executed a second time.
func theRecordedAnswerComesBack(ctx context.Context) (context.Context, error) {
	w := worldFrom(ctx)
	if w.last == nil {
		return stepFailed(ctx, fmt.Errorf("no command has been issued in this scenario"))
	}
	recorded, ok := w.keyed[w.lastKey]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("no answer was recorded under the key %q", w.lastKey))
	}
	if w.last.Status != recorded.Status || !bytes.Equal(w.last.Body, recorded.Body) {
		return stepFailed(ctx, fmt.Errorf("the answer is not the recorded one: %d %s, want %d %s",
			w.last.Status, w.last.message(), recorded.Status, recorded.message()))
	}
	return stepOK(ctx)
}

func commandRefusedAsKeyReuse(ctx context.Context) (context.Context, error) {
	w := worldFrom(ctx)
	if w.last == nil {
		return stepFailed(ctx, fmt.Errorf("no command has been issued in this scenario"))
	}
	if w.last.Status != http.StatusConflict || w.last.message() != idempotencyConflictMessage {
		return stepFailed(ctx, fmt.Errorf("the command was answered %d %q, want the key-misuse refusal %q",
			w.last.Status, w.last.message(), idempotencyConflictMessage))
	}
	return stepOK(ctx)
}

// noSecondRideIsBorn asserts the retry executed nothing: the caller's ride list
// for the player on the match day carries one ride, not two.
func noSecondRideIsBorn(ctx context.Context, player, matchDay string) (context.Context, error) {
	return rideCountIs(ctx, player, matchDay, 1, "one")
}

// secondMemberLocksUnderHerKey is the two-caller half of R-KERNEL-003: the
// second member picks the very key text the owner already used, as herself —
// her own credential on the real chain, so the claim carries her user id. The
// command executes under her claim; the owner's stored answer is untouched.
func secondMemberLocksUnderHerKey(ctx context.Context, tokens, team, matchDay, key string) (context.Context, error) {
	w := worldFrom(ctx)
	count, err := strconv.Atoi(tokens)
	if err != nil {
		return stepFailed(ctx, err)
	}
	day, err := strconv.Atoi(matchDay)
	if err != nil {
		return stepFailed(ctx, err)
	}
	m, err := w.secondMember()
	if err != nil {
		return stepFailed(ctx, err)
	}
	res, err := w.lockUnderKeyAs(ctx, m, secondMemberName, team, day, count, key)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if res.Status != http.StatusCreated {
		return stepFailed(ctx, fmt.Errorf("the second member locks under her key %q: %d %s", key, res.Status, res.message()))
	}
	return stepOK(ctx)
}

// theKeyWasClaimedHoursAgo ages a claim, the way the identity scenarios age a
// minted credential: the retention window is a fact about the row's own
// created_at, so the fixture moves that row instead of patching a clock the
// product reads (R-KERNEL-003's lazy sweep compares the row against now()).
func theKeyWasClaimedHoursAgo(ctx context.Context, key, hours string) (context.Context, error) {
	w := worldFrom(ctx)
	age, err := strconv.Atoi(hours)
	if err != nil {
		return stepFailed(ctx, err)
	}
	tag, err := w.app.Owner.Exec(ctx,
		`UPDATE game.idempotency_keys SET created_at = now() - make_interval(hours => $2) WHERE key = $1`,
		key, age)
	if err != nil {
		return stepFailed(ctx, fmt.Errorf("age the claim %q: %w", key, err))
	}
	if tag.RowsAffected() != 1 {
		return stepFailed(ctx, fmt.Errorf("%d claim row(s) carry the key %q, want exactly one", tag.RowsAffected(), key))
	}
	return stepOK(ctx)
}

// aRideIsBorn asserts the command executed: the player holds one ride on the
// match day.
func aRideIsBorn(ctx context.Context, player, matchDay string) (context.Context, error) {
	return rideCountIs(ctx, player, matchDay, 1, "one")
}

// aSecondRideIsBorn asserts a forgotten claim let the command run again: the
// player holds two rides on the match day, which a replay would never produce.
func aSecondRideIsBorn(ctx context.Context, player, matchDay string) (context.Context, error) {
	return rideCountIs(ctx, player, matchDay, 2, "two")
}

// rideCountIs lists the player's rides on the match day through the list route
// and asserts how many there are. The ride list is the caller's own view of the
// world, so the count is a product fact, not a row count.
func rideCountIs(ctx context.Context, player, matchDay string, want int, wantWord string) (context.Context, error) {
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

	res, err := w.app.call("GET", fmt.Sprintf("/v1/rides?player_id=%d&match_id=%d&page_size=100",
		playerID, matchID), nil, nil)
	if err != nil {
		return stepFailed(ctx, err)
	}
	w.last = res
	if res.Status != http.StatusOK {
		return stepFailed(ctx, fmt.Errorf("list the rides of %q on match day %d: %d %s",
			player, day, res.Status, res.message()))
	}
	var listed []struct {
		ID int `json:"id"`
	}
	if err := decodeEnvelope(res, "rides", &listed); err != nil {
		return stepFailed(ctx, err)
	}
	if len(listed) != want {
		return stepFailed(ctx, fmt.Errorf("the ride list carries %d ride(s) for %q on match day %d, want %s",
			len(listed), player, day, wantWord))
	}
	return stepOK(ctx)
}

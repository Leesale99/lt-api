package spec

// The identity steps: who may act (docs/spec/features/identity/{account,access}.feature).
//
// Unlike the ride and calendar steps, these drive the modules through the REAL
// middleware chain — httpx.Authenticate resolving the presented bearer token
// through the identity module, then each route's own RequirePermission gate
// resolving the principal's role codes through the same module (see
// harness_test.go's composeSpecApplication). A scenario therefore never
// asserts a principal the harness injected: it registers, activates and signs
// an account in through the identity module's own route table, receives the
// authentication token the service minted, and presents it, exactly as a
// client would.
//
// Accounts are seeded through the facade only — register → read the activation
// token out of the mail the service sent → activate with it. The one fact no
// facade surface offers is the role an account holds (registration always
// grants the standard user role, R-IDENTITY-012), so the administrator is
// promoted with the Owner pool as fixture state, the way the reference-data
// seeding itself does it.
//
// The clock a token carries is fixture state, the token-side analogue of the
// match clock: "her key from an earlier sign-in has passed its one day" moves
// the minted expiry into the past with the Owner pool, because no product path
// lets a caller age a credential.
//
// # Acting as somebody
//
// The actor plumbing is deliberately generic, because the player and settlement
// features act as several people over the same world:
//
//	w.ensureMember(name)            register her through the facade (unactivated)
//	w.activateMember(m)             spend the activation token the service mailed her
//	w.ensureMember + activate       an activated standard user
//	w.ensureAdministrator(ctx)      an activated administrator (role promoted, fixture state)
//	w.asMember(m, METHOD, path, …)  drive one gated route as her (signing her in if the
//	                                scenario never did, so her key is a real one)
//	w.gated(bearer, …)              drive one gated route with an explicit credential
//
// A member's key is therefore always a token the service minted; a refusal is
// always the real gate's answer, never a harness verdict about who she is.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"lt-api.aleksrdvn.com/internal/kernel/httpx"
)

// specPassword is the password every facade-seeded member signs in with: the
// features never state one, and the identity scenarios exercise the flows, not
// the password policy (that is the validator's business).
const specPassword = "pa55word123"

// tokenTolerance is how far a minted expiry may sit from the lifetime the rule
// states. The stamp is seconds-precision and the row is written moments after
// the clock is read, so the tolerance is about scheduling noise, not about the
// rule's value: hours or days of drift still fail.
const tokenTolerance = 5 * time.Minute

func registerIdentitySteps(sc *godog.ScenarioContext) {
	// The mail the service sends (Background).
	sc.Step(`^the mail the service sends is held$`, theMailTheServiceSendsIsHeld)

	// The accounts a scenario acts as, seeded through the facade.
	sc.Step(`^a member is registered as "([^"]+)" with the email "([^"]+)"$`, aMemberIsRegistered)
	sc.Step(`^a member is activated as "([^"]+)" with the email "([^"]+)"$`, aMemberIsActivated)
	sc.Step(`^the member "([^"]+)" is an activated account$`, theMemberIsAnActivatedAccount)
	sc.Step(`^the member "([^"]+)" is registered and has not activated$`, theMemberIsRegisteredAndNotActivated)
	sc.Step(`^the administrator is an activated account$`, theAdministratorIsAnActivatedAccount)
	sc.Step(`^the member "([^"]+)" is registered as the player "([^"]+)" in "([^"]+)" with ([0-9]+) tokens$`, theMemberIsRegisteredAsThePlayer)

	// Registration and the answer it gives.
	sc.Step(`^the visitor registers as "([^"]+)" with the email "([^"]+)"$`, theVisitorRegisters)
	sc.Step(`^the answer names "([^"]+)"$`, theAnswerNames)
	sc.Step(`^the answer names the email "([^"]+)"$`, theAnswerNamesTheEmail)
	sc.Step(`^the answer says the account is not yet activated$`, theAnswerSaysTheAccountIsNotYetActivated)
	sc.Step(`^the answer carries the stamp of when the account was made$`, theAnswerCarriesTheStampOfWhenTheAccountWasMade)
	sc.Step(`^the answer names the standard user role$`, theAnswerNamesTheStandardUserRole)
	sc.Step(`^the registration is refused as a duplicate$`, theRegistrationIsRefusedAsADuplicate)
	sc.Step(`^the refusal names the email as the field to change$`, theRefusalNamesTheEmailAsTheFieldToChange)

	// Activation: the mail, the token it carries, and the one use it has.
	sc.Step(`^the member "([^"]+)" activate[sd] her account with the activation token from her( first| second)? mail$`, theMemberActivatesHerAccount)
	sc.Step(`^the member "([^"]+)" presents the same activation token again$`, theMemberPresentsTheSameActivationToken)
	sc.Step(`^the member "([^"]+)" presents the activation token from her (first|second) mail$`, theMemberPresentsTheActivationTokenFromMail)
	sc.Step(`^her activation is refused as an expired or spent activation token$`, herActivationIsRefusedAsExpiredOrSpent)
	sc.Step(`^the member "([^"]+)" asks for a fresh activation mail$`, theMemberAsksForAFreshActivationMail)
	sc.Step(`^the member "([^"]+)" asked for a second activation mail$`, theMemberAskedForASecondActivationMail)
	sc.Step(`^the member "([^"]+)" is refused as an already activated account$`, theMemberIsRefusedAsAnAlreadyActivatedAccount)
	sc.Step(`^a fresh activation token arrives in her mail$`, aFreshActivationTokenArrivesInHerMail)

	// The lifetime a credential is minted with.
	sc.Step(`^her activation token is good for three days$`, herActivationTokenIsGoodForThreeDays)
	sc.Step(`^her sign-in token is good for one day$`, herKeyIsGoodForOneDay)
	sc.Step(`^her earlier sign-in token has passed its one day$`, herKeyHasPassedItsOneDay)

	// Signing in.
	sc.Step(`^the member "([^"]+)" signs in with her password$`, theMemberSignsInWithHerPassword)
	sc.Step(`^the member "([^"]+)" signed in with her password$`, theMemberSignsInWithHerPassword)
	sc.Step(`^the member "([^"]+)" signs in as "([^"]+)" with her password$`, theMemberSignsInAsWithHerPassword)
	sc.Step(`^the member "([^"]+)" signs in with a password that does not match hers$`, theMemberSignsInWithAWrongPassword)
	sc.Step(`^the visitor signs in as "([^"]+)" with the password "([^"]+)"$`, theVisitorSignsInAs)
	sc.Step(`^she is signed in$`, sheIsSignedIn)
	sc.Step(`^the sign-in is refused as an invalid credential$`, theSignInIsRefusedAsInvalidCredential)

	// The gated pages.
	sc.Step(`^the member "([^"]+)" opens the list of seasons$`, theMemberOpensTheListOfSeasons)
	sc.Step(`^the member "([^"]+)" opens the list of players$`, theMemberOpensTheListOfPlayers)
	sc.Step(`^the member "([^"]+)" records a team$`, theMemberRecordsATeam)
	sc.Step(`^the member "([^"]+)" renames the player "([^"]+)"$`, theMemberRenamesThePlayer)
	sc.Step(`^the visitor opens the list of seasons$`, theVisitorOpensTheListOfSeasons)
	sc.Step(`^the member "([^"]+)" opens the list of seasons with the activation token from her(?: (first|second))? mail$`, theMemberOpensSeasonsWithAnActivationToken)
	sc.Step(`^the member "([^"]+)" opens the list of seasons with that sign-in token$`, theMemberOpensSeasonsWithThatKey)
	sc.Step(`^the page is refused as unauthenticated$`, thePageIsRefusedAsUnauthenticated)
	sc.Step(`^she is refused as an unactivated account$`, sheIsRefusedAsAnUnactivatedAccount)
	sc.Step(`^she is refused as a permission she does not hold$`, sheIsRefusedAsAPermissionSheDoesNotHold)
	sc.Step(`^the page lists "([^"]+)"$`, thePageLists)
	sc.Step(`^no team was recorded$`, noTeamWasRecorded)
	sc.Step(`^the player "([^"]+)" still reads "([^"]+)"$`, thePlayerStillReads)

	// The permission matrix — reference data, read through the module.
	sc.Step(`^the standard user role carries:$`, theStandardUserRoleCarries)
	sc.Step(`^the standard user role carries no other code$`, theStandardUserRoleCarriesNoOtherCode)
	sc.Step(`^the administrator role carries all eleven codes$`, theAdministratorRoleCarriesAllElevenCodes)
}

// --- the accounts a scenario acts as --------------------------------------

// member is one account the scenarios sign in as. Every fact on it was read
// back from the service: the id, role and activation flag from the answer to
// the registration or activation, the key from the sign-in's token.
type member struct {
	Name      string
	Email     string
	Password  string
	UserID    int
	RoleID    int
	Activated bool

	// Key is the authentication token the member currently holds ("" before
	// her first sign-in) — what "opens the list of seasons with that key"
	// presents.
	Key string

	// SpentActivation is the activation token she already spent, so
	// "presents the same activation token again" presents that very token.
	SpentActivation string
}

// signIn is the outcome of the last sign-in attempt, whatever it was: the
// "she is signed in" / "refused as an invalid credential" steps read it.
type signIn struct {
	member *member
	status int
}

// userAnswer is the account as the registration and activation answers carry
// it (the frozen wire shape, dto.go).
type userAnswer struct {
	ID        int       `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	Activated bool      `json:"activated"`
	RoleID    int       `json:"role_id"`
}

// tokenAnswer is the token as the sign-in answer carries it.
type tokenAnswer struct {
	Plaintext string    `json:"user_token"`
	Expiry    time.Time `json:"expiry"`
}

// memberEmail is the address a Background member gets when the scenario does
// not name one ("the member \"Ana\" is an activated account").
func memberEmail(name string) string {
	return strings.ToLower(name) + "@example.com"
}

// memberOf finds the account a step names.
func (w *world) memberOf(name string) (*member, error) {
	m, ok := w.members[name]
	if !ok {
		return nil, fmt.Errorf("no account named %q was seeded in this scenario", name)
	}
	return m, nil
}

// ensureMember registers the named account through the facade, unless the
// scenario already did.
func (w *world) ensureMember(name string) (*member, error) {
	if m, ok := w.members[name]; ok {
		return m, nil
	}

	email := memberEmail(name)
	res, err := w.gated("", "POST", "/v1/users", map[string]any{
		"name":     name,
		"email":    email,
		"password": specPassword,
	}, nil)
	if err != nil {
		return nil, err
	}
	return w.recordRegistration(res, name, email)
}

// recordRegistration keeps the account the registration created. A refused
// registration records nothing: the scenarios that expect the refusal assert it
// in their own Then.
func (w *world) recordRegistration(res *wireResponse, name, email string) (*member, error) {
	if res.Status != http.StatusCreated {
		return nil, fmt.Errorf("register %q (%s): %d %s", name, email, res.Status, res.message())
	}

	var answer userAnswer
	if err := decodeEnvelope(res, "user", &answer); err != nil {
		return nil, err
	}

	m := &member{
		Name:      name,
		Email:     email,
		Password:  specPassword,
		UserID:    answer.ID,
		RoleID:    answer.RoleID,
		Activated: answer.Activated,
	}
	w.members[name] = m
	return m, nil
}

// activateMember spends the activation token the service mailed her: the
// member's own step, and the only way an account becomes able to act.
func (w *world) activateMember(m *member) error {
	if m.Activated {
		return nil
	}

	token, err := w.mailToken(m, 1)
	if err != nil {
		return err
	}
	return w.spendActivationToken(m, token)
}

// spendActivationToken presents one activation token and keeps what the answer
// says (the account is now activated).
func (w *world) spendActivationToken(m *member, token string) error {
	res, err := w.gated("", "PUT", "/v1/users/activated",
		map[string]any{"user_token": token}, nil)
	if err != nil {
		return err
	}
	if res.Status != http.StatusOK {
		return fmt.Errorf("activate %q with %q: %d %s", m.Name, token, res.Status, res.message())
	}

	var answer userAnswer
	if err := decodeEnvelope(res, "user", &answer); err != nil {
		return err
	}
	m.Activated = answer.Activated
	m.RoleID = answer.RoleID
	m.UserID = answer.ID
	m.SpentActivation = token
	return nil
}

// promoteToAdministrator grants the administrator role. Fixture state: role
// assignment is reference data no facade surface exposes (registration grants
// the standard user role, R-IDENTITY-012), so the one administrator a scenario
// needs is placed directly — the same way migration 000004 seeds the matrix.
func (w *world) promoteToAdministrator(ctx context.Context, m *member) error {
	roleID, err := w.roleID(ctx, "admin")
	if err != nil {
		return err
	}
	if err := w.exec(ctx, `UPDATE identity.users SET role_id = $2 WHERE id = $1`, m.UserID, roleID); err != nil {
		return err
	}
	m.RoleID = roleID
	return nil
}

// ensureAdministrator is the administrator every access scenario acts beside:
// an activated account holding the administrator role.
func (w *world) ensureAdministrator(ctx context.Context) (*member, error) {
	if m, ok := w.members["Admin"]; ok {
		return m, nil
	}

	m, err := w.ensureMember("Admin")
	if err != nil {
		return nil, err
	}
	if err := w.promoteToAdministrator(ctx, m); err != nil {
		return nil, err
	}
	if err := w.activateMember(m); err != nil {
		return nil, err
	}
	return m, nil
}

// ensureFixtureTeam plants the club a player registration names when the
// scenario built none: game.players.favorite_team_id is NOT NULL, and the
// access world is a season and its rounds only. The calendar world, planted
// with the Owner pool (doc.go) — never the subject of a scenario.
func (w *world) ensureFixtureTeam(ctx context.Context) (int, error) {
	if len(w.teamOrder) > 0 {
		return w.teams[w.teamOrder[0]], nil
	}
	if err := w.teamsInSeason(ctx, "Team A"); err != nil {
		return 0, err
	}
	return w.teams["Team A"], nil
}

// --- the gated wire --------------------------------------------------------

// gated drives one request through the real chain as the holder of bearer (""
// is anonymous), keeping the answer for the refusal steps (the same w.last the
// ride and calendar steps read).
func (w *world) gated(bearer, method, path string, body any, headers map[string]string) (*wireResponse, error) {
	res, err := w.app.callGated(method, path, body, headers, bearer)
	if err != nil {
		return nil, err
	}
	w.last = res
	return res, nil
}

// asMember drives one request as the member. A member who holds no key yet
// signs in first: the features spell the sign-in out only where the sign-in is
// what they prove, and "an activated account opens the page" means the account
// acts — with the key her own sign-in mints.
func (w *world) asMember(m *member, method, path string, body any, headers map[string]string) (*wireResponse, error) {
	if err := w.holdKey(m); err != nil {
		return nil, err
	}
	return w.gated(m.Key, method, path, body, headers)
}

// holdKey makes sure the member holds a key: her own sign-in is the only way
// one is minted, so a step that needs her credential signs her in when the
// scenario did not spell it out.
func (w *world) holdKey(m *member) error {
	if m.Key != "" {
		return nil
	}
	res, err := w.signIn(m, m.Email, m.Password)
	if err != nil {
		return err
	}
	if res.Status != http.StatusCreated {
		return fmt.Errorf("%q could not sign in to act: %d %s", m.Name, res.Status, res.message())
	}
	return nil
}

// signIn presents a password on the sign-in route — a pre-authentication flow,
// so no credential travels with it.
func (w *world) signIn(m *member, email, password string) (*wireResponse, error) {
	res, err := w.gated("", "POST", "/v1/user-tokens/authentication", map[string]any{
		"email":    email,
		"password": password,
	}, nil)
	if err != nil {
		return nil, err
	}

	w.lastSignIn = &signIn{member: m, status: res.Status}
	if res.Status == http.StatusCreated {
		var token tokenAnswer
		if err := decodeEnvelope(res, "user_token", &token); err != nil {
			return nil, err
		}
		m.Key = token.Plaintext
	}
	return res, nil
}

// --- the mail the service sends -------------------------------------------

// mailToken is the token the nth mail addressed to her carried — the way the
// member reads it from her inbox.
func (w *world) mailToken(m *member, nth int) (string, error) {
	held := w.app.Mail.to(m.Email)
	if len(held) < nth {
		return "", fmt.Errorf("%q has %d mail(s), needed the %s one", m.Email, len(held), ordinal(nth))
	}
	token := held[nth-1].Token
	if token == "" {
		return "", fmt.Errorf("the %s mail to %q carries no activation token", ordinal(nth), m.Email)
	}
	return token, nil
}

// ordinal names a mail by its position ("first", "second").
func ordinal(n int) string {
	switch n {
	case 1:
		return "first"
	case 2:
		return "second"
	}
	return fmt.Sprintf("%dth", n)
}

// mailOrdinal maps a step's "first"/"second" (or its absence) onto the mail's
// position.
func mailOrdinal(choice string) int {
	if strings.TrimSpace(choice) == "second" {
		return 2
	}
	return 1
}

// --- named refusals --------------------------------------------------------
//
// The features name a refusal ("as an unactivated account"), never a status or
// a sentence: these render the kernel writers' own answers, so the name the
// scenario uses and the sentence the service sends stay one fact.

type namedRefusal struct {
	status int
	body   string
}

func refusalOf(write func(http.ResponseWriter, *http.Request)) namedRefusal {
	recorder := httptest.NewRecorder()
	write(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	return namedRefusal{status: recorder.Code, body: strings.TrimSpace(recorder.Body.String())}
}

var (
	// unauthenticatedRequired is the gate's answer to a request with no
	// credential at all; unauthenticatedToken is httpx.Authenticate's answer to
	// one whose credential resolves to nothing (malformed, unknown, expired).
	// Both are the same refusal to the caller — 401, and no way to tell which.
	unauthenticatedRequired = refusalOf(httpx.AuthenticationRequired)
	unauthenticatedToken    = refusalOf(httpx.InvalidAuthenticationToken)
	unactivatedAccount      = refusalOf(httpx.InactiveAccount)
	noPermissionCode        = refusalOf(httpx.MissingPermission)
	noPermissionRecord      = refusalOf(httpx.Forbidden)
	invalidCredential       = refusalOf(httpx.InvalidCredentials)
)

// refusedAs reports whether the last answer is the named refusal.
func refusedAs(res *wireResponse, name namedRefusal) bool {
	return res.Status == name.status && strings.TrimSpace(string(res.Body)) == name.body
}

// --- Background ------------------------------------------------------------

func theMailTheServiceSendsIsHeld(ctx context.Context) (context.Context, error) {
	w := worldFrom(ctx)
	if w.app.Mail == nil {
		return stepFailed(ctx, fmt.Errorf("the identity module has no capturing mailer"))
	}
	w.app.Mail.reset()
	return stepOK(ctx)
}

// --- seeding the accounts --------------------------------------------------

func aMemberIsRegistered(ctx context.Context, name, email string) (context.Context, error) {
	w := worldFrom(ctx)
	res, err := w.gated("", "POST", "/v1/users", map[string]any{
		"name":     name,
		"email":    email,
		"password": specPassword,
	}, nil)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if _, err := w.recordRegistration(res, name, email); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

func aMemberIsActivated(ctx context.Context, name, email string) (context.Context, error) {
	w := worldFrom(ctx)
	if _, ok := w.members[name]; ok {
		return stepOK(ctx)
	}

	res, err := w.gated("", "POST", "/v1/users", map[string]any{
		"name":     name,
		"email":    email,
		"password": specPassword,
	}, nil)
	if err != nil {
		return stepFailed(ctx, err)
	}
	m, err := w.recordRegistration(res, name, email)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if err := w.activateMember(m); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

// theMemberIsAnActivatedAccount seeds the Background's account from its name
// alone: the access feature names no address and no password for the members
// it acts as, so both are derived (memberEmail, specPassword) and the account
// is registered, activated and left holding no key.
func theMemberIsAnActivatedAccount(ctx context.Context, name string) (context.Context, error) {
	w := worldFrom(ctx)
	if _, ok := w.members[name]; ok {
		return stepOK(ctx)
	}
	if _, err := w.ensureMember(name); err != nil {
		return stepFailed(ctx, err)
	}
	if err := w.activateMember(w.members[name]); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

func theMemberIsRegisteredAndNotActivated(ctx context.Context, name string) (context.Context, error) {
	w := worldFrom(ctx)
	if _, err := w.ensureMember(name); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

func theAdministratorIsAnActivatedAccount(ctx context.Context) (context.Context, error) {
	w := worldFrom(ctx)
	if _, err := w.ensureAdministrator(ctx); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

// theMemberIsRegisteredAsThePlayer registers the member's own player through
// the game module's route — the member is the actor, so the registration
// belongs to her (which is what makes another member's write to it a refusal).
// The token figure is the pilot's bankroll convention (the ledger has no
// balances yet), so it is not read here.
func theMemberIsRegisteredAsThePlayer(ctx context.Context, memberName, playerName, season, _ string) (context.Context, error) {
	w := worldFrom(ctx)
	m, err := w.memberOf(memberName)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if !m.Activated {
		return stepFailed(ctx, fmt.Errorf("member %q has not activated her account", memberName))
	}
	if w.seasonName != season {
		return stepFailed(ctx, fmt.Errorf("no season %q was built (have %q)", season, w.seasonName))
	}
	teamID, err := w.ensureFixtureTeam(ctx)
	if err != nil {
		return stepFailed(ctx, err)
	}

	res, err := w.asMember(m, "POST", fmt.Sprintf("/v1/seasons/%d/players", w.seasonID), map[string]any{
		"name":             playerName,
		"favorite_team_id": teamID,
	}, nil)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if res.Status != http.StatusCreated {
		return stepFailed(ctx, fmt.Errorf("register the player %q as %q: %d %s",
			playerName, memberName, res.Status, res.message()))
	}

	var stored struct {
		ID int `json:"id"`
	}
	if err := decodeEnvelope(res, "player", &stored); err != nil {
		return stepFailed(ctx, err)
	}
	w.players[playerName] = stored.ID
	return stepOK(ctx)
}

// --- registration and its answer ------------------------------------------

func theVisitorRegisters(ctx context.Context, name, email string) (context.Context, error) {
	w := worldFrom(ctx)
	res, err := w.gated("", "POST", "/v1/users", map[string]any{
		"name":     name,
		"email":    email,
		"password": specPassword,
	}, nil)
	if err != nil {
		return stepFailed(ctx, err)
	}
	// A refused registration seeds nothing: the scenario's Then asserts the
	// refusal, and the visitor stays a visitor.
	if res.Status == http.StatusCreated {
		if _, err := w.recordRegistration(res, name, email); err != nil {
			return stepFailed(ctx, err)
		}
	}
	return stepOK(ctx)
}

// lastUser is the account the last answer carries, failing the step when the
// answer carries none.
func lastUser(w *world) (userAnswer, error) {
	if w.last == nil {
		return userAnswer{}, fmt.Errorf("no request has been made in this scenario")
	}
	if w.last.Status != http.StatusCreated && w.last.Status != http.StatusOK {
		return userAnswer{}, fmt.Errorf("the last answer was a refusal: %d %s", w.last.Status, w.last.message())
	}
	var user userAnswer
	if err := decodeEnvelope(w.last, "user", &user); err != nil {
		return userAnswer{}, err
	}
	return user, nil
}

func theAnswerNames(ctx context.Context, name string) (context.Context, error) {
	w := worldFrom(ctx)
	user, err := lastUser(w)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if user.Name != name {
		return stepFailed(ctx, fmt.Errorf("the answer names %q, want %q", user.Name, name))
	}
	return stepOK(ctx)
}

func theAnswerNamesTheEmail(ctx context.Context, email string) (context.Context, error) {
	w := worldFrom(ctx)
	user, err := lastUser(w)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if !strings.EqualFold(user.Email, email) {
		return stepFailed(ctx, fmt.Errorf("the answer names the email %q, want %q", user.Email, email))
	}
	return stepOK(ctx)
}

func theAnswerSaysTheAccountIsNotYetActivated(ctx context.Context) (context.Context, error) {
	w := worldFrom(ctx)
	user, err := lastUser(w)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if user.Activated {
		return stepFailed(ctx, fmt.Errorf("the answer says the account %q is activated", user.Email))
	}
	return stepOK(ctx)
}

func theAnswerCarriesTheStampOfWhenTheAccountWasMade(ctx context.Context) (context.Context, error) {
	w := worldFrom(ctx)
	user, err := lastUser(w)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if user.CreatedAt.IsZero() {
		return stepFailed(ctx, fmt.Errorf("the answer carries no stamp of when the account was made"))
	}
	if age := now().Sub(user.CreatedAt); age > tokenTolerance || age < -tokenTolerance {
		return stepFailed(ctx, fmt.Errorf("the answer's stamp is %s away from now", age))
	}
	return stepOK(ctx)
}

func theAnswerNamesTheStandardUserRole(ctx context.Context) (context.Context, error) {
	w := worldFrom(ctx)
	user, err := lastUser(w)
	if err != nil {
		return stepFailed(ctx, err)
	}
	roleID, err := w.roleID(ctx, "user")
	if err != nil {
		return stepFailed(ctx, err)
	}
	if user.RoleID != roleID {
		return stepFailed(ctx, fmt.Errorf("the answer names role %d, want the standard user role (%d)",
			user.RoleID, roleID))
	}
	return stepOK(ctx)
}

// theRegistrationIsRefusedAsADuplicate is the shared "you already registered"
// refusal: an account's duplicate is a 422 naming the email (the registration
// handler's field error), a player's duplicate is the game handler's 409 — one
// business refusal, two surfaces. The account's field is pinned by its own
// step below; this one asserts the refusal itself.
func theRegistrationIsRefusedAsADuplicate(ctx context.Context) (context.Context, error) {
	w := worldFrom(ctx)
	if w.last == nil {
		return stepFailed(ctx, fmt.Errorf("no request has been made in this scenario"))
	}
	switch w.last.Status {
	case http.StatusUnprocessableEntity, http.StatusConflict:
		return stepOK(ctx)
	}
	return stepFailed(ctx, fmt.Errorf("the registration was not refused as a duplicate: %d %s",
		w.last.Status, w.last.message()))
}

func theRefusalNamesTheEmailAsTheFieldToChange(ctx context.Context) (context.Context, error) {
	w := worldFrom(ctx)
	if w.last == nil {
		return stepFailed(ctx, fmt.Errorf("no request has been made in this scenario"))
	}
	fields := w.last.refusalFields()
	if _, ok := fields["email"]; !ok {
		return stepFailed(ctx, fmt.Errorf("the refusal names no email field: %v", fields))
	}
	return stepOK(ctx)
}

// --- activation ------------------------------------------------------------

func theMemberActivatesHerAccount(ctx context.Context, name, choice string) (context.Context, error) {
	w := worldFrom(ctx)
	m, err := w.memberOf(name)
	if err != nil {
		return stepFailed(ctx, err)
	}
	token, err := w.mailToken(m, mailOrdinal(choice))
	if err != nil {
		return stepFailed(ctx, err)
	}
	if err := w.spendActivationToken(m, token); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

func theMemberPresentsTheSameActivationToken(ctx context.Context, name string) (context.Context, error) {
	w := worldFrom(ctx)
	m, err := w.memberOf(name)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if m.SpentActivation == "" {
		return stepFailed(ctx, fmt.Errorf("%q has spent no activation token in this scenario", name))
	}
	if _, err := w.gated("", "PUT", "/v1/users/activated",
		map[string]any{"user_token": m.SpentActivation}, nil); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

func theMemberPresentsTheActivationTokenFromMail(ctx context.Context, name, choice string) (context.Context, error) {
	w := worldFrom(ctx)
	m, err := w.memberOf(name)
	if err != nil {
		return stepFailed(ctx, err)
	}
	token, err := w.mailToken(m, mailOrdinal(choice))
	if err != nil {
		return stepFailed(ctx, err)
	}
	if _, err := w.gated("", "PUT", "/v1/users/activated",
		map[string]any{"user_token": token}, nil); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

// herActivationIsRefusedAsExpiredOrSpent reads the activation handler's field
// refusal: a token that no longer resolves — spent, expired, or minted for
// another purpose — is one answer, never a distinguishable state.
func herActivationIsRefusedAsExpiredOrSpent(ctx context.Context) (context.Context, error) {
	w := worldFrom(ctx)
	if w.last == nil {
		return stepFailed(ctx, fmt.Errorf("no request has been made in this scenario"))
	}
	if w.last.Status != http.StatusUnprocessableEntity {
		return stepFailed(ctx, fmt.Errorf("the activation was not refused: %d %s",
			w.last.Status, w.last.message()))
	}
	if message, ok := w.last.refusalFields()["user_token"]; !ok || message == "" {
		return stepFailed(ctx, fmt.Errorf("the refusal does not name the activation token: %s",
			string(w.last.Body)))
	}
	return stepOK(ctx)
}

func theMemberAsksForAFreshActivationMail(ctx context.Context, name string) (context.Context, error) {
	w := worldFrom(ctx)
	m, err := w.memberOf(name)
	if err != nil {
		return stepFailed(ctx, err)
	}
	w.mailMember = m
	w.mailCount = len(w.app.Mail.to(m.Email))
	// The request may be refused (an activated account cannot collect one):
	// the scenario's Then says which of the two it is.
	if _, err := w.gated("", "POST", "/v1/user-tokens/activation",
		map[string]any{"email": m.Email}, nil); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

func theMemberAskedForASecondActivationMail(ctx context.Context, name string) (context.Context, error) {
	w := worldFrom(ctx)
	m, err := w.memberOf(name)
	if err != nil {
		return stepFailed(ctx, err)
	}
	res, err := w.gated("", "POST", "/v1/user-tokens/activation",
		map[string]any{"email": m.Email}, nil)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if res.Status != http.StatusAccepted {
		return stepFailed(ctx, fmt.Errorf("a second activation mail for %q: %d %s",
			m.Email, res.Status, res.message()))
	}
	return stepOK(ctx)
}

func theMemberIsRefusedAsAnAlreadyActivatedAccount(ctx context.Context, name string) (context.Context, error) {
	w := worldFrom(ctx)
	if w.last == nil {
		return stepFailed(ctx, fmt.Errorf("no request has been made in this scenario"))
	}
	if w.last.Status != http.StatusUnprocessableEntity {
		return stepFailed(ctx, fmt.Errorf("the request was not refused: %d %s", w.last.Status, w.last.message()))
	}
	if message, ok := w.last.refusalFields()["email"]; !ok || message == "" {
		return stepFailed(ctx, fmt.Errorf("the refusal does not name the account's email: %s", string(w.last.Body)))
	}
	return stepOK(ctx)
}

func aFreshActivationTokenArrivesInHerMail(ctx context.Context) (context.Context, error) {
	w := worldFrom(ctx)
	if w.mailMember == nil {
		return stepFailed(ctx, fmt.Errorf("no fresh activation mail was asked for in this scenario"))
	}

	held := w.app.Mail.to(w.mailMember.Email)
	if len(held) <= w.mailCount {
		return stepFailed(ctx, fmt.Errorf("no fresh activation mail arrived for %q (%d held before the request)",
			w.mailMember.Email, w.mailCount))
	}
	if held[len(held)-1].Token == "" {
		return stepFailed(ctx, fmt.Errorf("the newest mail to %q carries no activation token", w.mailMember.Email))
	}
	return stepOK(ctx)
}

// --- token lifetimes -------------------------------------------------------

// mintedExpiry reads the expiry the store holds for the newest token of a
// scope: the rule is about what was MINTED, not about what the answer says.
func (w *world) mintedExpiry(ctx context.Context, m *member, scope string) (time.Time, error) {
	var expiry time.Time
	err := w.app.Owner.QueryRow(ctx, `
		SELECT expiry FROM identity.user_tokens
		WHERE user_id = $1 AND scope = $2
		ORDER BY expiry DESC
		LIMIT 1
	`, m.UserID, scope).Scan(&expiry)
	if err != nil {
		return time.Time{}, fmt.Errorf("read the %s token minted for %q: %w", scope, m.Email, err)
	}
	return expiry, nil
}

// assertTokenLifetime is the one assertion both lifetime scenarios make: the
// stamp the store holds sits a lifetime away from now, within the tolerance
// the seconds-precision stamp needs.
func (w *world) assertTokenLifetime(ctx context.Context, m *member, scope string, lifetime time.Duration) error {
	expiry, err := w.mintedExpiry(ctx, m, scope)
	if err != nil {
		return err
	}
	if drift := now().Add(lifetime).Sub(expiry); drift > tokenTolerance || drift < -tokenTolerance {
		return fmt.Errorf("the %s token for %q expires %s from now, want %s (±%s)",
			scope, m.Email, time.Until(expiry).Round(time.Second), lifetime, tokenTolerance)
	}
	return nil
}

func herActivationTokenIsGoodForThreeDays(ctx context.Context) (context.Context, error) {
	w := worldFrom(ctx)
	m, err := w.scenarioMember()
	if err != nil {
		return stepFailed(ctx, err)
	}
	if err := w.assertTokenLifetime(ctx, m, "activation", 3*24*time.Hour); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

func herKeyIsGoodForOneDay(ctx context.Context) (context.Context, error) {
	w := worldFrom(ctx)
	m, err := w.scenarioMember()
	if err != nil {
		return stepFailed(ctx, err)
	}
	if err := w.assertTokenLifetime(ctx, m, "authentication", 24*time.Hour); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

// herKeyHasPassedItsOneDay ages the credential: a real sign-in mints the key
// (when the scenario has not already), then its expiry is moved into the past
// with the Owner pool. Fixture state — the token-side clock, exactly as the
// match clock moves a kickoff; no product path lets a caller age a key.
func herKeyHasPassedItsOneDay(ctx context.Context) (context.Context, error) {
	w := worldFrom(ctx)
	m, err := w.scenarioMember()
	if err != nil {
		return stepFailed(ctx, err)
	}
	if m.Key == "" {
		if err := w.holdKey(m); err != nil {
			return stepFailed(ctx, err)
		}
	}

	if err := w.exec(ctx, `
		UPDATE identity.user_tokens
		SET expiry = date_trunc('second', now() - make_interval(secs => $3))
		WHERE user_id = $1 AND scope = $2
	`, m.UserID, "authentication", (25 * time.Hour).Seconds()); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

// scenarioMember is the account the scenario's steps last named. The lifetime
// scenarios name one member in their body; when several were seeded (access's
// Background), the most recently created is the one whose token was just
// minted.
func (w *world) scenarioMember() (*member, error) {
	if w.lastSignIn != nil && w.lastSignIn.member != nil {
		return w.lastSignIn.member, nil
	}
	var newest *member
	for _, m := range w.members {
		if newest == nil || m.UserID > newest.UserID {
			newest = m
		}
	}
	if newest == nil {
		return nil, fmt.Errorf("no account was seeded in this scenario")
	}
	return newest, nil
}

// --- signing in ------------------------------------------------------------

func theMemberSignsInWithHerPassword(ctx context.Context, name string) (context.Context, error) {
	w := worldFrom(ctx)
	m, err := w.memberOf(name)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if _, err := w.signIn(m, m.Email, m.Password); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

func theMemberSignsInAsWithHerPassword(ctx context.Context, name, email string) (context.Context, error) {
	w := worldFrom(ctx)
	m, err := w.memberOf(name)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if _, err := w.signIn(m, email, m.Password); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

func theMemberSignsInWithAWrongPassword(ctx context.Context, name string) (context.Context, error) {
	w := worldFrom(ctx)
	m, err := w.memberOf(name)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if _, err := w.signIn(m, m.Email, m.Password+"-not-hers"); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

func theVisitorSignsInAs(ctx context.Context, email, password string) (context.Context, error) {
	w := worldFrom(ctx)
	if _, err := w.signIn(&member{Email: email}, email, password); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

func sheIsSignedIn(ctx context.Context) (context.Context, error) {
	w := worldFrom(ctx)
	attempt := w.lastSignIn
	if attempt == nil {
		return stepFailed(ctx, fmt.Errorf("no sign-in was made in this scenario"))
	}
	if attempt.status != http.StatusCreated {
		return stepFailed(ctx, fmt.Errorf("the sign-in was refused: %d %s", attempt.status, w.last.message()))
	}
	if attempt.member == nil || attempt.member.Key == "" {
		return stepFailed(ctx, fmt.Errorf("the sign-in answered without a key"))
	}
	return stepOK(ctx)
}

func theSignInIsRefusedAsInvalidCredential(ctx context.Context) (context.Context, error) {
	w := worldFrom(ctx)
	if w.last == nil {
		return stepFailed(ctx, fmt.Errorf("no request has been made in this scenario"))
	}
	if !refusedAs(w.last, invalidCredential) {
		return stepFailed(ctx, fmt.Errorf("the sign-in was not refused as an invalid credential: %d %s",
			w.last.Status, w.last.message()))
	}
	return stepOK(ctx)
}

// --- the gated pages -------------------------------------------------------

func theMemberOpensTheListOfSeasons(ctx context.Context, name string) (context.Context, error) {
	w := worldFrom(ctx)
	m, err := w.memberOf(name)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if _, err := w.asMember(m, "GET", "/v1/seasons", nil, nil); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

func theMemberOpensTheListOfPlayers(ctx context.Context, name string) (context.Context, error) {
	w := worldFrom(ctx)
	m, err := w.memberOf(name)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if _, err := w.asMember(m, "GET", "/v1/players", nil, nil); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

func theMemberRecordsATeam(ctx context.Context, name string) (context.Context, error) {
	w := worldFrom(ctx)
	m, err := w.memberOf(name)
	if err != nil {
		return stepFailed(ctx, err)
	}
	recorded, err := w.teamCount(ctx)
	if err != nil {
		return stepFailed(ctx, err)
	}
	w.teamsRecorded = recorded

	if _, err := w.asMember(m, "POST", "/v1/teams", map[string]any{
		"name":        "Team She Recorded",
		"logo":        "https://spec.example/recorded.png",
		"description": "a club the member tries to record",
	}, nil); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

func theMemberRenamesThePlayer(ctx context.Context, name, player string) (context.Context, error) {
	w := worldFrom(ctx)
	m, err := w.memberOf(name)
	if err != nil {
		return stepFailed(ctx, err)
	}
	playerID, ok := w.players[player]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("player %q was never registered in this scenario", player))
	}
	if _, err := w.asMember(m, "PATCH",
		fmt.Sprintf("/v1/seasons/%d/players/%d", w.seasonID, playerID),
		map[string]any{"name": player + " II"}, nil); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

func theVisitorOpensTheListOfSeasons(ctx context.Context) (context.Context, error) {
	w := worldFrom(ctx)
	if _, err := w.gated("", "GET", "/v1/seasons", nil, nil); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

func theMemberOpensSeasonsWithAnActivationToken(ctx context.Context, name, choice string) (context.Context, error) {
	w := worldFrom(ctx)
	m, err := w.memberOf(name)
	if err != nil {
		return stepFailed(ctx, err)
	}
	token, err := w.mailToken(m, mailOrdinal(choice))
	if err != nil {
		return stepFailed(ctx, err)
	}
	if _, err := w.gated(token, "GET", "/v1/seasons", nil, nil); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

func theMemberOpensSeasonsWithThatKey(ctx context.Context, name string) (context.Context, error) {
	w := worldFrom(ctx)
	m, err := w.memberOf(name)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if m.Key == "" {
		return stepFailed(ctx, fmt.Errorf("%q holds no key: she has not signed in", name))
	}
	if _, err := w.asMember(m, "GET", "/v1/seasons", nil, nil); err != nil {
		return stepFailed(ctx, err)
	}
	return stepOK(ctx)
}

// thePageIsRefusedAsUnauthenticated accepts both of the gate's
// unauthenticated answers: the one a request with no credential receives and
// the one a credential that resolves to nothing receives. They are the same
// refusal to the caller (401), which is exactly what R-IDENTITY-008 states.
func thePageIsRefusedAsUnauthenticated(ctx context.Context) (context.Context, error) {
	w := worldFrom(ctx)
	if w.last == nil {
		return stepFailed(ctx, fmt.Errorf("no request has been made in this scenario"))
	}
	if refusedAs(w.last, unauthenticatedRequired) || refusedAs(w.last, unauthenticatedToken) {
		return stepOK(ctx)
	}
	return stepFailed(ctx, fmt.Errorf("the page was not refused as unauthenticated: %d %s",
		w.last.Status, w.last.message()))
}

func sheIsRefusedAsAnUnactivatedAccount(ctx context.Context) (context.Context, error) {
	w := worldFrom(ctx)
	if w.last == nil {
		return stepFailed(ctx, fmt.Errorf("no request has been made in this scenario"))
	}
	if !refusedAs(w.last, unactivatedAccount) {
		return stepFailed(ctx, fmt.Errorf("she was not refused as an unactivated account: %d %s",
			w.last.Status, w.last.message()))
	}
	return stepOK(ctx)
}

// sheIsRefusedAsAPermissionSheDoesNotHold accepts BOTH sentences a missing
// authorization produces: the gate's own missing-code refusal (the role
// carries nothing the page asks for) and the game handler's domain refusal
// (the role carries players:write but not players:write:any, so the page is
// served and the write to another member's player is refused). One business
// refusal — "she may not do this" — two sites, exactly as R-IDENTITY-017
// states; the status is the same 403 for both.
func sheIsRefusedAsAPermissionSheDoesNotHold(ctx context.Context) (context.Context, error) {
	w := worldFrom(ctx)
	if w.last == nil {
		return stepFailed(ctx, fmt.Errorf("no request has been made in this scenario"))
	}
	if refusedAs(w.last, noPermissionCode) || refusedAs(w.last, noPermissionRecord) {
		return stepOK(ctx)
	}
	return stepFailed(ctx, fmt.Errorf("she was not refused as a permission she does not hold: %d %s",
		w.last.Status, w.last.message()))
}

// thePageLists asserts the world's own season is on the page: the calendar's
// seasons carry no name field, so "Spring 2026" is the scenario's label for
// the season it built and the page listing it means that season's id.
func thePageLists(ctx context.Context, season string) (context.Context, error) {
	w := worldFrom(ctx)
	if w.last == nil {
		return stepFailed(ctx, fmt.Errorf("no request has been made in this scenario"))
	}
	if w.last.Status != http.StatusOK {
		return stepFailed(ctx, fmt.Errorf("the page was not served: %d %s", w.last.Status, w.last.message()))
	}
	if w.seasonName != season {
		return stepFailed(ctx, fmt.Errorf("no season %q was built (have %q)", season, w.seasonName))
	}

	var listed []seasonView
	if err := decodeEnvelope(w.last, "seasons", &listed); err != nil {
		return stepFailed(ctx, err)
	}
	for _, season := range listed {
		if season.ID == w.seasonID {
			return stepOK(ctx)
		}
	}
	return stepFailed(ctx, fmt.Errorf("the page lists %d season(s), none of them the world's %q",
		len(listed), w.seasonName))
}

func noTeamWasRecorded(ctx context.Context) (context.Context, error) {
	w := worldFrom(ctx)
	recorded, err := w.teamCount(ctx)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if recorded != w.teamsRecorded {
		return stepFailed(ctx, fmt.Errorf("the calendar carries %d team(s), %d before the write",
			recorded, w.teamsRecorded))
	}
	return stepOK(ctx)
}

// thePlayerStillReads reads the registration back through the game module's
// read route — as the administrator, so the read itself is never the refusal
// under test.
func thePlayerStillReads(ctx context.Context, player, name string) (context.Context, error) {
	w := worldFrom(ctx)
	playerID, ok := w.players[player]
	if !ok {
		return stepFailed(ctx, fmt.Errorf("player %q was never registered in this scenario", player))
	}
	admin, err := w.ensureAdministrator(ctx)
	if err != nil {
		return stepFailed(ctx, err)
	}

	res, err := w.asMember(admin, "GET", fmt.Sprintf("/v1/players/%d", playerID), nil, nil)
	if err != nil {
		return stepFailed(ctx, err)
	}
	if res.Status != http.StatusOK {
		return stepFailed(ctx, fmt.Errorf("read the player %q: %d %s", player, res.Status, res.message()))
	}

	var read struct {
		Name string `json:"name"`
	}
	if err := decodeEnvelope(res, "player", &read); err != nil {
		return stepFailed(ctx, err)
	}
	if read.Name != name {
		return stepFailed(ctx, fmt.Errorf("the player %q reads %q", player, read.Name))
	}
	return stepOK(ctx)
}

// --- the permission matrix -------------------------------------------------

// roleID is the id of a seeded role (reference data: 'admin' and 'user').
func (w *world) roleID(ctx context.Context, name string) (int, error) {
	return w.queryInt(ctx, `SELECT id FROM identity.roles WHERE name = $1`, name)
}

// codesOfRole reads a role's permission codes through the identity module —
// the same lookup the permission gate runs.
func (w *world) codesOfRole(ctx context.Context, name string) ([]string, error) {
	roleID, err := w.roleID(ctx, name)
	if err != nil {
		return nil, err
	}
	permissions, err := w.app.Identity.PermissionsForRole(ctx, roleID)
	if err != nil {
		return nil, fmt.Errorf("read the permissions of role %q: %w", name, err)
	}
	codes := append([]string(nil), permissions...)
	sort.Strings(codes)
	return codes, nil
}

func theStandardUserRoleCarries(ctx context.Context, table *godog.Table) (context.Context, error) {
	w := worldFrom(ctx)
	header := table.Rows[0].Cells
	column := -1
	for i, cell := range header {
		if cell.Value == "code" {
			column = i
		}
	}
	if column < 0 {
		return stepFailed(ctx, fmt.Errorf("the table has no %q column", "code"))
	}

	var listed []string
	for _, row := range table.Rows[1:] {
		listed = append(listed, row.Cells[column].Value)
	}
	sort.Strings(listed)

	codes, err := w.codesOfRole(ctx, "user")
	if err != nil {
		return stepFailed(ctx, err)
	}
	if strings.Join(codes, ",") != strings.Join(listed, ",") {
		return stepFailed(ctx, fmt.Errorf("the standard user role carries %v, the table lists %v", codes, listed))
	}

	w.listedCodes = listed
	return stepOK(ctx)
}

func theStandardUserRoleCarriesNoOtherCode(ctx context.Context) (context.Context, error) {
	w := worldFrom(ctx)
	codes, err := w.codesOfRole(ctx, "user")
	if err != nil {
		return stepFailed(ctx, err)
	}
	if len(w.listedCodes) == 0 {
		return stepFailed(ctx, fmt.Errorf("no matrix was listed before this step"))
	}
	if len(codes) != len(w.listedCodes) {
		return stepFailed(ctx, fmt.Errorf("the standard user role carries %d codes, the table listed %d: %v",
			len(codes), len(w.listedCodes), codes))
	}
	return stepOK(ctx)
}

func theAdministratorRoleCarriesAllElevenCodes(ctx context.Context) (context.Context, error) {
	w := worldFrom(ctx)
	codes, err := w.codesOfRole(ctx, "admin")
	if err != nil {
		return stepFailed(ctx, err)
	}

	var seeded []string
	rows, err := w.app.Owner.Query(ctx, `SELECT code FROM identity.permissions`)
	if err != nil {
		return stepFailed(ctx, fmt.Errorf("list the seeded permission codes: %w", err))
	}
	defer rows.Close()
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			return stepFailed(ctx, err)
		}
		seeded = append(seeded, code)
	}
	if err := rows.Err(); err != nil {
		return stepFailed(ctx, err)
	}
	sort.Strings(seeded)

	if len(codes) != 11 {
		return stepFailed(ctx, fmt.Errorf("the administrator role carries %d codes, want 11: %v", len(codes), codes))
	}
	if strings.Join(codes, ",") != strings.Join(seeded, ",") {
		return stepFailed(ctx, fmt.Errorf("the administrator role carries %v, the seeded matrix is %v", codes, seeded))
	}
	return stepOK(ctx)
}

// teamCount is the number of clubs on the calendar ("no team was recorded").
func (w *world) teamCount(ctx context.Context) (int, error) {
	return w.queryInt(ctx, `SELECT count(*) FROM schedule.teams`)
}

package spec

// The composition root: the same modules cmd/api/main.go builds, over the
// per-context pools internal/testdb opened, with no server. Two entry points
// hang off it: the bare router the ride and calendar steps drive (no
// middleware chain — see doc.go), and the authenticated router the identity
// steps drive, carrying the same route tables behind the real
// Authenticate → RequirePermission chain.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/julienschmidt/httprouter"

	"lt-api.aleksrdvn.com/internal/game"
	"lt-api.aleksrdvn.com/internal/identity"
	"lt-api.aleksrdvn.com/internal/kernel/eventbus"
	"lt-api.aleksrdvn.com/internal/kernel/httpx"
	"lt-api.aleksrdvn.com/internal/kernel/idempotency"
	"lt-api.aleksrdvn.com/internal/ledger"
	"lt-api.aleksrdvn.com/internal/schedule"
	"lt-api.aleksrdvn.com/internal/testdb"
)

// specApp is the process the scenarios run against: the modules, the bus they
// share, the owner pool the fixtures and sabotage writes use, the mailer the
// identity module mails through, and the two routers carrying the modules' own
// route tables — bare (pilot/calendar steps) and authenticated (identity
// steps).
type specApp struct {
	Bus      *eventbus.Bus
	Schedule *schedule.Module
	Game     *game.Module
	Identity *identity.Module
	Owner    *pgxpool.Pool
	Mail     *specMailer
	router   *httprouter.Router
	gated    http.Handler
	results  *resultRecorder
	seq      *sequence
}

// app is the one process the whole suite runs against (TestMain composes it).
var app *specApp

// composeSpecApplication mirrors cmd/api/main.go: bus → schedule module →
// ledger → identity module → game module (with its identity directory) → the
// calendar's game-reference adapter. No HTTP server, no middleware chain, no
// route decoration: the modules' own route tables hang off a bare router so a
// step can call them in-process.
func composeSpecApplication(db *testdb.DB) error {
	bus := eventbus.New()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	scheduleModule := schedule.NewModule(db.Schedule, bus, log)
	ledgerRepo := ledger.New()
	mailer := &specMailer{}
	identityModule := identity.NewModule(db.Identity, mailer)

	gameModule := game.NewModule(db.Game, scheduleModule, ledgerRepo, idempotency.New(), bus, log)
	gameModule.UseIdentity(identityModule)
	scheduleModule.UseGameRefs(specGameRefs{game: gameModule})

	recorder := &resultRecorder{}
	// Subscribe after the game module: the settlement handler runs first (as
	// in production), the recorder observes the payload the calendar
	// published — the event a duplicate-delivery step replays.
	eventbus.Subscribe(bus, recorder.record)

	// The bare router: the modules' route tables with no middleware chain (see
	// doc.go). The ride and calendar steps call it.
	router := httprouter.New()
	scheduleModule.RegisterRoutes(router, allowAll)
	gameModule.RegisterRoutes(router, allowAll)

	// The authenticated router: the same route tables behind the chain
	// cmd/api/routes.go builds — Authenticate resolving the bearer token
	// through the identity module, then each route's RequirePermission gate
	// resolving the role's codes through the same module. The identity module's
	// own pre-authentication routes carry no gate, exactly as in production.
	chain := &httpx.Middleware{
		Logger:      log,
		Users:       identityModule,
		Permissions: identityModule,
	}
	gatedRouter := httprouter.New()
	scheduleModule.RegisterRoutes(gatedRouter, chain.RequirePermission)
	gameModule.RegisterRoutes(gatedRouter, chain.RequirePermission)
	identityModule.RegisterRoutes(gatedRouter, log)

	app = &specApp{
		Bus:      bus,
		Schedule: scheduleModule,
		Game:     gameModule,
		Identity: identityModule,
		Owner:    db.Owner,
		Mail:     mailer,
		router:   router,
		gated:    chain.RecoverPanic(chain.Authenticate(gatedRouter)),
		results:  recorder,
		seq:      &sequence{},
	}

	return nil
}

// allowAll is the route tables' permission gate: the spec suite drives the
// modules over a database whose fixtures carry no principals, so the gate adds
// nothing here. Authorization is proved by the identity scenarios (and the E2E
// suite's auth wall), never by a ride scenario.
func allowAll(_ string, next http.HandlerFunc) http.HandlerFunc { return next }

// specGameRefs adapts the game facade to the calendar's consumer-side port —
// the same adapter cmd/api/gamerefs.go installs.
type specGameRefs struct{ game *game.Module }

func (g specGameRefs) PlayersExistForTeam(ctx context.Context, teamID int) (bool, error) {
	return g.game.PlayersExistForTeam(ctx, teamID)
}

func (g specGameRefs) RidesExistForTeam(ctx context.Context, teamID int) (bool, error) {
	return g.game.RidesExistForTeam(ctx, teamID)
}

func (g specGameRefs) RidesExistForMatch(ctx context.Context, matchID int) (bool, error) {
	return g.game.RidesExistForMatch(ctx, matchID)
}

func (g specGameRefs) DeletePlayersForSeasonTx(ctx context.Context, tx pgx.Tx, seasonID int) error {
	err := g.game.DeletePlayersForSeasonTx(ctx, tx, seasonID)
	if errors.Is(err, game.ErrRecordInUse) {
		return schedule.ErrRecordInUse
	}
	return err
}

// specMailer is the identity module's mail transport: what the service mails
// is HELD instead of delivered, so a scenario reads the activation token the
// way the member reads it from her mail (identity/account.feature's
// Background). The service dispatches inline (no background runner is
// installed in this process), so a Send is recorded before the handler that
// booked it answers.
type specMailer struct {
	mu    sync.Mutex
	mails []specMail
}

// specMail is one held delivery: who it was for, which template carried it and
// the token it carried ("" for a mail that carries none).
type specMail struct {
	Recipient string
	Template  string
	Token     string
}

func (m *specMailer) Send(_ context.Context, recipient string, templateFile string, data any) error {
	mail := specMail{Recipient: recipient, Template: templateFile}
	if fields, ok := data.(map[string]any); ok {
		mail.Token, _ = fields["activationToken"].(string)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.mails = append(m.mails, mail)
	return nil
}

// reset empties the held mail — the per-scenario reset's second half (the
// database is reset by the Before hook, the mail here).
func (m *specMailer) reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mails = nil
}

// to lists the held mail addressed to recipient, oldest first (the address
// compares case-insensitively, as the store's citext column does).
func (m *specMailer) to(recipient string) []specMail {
	m.mu.Lock()
	defer m.mu.Unlock()

	var held []specMail
	for _, mail := range m.mails {
		if strings.EqualFold(mail.Recipient, recipient) {
			held = append(held, mail)
		}
	}
	return held
}

// resultRecorder keeps every match-result payload the calendar publishes, so a
// scenario can deliver the same event again (the settlement's claim makes the
// second delivery a no-op — the rule R-GAME-016 states).
type resultRecorder struct {
	mu     sync.Mutex
	events []schedule.MatchResultRecorded
}

func (r *resultRecorder) record(_ context.Context, event schedule.MatchResultRecorded) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
	return nil
}

// sequence hands out process-unique idempotency keys: every ride command is
// idempotent (ADR-024) and a repeated key would replay instead of execute.
type sequence struct {
	mu sync.Mutex
	n  int
}

func (s *sequence) next(prefix string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n++
	return fmt.Sprintf("spec-%s-%d", prefix, s.n)
}

// wireResponse is one in-process handler answer.
type wireResponse struct {
	Status int
	Body   []byte
}

// message returns the response's human refusal — the envelope's "error"
// member, or the raw body when the answer carries a field map.
func (r wireResponse) message() string {
	var envelope struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(r.Body, &envelope); err == nil && envelope.Error != "" {
		return envelope.Error
	}
	return string(bytes.TrimSpace(r.Body))
}

// envelope decodes the answer's envelope ({"ride": {...}}, {"match": {...}}, …).
func (r wireResponse) envelope() map[string]json.RawMessage {
	var out map[string]json.RawMessage
	_ = json.Unmarshal(r.Body, &out)
	return out
}

// refusalFields returns the field map a validation refusal carries
// ({"error": {"email": "…"}}) — nil when the refusal names no field.
func (r wireResponse) refusalFields() map[string]string {
	raw, ok := r.envelope()["error"]
	if !ok {
		return nil
	}
	var fields map[string]string
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil
	}
	return fields
}

// fixtureCallerUserID is the principal the bare router's steps travel as — the
// fixture's caller, not an account: the identity scenarios mint real users and
// their serial ids start at 1, so the fixture's id is reserved far above them.
// The ride claims are scoped per caller (R-KERNEL-003), so a collision here
// would make a two-caller scenario replay instead of execute.
const fixtureCallerUserID = 900_000

// call drives one module route in-process: a recorder instead of a connection,
// the principal the wire handlers read supplied directly (no middleware chain),
// and the modules' own route table as the handler.
func (a *specApp) call(method, path string, body any, headers map[string]string) (*wireResponse, error) {
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal %s %s body: %w", method, path, err)
		}
	}

	req := httptest.NewRequest(method, path, bytes.NewReader(payload))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	// The authenticated principal and permission set the middleware chain
	// would have resolved. The player handlers are the only ones that read
	// them; the fixtures carry no identity users, so the principal is a
	// fixture-only user id and the permission set is the writable one.
	req = httpx.SetAuthenticatedUser(req, httpx.AuthUser{ID: fixtureCallerUserID, Activated: true, RoleID: 1})
	req = httpx.SetPermissions(req, httpx.Permissions{"players:write", "players:write:any"})

	recorder := httptest.NewRecorder()
	a.router.ServeHTTP(recorder, req)

	return &wireResponse{Status: recorder.Code, Body: recorder.Body.Bytes()}, nil
}

// callGated drives one module route through the REAL middleware chain: the
// bearer credential the caller presents ("" is anonymous) is resolved by
// httpx.Authenticate through the identity module, and the route's own
// RequirePermission gate resolves the principal's role codes the same way. A
// refused request is an ordinary wireResponse — the refusal steps read it.
func (a *specApp) callGated(method, path string, body any, headers map[string]string, bearer string) (*wireResponse, error) {
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal %s %s body: %w", method, path, err)
		}
	}

	req := httptest.NewRequest(method, path, bytes.NewReader(payload))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	recorder := httptest.NewRecorder()
	a.gated.ServeHTTP(recorder, req)

	return &wireResponse{Status: recorder.Code, Body: recorder.Body.Bytes()}, nil
}

// publishResult delivers one calendar match-result payload to the event seam —
// the settlement's own entry point.
func (a *specApp) publishResult(ctx context.Context, event schedule.MatchResultRecorded) error {
	return a.Bus.Publish(ctx, event)
}

// now is the suite's clock read (the product reads time.Now; the fixture times
// are relative to it).
func now() time.Time { return time.Now() }

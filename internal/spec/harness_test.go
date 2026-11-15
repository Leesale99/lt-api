package spec

// The composition root: the same modules cmd/api/main.go builds, over the
// per-context pools internal/testdb opened, with no server and no middleware
// chain. See doc.go for why the modules' route tables are the entry point a
// facade step drives.

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
// share, the owner pool the fixtures and sabotage writes use, and the router
// carrying the modules' own route tables.
type specApp struct {
	Bus      *eventbus.Bus
	Schedule *schedule.Module
	Game     *game.Module
	Identity *identity.Module
	Owner    *pgxpool.Pool
	router   *httprouter.Router
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
	identityModule := identity.NewModule(db.Identity, nopMailer{})

	gameModule := game.NewModule(db.Game, scheduleModule, ledgerRepo, idempotency.New(), bus, log)
	gameModule.UseIdentity(identityModule)
	scheduleModule.UseGameRefs(specGameRefs{game: gameModule})

	recorder := &resultRecorder{}
	// Subscribe after the game module: the settlement handler runs first (as
	// in production), the recorder observes the payload the calendar
	// published — the event a duplicate-delivery step replays.
	eventbus.Subscribe(bus, recorder.record)

	router := httprouter.New()
	scheduleModule.RegisterRoutes(router, allowAll)
	gameModule.RegisterRoutes(router, allowAll)

	app = &specApp{
		Bus:      bus,
		Schedule: scheduleModule,
		Game:     gameModule,
		Identity: identityModule,
		Owner:    db.Owner,
		router:   router,
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

// nopMailer stands in for the SMTP client: nothing leaves the process.
type nopMailer struct{}

func (nopMailer) Send(_ context.Context, _ string, _ string, _ any) error { return nil }

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
	req = httpx.SetAuthenticatedUser(req, httpx.AuthUser{ID: 1, Activated: true, RoleID: 1})
	req = httpx.SetPermissions(req, httpx.Permissions{"players:write", "players:write:any"})

	recorder := httptest.NewRecorder()
	a.router.ServeHTTP(recorder, req)

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

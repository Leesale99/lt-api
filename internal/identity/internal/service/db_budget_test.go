package service

// Tests for R-KERNEL-005 — "the database budget pays for storage, not for
// hashing": persistence.DBTimeout opens only around the store calls it pays
// for, and the bcrypt work (the hash on registration, the compare on login)
// runs outside every window.
//
// The probe is the context the store call receives. The Service holds concrete
// repositories, so a test cannot hand it a fake store; the seam is pgx's own:
// a pool whose tracer records the context each store call acquires/drives a
// connection with. Two flavours, for two shapes:
//
//   - Register (no database): the acquire tracer samples the window, then
//     cancels it, so the store call fails instantly at acquire and the second
//     write is never reached — no dial, no server, no goroutine left behind.
//   - Authenticate (suite database): the query tracer samples both windows of
//     the real flow — the lookup, the untimed compare in between, then the
//     token insert — which is the only way to observe work that happens
//     BETWEEN two store calls.
//
// A regression that moves the crypto inside a window shows up as a deadline
// that is one hash (or one compare) earlier than it should be.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lt-api.aleksrdvn.com/internal/identity/internal/model"
	"lt-api.aleksrdvn.com/internal/identity/internal/repository"
	"lt-api.aleksrdvn.com/internal/kernel/persistence"
)

// probePassword is the plaintext both probes hash. Its length is irrelevant to
// bcrypt's cost (cost 12 is a fixed amount of key setup), only to the 72-byte
// gate the model enforces before hashing.
const probePassword = "pa55word123"

// budgetSlack is how much of the 3s budget may already be spent when a store
// call starts. The window opens inside the use case and the sample is taken on
// the same goroutine a few instructions later, so a correct call measures
// microseconds; the slack absorbs a scheduler tick or a GC pause. It must stay
// well below one bcrypt cost-12 operation (~200ms here) or a regression that
// pays for crypto out of the budget would slip through this check — the
// deadline-position check below fails it independently.
const budgetSlack = 100 * time.Millisecond

// deadlineSample is one store call's DB-budget window: the deadline of the
// context it was handed, whether it had one at all, and when it was handed it.
type deadlineSample struct {
	deadline time.Time
	live     bool
	at       time.Time
}

func sampleBudget(ctx context.Context) deadlineSample {
	deadline, ok := ctx.Deadline()
	return deadlineSample{deadline: deadline, live: ok, at: time.Now()}
}

// assertFreshWindow: the store call ran under a live persistence.DBTimeout
// window that had barely started. Fails when the call got no deadline (the
// budget was dropped) or when part of the budget was already spent before the
// call (the window opened around work that is not storage).
func assertFreshWindow(t *testing.T, label string, s deadlineSample) {
	t.Helper()

	if !s.live {
		t.Fatalf("%s: store call ran with no deadline, want a live %s DB budget", label, persistence.DBTimeout)
	}

	if remaining := s.deadline.Sub(s.at); remaining < persistence.DBTimeout-budgetSlack {
		t.Errorf("%s: only %s of the %s DB budget was left when the store call started, want at least %s — the window opened before work it should not pay for",
			label, remaining, persistence.DBTimeout, persistence.DBTimeout-budgetSlack)
	}
}

// assertWindowOpenedAfter asserts the window the store call received was
// opened by work that ran BEFORE it, not inside it: with the crypto outside,
// the window's deadline is callStart + work + DBTimeout; with the crypto
// inside, it is callStart + DBTimeout.
//
// work is measured on the same machine immediately before the call, and the
// threshold asks for only half of it, so the check survives a hash that runs
// 2x slower or faster than the calibration while a regression still misses the
// threshold by roughly work/2.
func assertWindowOpenedAfter(t *testing.T, label string, s deadlineSample, callStart time.Time, work time.Duration) {
	t.Helper()

	gap := s.deadline.Sub(callStart)
	if want := persistence.DBTimeout + work/2; gap < want {
		t.Errorf("%s: budget deadline is %s after the call started, want at least %s (DBTimeout %s + half of the measured %s of crypto) — the window opened before the crypto ran, so the crypto is being paid for out of the DB budget",
			label, gap, want, persistence.DBTimeout, work)
	}
}

// serviceOn wires the real use cases onto the given pool and hands back the
// repositories too, so a test can plant fixtures through the same pool. The
// pool is the only seam: Repositories carries concrete repository types.
func serviceOn(pool *pgxpool.Pool) (*Service, Repositories) {
	repos := Repositories{
		Users:       repository.NewUserRepository(pool),
		UserTokens:  repository.NewUserTokenRepository(pool),
		Permissions: repository.NewPermissionRepository(pool),
	}

	return New(pool, repos, nopMailer{}), repos
}

// deadPoolTracer samples the context of every acquire and then cancels it, so
// puddle returns before dialling: the store call fails with context.Canceled
// immediately and every sample is a store call. Nothing here needs a server.
type deadPoolTracer struct {
	mu      sync.Mutex
	samples []deadlineSample
}

func (d *deadPoolTracer) TraceAcquireStart(ctx context.Context, _ *pgxpool.Pool, _ pgxpool.TraceAcquireStartData) context.Context {
	d.mu.Lock()
	d.samples = append(d.samples, sampleBudget(ctx))
	d.mu.Unlock()

	// The deadline survives the wrap (WithCancel delegates Deadline to its
	// parent), so the store call still reports the window it was given.
	failed, cancel := context.WithCancel(ctx)
	cancel()

	return failed
}

func (d *deadPoolTracer) TraceAcquireEnd(context.Context, *pgxpool.Pool, pgxpool.TraceAcquireEndData) {
}

// TraceQueryStart/TraceQueryEnd exist only because ConnConfig.Tracer is typed
// pgx.QueryTracer: a dead pool never gets far enough to run a statement.
func (d *deadPoolTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	return ctx
}

func (d *deadPoolTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {
}

func (d *deadPoolTracer) seen() []deadlineSample {
	d.mu.Lock()
	defer d.mu.Unlock()

	return append([]deadlineSample(nil), d.samples...)
}

// TestRegisterHashesOutsideDBBudget: Register's insert runs under a live 3s
// budget that opened AFTER the bcrypt hash — the hash is not paid for out of
// the database budget.
//
// Failure mode it exists for: a Register that opens dbCtx before hashing (or
// wraps the hash in it) leaves the Insert ~one hash into the budget, and both
// assertions trip — the fresh-window check by the hash's share of the budget,
// the deadline-position check by ~hash/2.
func TestRegisterHashesOutsideDBBudget(t *testing.T) {
	cfg, err := pgxpool.ParseConfig("postgres://probe@127.0.0.1:1/probe")
	if err != nil {
		t.Fatalf("parse probe pool config: %v", err)
	}
	tracer := &deadPoolTracer{}
	cfg.ConnConfig.Tracer = tracer // pgxpool picks AcquireTracer up from this field
	cfg.MaxConns = 1

	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("open probe pool: %v", err)
	}
	t.Cleanup(pool.Close)

	svc, _ := serviceOn(pool)

	// Calibration: one hash of the same plaintext, same fixed bcrypt cost (12),
	// measured immediately before the call under test.
	probe := model.User{Name: "Probe", Email: "probe@example.com"}
	hashStart := time.Now()
	if err := probe.Password.Set(probePassword); err != nil {
		t.Fatalf("calibration hash: %v", err)
	}
	hashDur := time.Since(hashStart)

	callStart := time.Now()
	_, err = svc.Register(context.Background(), model.User{Name: "Alice", Email: "alice@example.com"}, probePassword)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Register on the dead pool = %v, want context.Canceled from the probe's cancelled acquire", err)
	}

	samples := tracer.seen()
	if len(samples) != 1 {
		t.Fatalf("store calls after Register = %d, want 1 (users.Insert; the token write is unreachable once the insert fails)", len(samples))
	}

	assertFreshWindow(t, "Register/users.Insert", samples[0])
	assertWindowOpenedAfter(t, "Register/users.Insert", samples[0], callStart, hashDur)
}

// queryBudgetTracer samples the window of every statement a connection runs.
type queryBudgetTracer struct {
	mu      sync.Mutex
	samples []deadlineSample
}

func (q *queryBudgetTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	q.mu.Lock()
	q.samples = append(q.samples, sampleBudget(ctx))
	q.mu.Unlock()

	return ctx
}

func (q *queryBudgetTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func (q *queryBudgetTracer) reset() {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.samples = nil
}

func (q *queryBudgetTracer) seen() []deadlineSample {
	q.mu.Lock()
	defer q.mu.Unlock()

	return append([]deadlineSample(nil), q.samples...)
}

// TestAuthenticateComparesOutsideDBBudget: Authenticate's two store calls each
// run under their own fresh 3s window, and the bcrypt compare runs BETWEEN them
// — the second window's deadline is a full budget after a point that already
// includes the compare, so neither window paid for it.
//
// Failure mode it exists for: a compare squeezed into either window (opening
// one window around the whole use case, or reusing the lookup window for the
// token insert) makes the second deadline ~one compare earlier than the
// assertion demands.
func TestAuthenticateComparesOutsideDBBudget(t *testing.T) {
	requireDB(t)

	tracer := &queryBudgetTracer{}

	// The suite's identity pool config carries SET ROLE/search_path, so the
	// traced copy connects exactly as the wired repositories do.
	cfg := identityPool.Config()
	cfg.ConnConfig.Tracer = tracer
	// Ping itself is statement-free, but a ping after an idle second would
	// destroy and rebuild the connection — one more AfterConnect (SET ROLE)
	// statement in the trace. Off, so the count below is the store calls only.
	cfg.ShouldPing = func(context.Context, pgxpool.ShouldPingParams) bool { return false }

	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("open traced identity pool: %v", err)
	}
	t.Cleanup(pool.Close)

	svc, repos := serviceOn(pool)

	// Warm one connection so testdb's AfterConnect (SET ROLE, SET search_path)
	// is not mistaken for a store call, then forget everything it traced.
	conn, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("warm traced pool: %v", err)
	}
	conn.Release()

	resetUsers(t)
	if _, err := repos.Users.Insert(context.Background(), mustUser("Alice", "alice@example.com", probePassword)); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	// Calibration: one compare of the same plaintext against a cost-12 hash.
	probe := model.User{Name: "Probe", Email: "probe@example.com"}
	if err := probe.Password.Set(probePassword); err != nil {
		t.Fatalf("calibration hash: %v", err)
	}
	compareStart := time.Now()
	match, err := probe.Password.Matches(probePassword)
	compareDur := time.Since(compareStart)
	if err != nil || !match {
		t.Fatalf("calibration compare = (%v, %v), want (true, nil)", match, err)
	}

	tracer.reset()

	callStart := time.Now()
	token, err := svc.Authenticate(context.Background(), "alice@example.com", probePassword)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if token.Plaintext == "" {
		t.Fatal("Authenticate returned an empty token plaintext")
	}

	// The seed and the warm-up are behind a reset, so these two are the flow's
	// own store calls: the lookup, then the token insert.
	samples := tracer.seen()
	if len(samples) != 2 {
		t.Fatalf("store calls after Authenticate = %d, want 2 (users.GetByEmail, user_tokens insert)", len(samples))
	}

	assertFreshWindow(t, "Authenticate/users.GetByEmail", samples[0])
	assertFreshWindow(t, "Authenticate/user_tokens insert", samples[1])
	assertWindowOpenedAfter(t, "Authenticate/user_tokens insert", samples[1], callStart, compareDur)
}

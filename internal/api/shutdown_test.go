package api

// Shutdown-lifecycle tests: they drive runServer with injected servers and
// self-delivered signals, so they need neither the database (no requireDB)
// nor the real routes. The point is proving the three shutdown guarantees:
// the drain hits its timeout, a second signal cuts the drain short, and a
// stuck background task cannot hold the process past its budget.

import (
	"context"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"
)

// quietLogger discards output; these tests assert on behavior, not log text.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// freeAddr returns a loopback address that is (almost certainly) free. The
// listener is closed immediately; re-binding it races with nothing here
// except other processes, which is acceptable for tests.
func freeAddr(t *testing.T) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find free port: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

// hangServer returns a server whose handler blocks forever on release.
// A request sent to it keeps the connection busy, so srv.Shutdown waits.
func hangServer(addr string, block <-chan struct{}) *http.Server {
	return &http.Server{
		Addr: addr,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-block
		}),
		ErrorLog: log.New(io.Discard, "", 0),
	}
}

// busyConn opens a connection and puts it in-flight, so the drain sees a
// busy connection instead of an idle one.
func busyConn(t *testing.T, addr string) net.Conn {
	t.Helper()

	var conn net.Conn
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.Dial("tcp", addr)
		if err == nil {
			conn = c
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if conn == nil {
		t.Fatal("server never became reachable")
	}

	// Minimal request: the handler starts and hangs, making the connection
	// busy. Without this the connection is idle and Shutdown closes it at
	// once — no timeout to observe.
	if _, err := conn.Write([]byte("GET / HTTP/1.1\r\nHost: test\r\n\r\n")); err != nil {
		t.Fatalf("write request: %v", err)
	}
	return conn
}

// startServe runs runServer in the background and returns its result.
func startServe(app *Application, srv *http.Server) <-chan error {
	errc := make(chan error, 1)
	go func() { errc <- app.runServer(srv) }()
	return errc
}

// sigterm delivers SIGTERM to this process: the same path an orchestrator
// uses. Safe because the tests registered signal.NotifyContext beforehand.
func sigterm(t *testing.T) {
	t.Helper()
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("send SIGTERM: %v", err)
	}
}

// awaitServeResult fails the test if runServer has not returned within
// d, and otherwise returns its error.
func awaitServeResult(t *testing.T, errc <-chan error, d time.Duration) error {
	t.Helper()

	select {
	case err := <-errc:
		return err
	case <-time.After(d):
		t.Fatalf("runServer did not return within %v", d)
		return nil
	}
}

// TestDrainTimeoutForceCloses: a handler that never finishes must not hold
// the process — the drain hits its grace budget, force-closes, and Serve
// still returns nil (bounded shutdown, not a failure).
func TestDrainTimeoutForceCloses(t *testing.T) {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	const grace = 300 * time.Millisecond
	app := &Application{
		Logger:               quietLogger(),
		RootCtx:              ctx,
		ShutdownGracePeriod:  grace,
		BackgroundTaskBudget: 300 * time.Millisecond,
	}

	block := make(chan struct{})
	defer close(block) // release the hung handler so nothing leaks at teardown

	srv := hangServer(freeAddr(t), block)
	errc := startServe(app, srv)

	conn := busyConn(t, srv.Addr)
	defer conn.Close()

	start := time.Now()
	sigterm(t)

	err := awaitServeResult(t, errc, 5*time.Second)
	if err != nil {
		t.Fatalf("Serve returned %v, want nil (drain timeout is handled, not fatal)", err)
	}
	if elapsed := time.Since(start); elapsed < grace-50*time.Millisecond {
		t.Fatalf("Serve returned after %v; want the drain to wait out the %v grace period first", elapsed, grace)
	}
}

// TestSecondSignalForceCloses: while the drain is stuck on a hanging
// handler, a second SIGTERM must force-close immediately instead of waiting
// out the (long) grace period.
func TestSecondSignalForceClosesDuringDrain(t *testing.T) {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	const grace = 10 * time.Second // long enough that a pass would be obvious
	app := &Application{
		Logger:               quietLogger(),
		RootCtx:              ctx,
		ShutdownGracePeriod:  grace,
		BackgroundTaskBudget: 300 * time.Millisecond,
	}

	block := make(chan struct{})
	defer close(block)

	srv := hangServer(freeAddr(t), block)
	errc := startServe(app, srv)

	conn := busyConn(t, srv.Addr)
	defer conn.Close()

	sigterm(t)                         // first signal: begins the (stuck) drain
	time.Sleep(100 * time.Millisecond) // let the escalation tripwire arm

	start := time.Now()
	sigterm(t) // second signal: must force-close, not wait for grace

	err := awaitServeResult(t, errc, 3*time.Second)
	if err != nil {
		t.Fatalf("Serve returned %v, want nil after force-close", err)
	}
	// Returning ~instantly (vs. after the 10s grace) is the assertion that
	// Close cut the drain short.
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Serve returned after %v; second signal should have force-closed immediately", elapsed)
	}
}

// TestBackgroundTaskBudgetBoundsWait: a background task that ignores
// cancellation entirely (worst case) must not hold Serve past its budget —
// Serve exits with a warning instead, and the process terminates.
func TestBackgroundTaskBudgetBoundsWait(t *testing.T) {
	root, cancel := context.WithCancel(context.Background())
	defer cancel()

	const budget = 200 * time.Millisecond
	app := &Application{
		Logger:               quietLogger(),
		RootCtx:              root,
		ShutdownGracePeriod:  time.Second,
		BackgroundTaskBudget: budget,
	}

	block := make(chan struct{})
	defer close(block)

	// Spawn before Serve: mimics a request that kicked off a send, then the
	// process got its shutdown signal.
	app.background(func(ctx context.Context) {
		<-block // ignores ctx on purpose — the misbehaving-task worst case
	})

	srv := hangServer(freeAddr(t), make(chan struct{})) // never hangs: no requests
	errc := startServe(app, srv)

	// Wait until listening, then cancel the root the way a signal would.
	conn := busyConn(t, srv.Addr)
	defer conn.Close()
	cancel()

	start := time.Now()
	err := awaitServeResult(t, errc, 3*time.Second)
	if err != nil {
		t.Fatalf("Serve returned %v, want nil (budget exceeded is a warning, not an error)", err)
	}
	if elapsed := time.Since(start); elapsed < budget-50*time.Millisecond {
		t.Fatalf("Serve returned after %v; want the %v background budget to engage", elapsed, budget)
	}
}

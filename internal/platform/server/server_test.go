package server

// Serve/Background tests: the two rules this package owns.
//
// R-PLATFORM-009 (bounded connections): the three http.Server timeouts are
// written inline in Serve's construction, with no injection seam, so they are
// proven the only way they can be — through Serve() itself, from the wire, on
// a real bound port. Each probe is a raw TCP client measuring when the server
// hangs up:
//
//   - ReadTimeout 5s: a connection that sends nothing is closed ~5s after it
//     is accepted. Window [3s, 15s].
//   - WriteTimeout 10s: a handler streaming 4KB every 250ms has its
//     connection cut ~10s after the request header was read. Window
//     [8s, 25s].
//   - IdleTimeout 1min: a keep-alive connection that goes silent after one
//     completed request/response is closed ~60s later. Window [50s, 100s].
//
// The lower bounds are far below the constants (never a "too early" flake
// from a fast machine); the upper bounds are far above them (never a "too
// late" flake from a loaded CI box), and every probe arms its own read
// deadline ABOVE the upper bound, so a server that never closes fails with an
// explicit timeout instead of hanging the suite. Total added runtime is
// ~75s, dominated by the idle probe — the audit already runs minute-scale DB
// suites.
//
// Serve binds ":<Port>", so the port is pre-picked (listen ":0", remember,
// close, reuse): the caller cannot hand Serve a listener of its own.
//
// R-PLATFORM-011 (background survival): a task booked with Background that
// panics is recovered, logged at Error level with the panic's message, and
// the tracker still accepts and drains later work — the process keeps
// serving. Deterministic through done channels; no polling.

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// freePort returns a loopback port that is (almost certainly) free for
// Serve's ":%d" address. Same listen-remember-close trick as freeAddr (the
// house helper in shutdown_test.go), reshaped because Serve owns its own
// listener and therefore needs a number, not an address. Re-binding races
// with nothing here except other processes, which is acceptable for tests.
func freePort(t *testing.T) int {
	t.Helper()

	_, portText, err := net.SplitHostPort(freeAddr(t))
	if err != nil {
		t.Fatalf("split free address: %v", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("free address has a non-numeric port %q: %v", portText, err)
	}
	return port
}

// serveForTest starts Serve on a free port with the given handler and
// registers the teardown: cancel the root (the process-wide signal root's
// stand-in) and wait for Serve to return through the full shutdown path.
// Serve's own RunOptions defaults apply (10s drain, 5s task budget), so the
// wait is bounded above them.
func serveForTest(t *testing.T, handler http.Handler) *Server {
	t.Helper()

	root, cancel := context.WithCancel(context.Background())
	s := &Server{
		Port:    freePort(t),
		Logger:  quietLogger(),
		RootCtx: root,
		Handler: handler,
	}

	errc := make(chan error, 1)
	go func() { errc <- s.Serve() }()

	t.Cleanup(func() {
		cancel()
		select {
		case err := <-errc:
			if err != nil {
				t.Errorf("Serve returned %v, want nil after RootCtx cancellation", err)
			}
		case <-time.After(20 * time.Second):
			t.Errorf("Serve did not return within 20s of RootCtx cancellation")
		}
	})

	return s
}

// dialServed connects to the server, retrying briefly: Serve starts listening
// asynchronously, so the first dials can race the bind. The loop is bounded
// and returns a failure rather than a nil connection.
func dialServed(t *testing.T, s *Server) net.Conn {
	t.Helper()

	addr := fmt.Sprintf("127.0.0.1:%d", s.Port)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.Dial("tcp", addr)
		if err == nil {
			t.Cleanup(func() { _ = conn.Close() })
			return conn
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("server at %s never became reachable", addr)
	return nil
}

// readUntilClosed reads whatever the server sends and returns the elapsed
// time until the connection ends. A read deadline timeout is reported as a
// failure: it means the server never hung up within the window.
func readUntilClosed(t *testing.T, r io.Reader, window time.Duration) time.Duration {
	t.Helper()

	buf := make([]byte, 4096)
	start := time.Now()
	for {
		if _, err := r.Read(buf); err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				t.Fatalf("server did not close the connection within %v (client read deadline expired)", window)
			}
			t.Logf("connection closed after %v: %v", time.Since(start), err)
			return time.Since(start)
		}
	}
}

// TestServeReadTimeoutClosesSilentConnection: a client that connects and sends
// nothing is disconnected by the server's ReadTimeout (5s) instead of holding
// a slot forever. Window [3s, 15s]; fails early if the timeout disappears
// (the client read deadline at 15s fires) or is mis-set to something short.
func TestServeReadTimeoutClosesSilentConnection(t *testing.T) {
	const (
		lower  = 3 * time.Second  // ReadTimeout is 5s: 2s of slack for the accept
		upper  = 15 * time.Second // 3x the constant: CI slowness cannot flake it
		margin = upper + 5*time.Second
	)

	s := serveForTest(t, http.NewServeMux())
	conn := dialServed(t, s)

	_ = conn.SetReadDeadline(time.Now().Add(margin))
	elapsed := readUntilClosed(t, conn, upper)

	if elapsed < lower {
		t.Fatalf("server closed an idle-on-connect socket after %v, want the 5s ReadTimeout (window [%v, %v])", elapsed, lower, upper)
	}
	if elapsed > upper {
		t.Fatalf("server closed the silent connection after %v, want within [%v, %v] around the 5s ReadTimeout", elapsed, lower, upper)
	}
}

// TestServeWriteTimeoutClosesStreamingResponse: a handler that streams without
// end cannot hold the connection past WriteTimeout (10s) — the server cuts it,
// which the client observes as EOF at ~10s. Streaming in 4KB chunks every
// 250ms keeps the write side genuinely busy (so the cut is not an idle
// artifact). Window [8s, 25s]; fails if the timeout disappears (client read
// deadline at 30s fires) or fires early.
func TestServeWriteTimeoutClosesStreamingResponse(t *testing.T) {
	const (
		lower  = 8 * time.Second  // WriteTimeout is 10s: 2s of slack for the header read
		upper  = 25 * time.Second // 2.5x the constant
		margin = upper + 5*time.Second
	)

	// stop releases the handler at teardown, so the drain never has to wait
	// out its grace period on a handler that is still streaming.
	stop := make(chan struct{})
	defer close(stop)

	chunk := bytes.Repeat([]byte("x"), 4096)
	s := serveForTest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)

		flusher, _ := w.(http.Flusher)
		for {
			if _, err := w.Write(chunk); err != nil {
				return // the connection is gone: the write timeout did its job
			}
			if flusher != nil {
				flusher.Flush()
			}

			select {
			case <-stop:
				return
			case <-r.Context().Done():
				return
			case <-time.After(250 * time.Millisecond):
			}
		}
	}))

	conn := dialServed(t, s)
	if _, err := io.WriteString(conn, "GET /stream HTTP/1.1\r\nHost: test\r\n\r\n"); err != nil {
		t.Fatalf("write request: %v", err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(margin))
	start := time.Now()

	buf := make([]byte, 8192)
	var received int
	for {
		n, err := conn.Read(buf)
		received += n
		if err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				t.Fatalf("server did not cut the streaming response within %v (client read deadline expired)", upper)
			}
			break
		}
	}
	elapsed := time.Since(start)

	if received < 4096 {
		t.Fatalf("client received %d bytes before the cut, want a genuinely streaming response (>= 4KB)", received)
	}
	if elapsed < lower {
		t.Fatalf("server cut the streaming response after %v, want the 10s WriteTimeout (window [%v, %v])", elapsed, lower, upper)
	}
	if elapsed > upper {
		t.Fatalf("server cut the streaming response after %v, want within [%v, %v] around the 10s WriteTimeout", elapsed, lower, upper)
	}
}

// TestServeIdleTimeoutClosesKeepAlive: a keep-alive connection that completes
// one request/response and then goes silent is closed by the server's
// IdleTimeout (1min) rather than being kept forever. Window [50s, 100s]; the
// client read deadline sits at 110s, so a missing idle timeout fails with an
// explicit message instead of hanging the suite.
func TestServeIdleTimeoutClosesKeepAlive(t *testing.T) {
	const (
		lower  = 50 * time.Second  // IdleTimeout is 60s: 10s of slack
		upper  = 100 * time.Second // 1.67x the constant
		margin = 110 * time.Second // above the upper bound: timeout == failure
	)

	s := serveForTest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "idle-probe-ok")
	}))

	conn := dialServed(t, s)
	if _, err := io.WriteString(conn, "GET /idle HTTP/1.1\r\nHost: test\r\n\r\n"); err != nil {
		t.Fatalf("write request: %v", err)
	}

	// Consume the complete response over the connection, then stop talking:
	// the connection is now idle and only IdleTimeout can close it.
	reader := bufio.NewReader(conn)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	resp, err := http.ReadResponse(reader, &http.Request{Method: http.MethodGet})
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	_ = resp.Body.Close()
	if string(body) != "idle-probe-ok" {
		t.Fatalf("response body = %q, want %q", body, "idle-probe-ok")
	}

	// The clock starts when the connection becomes idle — right here.
	_ = conn.SetReadDeadline(time.Now().Add(margin))
	elapsed := readUntilClosed(t, reader, upper)

	if elapsed < lower {
		t.Fatalf("server closed the idle keep-alive connection after %v, want the 1min IdleTimeout (window [%v, %v])", elapsed, lower, upper)
	}
	if elapsed > upper {
		t.Fatalf("server closed the idle keep-alive connection after %v, want within [%v, %v] around the 1min IdleTimeout", elapsed, lower, upper)
	}
}

// TestBackgroundPanicIsContainedAndRecorded: a background task that panics is
// recovered (a panic escaping the goroutine would crash the test binary — so
// "the process keeps running" is asserted by this test completing at all),
// the panic message reaches the logger at Error level, and the tracker still
// books and drains later work. Fails if Background stops recovering, stops
// logging the recovered value, or the panic unwinds the waitgroup bookkeeping.
func TestBackgroundPanicIsContainedAndRecorded(t *testing.T) {
	const panicMessage = "background task exploded"

	var logged bytes.Buffer
	s := &Server{
		Logger:  slog.New(slog.NewTextHandler(&logged, nil)),
		RootCtx: context.Background(),
	}

	panicking := make(chan struct{})
	s.Background(func(context.Context) {
		close(panicking)
		panic(panicMessage)
	})
	<-panicking

	// The tracker must still work after a contained panic: book one more task
	// that completes normally. (It gets RootCtx, the process-wide root, as
	// the rule requires.)
	var gotCtx context.Context
	second := make(chan struct{})
	s.Background(func(ctx context.Context) {
		gotCtx = ctx
		close(second)
	})
	<-second

	if gotCtx != s.RootCtx {
		t.Fatalf("background task received ctx %v, want the server's RootCtx", gotCtx)
	}

	// Same-package access to the tracker: it must drain — the panicking task
	// counted as done, not as a leaked slot. wg.Wait also orders the log
	// write before the read below.
	drained := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(drained)
	}()
	select {
	case <-drained:
	case <-time.After(5 * time.Second):
		t.Fatal("tracker did not drain after a panicking task: the panic leaked a waitgroup slot")
	}

	out := logged.String()
	if !strings.Contains(out, panicMessage) {
		t.Fatalf("logger recorded %q, want the panic message %q", out, panicMessage)
	}
	if !strings.Contains(out, "level=ERROR") {
		t.Fatalf("logger recorded %q, want the failure at Error level", out)
	}
}

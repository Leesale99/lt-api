package mailer

// Mailer tests: the two rules this package owns, at the smallest scale that
// can contain them.
//
// R-PLATFORM-006 (config refusal): New refuses an incomplete configuration
// with ErrMissingCredentials (errors.Is), constructs fine from a complete one
// without dialing anything, and a delivery failure is NOT that sentinel — the
// two failure classes are distinguishable, which is what lets main crash on
// the first and a worker retry the second.
//
// R-PLATFORM-007 (retry ladder): Send against a local SMTP endpoint that
// rejects every greeting dials exactly three times (the rule's number, pinned
// literally), returns the LAST attempt's transport error, spends no pause
// after the last attempt, jitters each pause (measured across concurrent
// runs) and returns ctx.Err() at once when the caller cancels mid-ladder.
//
// The endpoint is a real TCP listener that speaks one line of SMTP (a
// non-220 greeting) and closes, because the mailer's only seam is the wire:
// the client, the clock and the retry constants are all unexported and not
// injectable. Timing assertions therefore use the code's own constants
// (maxRetries, baseDelay) with wide upper margins — the same real-time house
// pattern as internal/platform/server/shutdown_test.go. A 554 greeting makes
// each attempt fail immediately, so the only time spent is the ladder's own
// backoff: each test runs in ~1.5-3s, never more.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/textproto"
	"strings"
	"sync"
	"testing"
	"time"
)

// templateFile is a real embedded template: Send parses it before dialing,
// so a missing template would mask the transport behaviour under test.
const templateFile = "user_token_activation.tmpl"

// templateData satisfies the activation template's one field.
var templateData = map[string]string{"activationToken": "test-token"}

// smtpConfig is a complete, valid configuration whose parts tests blank out.
type smtpConfig struct {
	host     string
	port     int
	username string
	password string
	sender   string
}

// validConfig is complete and points at a port nothing listens on: New must
// not dial, so a construction from this config succeeds without a server.
func validConfig() smtpConfig {
	return smtpConfig{
		host:     "127.0.0.1",
		port:     2525,
		username: "lt-user",
		password: "lt-secret",
		sender:   "no-reply@example.test",
	}
}

func (c smtpConfig) new() (*Mailer, error) {
	return New(c.host, c.port, c.username, c.password, c.sender)
}

// failingSMTP is a local SMTP endpoint that rejects every connection at the
// greeting: it accepts, writes one non-220 line, and reads until the client
// hangs up. One accepted connection == one dial attempt, which is exactly
// what the retry ladder must be observed through.
//
// reply(n) yields the greeting for attempt n (1-based), so tests can make
// every attempt's failure identifiable in the returned error.
type failingSMTP struct {
	ln    net.Listener
	reply func(attempt int) string

	mu       sync.Mutex
	accepts  []time.Time
	first    chan struct{}
	firstOne sync.Once
}

func newFailingSMTP(t *testing.T, reply func(attempt int) string) *failingSMTP {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("start fake SMTP listener: %v", err)
	}

	f := &failingSMTP{ln: ln, reply: reply, first: make(chan struct{})}
	go f.serve()
	t.Cleanup(func() { _ = ln.Close() })

	return f
}

func (f *failingSMTP) serve() {
	for {
		conn, err := f.ln.Accept()
		if err != nil {
			return // listener closed at teardown
		}

		f.mu.Lock()
		f.accepts = append(f.accepts, time.Now())
		attempt := len(f.accepts)
		f.mu.Unlock()
		f.firstOne.Do(func() { close(f.first) })

		go f.reject(conn, attempt)
	}
}

// reject answers the greeting with a non-220 line. net/smtp surfaces it as a
// *textproto.Error carrying the line's text, so each attempt's identity is
// visible in the error Send eventually returns.
func (f *failingSMTP) reject(conn net.Conn, attempt int) {
	defer func() { _ = conn.Close() }()

	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := fmt.Fprintf(conn, "%s\r\n", f.reply(attempt)); err != nil {
		return
	}
	// Drain until the client gives up, so the close is orderly rather than a
	// reset that could race the error the client is already handling.
	_, _ = io.Copy(io.Discard, conn)
}

func (f *failingSMTP) port() int {
	return f.ln.Addr().(*net.TCPAddr).Port
}

// attemptTimes copies the attempt timestamps in order.
func (f *failingSMTP) attemptTimes() []time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]time.Time(nil), f.accepts...)
}

// mailerFor returns a complete mailer aimed at f.
func mailerFor(t *testing.T, f *failingSMTP) *Mailer {
	t.Helper()

	cfg := validConfig()
	cfg.port = f.port()

	m, err := cfg.new()
	if err != nil {
		t.Fatalf("New with a complete config: %v", err)
	}
	return m
}

// isTransportError reports whether err came from the wire — an SMTP protocol
// error, a network error, or EOF while the greeting was expected — as opposed
// to the constructor's configuration refusal.
func isTransportError(err error) bool {
	var textErr *textproto.Error
	var netErr net.Error

	return errors.As(err, &textErr) || errors.As(err, &netErr) || errors.Is(err, io.EOF)
}

// TestNewRefusesIncompleteConfig: every required setting, blanked on its own,
// makes New refuse with ErrMissingCredentials and hand back no mailer. Each
// case fails the moment the emptiness check stops covering one field, or the
// sentinel is replaced by a plain error.
func TestNewRefusesIncompleteConfig(t *testing.T) {
	cases := []struct {
		field string
		blank func(*smtpConfig)
	}{
		{"host", func(c *smtpConfig) { c.host = "" }},
		{"username", func(c *smtpConfig) { c.username = "" }},
		{"password", func(c *smtpConfig) { c.password = "" }},
		{"sender", func(c *smtpConfig) { c.sender = "" }},
	}

	for _, tc := range cases {
		t.Run(tc.field+" empty", func(t *testing.T) {
			cfg := validConfig()
			tc.blank(&cfg)

			m, err := cfg.new()
			if !errors.Is(err, ErrMissingCredentials) {
				t.Fatalf("New with %s empty returned %v, want ErrMissingCredentials", tc.field, err)
			}
			if m != nil {
				t.Fatalf("New with %s empty returned a mailer, want nil", tc.field)
			}
		})
	}
}

// TestNewAcceptsCompleteConfig: a complete configuration constructs without
// dialing — nothing listens on validConfig's port, so a dial would surface as
// an error instead of a usable mailer. Fails if New starts validating by
// connecting (or if the emptiness check overreaches).
func TestNewAcceptsCompleteConfig(t *testing.T) {
	m, err := validConfig().new()
	if err != nil {
		t.Fatalf("New with a complete config (and nothing listening) returned %v, want nil — New must not dial", err)
	}
	if m == nil {
		t.Fatal("New returned a nil mailer and a nil error")
	}
}

// TestSendFailureIsNotConfigError: a delivery against a live-but-rejecting
// SMTP endpoint returns a transport-class error that is NOT
// ErrMissingCredentials, so a network failure is never mistaken for a
// configuration mistake (nor retried as if it were one). Fails if Send
// returns the sentinel, or nil, or a non-transport error.
func TestSendFailureIsNotConfigError(t *testing.T) {
	f := newFailingSMTP(t, func(int) string { return "554 delivery unavailable" })
	m := mailerFor(t, f)

	err := m.Send(context.Background(), "player@example.test", templateFile, templateData)
	if err == nil {
		t.Fatal("Send against a rejecting endpoint returned nil")
	}
	if errors.Is(err, ErrMissingCredentials) {
		t.Fatalf("a delivery failure reported as a config error: %v", err)
	}
	if !isTransportError(err) {
		t.Fatalf("Send returned %v, want a transport-class error (SMTP/net/EOF) so callers can classify it", err)
	}
}

// TestSendRetriesBoundedAttempts: a send that always fails dials exactly
// maxRetries times, returns the transport error of the LAST attempt, and does
// not pause after it. Fails if the ladder grows or shrinks (attempt count),
// if an earlier attempt's error is returned instead of the last one, or if a
// final sleep is reintroduced (the return comes back more than baseDelay
// after the last accept).
func TestSendRetriesBoundedAttempts(t *testing.T) {
	// The rule's number, written literally: the statement bounds a delivery at
	// three attempts, so raising the constant must fail here rather than
	// silently rewrite the bound.
	const wantAttempts = 3
	if maxRetries != wantAttempts {
		t.Fatalf("maxRetries is %d, want the rule's %d attempts", maxRetries, wantAttempts)
	}

	f := newFailingSMTP(t, func(attempt int) string {
		return fmt.Sprintf("554 attempt-%d", attempt)
	})
	m := mailerFor(t, f)

	err := m.Send(context.Background(), "player@example.test", templateFile, templateData)
	if err == nil {
		t.Fatal("Send against an always-failing endpoint returned nil")
	}

	times := f.attemptTimes()
	if len(times) != wantAttempts {
		t.Fatalf("observed %d dial attempts, want exactly %d", len(times), wantAttempts)
	}

	// Each attempt's greeting is unique: the returned error must name the
	// last one, which is what proves the ladder returns the final transport
	// error rather than the first.
	if want := fmt.Sprintf("attempt-%d", wantAttempts); !strings.Contains(err.Error(), want) {
		t.Fatalf("Send returned %q, want the last attempt's error (containing %q)", err, want)
	}
	for attempt := 1; attempt < wantAttempts; attempt++ {
		if stale := fmt.Sprintf("attempt-%d", attempt); strings.Contains(err.Error(), stale) {
			t.Fatalf("Send returned %q, which carries an earlier attempt's error (%q)", err, stale)
		}
	}
	if errors.Is(err, ErrMissingCredentials) {
		t.Fatalf("a delivery failure reported as a config error: %v", err)
	}

	// "No sleep after the final attempt": the next pause would be
	// baseDelay<<(wantAttempts-1) = 2s, so returning within a fraction of
	// baseDelay of the last accept is the proof (300ms margin covers the
	// close sequence and scheduling, and is 1.6x below baseDelay).
	if after := time.Since(times[len(times)-1]); after > 300*time.Millisecond {
		t.Fatalf("Send returned %v after the last attempt, want no pause after it (margin 300ms)", after)
	}
}

// TestSendJitterIsRandomized: the pauses between attempts are jittered, so a
// fleet of failing senders desynchronizes instead of retrying in waves. Run
// several independent ladders CONCURRENTLY (one listener each) so the whole
// test still costs one ladder's wall time, then compare the first pauses
// across runs: each must sit in the code's bracket for that attempt, and the
// spread across runs must exceed 50ms.
//
// Margins: pause(a) = baseDelay<<a + rand[0, baseDelay<<a), i.e. it lands in
// [baseDelay<<a, 2·baseDelay<<a); the upper bound is padded by 500ms for
// dial/accept/scheduling overhead. Variety: with 5 uniform draws over a 500ms
// range, the chance that all land inside one 50ms window is ~5·(50/500)^4 =
// 0.05%, while a de-jittered ladder (fixed 500ms pauses) lands within a few
// ms of each other and fails loudly.
func TestSendJitterIsRandomized(t *testing.T) {
	const (
		runs     = 5
		attempts = 3 // the rule's three attempts (see TestSendRetriesBoundedAttempts)
		minPause = 50 * time.Millisecond
		// overhead pads the observed gap above the code's exclusive intent:
		// dial + greeting + accept handling on a loaded machine.
		overhead = 500 * time.Millisecond
	)

	// Build every listener before starting, so the goroutines below do
	// nothing but send.
	fakes := make([]*failingSMTP, runs)
	senders := make([]*Mailer, runs)
	for i := range runs {
		fakes[i] = newFailingSMTP(t, func(int) string { return "554 jitter" })
		senders[i] = mailerFor(t, fakes[i])
	}

	errs := make([]error, runs)
	var wg sync.WaitGroup
	for i := range runs {
		wg.Go(func() {
			errs[i] = senders[i].Send(context.Background(), "player@example.test", templateFile, templateData)
		})
	}
	wg.Wait()

	firstPauses := make([]time.Duration, runs)
	for i, f := range fakes {
		if errs[i] == nil {
			t.Fatalf("run %d: Send returned nil, want a transport error", i)
		}

		times := f.attemptTimes()
		if len(times) != attempts {
			t.Fatalf("run %d: observed %d dial attempts, want %d", i, len(times), attempts)
		}

		for attempt := range attempts - 1 {
			pause := times[attempt+1].Sub(times[attempt])
			lower := baseDelay << attempt
			upper := 2*lower + overhead
			if pause < lower || pause >= upper {
				t.Fatalf("run %d: pause after attempt %d was %v, want within [%v, %v)", i, attempt+1, pause, lower, upper)
			}
			if attempt == 0 {
				firstPauses[i] = pause
			}
		}
	}

	minPauseSeen, maxPauseSeen := firstPauses[0], firstPauses[0]
	for _, p := range firstPauses[1:] {
		minPauseSeen = min(minPauseSeen, p)
		maxPauseSeen = max(maxPauseSeen, p)
	}
	if spread := maxPauseSeen - minPauseSeen; spread <= minPause {
		t.Fatalf("first pauses across %d runs spanned only %v (%v..%v): the jitter is not random, failing senders would retry in a wave",
			runs, spread, minPauseSeen, maxPauseSeen)
	}
}

// TestSendStopsOnCancellation: canceling the context during the ladder ends
// the send at once with ctx.Err(), without spending the remaining attempts.
// Fails if a canceled send keeps redialing (attempts reach maxRetries) or
// sleeps out its backoff before noticing (the canceled send would then take
// at least baseDelay = 500ms; the assertion allows 300ms).
func TestSendStopsOnCancellation(t *testing.T) {
	f := newFailingSMTP(t, func(int) string { return "554 canceled" })
	m := mailerFor(t, f)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sendErr := make(chan error, 1)
	go func() {
		sendErr <- m.Send(ctx, "player@example.test", templateFile, templateData)
	}()

	<-f.first // at least one attempt is in flight before the caller gives up
	cancelAt := time.Now()
	cancel()

	var err error
	select {
	case err = <-sendErr:
	case <-time.After(5 * time.Second):
		t.Fatal("Send did not return within 5s of cancellation")
	}

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Send after cancellation returned %v, want context.Canceled", err)
	}
	if elapsed := time.Since(cancelAt); elapsed > 300*time.Millisecond {
		t.Fatalf("Send returned %v after cancellation, want it to stop at once (the next pause is at least %v)", elapsed, baseDelay)
	}
	if attempts := f.attemptTimes(); len(attempts) >= 3 {
		t.Fatalf("observed %d dial attempts after cancellation, want fewer than the rule's 3", len(attempts))
	} else {
		// Proves the send really was mid-ladder: one attempt happened, and
		// only the cancellation (not the ladder's end) stopped it.
		t.Logf("canceled after %d of %d attempts", len(attempts), maxRetries)
	}
}

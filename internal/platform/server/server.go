// Package server owns the process lifecycle of the HTTP services in this
// repo: the http.Server, the signal-driven graceful drain, the second-signal
// force-close escalation, and the bounded wait for background tasks.
//
// It is deliberately ignorant of what it serves: Server receives a fully
// built http.Handler (router + middleware, composed by the caller) and never
// imports a module, a store or a mailer. Handlers that outlive their request
// register on Background — the same waitgroup Serve waits out during
// shutdown — so tracker and lifecycle stay one thing.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// Server is the process lifecycle: config that only the server and the
// handler chain need, the fully-built handler, and the background-task
// tracker. Named Server rather than Application: it serves, it does not own
// the domain wiring (net/http.Server stutter precedent).
type Server struct {
	Version string
	Port    int
	Env     string
	Logger  *slog.Logger
	// RootCtx is the process-wide cancel root: canceled on SIGTERM/SIGINT
	// (main wires it to the signal context) and handed to every background
	// task started via Background(). It is set once at construction and
	// never mutated — a dependency like Logger, not ambient mutable state.
	RootCtx context.Context
	// Handler is the fully-built handler chain (router + middleware) to
	// serve. The server never builds it and never looks inside it.
	Handler http.Handler

	wg sync.WaitGroup
}

// RunOptions carries the connection timeouts and the shutdown budgets consumed
// only by the server lifecycle. They are run options, not Server state: they
// cannot drift mid-flight and main needs no knowledge of them (zero value =
// the defaults in consts.go). Tests pass short values so timeout and shutdown
// scenarios run in ms instead of sleeping through the production constants.
type RunOptions struct {
	// ReadTimeout bounds the wait for a request on an accepted connection.
	ReadTimeout time.Duration
	// WriteTimeout bounds the response write once the request header is read.
	WriteTimeout time.Duration
	// IdleTimeout bounds the silence of a keep-alive connection between
	// requests.
	IdleTimeout time.Duration
	// GracePeriod bounds the HTTP drain before force-close.
	GracePeriod time.Duration
	// TaskBudget bounds the wait for background goroutines after the drain.
	TaskBudget time.Duration
}

// readTimeout is the connection read budget: explicit override if set, else
// the package default.
func (o RunOptions) readTimeout() time.Duration {
	if o.ReadTimeout > 0 {
		return o.ReadTimeout
	}
	return ReadTimeout
}

// writeTimeout is the response write budget, same override rule.
func (o RunOptions) writeTimeout() time.Duration {
	if o.WriteTimeout > 0 {
		return o.WriteTimeout
	}
	return WriteTimeout
}

// idleTimeout is the keep-alive silence budget, same override rule.
func (o RunOptions) idleTimeout() time.Duration {
	if o.IdleTimeout > 0 {
		return o.IdleTimeout
	}
	return IdleTimeout
}

// gracePeriod is the HTTP-drain budget: explicit override if set, else the
// package default.
func (o RunOptions) gracePeriod() time.Duration {
	if o.GracePeriod > 0 {
		return o.GracePeriod
	}
	return ShutdownGracePeriod
}

// taskBudget is the background-goroutine wait budget, same override rule.
func (o RunOptions) taskBudget() time.Duration {
	if o.TaskBudget > 0 {
		return o.TaskBudget
	}
	return BackgroundTaskBudget
}

// Background runs fn on a tracked goroutine and hands it the process-wide
// cancel root: fn must honor ctx cancellation (it does not extend the
// request's lifetime — by the time fn runs, the request may already be done).
//
// The goroutines booked here are exactly the ones runServer waits out
// (bounded by RunOptions.TaskBudget) during shutdown, so the tracker and the
// lifecycle that waits on it live together. A panicking task is logged and
// contained: it must not take the process down after the response was sent.
func (s *Server) Background(fn func(ctx context.Context)) {
	s.wg.Go(func() {
		defer func() {
			pv := recover()
			if pv != nil {
				s.Logger.Error(fmt.Sprintf("%v", pv))
			}
		}()

		fn(s.RootCtx)
	})
}

// Serve builds the http.Server around the handler chain and runs it through
// the full shutdown lifecycle with the default timeouts and budgets.
func (s *Server) Serve() error {
	return s.serveWithOptions(RunOptions{})
}

// serveWithOptions is Serve with the connection timeouts and shutdown budgets
// injectable. The composition root calls Serve (zero value: the consts.go
// defaults); tests call this so a timeout scenario observes the real server
// mechanism in ms instead of sleeping through the production constant.
func (s *Server) serveWithOptions(opts RunOptions) error {
	return s.runServer(s.newServer(opts), opts)
}

// newServer builds the http.Server around the handler chain: the address from
// the port, the handler as-is, the three connection timeouts from opts (zero
// value = consts.go defaults) and the error log routed through the package
// logger at Error level. The one place the server is constructed, so the
// production timeouts and the test-injected ones cannot diverge.
func (s *Server) newServer(opts RunOptions) *http.Server {
	return &http.Server{
		Addr:         fmt.Sprintf(":%d", s.Port),
		Handler:      s.Handler,
		IdleTimeout:  opts.idleTimeout(),
		ReadTimeout:  opts.readTimeout(),
		WriteTimeout: opts.writeTimeout(),
		ErrorLog:     slog.NewLogLogger(s.Logger.Handler(), slog.LevelError),
	}
}

// runServer is Serve's testable core: the full shutdown lifecycle (signal
// reaction, escalation, drain, bounded background wait) decoupled from how
// the http.Server and its handler are built. Tests inject a server with a
// hanging handler; production passes the one built in Serve.
func (s *Server) runServer(srv *http.Server, opts RunOptions) error {
	shutdownError := make(chan error)

	go func() {
		<-s.RootCtx.Done()

		s.Logger.Info("stopping server", "addr", srv.Addr)

		// Second-signal escalation: a further SIGTERM/SIGINT during the drain
		// force-closes the server instead of being swallowed by the (already
		// canceled) signal context. signal.Notify stacks with NotifyContext's
		// registration, so arming this now catches the next signal.
		secondSignal := make(chan os.Signal, 1)
		signal.Notify(secondSignal, syscall.SIGINT, syscall.SIGTERM)
		defer signal.Stop(secondSignal)

		drainDone := make(chan struct{})
		go func() {
			select {
			case sig := <-secondSignal:
				s.Logger.Warn("second signal during drain, force-closing", "signal", sig.String())
				_ = srv.Close() // force-close escalation: conns are being dropped regardless
			case <-drainDone:
			}
		}()

		shutdownError <- s.shutdown(srv, opts)

		close(drainDone) // drain over: the watcher goroutine can exit
	}()

	s.Logger.Info("starting server", "addr", srv.Addr, "env", s.Env)

	err := srv.ListenAndServe()
	if !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	// The signal goroutine owns draining; its result (drained, force-closed,
	// or a real failure) is the server's shutdown result.
	err = <-shutdownError
	if err != nil {
		return err
	}

	s.Logger.Info("waiting for background tasks")

	// Bounded wait: background tasks were handed the (already canceled)
	// cancel root, so well-behaved ones finish fast — this budget only
	// bounds misbehaving ones. Timeout is not an error: the shutdown did
	// complete, tasks were abandoned (and said so in the log).
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(opts.taskBudget()):
		s.Logger.Warn("background tasks exceeded budget, exiting", "budget", opts.taskBudget())
	}

	s.Logger.Info("shutdown complete")
	return nil
}

// shutdown drains active connections within a fixed budget, then force-closes
// whatever is left. A completed force-close after a drain timeout is not an
// error — the server did stop, just ungracefully — so only a failing force
// close (or a non-timeout drain failure) is surfaced as an error.
func (s *Server) shutdown(srv *http.Server, opts RunOptions) error {
	ctx, cancel := context.WithTimeout(context.Background(), opts.gracePeriod())
	defer cancel()

	err := srv.Shutdown(ctx)
	if err == nil {
		return nil
	}

	if !errors.Is(err, context.DeadlineExceeded) {
		return err
	}

	s.Logger.Warn("drain exceeded budget, force-closing connections", "budget", opts.gracePeriod())

	if closeErr := srv.Close(); closeErr != nil {
		s.Logger.Error("force close failed", "error", closeErr)
		return closeErr
	}

	return nil
}

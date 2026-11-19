package server

import "time"

// Process lifecycle budgets owned by this package. RunOptions overrides them
// per call (tests use short values); the composition root passes no budgets,
// so these are the process-wide defaults.

// ShutdownGracePeriod bounds the HTTP drain before force-close.
// rule:R-PLATFORM-010
const ShutdownGracePeriod = 10 * time.Second

// BackgroundTaskBudget bounds the wait for background goroutines (mailer
// sends) after the HTTP drain. Sized below ShutdownGracePeriod so the total
// worst-case shutdown (drain + workers) stays inside a typical orchestrator
// kill window (Kubernetes default: 30s) with margin to spare.
// rule:R-PLATFORM-010
const BackgroundTaskBudget = 5 * time.Second

// ReadTimeout bounds how long the server waits for a request to arrive on an
// accepted connection. Without it a client that connects and says nothing —
// or trickles bytes — holds a connection slot forever.
const ReadTimeout = 5 * time.Second

// WriteTimeout bounds the response write, counted from the end of the request
// header read. It is what cuts a handler that streams without end: the client
// must not be able to pin a connection by never letting the response finish.
const WriteTimeout = 10 * time.Second

// IdleTimeout bounds how long a keep-alive connection may sit silent between
// requests before the server closes it — the timeout that retires the
// connections of a client that finished its work and walked away.
const IdleTimeout = time.Minute

package server

import "time"

// Process lifecycle budgets owned by this package. RunOptions overrides them
// per call (tests use short values); the composition root passes no budgets,
// so these are the process-wide defaults.

// ShutdownGracePeriod bounds the HTTP drain before force-close.
const ShutdownGracePeriod = 10 * time.Second

// BackgroundTaskBudget bounds the wait for background goroutines (mailer
// sends) after the HTTP drain. Sized below ShutdownGracePeriod so the total
// worst-case shutdown (drain + workers) stays inside a typical orchestrator
// kill window (Kubernetes default: 30s) with margin to spare.
const BackgroundTaskBudget = 5 * time.Second

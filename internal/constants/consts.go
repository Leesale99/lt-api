package constants

import "time"

const DBTimeout = 3 * time.Second

const (
	DefaultPage     = 1
	DefaultPageSize = 20
)

const (
	ActivationTokenTTL     = 3 * 24 * time.Hour
	AuthenticationTokenTTL = 24 * time.Hour
)

const ShutdownGracePeriod = 10 * time.Second

// BackgroundTaskBudget bounds the wait for background goroutines (mailer
// sends) after the HTTP drain. Sized below ShutdownGracePeriod so the total
// worst-case shutdown (drain + workers) stays inside a typical orchestrator
// kill window (Kubernetes default: 30s) with margin to spare.
const BackgroundTaskBudget = 5 * time.Second

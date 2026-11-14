// Package service holds the game module's use cases (ADR-028, hexagonal
// shape): the ride commands, the settlement handlers that consume the
// calendar's events, the player registrations and the reference reads the
// calendar consults before deleting.
//
// It consumes ports only. The ride state machine and its rules live in
// core/domain; the SQL lives in adapter/outbound/postgres; the calendar
// arrives through adapter/outbound's anti-corruption layer; the ledger and
// the idempotency engine arrive structurally at the composition root. No
// import of another bounded context appears in this package.
package service

import (
	"time"

	"github.com/jackc/pgx/v5"

	"lt-api.aleksrdvn.com/internal/game/internal/core/domain"
	"lt-api.aleksrdvn.com/internal/game/internal/core/port"
)

// pgxTx is the transaction handle the port's *Tx methods take (the documented
// pragmatic exception: the tx type is the boundary's shared currency).
type pgxTx = pgx.Tx

// Deps is the module's dependency set — every port the use cases drive.
// Clock is the injected time source (nil means time.Now): the phase gate
// must read the clock through the same seam the tests control, never
// inline.
type Deps struct {
	Rides       port.RideRepository
	Players     port.PlayerRepository
	Schedule    port.Schedule
	Ledger      port.Ledger
	Idempotency port.Idempotency
	Transactor  port.Transactor
	Clock       func() time.Time
}

// Service is the game context's entry point. It carries no state beyond its
// ports; every method is one use case.
type Service struct {
	rides       port.RideRepository
	players     port.PlayerRepository
	schedule    port.Schedule
	ledger      port.Ledger
	idempotency port.Idempotency
	transactor  port.Transactor
	now         func() time.Time
}

func New(deps Deps) *Service {
	clock := deps.Clock
	if clock == nil {
		clock = time.Now
	}

	return &Service{
		rides:       deps.Rides,
		players:     deps.Players,
		schedule:    deps.Schedule,
		ledger:      deps.Ledger,
		idempotency: deps.Idempotency,
		transactor:  deps.Transactor,
		now:         clock,
	}
}

// RideMarshal is the presentation callback a ride command hands its result
// to: it returns the status and marshaled body the command stores as its
// replayable response (ADR-024). It is game-local (the stored answer is a
// ride); the persistence of that answer is the idempotency engine's.
type RideMarshal func(ride domain.Ride) (int, []byte, error)

// rideResponse runs the command's presentation callback (ADR-024): its answer
// is exactly what gets stored and what a replay returns byte-identical — the
// transport owns status and body shape, the service never re-serializes on
// top of it.
func rideResponse(ride domain.Ride, marshal RideMarshal) (port.SavedResponse, error) {
	status, body, err := marshal(ride)
	if err != nil {
		return port.SavedResponse{}, err
	}
	return port.SavedResponse{Status: status, Body: body}, nil
}

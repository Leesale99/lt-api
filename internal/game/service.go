// Package game owns the match/round lifecycle domain: the state machines
// for matches, rounds and seasons, their persistence, and the schema
// gates that keep Go-side validation and the SQL CHECK constraints in
// lockstep.
package game

import (
	"context"
	"errors"
	"time"

	"lt-api.aleksrdvn.com/internal/store"
)

var (
	ErrRoundNotOpen = errors.New("round is not open")
	ErrNoNextMatch  = errors.New("team has no upcoming match")
)

type Service struct {
	Store *Store
}

func NewService(store *Store) *Service {
	return &Service{
		Store: store,
	}
}

func (s *Service) RidePhase(ctx context.Context, ride Ride) (RoundPhase, error) {
	status, firstStartsAt, lastEndedAt, err := s.Store.Matches.PhaseForMatch(ctx, ride.MatchID)
	if err != nil {
		return "", err
	}

	if status != RoundOpen {
		return "", ErrRoundNotOpen
	}

	phase := Phase(time.Now(), firstStartsAt, lastEndedAt)

	return phase, nil
}

func (s *Service) RideCreate(ctx context.Context, ride Ride) (Ride, error) {
	phase, err := s.RidePhase(ctx, ride)
	if err != nil {
		return Ride{}, err
	}

	err = ride.Create(phase)
	if err != nil {
		return Ride{}, err
	}

	return s.Store.Rides.Insert(ctx, ride)
}

func (s *Service) RideLock(ctx context.Context, rideID int) (Ride, error) {
	ride, err := s.Store.Rides.Get(ctx, rideID)
	if err != nil {
		return Ride{}, err
	}

	phase, err := s.RidePhase(ctx, ride)
	if err != nil {
		return Ride{}, err
	}

	nextMatch, err := s.Store.Matches.NextForTeam(ctx, ride.TeamID)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrRecordNotFound):
			return Ride{}, ErrNoNextMatch
		default:
			return Ride{}, err
		}
	}

	err = ride.Lock(phase, nextMatch.ID)
	if err != nil {
		return Ride{}, err
	}

	return s.Store.Rides.Update(ctx, ride)
}

func (s *Service) RideBurn(ctx context.Context, rideID int) (Ride, error) {
	ride, err := s.Store.Rides.Get(ctx, rideID)
	if err != nil {
		return Ride{}, err
	}

	phase, err := s.RidePhase(ctx, ride)
	if err != nil {
		return Ride{}, err
	}

	err = ride.Burn(phase)
	if err != nil {
		return Ride{}, err
	}

	return s.Store.Rides.Update(ctx, ride)
}

func (s *Service) RideUnlock(ctx context.Context, rideID int) (Ride, error) {
	ride, err := s.Store.Rides.Get(ctx, rideID)
	if err != nil {
		return Ride{}, err
	}

	phase, err := s.RidePhase(ctx, ride)
	if err != nil {
		return Ride{}, err
	}

	err = ride.Unlock(phase)
	if err != nil {
		return Ride{}, err
	}

	return s.Store.Rides.Update(ctx, ride)
}

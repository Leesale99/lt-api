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

func (s *Service) RidePhase(ctx context.Context, rideID int) (Ride, RoundPhase, error) {
	ride, err := s.Store.Rides.Get(ctx, rideID)
	if err != nil {
		return Ride{}, "", err
	}

	match, err := s.Store.Matches.Get(ctx, ride.MatchID)
	if err != nil {
		return ride, "", err
	}

	round, err := s.Store.Rounds.Get(ctx, match.RoundID)
	if err != nil {
		return ride, "", err
	}

	if round.Status != RoundOpen {
		return ride, "", ErrRoundNotOpen
	}

	firstStartsAt, lastEndedAt, err := s.Store.Matches.PhaseWindow(ctx, round.ID)
	if err != nil {
		return ride, "", err
	}

	phase := Phase(time.Now(), firstStartsAt, lastEndedAt)

	return ride, phase, nil
}

func (s *Service) RideLock(ctx context.Context, rideID int) (Ride, error) {
	ride, phase, err := s.RidePhase(ctx, rideID)
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
	ride, phase, err := s.RidePhase(ctx, rideID)
	if err != nil {
		return Ride{}, err
	}

	err = ride.Burn(phase)
	if err != nil {
		return Ride{}, err
	}

	return ride, nil
}

func (s *Service) RideUnlock(ctx context.Context, rideID int) (Ride, error) {
	ride, phase, err := s.RidePhase(ctx, rideID)
	if err != nil {
		return Ride{}, err
	}

	err = ride.Unlock(phase)
	if err != nil {
		return Ride{}, err
	}

	return ride, nil
}

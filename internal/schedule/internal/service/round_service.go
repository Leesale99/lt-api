package service

import (
	"context"
	"log/slog"

	"lt-api.aleksrdvn.com/internal/eventbus"
	"lt-api.aleksrdvn.com/internal/schedule/internal/model"
	"lt-api.aleksrdvn.com/internal/schedule/internal/repository"
	"lt-api.aleksrdvn.com/internal/shared/persistence"
)

// RoundService is the round use case surface. Plain CRUD and the Open
// transition pass through to the repository; Close and the PATCH lifecycle
// dispatch live in round_lifecycle.go, where the post-commit event and the
// transition decision belong.
type RoundService struct {
	repo *repository.RoundRepository

	// matches is the results read behind Close's RoundCompleted payload —
	// the same repository the resolve script writes through, read
	// post-commit.
	matches *repository.MatchRepository

	bus *eventbus.Bus
	log *slog.Logger
}

func NewRoundService(repo *repository.RoundRepository, matches *repository.MatchRepository, bus *eventbus.Bus, log *slog.Logger) *RoundService {
	return &RoundService{repo: repo, matches: matches, bus: bus, log: log}
}

func (s *RoundService) Get(ctx context.Context, id int) (model.Round, error) {
	return s.repo.Get(ctx, id)
}

func (s *RoundService) Insert(ctx context.Context, round model.Round) (model.Round, error) {
	return s.repo.Insert(ctx, round)
}

// Open is the created → open transition: the round flip and its season's
// open → in_progress flip commit together (RoundRepository.Open). No event:
// opening a round announces nothing to the other contexts.
func (s *RoundService) Open(ctx context.Context, round model.Round) (model.Round, error) {
	return s.repo.Open(ctx, round)
}

func (s *RoundService) GetAll(ctx context.Context, seasonID int, status model.RoundStatus, filters persistence.Filters) ([]model.Round, persistence.Metadata, error) {
	return s.repo.GetAll(ctx, seasonID, status, filters)
}

func (s *RoundService) Delete(ctx context.Context, id, seasonID int) error {
	return s.repo.Delete(ctx, id, seasonID)
}

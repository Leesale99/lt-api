package service

import (
	"context"

	"lt-api.aleksrdvn.com/internal/kernel/persistence"
	"lt-api.aleksrdvn.com/internal/schedule/internal/model"
	"lt-api.aleksrdvn.com/internal/schedule/internal/repository"
)

// SeasonService is the season use case surface: pure pass-throughs to the
// repository (seasons carry no rule beyond their validators, which the
// handlers run on request shape). The delete policy lives in delete_policy.go
// because it answers a cross-context question.
type SeasonService struct {
	repo *repository.SeasonRepository
}

func NewSeasonService(repo *repository.SeasonRepository) *SeasonService {
	return &SeasonService{repo: repo}
}

func (s *SeasonService) Get(ctx context.Context, id int) (model.Season, error) {
	return s.repo.Get(ctx, id)
}

func (s *SeasonService) Insert(ctx context.Context, season model.Season) (model.Season, error) {
	return s.repo.Insert(ctx, season)
}

func (s *SeasonService) Update(ctx context.Context, season model.Season) (model.Season, error) {
	return s.repo.Update(ctx, season)
}

func (s *SeasonService) GetAll(ctx context.Context, id int, status model.SeasonStatus, filters persistence.Filters) ([]model.Season, persistence.Metadata, error) {
	return s.repo.GetAll(ctx, id, status, filters)
}

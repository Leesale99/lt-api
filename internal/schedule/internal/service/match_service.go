package service

import (
	"context"

	"lt-api.aleksrdvn.com/internal/schedule/internal/model"
	"lt-api.aleksrdvn.com/internal/schedule/internal/repository"
	"lt-api.aleksrdvn.com/internal/shared/persistence"
)

// MatchService is the match use case surface: pure pass-throughs to the
// repository (matches carry no rule beyond their validators). The resolve
// script is Service.ResolveMatch (resolve_match.go) and the delete policy is
// delete_policy.go — both need more than the repository.
type MatchService struct {
	repo *repository.MatchRepository
}

func NewMatchService(repo *repository.MatchRepository) *MatchService {
	return &MatchService{repo: repo}
}

func (s *MatchService) Get(ctx context.Context, id int) (model.Match, error) {
	return s.repo.Get(ctx, id)
}

func (s *MatchService) Insert(ctx context.Context, match model.Match) (model.Match, error) {
	return s.repo.Insert(ctx, match)
}

func (s *MatchService) Update(ctx context.Context, match model.Match) (model.Match, error) {
	return s.repo.Update(ctx, match)
}

func (s *MatchService) GetAll(ctx context.Context, seasonID, roundID int, status model.MatchStatus, filters persistence.Filters) ([]model.Match, persistence.Metadata, error) {
	return s.repo.GetAll(ctx, seasonID, roundID, status, filters)
}

package service

import (
	"context"

	"lt-api.aleksrdvn.com/internal/kernel/persistence"
	"lt-api.aleksrdvn.com/internal/schedule/internal/model"
	"lt-api.aleksrdvn.com/internal/schedule/internal/repository"
)

// TeamService is the team use case surface: pure pass-throughs to the
// repository (teams carry no rule beyond their validator). The delete policy
// lives in delete_policy.go because it answers a cross-context question.
type TeamService struct {
	repo *repository.TeamRepository
}

func NewTeamService(repo *repository.TeamRepository) *TeamService {
	return &TeamService{repo: repo}
}

func (s *TeamService) Get(ctx context.Context, id int) (model.Team, error) {
	return s.repo.Get(ctx, id)
}

func (s *TeamService) Insert(ctx context.Context, team model.Team) (model.Team, error) {
	return s.repo.Insert(ctx, team)
}

func (s *TeamService) Update(ctx context.Context, team model.Team) (model.Team, error) {
	return s.repo.Update(ctx, team)
}

func (s *TeamService) GetAll(ctx context.Context, name string, filters persistence.Filters) ([]model.Team, persistence.Metadata, error) {
	return s.repo.GetAll(ctx, name, filters)
}

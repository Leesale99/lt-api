package http

// The frozen wire DTOs. The domain's values carry no JSON tags; these types
// are the HTTP shape the pre-refactor handlers emitted, field for field and
// tag for tag, so the boundary translation changes nothing on the wire.

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/shopspring/decimal"

	"lt-api.aleksrdvn.com/internal/game/internal/core/domain"
	"lt-api.aleksrdvn.com/internal/shared/httpx"
)

// RideResponse is a ride as the API exposes it (the frozen wire shape).
type RideResponse struct {
	ID           int              `json:"id"`
	CreatedAt    time.Time        `json:"-"`
	PlayerID     int              `json:"player_id"`
	TeamID       int              `json:"team_id"`
	MatchID      int              `json:"match_id"`
	State        domain.RideState `json:"state"`
	TokensLocked decimal.Decimal  `json:"tokens_locked"`
	BaseAtLock   decimal.Decimal  `json:"base_at_lock"`
	Acc          decimal.Decimal  `json:"-"`
	Streak       int              `json:"-"`
	Version      int              `json:"version"`
}

// rideFromDomain maps one ride onto its wire shape.
func rideFromDomain(ride domain.Ride) RideResponse {
	return RideResponse{
		ID:           ride.ID,
		CreatedAt:    ride.CreatedAt,
		PlayerID:     ride.PlayerID,
		TeamID:       ride.TeamID,
		MatchID:      ride.MatchID,
		State:        ride.State,
		TokensLocked: ride.TokensLocked,
		BaseAtLock:   ride.BaseAtLock,
		Acc:          ride.Acc,
		Streak:       ride.Streak,
		Version:      ride.Version,
	}
}

// ridesFromDomain maps a ride slice for the list endpoint.
func ridesFromDomain(rides []domain.Ride) []RideResponse {
	out := make([]RideResponse, 0, len(rides))
	for _, ride := range rides {
		out = append(out, rideFromDomain(ride))
	}
	return out
}

// playerResponse is a player as the API exposes it.
type playerResponse struct {
	ID             int       `json:"id"`
	CreatedAt      time.Time `json:"-"`
	Name           string    `json:"name"`
	UserID         *int      `json:"-"`
	SeasonID       int       `json:"season_id"`
	FavoriteTeamID int       `json:"favorite_team_id"`
	Version        int       `json:"version"`
}

// playerFromDomain maps one player onto its wire shape.
func playerFromDomain(player domain.Player) playerResponse {
	return playerResponse{
		ID:             player.ID,
		CreatedAt:      player.CreatedAt,
		Name:           player.Name,
		UserID:         player.UserID,
		SeasonID:       player.SeasonID,
		FavoriteTeamID: player.FavoriteTeamID,
		Version:        player.Version,
	}
}

// playersFromDomain maps a player slice for the list endpoint.
func playersFromDomain(players []domain.Player) []playerResponse {
	out := make([]playerResponse, 0, len(players))
	for _, player := range players {
		out = append(out, playerFromDomain(player))
	}
	return out
}

// rideMarshal is the presentation callback the ride command handlers share:
// the same shape writeJSON emits (MarshalIndent + newline), so a replay is
// byte-identical to the original response (ADR-024).
func rideMarshal(ride domain.Ride) (int, []byte, error) {
	body, err := json.MarshalIndent(httpx.Envelope{"ride": rideFromDomain(ride)}, "", "\t")
	if err != nil {
		return 0, nil, err
	}
	return http.StatusOK, append(body, '\n'), nil
}

// rideCreateMarshal is create's presentation callback: same envelope as
// rideMarshal, but status 201 — that status is what the command stores, so a
// replay answers 201 too (ADR-024: the stored answer is the replay).
func rideCreateMarshal(ride domain.Ride) (int, []byte, error) {
	body, err := json.MarshalIndent(httpx.Envelope{"ride": rideFromDomain(ride)}, "", "\t")
	if err != nil {
		return 0, nil, err
	}
	return http.StatusCreated, append(body, '\n'), nil
}

package postgres

// Adapter-level tests for PlayerRepository's delete: the happy path, the
// unknown row, and the driver refusal a registration still carrying rides
// produces — rides_player_id_fkey's 23503, which the store maps to
// domain.ErrRecordInUse (R-GAME-035). Harness in testmain_test.go.

import (
	"context"
	"errors"
	"testing"

	"github.com/shopspring/decimal"

	"lt-api.aleksrdvn.com/internal/game/internal/core/domain"
)

func TestPlayerRepository_Delete(t *testing.T) {
	requireDB(t)

	ctx := context.Background()
	players := NewPlayerRepository(gamePool)

	t.Run("an unreferenced registration is deleted", func(t *testing.T) {
		roundID, homeID, awayID := seedFixture(ctx, t)
		_ = roundID
		_ = awayID
		playerID := plantPlayer(ctx, t, homeID)

		if err := players.Delete(ctx, playerID, 1); err != nil {
			t.Fatalf("Delete() = %v, want nil", err)
		}

		if _, err := players.Get(ctx, playerID); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("Get() = %v, want domain.ErrNotFound after the delete", err)
		}
	})

	t.Run("an unknown registration is ErrNotFound", func(t *testing.T) {
		seedFixture(ctx, t)

		if err := players.Delete(ctx, 424242, 1); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("Delete() = %v, want domain.ErrNotFound", err)
		}
	})

	t.Run("a registration its rides still name is ErrRecordInUse", func(t *testing.T) {
		roundID, homeID, awayID := seedFixture(ctx, t)
		matchID := insertFutureMatch(ctx, t, 1, roundID, homeID, awayID, "30 days")
		playerID := plantPlayer(ctx, t, homeID)

		if _, err := rideRepo.Insert(ctx, domain.Ride{
			PlayerID:     playerID,
			TeamID:       homeID,
			MatchID:      matchID,
			State:        domain.RideLocked,
			TokensLocked: decimal.NewFromInt(100),
			BaseAtLock:   decimal.NewFromInt(95),
		}); err != nil {
			t.Fatalf("plant ride: %v", err)
		}

		err := players.Delete(ctx, playerID, 1)

		if !errors.Is(err, domain.ErrRecordInUse) {
			t.Fatalf("Delete() = %v, want domain.ErrRecordInUse (rides_player_id_fkey)", err)
		}

		// The database refused the delete: the registration is still there.
		if _, err := players.Get(ctx, playerID); err != nil {
			t.Fatalf("Get() = %v, want the registration untouched", err)
		}
	})
}

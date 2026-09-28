package game

// Unit tests for the season and round update validators: the lifecycle
// no-regression rule shared via checkStatusRegression (ADR-008). Pure logic,
// no database. The vocabulary and shape rules these validators also run are
// the same code paths already covered by the API tests; here the focus is
// the transition matrix.

import (
	"testing"

	"lt-api.aleksrdvn.com/internal/validator"
)

// assertValidatorErrors compares a validator's errors against the expected
// field -> message map (empty map = must be valid). Shared by the validator
// unit tests in this package.
func assertValidatorErrors(t *testing.T, v *validator.Validator, wantErrs map[string]string) {
	t.Helper()

	if len(v.Errors) != len(wantErrs) {
		t.Fatalf("got errors %v, want %v", v.Errors, wantErrs)
	}
	for field, wantMsg := range wantErrs {
		gotMsg, ok := v.Errors[field]
		if !ok {
			t.Errorf("expected error on field %q, got errors %v", field, v.Errors)
		} else if gotMsg != wantMsg {
			t.Errorf("field %q: got message %q, want %q", field, gotMsg, wantMsg)
		}
	}
}

func TestValidateSeasonUpdate(t *testing.T) {
	tests := []struct {
		name     string
		old      SeasonStatus
		new      SeasonStatus
		wantErrs map[string]string
	}{
		{
			name:     "created to open",
			old:      SeasonCreated,
			new:      SeasonOpen,
			wantErrs: map[string]string{},
		},
		{
			name:     "open to in_progress",
			old:      SeasonOpen,
			new:      SeasonInProgress,
			wantErrs: map[string]string{},
		},
		{
			name:     "in_progress to closed",
			old:      SeasonInProgress,
			new:      SeasonClosed,
			wantErrs: map[string]string{},
		},
		{
			name:     "same status is a no-op update",
			old:      SeasonOpen,
			new:      SeasonOpen,
			wantErrs: map[string]string{},
		},
		{
			name:     "open back to created",
			old:      SeasonOpen,
			new:      SeasonCreated,
			wantErrs: map[string]string{"status": "cannot move to an earlier stage of the season lifecycle"},
		},
		{
			name:     "in_progress back to open",
			old:      SeasonInProgress,
			new:      SeasonOpen,
			wantErrs: map[string]string{"status": "cannot move to an earlier stage of the season lifecycle"},
		},
		{
			name:     "closed back to in_progress",
			old:      SeasonClosed,
			new:      SeasonInProgress,
			wantErrs: map[string]string{"status": "cannot move to an earlier stage of the season lifecycle"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := validator.New()
			ValidateSeasonUpdate(v, Season{Status: tt.old}, Season{Status: tt.new})
			assertValidatorErrors(t, v, tt.wantErrs)
		})
	}
}

func TestValidateRoundUpdate(t *testing.T) {
	round := func(status RoundStatus) Round { return Round{SeasonID: 1, Number: 1, Status: status} }

	tests := []struct {
		name     string
		old      RoundStatus
		new      RoundStatus
		wantErrs map[string]string
	}{
		{
			name:     "created to open",
			old:      RoundCreated,
			new:      RoundOpen,
			wantErrs: map[string]string{},
		},
		{
			name:     "open to closed",
			old:      RoundOpen,
			new:      RoundClosed,
			wantErrs: map[string]string{},
		},
		{
			name:     "same status is a no-op update",
			old:      RoundOpen,
			new:      RoundOpen,
			wantErrs: map[string]string{},
		},
		{
			name:     "open back to created",
			old:      RoundOpen,
			new:      RoundCreated,
			wantErrs: map[string]string{"status": "cannot move to an earlier stage of the round lifecycle"},
		},
		{
			name:     "closed back to open",
			old:      RoundClosed,
			new:      RoundOpen,
			wantErrs: map[string]string{"status": "cannot move to an earlier stage of the round lifecycle"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := validator.New()
			ValidateRoundUpdate(v, round(tt.old), round(tt.new))
			assertValidatorErrors(t, v, tt.wantErrs)
		})
	}
}

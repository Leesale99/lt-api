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
		old      string
		new      string
		wantErrs map[string]string
	}{
		{
			name:     "created to open",
			old:      "created",
			new:      "open",
			wantErrs: map[string]string{},
		},
		{
			name:     "open to in_progress",
			old:      "open",
			new:      "in_progress",
			wantErrs: map[string]string{},
		},
		{
			name:     "in_progress to closed",
			old:      "in_progress",
			new:      "closed",
			wantErrs: map[string]string{},
		},
		{
			name:     "same status is a no-op update",
			old:      "open",
			new:      "open",
			wantErrs: map[string]string{},
		},
		{
			name:     "open back to created",
			old:      "open",
			new:      "created",
			wantErrs: map[string]string{"status": "cannot move to an earlier stage of the season lifecycle"},
		},
		{
			name:     "in_progress back to open",
			old:      "in_progress",
			new:      "open",
			wantErrs: map[string]string{"status": "cannot move to an earlier stage of the season lifecycle"},
		},
		{
			name:     "closed back to in_progress",
			old:      "closed",
			new:      "in_progress",
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
	round := func(status string) Round { return Round{SeasonID: 1, Number: 1, Status: status} }

	tests := []struct {
		name     string
		old      string
		new      string
		wantErrs map[string]string
	}{
		{
			name:     "created to open",
			old:      "created",
			new:      "open",
			wantErrs: map[string]string{},
		},
		{
			name:     "open to closed",
			old:      "open",
			new:      "closed",
			wantErrs: map[string]string{},
		},
		{
			name:     "same status is a no-op update",
			old:      "open",
			new:      "open",
			wantErrs: map[string]string{},
		},
		{
			name:     "open back to created",
			old:      "open",
			new:      "created",
			wantErrs: map[string]string{"status": "cannot move to an earlier stage of the round lifecycle"},
		},
		{
			name:     "closed back to open",
			old:      "closed",
			new:      "open",
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

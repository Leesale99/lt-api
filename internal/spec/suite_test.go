package spec

// The godog suite's registration: one hook that builds a fresh world per
// scenario, and the three step families (ride lifecycle, settlement, database
// sabotage).

import (
	"context"

	"github.com/cucumber/godog"
)

// InitializeScenario wires the suite. It runs once per suite, not per scenario;
// the Before hook builds the scenario's world and attaches it to the context the
// steps thread between themselves.
func InitializeScenario(sc *godog.ScenarioContext) {
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		w, err := newWorld(ctx)
		if err != nil {
			return ctx, err
		}
		return context.WithValue(ctx, worldKey{}, w), nil
	})

	registerRideSteps(sc)
	registerSettlementSteps(sc)
	registerSabotageSteps(sc)
}

// stepOK is the no-failure return for a step.
func stepOK(ctx context.Context) (context.Context, error) { return ctx, nil }

// stepFailed reports a step failure.
func stepFailed(ctx context.Context, err error) (context.Context, error) { return ctx, err }

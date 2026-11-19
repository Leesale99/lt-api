> **Superseded — historical record.** Nothing reads docs/spec/history/; kept for provenance.
> Coverage is reported live by `make audit` (`spec/check`); this hand-maintained copy is stale by design.

# Coverage — where every implemented rule is proved

This is the C6 snapshot of the C-fanout: it pairs `docs/spec/rules.yaml` (the canonical
catalog) with the feature tree under `docs/spec/features/` and records, rule by rule, which
of the 106 implemented rules has a scenario, which verification pointers already carry it,
and — where no scenario exists — the one reason it was left out. It is a hand-written
snapshot for gate C7; Phase E may regenerate it from the two sources with `cmd/spec`. The
locked decision 3 target it is measured against is **~45–60 business-visible rules with
scenarios** (`plan.md`, decision 3: mechanical field rules stay unit-tested but catalogued).

A row's **scenario** cell is the catalog's representative `verification.gherkin` pointer,
verbatim; **unit** / **e2e** list the rule's existing pointers abbreviated to
`<module>/<path>::Test` (minus the `internal/` prefix); an **excluded** row carries the
pointers it does have plus one line of rationale, category first. "—" means the rule has no
pointer of that kind today — not that none could exist.

## schedule — 30 implemented rules, 25 with a scenario

| rule | scenario | unit | e2e | excluded (reason) |
| --- | --- | --- | --- | --- |
| `R-SCHEDULE-001` | — | schedule/model/matches_validation_test.go::TestValidateNewMatch | e2e/seasons_test.go::TestCreateSeasonHandler<br>e2e/rounds_test.go::TestCreateRoundHandler<br>e2e/matches_test.go::TestCreateMatchHandler | interface — the create shape is framing; the "client cannot choose" half is a wire unknown-key refusal. |
| `R-SCHEDULE-002` | docs/spec/features/schedule/fixtures.feature::A fixture cannot be announced for a kickoff that has arrived | schedule/model/matches_validation_test.go::TestValidateNewMatch | e2e/matches_test.go::TestCreateMatchHandler |  |
| `R-SCHEDULE-003` | docs/spec/features/schedule/lifecycle.feature::A closed match cannot go back to running | schedule/model/lifecycle_test.go::TestValidateSeasonUpdate<br>schedule/model/lifecycle_test.go::TestValidateRoundUpdate<br>schedule/model/matches_validation_test.go::TestValidateMatchUpdate<br>schedule/repository/constraints_test.go::TestStatusRankGatesMirrorGoRanks<br>schedule/repository/gates_test.go::TestRoundFreezeGate<br>schedule/repository/gates_test.go::TestSeasonFreezeGate | e2e/rounds_test.go::TestUpdateRoundHandler<br>e2e/matches_test.go::TestUpdateMatchHandler |  |
| `R-SCHEDULE-004` | docs/spec/features/schedule/lifecycle.feature::A status the league never defined cannot be stored | schedule/repository/constraints_test.go::TestStatusVocabularyIsInSync<br>schedule/model/matches_validation_test.go::TestValidateNewMatch | e2e/rounds_test.go::TestUpdateRoundHandler |  |
| `R-SCHEDULE-005` | docs/spec/features/schedule/lifecycle.feature::A started match cannot be postponed | schedule/repository/gates_test.go::TestMatchFreezeGate<br>schedule/repository/gates_test.go::TestRoundFreezeGate<br>schedule/repository/gates_test.go::TestSeasonFreezeGate | e2e/matches_test.go::TestPostponeStartedMatchFrozen |  |
| `R-SCHEDULE-006` | docs/spec/features/schedule/fixtures.feature::The database refuses a score that does not fit the match | schedule/repository/constraints_test.go::TestMatchConstraints<br>schedule/model/matches_validation_test.go::TestValidateNewMatch<br>schedule/model/matches_validation_test.go::TestValidateMatchUpdate | — |  |
| `R-SCHEDULE-007` | docs/spec/features/schedule/fixtures.feature::The database refuses an end that does not fit the match | schedule/repository/constraints_test.go::TestMatchConstraints | — |  |
| `R-SCHEDULE-008` | docs/spec/features/schedule/fixtures.feature::The end the organiser resolved with is the end the calendar keeps | schedule/repository/constraints_test.go::TestMatchConstraints | e2e/matches_test.go::TestResolveMatchHandler |  |
| `R-SCHEDULE-009` | docs/spec/features/schedule/fixtures.feature::The database refuses a fixture priced at evens or below | schedule/repository/constraints_test.go::TestMatchConstraints<br>schedule/model/matches_validation_test.go::TestValidateNewMatch | — |  |
| `R-SCHEDULE-010` | docs/spec/features/schedule/fixtures.feature::The organiser cannot announce a team against itself | schedule/repository/constraints_test.go::TestMatchConstraints | — |  |
| `R-SCHEDULE-011` | docs/spec/features/schedule/fixtures.feature::The database refuses a fixture moved into another season | schedule/repository/constraints_test.go::TestMatchConstraints | e2e/matches_test.go::TestCreateMatchHandler |  |
| `R-SCHEDULE-012` | — | — | e2e/rounds_test.go::TestUpdateRoundHandler<br>e2e/matches_test.go::TestUpdateMatchHandler | repository-shaped — a parentless round is re-parentable in SQL with no refusal; the observable half is wire framing. |
| `R-SCHEDULE-013` | docs/spec/features/schedule/fixtures.feature::The database refuses a round numbered outside the fixture list | schedule/repository/constraints_test.go::TestRoundAndSeasonConstraints | e2e/rounds_test.go::TestUpdateRoundHandler<br>e2e/rounds_test.go::TestCreateRoundHandler |  |
| `R-SCHEDULE-014` | docs/spec/features/schedule/fixtures.feature::The organiser cannot add a second round 1 | schedule/repository/constraints_test.go::TestRoundAndSeasonConstraints | e2e/rounds_test.go::TestCreateRoundDuplicateNumber<br>e2e/rounds_test.go::TestUpdateRoundHandler |  |
| `R-SCHEDULE-015` | docs/spec/features/schedule/lifecycle.feature::The database refuses a round opened behind an unclosed predecessor | schedule/service/rounds_close_test.go::TestRoundClose_MidTxFailureLeavesNoTrace | e2e/rounds_test.go::TestUpdateRoundHandler |  |
| `R-SCHEDULE-016` | docs/spec/features/schedule/lifecycle.feature::The database refuses a close over a match still running | schedule/service/rounds_close_test.go::TestRoundClose | e2e/rounds_test.go::TestUpdateRoundHandler |  |
| `R-SCHEDULE-017` | docs/spec/features/schedule/lifecycle.feature::Opening round 1 starts the season | — | e2e/rounds_test.go::TestOpenRoundStartsSeason<br>e2e/rounds_test.go::TestOpenRoundSeasonFlipIsIdempotent |  |
| `R-SCHEDULE-018` | docs/spec/features/schedule/lifecycle.feature::Closing round 38 closes the season | schedule/service/rounds_close_test.go::TestRoundClose | — |  |
| `R-SCHEDULE-019` | — | schedule/service/rounds_close_test.go::TestRoundClose | — | mechanical — the announce half is an in-process event fact; the row-visible half is R-GAME-009's scenario. |
| `R-SCHEDULE-020` | docs/spec/features/schedule/resolve.feature::A result ending in the future is refused | — | e2e/matches_test.go::TestResolveMatchHandler |  |
| `R-SCHEDULE-021` | docs/spec/features/schedule/resolve.feature::A result written against an outdated reading is refused | game/core/service/rides_concurrency_test.go::TestResolveMatchRaces | e2e/matches_test.go::TestResolveMatchHandler<br>e2e/matches_test.go::TestResolveMatchSettlesRowOnce |  |
| `R-SCHEDULE-022` | docs/spec/features/schedule/resolve.feature::A lock landing exactly an hour before kickoff is refused | schedule/model/round_phase_test.go::TestPhase | — |  |
| `R-SCHEDULE-023` | docs/spec/features/schedule/deletion.feature::The database refuses to delete a round with a started match | schedule/repository/gates_test.go::TestRoundDeleteGateRejectsMatchHistory<br>schedule/repository/gates_test.go::TestSeasonDeleteGateRejectsMatchHistory | e2e/rounds_test.go::TestDeleteRoundHandler<br>e2e/seasons_test.go::TestDeleteSeasonHandler<br>e2e/seasons_test.go::TestDeleteSeasonRefusalKeepsSeasonPlayers |  |
| `R-SCHEDULE-024` | docs/spec/features/schedule/deletion.feature::Deleting a season takes its rounds and fixtures with it | schedule/repository/constraints_test.go::TestMatchConstraints | e2e/rounds_test.go::TestDeleteOpenRoundCascadesMatches<br>e2e/seasons_test.go::TestDeleteCreatedSeasonHandler |  |
| `R-SCHEDULE-025` | docs/spec/features/schedule/deletion.feature::A team a fixture names cannot be deleted | schedule/repository/constraints_test.go::TestTeamDeleteRestricted | e2e/teams_test.go::TestDeleteTeamHandler<br>e2e/teams_test.go::TestDeleteUnreferencedTeamHandler |  |
| `R-SCHEDULE-026` | docs/spec/features/schedule/deletion.feature::A match a ride still sits on cannot be deleted | schedule/service/rounds_close_test.go::TestMatchDeleteRestricted | e2e/matches_test.go::TestDeleteMatchHandler<br>e2e/teams_test.go::TestDeleteTeamHandler<br>e2e/seasons_test.go::TestDeleteSeasonHandler<br>e2e/seasons_test.go::TestDeleteSeasonRefusalKeepsSeasonPlayers |  |
| `R-SCHEDULE-027` | docs/spec/features/schedule/deletion.feature::A deleted season takes its registrations with it | — | e2e/seasons_test.go::TestDeleteSeasonHandler |  |
| `R-SCHEDULE-028` | docs/spec/features/schedule/deletion.feature::An edit written against an outdated reading is refused | — | e2e/rounds_test.go::TestUpdateRoundHandler<br>e2e/matches_test.go::TestUpdateMatchHandler<br>e2e/teams_test.go::TestUpdateTeamHandler<br>e2e/seasons_test.go::TestUpdateSeasonHandler |  |
| `R-SCHEDULE-029` | — | schedule/repository/constraints_test.go::TestTeamConstraints | e2e/teams_test.go::TestCreateTeamHandler | mechanical — name, badge and description bounds are field rules, unit territory. |
| `R-SCHEDULE-030` | — | — | e2e/teams_test.go::TestCreateTeamHandler | interface — the badge is a URL shape, mechanical besides. |

## game — 25 implemented rules, 18 with a scenario

| rule | scenario | unit | e2e | excluded (reason) |
| --- | --- | --- | --- | --- |
| `R-GAME-001` | docs/spec/features/game/ride_lifecycle.feature::A new ride is born locked | game/core/domain/ride_test.go::TestRide_Create<br>game/adapter/outbound/postgres/ride_repo_test.go::TestRideRepository_InsertAndGet | e2e/rides_test.go::TestCreateRideHandler |  |
| `R-GAME-002` | docs/spec/features/game/ride_lifecycle.feature::A won match leaves the ride waiting on a decision | game/core/domain/ride_test.go::TestRide_InvalidTransitions<br>game/adapter/outbound/postgres/ride_repo_test.go::TestRideRepository_Update | e2e/rides_test.go::TestRideCommandHandlers |  |
| `R-GAME-003` | docs/spec/features/game/ride_lifecycle.feature::A burned ride accepts no further command | game/core/domain/ride_test.go::TestRide_TerminalStates<br>game/adapter/outbound/postgres/ride_repo_test.go::TestRideRepository_Update | — |  |
| `R-GAME-004` | docs/spec/features/game/settlement.feature::A stake of nothing is refused and no ride is born | game/core/domain/ride_validation_test.go::TestValidateRide | e2e/rides_test.go::TestCreateRideHandler |  |
| `R-GAME-005` | docs/spec/features/game/settlement.feature::Unlocking a won ride forfeits the bonus it accrued | game/core/service/constraints_test.go::TestRideMoneyStateCoupling<br>game/core/domain/ride_test.go::TestRide_Unlock<br>game/core/domain/ride_test.go::TestRide_Lost<br>game/adapter/outbound/postgres/ride_repo_test.go::TestRideRepository_Update | — |  |
| `R-GAME-006` | docs/spec/features/game/settlement.feature::A win accrues the stake times the odds minus one times the streak multiplier | game/core/domain/ride_bonus_test.go::TestCalculateBonus<br>game/core/service/service_test.go::TestService_OnMatchResultRecorded | — |  |
| `R-GAME-007` | docs/spec/features/game/ride_lifecycle.feature::A lock attempt inside the last hour before kickoff is refused | game/core/domain/ride_test.go::TestRide_Create<br>game/core/domain/ride_test.go::TestRide_WonPending<br>game/core/domain/ride_test.go::TestRide_Lost<br>game/core/domain/ride_test.go::TestRide_Lock<br>game/core/domain/ride_test.go::TestRide_Burn<br>game/core/domain/ride_test.go::TestRide_Unlock<br>game/core/service/service_test.go::TestService_RideCommandPhaseRejection | e2e/rides_test.go::TestRideCommandHandlers |  |
| `R-GAME-008` | docs/spec/features/game/settlement.feature::A command after the round has closed is refused and writes nothing | game/core/service/rides_concurrency_test.go::TestService_RideLockRacesRoundClose<br>game/core/service/rides_concurrency_test.go::TestService_RideCreateRacesRoundClose<br>game/adapter/outbound/postgres/ride_repo_test.go::TestRideRepository_DecisionGuardedTx | e2e/rides_test.go::TestCreateRideHandler |  |
| `R-GAME-009` | docs/spec/features/game/settlement.feature::A completing round unlocks the ride still waiting on a decision | game/core/service/service_test.go::TestService_OnRoundCompleted<br>game/core/service/rides_concurrency_test.go::TestSettlementHandlersSerializeOnTheRound | — |  |
| `R-GAME-010` | docs/spec/features/game/settlement.feature::Each ride arrives where the committed result puts it<br>docs/spec/features/game/settlement.feature::A settlement that misreports the odds is refused and pays nothing | game/core/service/score_state_rule_test.go::TestWinnerScoreStateRule<br>game/core/service/score_state_rule_test.go::TestScoreStateRuleDB<br>schedule/model/matches_winner_test.go::TestWinner<br>game/core/service/service_test.go::TestService_OnMatchResultRecorded<br>game/adapter/outbound/postgres/ride_repo_test.go::TestRideRepository_Update | — |  |
| `R-GAME-011` | docs/spec/features/game/players.feature::A change to someone else's registration is refused | — | e2e/players_test.go::TestUpdatePlayerOwnership<br>e2e/players_test.go::TestDeletePlayerHandler |  |
| `R-GAME-012` | docs/spec/features/game/players.feature::A registration made in a season belongs to the member who made it | — | e2e/players_test.go::TestCreatePlayerHandler<br>e2e/players_test.go::TestUpdatePlayerOwnership |  |
| `R-GAME-013` | docs/spec/features/game/players.feature::A registration naming a team the calendar does not carry is refused | game/core/service/service_test.go::TestService_RideCreateUnknownParents | e2e/players_test.go::TestCreatePlayerHandler<br>e2e/players_test.go::TestUpdatePlayerHandler |  |
| `R-GAME-014` | docs/spec/features/game/players.feature::A second registration in the same season is refused as a duplicate | — | e2e/players_test.go::TestCreatePlayerDuplicateRegistration |  |
| `R-GAME-015` | — | game/core/service/service_test.go::TestService_RideUnlock<br>game/core/service/service_test.go::TestService_OnMatchResultRecorded<br>game/core/service/service_test.go::TestService_OnRoundCompleted<br>game/core/service/constraints_test.go::TestRideMoneyStateCoupling | — | mechanical — the forfeit-before-the-state-move ordering is internal; its observable face is R-GAME-005's scenario. |
| `R-GAME-016` | docs/spec/features/game/settlement.feature::The same result arriving twice pays the bonus once | game/core/service/rides_concurrency_test.go::TestSettlementHandlersSerializeOnTheRound<br>game/core/service/service_test.go::TestService_OnMatchResultRecorded<br>game/core/service/service_test.go::TestService_OnRoundCompleted | e2e/matches_test.go::TestResolveMatchSettlesRowOnce |  |
| `R-GAME-017` | docs/spec/features/game/players.feature::An edit claiming a version the registration has moved past is refused as a stale reading | — | e2e/players_test.go::TestUpdatePlayerHandler |  |
| `R-GAME-018` | docs/spec/features/game/idempotency.feature::A retry under the same key comes back with the recorded answer | game/core/service/idempotency_test.go::TestService_RideLockIdempotency<br>game/core/service/idempotency_test.go::TestService_RideCreateIdempotent | e2e/rides_test.go::TestCreateRideIdempotentHandler<br>e2e/rides_test.go::TestRideLockIdempotentHandler |  |
| `R-GAME-020` | — | game/core/domain/ride_validation_test.go::TestValidateRide<br>game/core/domain/ride_validation_test.go::TestValidateRideState<br>kernel/idempotency/idempotency_test.go::TestValidateIdempotencyToken | e2e/rides_test.go::TestCreateRideHandler<br>e2e/rides_test.go::TestRideCommandHandlers<br>e2e/rides_test.go::TestCreateRideHandlerBodyTooLarge<br>e2e/rides_test.go::TestListRidesHandler | interface — the ride command's framing: fields, sizes, body shape. |
| `R-GAME-021` | — | — | e2e/players_test.go::TestCreatePlayerHandler<br>e2e/players_test.go::TestUpdatePlayerHandler | interface — the registration command's framing. |
| `R-GAME-023` | — | game/core/service/rides_concurrency_test.go::TestSettlementHandlersSerializeOnTheRound | — | concurrency — two settlements running back to back is a §10 interleaving. |
| `R-GAME-024` | — | game/core/service/rides_concurrency_test.go::TestService_RideCommandsRaceOnOneRide<br>game/core/service/rides_concurrency_test.go::TestService_RideBurnRacesExactlyOnce<br>game/adapter/outbound/postgres/ride_repo_test.go::TestRideRepository_DecisionGuardedTx | — | refusal-mapping — the fixed order of refusal causes is a mechanism; the refusals themselves are the pilot's. |
| `R-GAME-025` | docs/spec/features/game/ride_lifecycle.feature::Continuing moves the won ride onto its next match | game/adapter/outbound/postgres/ride_repo_test.go::TestRideRepository_Update<br>game/core/domain/ride_test.go::TestRide_Lock | — |  |
| `R-GAME-026` | — | game/core/service/service_test.go::TestService_RideCreateUnknownParents<br>game/core/service/service_test.go::TestService_RideLock<br>game/core/service/service_test.go::TestService_RideCreate | — | boundary — the calendar facade is wiring; the observables it produces belong to other rules. |
| `R-GAME-027` | — | game/core/service/service_test.go::TestService_OnRoundCompleted | — | persistence-fact — the partial index's shape, not what it decides. |

## identity — 22 implemented rules, 11 with a scenario

| rule | scenario | unit | e2e | excluded (reason) |
| --- | --- | --- | --- | --- |
| `R-IDENTITY-001` | — | identity/model/user_test.go::TestValidateRegistration | e2e/users_test.go::TestRegisterUserHandler | interface — the fields a registration carries are framed before the use case runs. |
| `R-IDENTITY-002` | — | identity/model/user_test.go::TestValidateRegistration | e2e/users_test.go::TestRegisterUserHandler | interface — password length is field framing, checked before it is hashed. |
| `R-IDENTITY-003` | — | identity/model/user_test.go::TestPasswordHashRoundTrip<br>identity/repository/user_repo_test.go::TestInsert | e2e/users_test.go::TestRegisterUserHandlerSuccessState | persistence-fact — hash-only storage is domain-internal; no facade observable. |
| `R-IDENTITY-004` | — | — | e2e/users_test.go::TestRegisterUserHandlerSuccessState<br>e2e/users_test.go::TestRegisterUserHandler | interface — what the answer must not carry is the shape of the answer; e2e holds it. |
| `R-IDENTITY-005` | — | identity/model/user_token_test.go::TestGenerateUserToken | e2e/auth_test.go::TestAuthenticateMiddleware | mechanical — the token length is a field rule, and the mailer is a no-op so the mint has no observer. |
| `R-IDENTITY-006` | — | identity/repository/user_token_repo_test.go::TestNewAndGetForToken<br>identity/model/user_token_test.go::TestGenerateUserToken | — | persistence-fact — only a token's hash is stored; nothing is refused. |
| `R-IDENTITY-007` | docs/spec/features/identity/account.feature::Her activation token is good for three days | — | — |  |
| `R-IDENTITY-008` | docs/spec/features/identity/account.feature::A sign-in token past its day is refused exactly as one that never existed | identity/repository/user_token_repo_test.go::TestNewAndGetForToken | e2e/auth_test.go::TestAuthenticateMiddleware |  |
| `R-IDENTITY-009` | docs/spec/features/identity/account.feature::The same activation token cannot be spent twice | identity/service/activation_test.go::TestActivate<br>identity/repository/user_token_repo_test.go::TestDeleteAllForUser | — |  |
| `R-IDENTITY-010` | — | identity/repository/user_repo_test.go::TestUpdate<br>identity/service/activation_test.go::TestActivate | — | persistence-fact — the account-row CAS; no surface offers an account a version claim. |
| `R-IDENTITY-011` | docs/spec/features/identity/account.feature::An email already taken in another case is refused as a duplicate | identity/repository/user_repo_test.go::TestInsert<br>identity/repository/user_repo_test.go::TestUpdate<br>identity/repository/user_repo_test.go::TestGetByEmail | e2e/users_test.go::TestRegisterUserHandlerDuplicateIsolatedFromValidation |  |
| `R-IDENTITY-012` | docs/spec/features/identity/account.feature::A registration arrives holding the standard user role | — | — |  |
| `R-IDENTITY-013` | — | — | e2e/auth_test.go::TestRequirePermissionMiddleware | persistence-fact — seeded uniqueness bookkeeping at the schema; nothing a page can observe. |
| `R-IDENTITY-014` | docs/spec/features/identity/access.feature::The standard user may read the calendar and manage her own player | — | e2e/auth_test.go::TestRequirePermissionMiddleware<br>e2e/rides_test.go::TestRideRoutesPermissionGate |  |
| `R-IDENTITY-015` | — | — | e2e/auth_test.go::TestRequirePermissionMiddleware | persistence-fact — a caller's permissions resolve through a join on every request; no facade makes the join visible. |
| `R-IDENTITY-016` | docs/spec/features/identity/access.feature::An unactivated account is refused at every gated page | — | e2e/auth_test.go::TestRequirePermissionMiddleware<br>e2e/rides_test.go::TestRideRoutesPermissionGate |  |
| `R-IDENTITY-017` | docs/spec/features/identity/access.feature::A code the standard user's role half carries is refused | — | e2e/auth_test.go::TestRequirePermissionMiddleware<br>e2e/rides_test.go::TestRideRoutesPermissionGate |  |
| `R-IDENTITY-018` | docs/spec/features/identity/access.feature::An unknown email is refused as an invalid credential | identity/model/user_test.go::TestPasswordHashRoundTrip | — |  |
| `R-IDENTITY-019` | docs/spec/features/identity/account.feature::A newly registered account arrives unactivated | — | e2e/users_test.go::TestRegisterUserHandlerSuccessState<br>e2e/users_test.go::TestRegisterUserHandler |  |
| `R-IDENTITY-020` | — | identity/repository/user_repo_test.go::TestInsert | e2e/users_test.go::TestRegisterUserHandler | persistence-fact — the store stamps the identity facts; nothing is refused. |
| `R-IDENTITY-021` | — | testdb/boundary_test.go::TestIdentityAppBoundary<br>testdb/boundary_test.go::TestIdentityAppInsertAllowed | — | boundary — grants and the composition root own which tables a role may reach. |
| `R-IDENTITY-022` | docs/spec/features/identity/account.feature::An activated account cannot collect another activation mail | identity/service/activation_test.go::TestActivate | — |  |

## kernel — 14 implemented rules, 1 with a scenario

That one scenario is a guest in the game tree: `R-KERNEL-003`'s observable face is the ride
command, so it is proved in `game/idempotency.feature` beside `R-GAME-018`.

| rule | scenario | unit | e2e | excluded (reason) |
| --- | --- | --- | --- | --- |
| `R-KERNEL-001` | — | kernel/idempotency/idempotency_test.go::TestValidateIdempotencyToken | e2e/rides_test.go::TestCreateRideHandler | interface — a usable idempotency key is request framing. |
| `R-KERNEL-002` | — | game/core/service/idempotency_test.go::TestService_RideCreateIdempotent | e2e/rides_test.go::TestCreateRideIdempotentHandler<br>e2e/rides_test.go::TestRideLockIdempotentHandler<br>e2e/rides_test.go::TestCreateRideHandlerBodyTooLarge | interface — the dedup digest of the request is protocol. |
| `R-KERNEL-003` | docs/spec/features/game/idempotency.feature::The same key on a different command is refused, never replayed | game/core/service/idempotency_test.go::TestService_RideLockIdempotency<br>game/core/service/idempotency_test.go::TestService_RideCreateIdempotent | e2e/rides_test.go::TestRideLockIdempotentHandler<br>e2e/rides_test.go::TestCreateRideIdempotentHandler |  |
| `R-KERNEL-004` | — | game/core/service/idempotency_test.go::TestService_RideLockIdempotency<br>game/core/service/idempotency_test.go::TestService_RideCreateIdempotent | e2e/rides_test.go::TestCreateRideIdempotentHandler<br>e2e/rides_test.go::TestRideLockIdempotentHandler | mechanical — the claim committing with its command; its observable half is R-GAME-018's refused-command scenario. |
| `R-KERNEL-005` | — | — | — | mechanical — the database-time budget is an internal cost policy. |
| `R-KERNEL-006` | — | — | e2e/teams_test.go::TestCreateTeamHandler<br>e2e/rides_test.go::TestCreateRideHandler | interface — collected validation is a shape of the answer. |
| `R-KERNEL-007` | — | — | e2e/teams_test.go::TestListTeamsHandler<br>e2e/rides_test.go::TestListRidesHandler | interface — bounding a list and its sort is pagination framing. |
| `R-KERNEL-008` | — | — | e2e/auth_test.go::TestAuthenticateMiddleware | interface — a malformed credential is refused before any handler. |
| `R-KERNEL-009` | — | kernel/httpx/ratelimiter_test.go::TestRateLimitMiddlewareDenial<br>kernel/httpx/ratelimiter_test.go::TestRateLimiterSustainedCapped<br>kernel/httpx/ratelimiter_test.go::TestRateLimiterIPsIsolated<br>kernel/httpx/ratelimiter_test.go::TestRateLimiterStaleBucketsEvicted | — | interface — the token bucket and its overdraw are middleware framing. |
| `R-KERNEL-010` | — | — | e2e/cors_test.go::TestEnableCORS | interface — the origin allowlist is middleware framing. |
| `R-KERNEL-011` | — | — | — | interface — a panic's answer is a shape of the answer, deliberately opaque. |
| `R-KERNEL-012` | — | — | e2e/rides_test.go::TestCreateRideHandlerBodyTooLarge | interface — the one-megabyte cap is body framing. |
| `R-KERNEL-013` | — | kernel/eventbus/eventbus_test.go::TestBus_SubscribeAndPublish<br>kernel/eventbus/eventbus_test.go::TestBus_PublishReturnsFirstHandlerError | e2e/matches_test.go::TestResolveMatchSettlesRowOnce | mechanical — the post-commit, same-process event is a mechanism. |
| `R-KERNEL-014` | — | game/core/service/service_test.go::TestService_OnMatchResultRecorded<br>game/core/service/service_test.go::TestService_OnRoundCompleted | e2e/matches_test.go::TestResolveMatchSettlesRowOnce | mechanical — claiming the event before acting is orchestration. |

## platform — 15 implemented rules, none with a scenario

| rule | scenario | unit | e2e | excluded (reason) |
| --- | --- | --- | --- | --- |
| `R-PLATFORM-001` | — | platform/config/config_test.go::TestParse | — | process-level — flag over environment over default is config resolution, not facade-provable. |
| `R-PLATFORM-002` | — | platform/config/config_test.go::TestParse<br>platform/config/config_test.go::TestValidate | — | process-level — startup refusal is a boot behaviour. |
| `R-PLATFORM-003` | — | platform/config/config_test.go::TestValidate<br>platform/config/config_test.go::TestParse | — | process-level — setting bounds are config validation. |
| `R-PLATFORM-004` | — | platform/config/config_test.go::TestParse | — | process-level — the log-level enumeration is config parsing. |
| `R-PLATFORM-005` | — | platform/config/config_test.go::TestParseCORSOrigins<br>platform/config/config_test.go::TestParse | — | process-level — the cross-origin allowlist's shape is config. |
| `R-PLATFORM-006` | — | — | — | process-level — the mailer's completeness check runs at boot. |
| `R-PLATFORM-007` | — | — | — | process-level — delivery retry is a background process. |
| `R-PLATFORM-008` | — | platform/logging/logging_test.go::TestNewJSONOutput<br>platform/logging/logging_test.go::TestNewLevelFiltering<br>platform/logging/logging_test.go::TestNewSourceShortFileInDevelopment<br>platform/logging/logging_test.go::TestPgxLevelMapping<br>platform/logging/logging_test.go::TestPgxDataBecomesSortedAttributes | — | process-level — one log stream per process is a runtime property. |
| `R-PLATFORM-009` | — | — | — | process-level — connection time bounds are a runtime property. |
| `R-PLATFORM-010` | — | platform/server/shutdown_test.go::TestDrainTimeoutForceCloses<br>platform/server/shutdown_test.go::TestSecondSignalForceClosesDuringDrain<br>platform/server/shutdown_test.go::TestBackgroundTaskBudgetBoundsWait | — | process-level — bounded shutdown is a process property. |
| `R-PLATFORM-011` | — | — | — | process-level — background-work failure isolation is a process property. |
| `R-PLATFORM-012` | — | testdb/boundary_test.go::TestGameAppBoundary<br>testdb/boundary_test.go::TestScheduleAppBoundary<br>testdb/boundary_test.go::TestIdentityAppBoundary | — | boundary — one schema per context and one role per pool is the boundary suite's fact. |
| `R-PLATFORM-013` | — | arch/arch_test.go::TestImportBoundaries<br>arch/arch_test.go::TestNonModulesNeverImportModules<br>arch/arch_test.go::TestModulesNeverImportPlatform<br>arch/arch_test.go::TestKernelImportsOnlyKernel | e2e/teams_test.go::TestDeleteTeamHandler | boundary — one-way dependencies and facade-only meetings are the arch tests' fact. |
| `R-PLATFORM-014` | — | — | e2e/cors_test.go::TestEnableCORS<br>e2e/auth_test.go::TestAuthenticateMiddleware | process-level — the request chain's order is composition, observed only in e2e. |
| `R-PLATFORM-015` | — | — | — | boundary — a pool speaking as its context's role is the boundary suite's fact. |

## ledger — specified-only

No ledger rule is implemented yet: all seven (R-LEDGER-001..007) are `specified-only`, carry
an `expert-ref` and no scenarios, and enter the executable specification in Phase F.

## Totals

**Rules with scenarios: 55 of the 106 implemented** — 12 pilot game rules plus 43 fan-out
rules (schedule 25, identity 11, game-remainder 6, kernel guest 1). 55 is inside the locked
decision-3 band of ~45–60. The 51 remaining implemented rules are excluded, each with a
reason in the tables above.

| module | implemented | with a scenario | scenarios in the tree (as written / godog pickles) |
| --- | ---: | ---: | ---: |
| schedule | 30 | 25 | 61 / 75 |
| game | 25 | 18 | 49 / 68 |
| identity | 22 | 11 | 22 / 25 |
| kernel | 14 | 1 | — (its guest scenario counts under game) |
| platform | 15 | 0 | 0 / 0 |
| ledger | 0 (7 specified-only) | 0 | 0 / 0 |
| **total** | **106** | **55** | **132 / 168** |

The tree counts are the ten feature files: 61 written schedule scenarios expand to 75 godog
scenarios, 22 identity to 25, 49 game to 68 — 132 as written, 168 as the harness runs them,
of which **167 run by default and the 168th is the known-defect finding** below. The pilot's
share of that is 51 of the game pickles (ride lifecycle 27, settlement 24).

**Policy 3 note.** Nine implemented rules still carry no unit/e2e pointer, so
`go run ./cmd/spec check` reports nine `3-implemented` errors: R-IDENTITY-007,
R-IDENTITY-012, R-KERNEL-005, R-KERNEL-011, R-PLATFORM-006, R-PLATFORM-007, R-PLATFORM-009,
R-PLATFORM-011, R-PLATFORM-015. Two of them (R-IDENTITY-007, R-IDENTITY-012) gained
**gherkin** pointers this phase but no unit/e2e one, and the checker's policy 3 counts
unit/e2e only. A one-line change in `cmd/spec` — counting `verification.gherkin` as a
verification pointer — would settle those two; Phase D/E owns `cmd/spec`, so this snapshot
reports the count rather than changing the policy.

## Known defects

One scenario is tagged `@known-defect`:

- `docs/spec/features/game/players.feature::A player still carrying rides is refused without a named reason`
  → misalignment-report §6 item **#2**. An unmapped SQLSTATE surfaces as an unnamed server
  error instead of a business refusal; this scenario is its player-DELETE surface
  (`rides_player_id_fkey` → 23503 with no `IsDeleteRestricted` branch). It sits under
  R-GAME-011's `Rule:` group as the delete command's surface, does not count towards
  R-GAME-011's coverage, is skipped loudly by default and runs as a finding under
  `SPEC_KNOWN_DEFECTS=1`.

Four catalogued §6 items remain deliberately scenario-less:

- **#3 (odds round-trip echo)** — the echo is a fact about the answer body, and the DSL has
  no body-fact step; the refusal half is R-SCHEDULE-009's and is covered.
- **#8 (login timing asymmetry)** — latency needs a clock no feature reads; the *refusal*
  equivalence is R-IDENTITY-018's and is covered.
- **#9 (activation endpoint is an account oracle)** — deliberate existence disclosure that
  needs a product decision before it can be asserted either way; R-IDENTITY-022 asserts only
  the account's own refusal.
- **#15 (a body-less command ignores its body in the digest)** — a wire-contract trap with no
  business-visible consequence today; it stays a catalogued note on R-KERNEL-002.

## Exclusions by category (footnote)

51 implemented rules carry no scenario: **interface 16**, **process-level 12**,
**mechanical 8**, **persistence-fact 7**, **boundary 5**, **refusal-mapping 1**,
**repository-shaped 1**, **concurrency 1**. (Counted from the excluded cells above; every
category name is the first word of the reason.)

<!-- GENERATED — do not edit. -->
# Business rules — `game`

> **GENERATED — do not edit.** This is the `game` module's slice of the readable
> catalog view. The canonical, hand-edited catalog is `docs/spec/rules.yaml`.
>
> Change the YAML, never this file, then regenerate from the dashboard:

```sh
go run ./cmd/spec gen --rules docs/spec/rules.yaml --out docs/spec/business-rules.md
```

The rules themselves live in `../features/game/`; the format authority is
`rules.schema.md` and the checker is `go run ./cmd/spec check`.

**33 rules** — 26 implemented, 7 specified-only.

## Module `game`

### Entity `idempotency_key`

#### R-GAME-018 — A ride command claims its key before it runs

`implemented` · `orchestration` · authority `service`

Every ride command first claims its idempotency key inside its own transaction, and only then reads, gates and writes; a command whose key is already completed returns the recorded answer without executing. The claim commits with the answer, so a failed command leaves nothing recorded and its retry runs for real.

**Authority** `service`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/game/internal/core/service/ride_commands.go::Service.RideCreate` | `service` | `authoritative` | — |
| `internal/game/internal/core/service/ride_commands.go::Service.RideLock` | `service` | `authoritative` | — |
| `internal/game/internal/core/service/ride_commands.go::Service.RideBurn` | `service` | `authoritative` | — |
| `internal/game/internal/core/service/ride_commands.go::Service.RideUnlock` | `service` | `authoritative` | — |

**Verification**

- unit: `internal/game/internal/core/service/idempotency_test.go::TestService_RideLockIdempotency`
- unit: `internal/game/internal/core/service/idempotency_test.go::TestService_RideCreateIdempotent`
- e2e: `internal/e2e/rides_test.go::TestCreateRideIdempotentHandler`
- e2e: `internal/e2e/rides_test.go::TestRideLockIdempotentHandler`

**Expert ref** `docs/league-tokens-learning-phases/phase-04-concurrency-and-idempotency.md`

**Notes**

> Four authoritative sites, one per command: the ordering (claim first, then execute, then store, then commit) is the same statement applied to four disjoint commands, so it is one rule with four sites, not four rules. No refusal: a claim that loses is not a refusal — it returns the recorded answer, which is a success. The recorded answer is the transport's presentation callback output (status + marshaled body), stored byte for byte so the replay is byte-identical, and create's Location header is re-derived from the stored body rather than persisted. The claim table and the single-flight mechanics are the kernel engine's; what this rule owns is that the *game* commands call it first and inside one transaction. The caller is part of the claim's identity since the ride routes became permission-gated: the handlers put the authenticated principal's id on the token (`port.Token.UserID`) and the kernel claims (key, user_id), so two callers that pick the same key hold two claims (R-KERNEL-003 owns the scope and the retention window). Related but distinct: R-KERNEL-004 owns the transactional coupling (the claim commits with the command's effect, or not at all); this rule owns the ordering inside the command, and the kernel claim rule (R-KERNEL-003) owns what the caller sees on a replay. stale-pointer: phase-04's §"Later, for HTTP commands, add an idempotency key table" is superseded sequencing, not a disagreement — migrations/000010 and this rule's sites are that table. Nothing else in the doc names a path that no longer exists.

### Entity `player`

#### R-GAME-011 — A registration is managed by its owner or an admin

`implemented` · `policy` · authority `service`

The player who registered a season entry may always change or withdraw it; any other principal may do so only with the administrative permission. A registration that belongs to nobody is admin-only by design, and a non-owner's attempt is refused before anything is written.

**Authority** `service`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/game/internal/core/service/player_commands.go::canManagePlayer` | `service` | `authoritative` | — |
| `internal/game/internal/core/service/player_commands.go::Service.PlayerUpdate` | `service` | `authoritative` | — |
| `internal/game/internal/core/service/player_commands.go::Service.PlayerDelete` | `service` | `authoritative` | — |
| `internal/game/internal/adapter/inbound/http/players.go::actorOf` | `handler` | `advisory-copy` | — |

**Refusal** `synthetic` → _(unnamed)_ → `ErrForbidden` → `403`

**Verification**

- e2e: `internal/e2e/players_test.go::TestUpdatePlayerOwnership`
- e2e: `internal/e2e/players_test.go::TestDeletePlayerHandler`

#### R-GAME-012 — A registration belongs to the caller and the season in the route

`implemented` · `policy` · authority `service`

A season entry is created only for the authenticated principal: the request can never name a different owner, and the season comes from the route, not the body. A create without a principal is refused, never stored ownerless.

**Authority** `service`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/game/internal/core/service/player_commands.go::Service.PlayerCreate` | `service` | `authoritative` | — |
| `internal/game/internal/adapter/inbound/http/players.go::createPlayer` | `handler` | `advisory-copy` | — |

**Refusal** `synthetic` → _(unnamed)_ → `ErrForbidden` → `401`

**Verification**

- e2e: `internal/e2e/players_test.go::TestCreatePlayerHandler`
- e2e: `internal/e2e/players_test.go::TestUpdatePlayerOwnership`

#### R-GAME-013 — Another context's records are checked when game writes a reference to them

`implemented` · `boundary` · authority `service`

A registration's season and favourite team must exist in the calendar, and a ride's team must exist, at the moment game writes them — the cross-context foreign keys no longer do it. A missing favourite team is refused on the field, a missing season or team elsewhere is not-found, and a registration also requires its user to exist in the identity context.

**Authority** `service` · scope `cross-table`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/game/internal/core/service/player_commands.go::Service.referenceTeam` | `service` | `authoritative` | — |
| `internal/game/internal/core/service/player_commands.go::Service.PlayerCreate` | `service` | `authoritative` | — |
| `internal/game/internal/core/service/ride_commands.go::Service.RideCreate` | `service` | `authoritative` | — |
| `internal/game/internal/adapter/inbound/http/players.go::createPlayer` | `handler` | `defense-in-depth` | — |

**Refusal** `synthetic` → _(unnamed)_ → `ValidationError` → `422`

**Verification**

- unit: `internal/game/internal/core/service/service_test.go::TestService_RideCreateUnknownParents`
- unit: `internal/game/internal/adapter/inbound/http/players_test.go::TestCreatePlayerNamesTheUserField`
- e2e: `internal/e2e/players_test.go::TestCreatePlayerHandler`
- e2e: `internal/e2e/players_test.go::TestUpdatePlayerHandler`

**Knowledge ref** `docs/league-tokens-learning-phases/phase-05-modular-monolith-boundaries.md`

**Notes**

> Category boundary (the cross-context FKs came out in 000016 so the composition moved into the use cases), but authority is the service, not the composition root, because each use case performs its own check before its write — the deviation from the boundary default is deliberate and this note is the reason. Disjoint cases of one statement, one refusal per case: an unknown favourite team is a field-level 422 on favorite_team_id (the player, not the team, is the resource); an unknown season in the route and an unknown ride team are 404; the ride's match reference is not checked here — it is the round gate's (R-GAME-008) and answers the round refusal. Tie: the handler site fails loudly — it asks identity for the principal's user row and its refusal keeps the field-error shape, but the user reference is checked *only* there and only when the identity directory was installed (`if h.users != nil`): with the FK gone, a module wired without identity silently plants an orphan user_id. Its 422 used to name favorite_team_id — a leftover of the FK-era shape that mislabelled the failing reference; it now names user_id, the principal's user account (misalignment-report §6 item 12's mislabel half, fixed; the optional-check half is still a defect, not a rule). stale-pointer: two repository comments still describe FK-era behaviour after 000016 — ride_repo.go::insertRide's FK branch says "the referenced player, team or match does not exist", though with the cross-context FKs gone the only surviving reference on that INSERT is rides_player_id_fkey (a missing team or match is the port's check), and player_repo.go::GetAll's "favorite_team_id is never 0 in the schema (FK)" names an FK that 000016 dropped (0 still works as the no-filter sentinel).

#### R-GAME-014 — One registration per principal per season

`implemented` · `invariant` · authority `database`

A person may be registered in a season only once. A second registration for the same principal in the same season is refused as a duplicate, so a player never holds two season entries and two token balances.

**Authority** `database` · mechanism `constraint` · scope `row-local`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `migrations/000004_add_permissions.up.sql` | `db-constraint` | `authoritative` | — |
| `internal/game/internal/adapter/outbound/postgres/player_repo.go::PlayerRepository.Insert` | `repository` | `advisory-copy` | — |

**Refusal** `23505` → `players_season_id_user_id_key` → `ErrDuplicateRecord` → `409`

**Verification**

- e2e: `internal/e2e/players_test.go::TestCreatePlayerDuplicateRegistration`

#### R-GAME-017 — A player write may claim the version it read

`implemented` · `policy` · authority `service`

A player patch may state the version it read; if the stored registration has moved on since, the write is refused as a conflict instead of silently overwriting the other change. Without a claim the write still re-checks the row's version at the statement, so a concurrent edit can never be lost.

**Authority** `service` · scope `row-local`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/game/internal/core/service/player_commands.go::Service.PlayerUpdate` | `service` | `authoritative` | — |
| `internal/game/internal/adapter/outbound/postgres/player_repo.go::PlayerRepository.Update` | `repository` | `defense-in-depth` | — |
| `internal/game/internal/adapter/inbound/http/players.go::updatePlayer` | `handler` | `advisory-copy` | — |

**Refusal** `synthetic` → _(unnamed)_ → `ErrEditConflict` → `409`

**Verification**

- e2e: `internal/e2e/players_test.go::TestUpdatePlayerHandler`

#### R-GAME-021 — Player requests are framed before the command runs

`implemented` · `interface` · authority `handler`

A registration carries a name of at most two hundred characters and a favourite team; a patch may name either field and the *merged* registration — not the patch — is what must still be valid. A request whose body is empty, malformed or names an unknown field is refused before any use case runs.

**Authority** `handler`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/game/internal/adapter/inbound/http/players.go::createPlayer` | `handler` | `authoritative` | — |
| `internal/game/internal/adapter/inbound/http/players.go::updatePlayer` | `handler` | `authoritative` | — |
| `internal/game/internal/core/domain/player.go::ValidatePlayer` | `validator` | `authoritative` | — |
| `internal/game/internal/core/service/player_commands.go::Service.PlayerUpdate` | `service` | `authoritative` | — |

**Refusal** `synthetic` → _(unnamed)_ → `ValidationError` → `422`

**Verification**

- e2e: `internal/e2e/players_test.go::TestCreatePlayerHandler`
- e2e: `internal/e2e/players_test.go::TestUpdatePlayerHandler`

#### R-GAME-034 — A registration is born with its starting currency

`specified-only` · `orchestration` · authority `service`

Registering a player creates the season registration and grants the starting currency in one transaction: a registration that exists without its starting currency is not a state the system can reach.

**Authority** `service`

**Expert ref** `docs/league-tokens-learning-phases/phase-03-transactions-and-atomic-operations.md`

#### R-GAME-035 — A registration the rides still name is not withdrawn

`implemented` · `invariant` · authority `database`

A withdrawal removes the season entry only while nothing else points at it: a registration that still carries rides cannot be deleted, because the rides' history would be left naming a player that is gone. The caller is told the registration is still referenced, never only that the server failed.

**Authority** `database` · mechanism `constraint` · scope `cross-table`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `migrations/000006_create_rides_table.up.sql` | `db-constraint` | `authoritative` | — |
| `internal/game/internal/adapter/outbound/postgres/player_repo.go::PlayerRepository.Delete` | `repository` | `advisory-copy` | — |

**Refusal** `23503` → `rides_player_id_fkey` → `ErrRecordInUse` → `409`

**Verification**

- unit: `internal/game/internal/adapter/outbound/postgres/player_repo_test.go::TestPlayerRepository_Delete`
- e2e: `internal/e2e/players_test.go::TestDeletePlayerHandler`

### Entity `ride`

#### R-GAME-001 — Rides are born locked

`implemented` · `invariant` · authority `database`

A ride may only come into existence in the locked state. Every other state is reachable only through a transition the state gate checks, so no writer — the application, a migration script, a test shortcut — can plant a ride directly in won_pending, burned, unlocked or lost.

**Authority** `database` · mechanism `trigger` · scope `row-local`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `migrations/000012_add_rides_insert_state_gate.up.sql` | `db-trigger` | `authoritative` | — |
| `internal/game/internal/core/domain/ride.go::Ride.Create` | `domain` | `defense-in-depth` | — |

**Refusal** `P0001` → `ride_insert_requires_locked_state` → `ErrInvalidTransition` → `409`

**Verification**

- unit: `internal/game/internal/core/domain/ride_test.go::TestRide_Create`
- unit: `internal/game/internal/adapter/outbound/postgres/ride_repo_test.go::TestRideRepository_InsertAndGet`
- e2e: `internal/e2e/rides_test.go::TestCreateRideHandler`

**Expert ref** `docs/league-tokens-learning-phases/phase-02-domain-model-and-state-machines.md`

#### R-GAME-002 — Ride state transitions follow the five-state graph

`implemented` · `invariant` · authority `database`

A ride moves only along the legal graph: locked → won_pending or lost; won_pending → locked (continuation), burned or unlocked. No other state change is possible for any writer.

**Authority** `database` · mechanism `trigger` · scope `row-local`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `migrations/000017_app_level_result_agreement.up.sql` | `db-trigger` | `authoritative` | — |
| `internal/game/internal/core/domain/ride.go::transitions` | `domain` | `defense-in-depth` | — |
| `internal/game/internal/core/domain/ride.go::canTransition` | `domain` | `defense-in-depth` | — |

**Refusal** `P0001` → `ride_state_transition_illegal` → `ErrInvalidTransition` → `409`

**Verification**

- unit: `internal/game/internal/core/domain/ride_test.go::TestRide_InvalidTransitions`
- unit: `internal/game/internal/adapter/outbound/postgres/ride_repo_test.go::TestRideRepository_DecisionGuardedTx`
- e2e: `internal/e2e/rides_test.go::TestRideCommandHandlers`

**Expert ref** `docs/league-tokens-learning-phases/phase-02-domain-model-and-state-machines.md`

#### R-GAME-003 — Terminal rides are frozen

`implemented` · `invariant` · authority `database`

A burned, lost or unlocked ride is final: its state never changes again and it is never re-pointed at another match. A state-preserving rewrite of the row is not a transition and is allowed.

**Authority** `database` · mechanism `trigger` · scope `row-local`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `migrations/000017_app_level_result_agreement.up.sql` | `db-trigger` | `authoritative` | — |
| `internal/game/internal/core/domain/ride.go::canTransition` | `domain` | `defense-in-depth` | — |

**Refusal** `P0001` → `ride_terminal_state_frozen` → `ErrInvalidTransition` → `409`

**Verification**

- unit: `internal/game/internal/core/domain/ride_test.go::TestRide_TerminalStates`
- unit: `internal/game/internal/adapter/outbound/postgres/ride_repo_test.go::TestRideRepository_TerminalRideGate`

**Expert ref** `docs/league-tokens-learning-phases/phase-02-domain-model-and-state-machines.md`

#### R-GAME-004 — Ride money columns stay in their domain

`implemented` · `invariant` · authority `database`

A ride carries a positive whole token stake, a positive base price captured at lock time, a non-negative accumulated bonus and a non-negative streak. There is no representable ride with zero or negative money.

**Authority** `database` · mechanism `constraint` · scope `row-local`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `migrations/000006_create_rides_table.up.sql` | `db-constraint` | `authoritative` | — |
| `internal/game/internal/core/domain/ride.go::ValidateRide` | `domain` | `advisory-copy` | — |
| `internal/game/internal/adapter/outbound/postgres/ride_repo.go::insertRide` | `repository` | `advisory-copy` | — |

**Refusal** `23514` → `rides_tokens_locked_check`, `rides_base_at_lock_check`, `rides_bonus_acc_check`, `rides_streak_check` → `ValidationError` → `422`

**Verification**

- unit: `internal/game/internal/core/domain/ride_validation_test.go::TestValidateRide`
- unit: `internal/game/internal/adapter/outbound/postgres/ride_repo_test.go::TestRideRepository_InsertCheckViolations`
- e2e: `internal/e2e/rides_test.go::TestCreateRideHandler`

#### R-GAME-005 — Unlocked and lost rides carry no accrued bonus

`implemented` · `invariant` · authority `database`

An unlocked or lost ride forfeits everything it accrued: its accumulated bonus is zero at the moment it reaches that state. A burned ride keeps the bonus it accrued, because the tokens and the bonus are fulfilled as TB.

**Authority** `database` · mechanism `constraint` · scope `row-local`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `migrations/000013_add_rides_money_state_coupling.up.sql` | `db-constraint` | `authoritative` | — |
| `internal/ledger/postgres.go::Repository.ReleaseBonus` | `repository` | `defense-in-depth` | — |
| `internal/game/internal/core/domain/ride.go::Ride.Unlock` | `domain` | `defense-in-depth` | — |
| `internal/game/internal/core/domain/ride.go::Ride.Lost` | `domain` | `defense-in-depth` | — |

**Refusal** `23514` → `rides_money_state_coupling` → `ErrInvalidTransition` → `409`

**Verification**

- unit: `internal/game/internal/core/service/constraints_test.go::TestRideMoneyStateCoupling`
- unit: `internal/game/internal/core/domain/ride_test.go::TestRide_Unlock`
- unit: `internal/game/internal/core/domain/ride_test.go::TestRide_Lost`
- unit: `internal/game/internal/adapter/outbound/postgres/ride_repo_test.go::TestRideRepository_DecisionGuardedTx`

**Notes**

> stale-pointer: the rule holds — the CHECK is enforced, mapped by constraint name to ErrInvalidTransition (409), and probed by TestRideMoneyStateCoupling — but migration 000013's change-detector prose no longer describes the code. It names the domain copies as "(internal/game/rides.go)", the persistence copy as "rideAutoUnlockSQL's bonus_acc = 0 (internal/game/rounds.go)" and the DB probe as "(internal/game/constraints_test.go)". None of those paths exist after the modular-monolith refactor: the domain operations are in internal/game/internal/core/domain/ride.go, the copy that actually reaches the row is internal/ledger/postgres.go::Repository.ReleaseBonus (the repository's UPDATE deliberately omits every money column), and the probe is internal/game/internal/core/service/constraints_test.go. The rule's content is unchanged, so the status stays `implemented` (gate A2 / D2: rot is not drift); the applied migration is immutable, so the fix is a comment at the current sites, not a migration rewrite, and this `stale-pointer` note surfaces in the Phase B misalignment report alongside every `drifted` rule. Second finding: the domain copies zero an in-memory Acc that the repository never persists, so they are documentation of intent — the load-bearing copies are the CHECK and the ledger's release-before-flip order. Tie: the ReleaseBonus and domain copies fail loudly — they never drift silently, because the CHECK refuses an unlocked/lost row carrying acc (23514 → 409) and TestRideMoneyStateCoupling pins the refusal name. A shared fixture (moneyStateRule: unlocked/lost/burned × acc 0/100, run through the ledger seam and direct SQL) would tie them without relying on the refusal.

#### R-GAME-006 — A win accrues tokens × (odds − 1) × (1 + 0.20 × streak)

`implemented` · `policy` · authority `domain`

Winning a ride accrues a bonus equal to the locked tokens, multiplied by the team's decimal odds minus one, multiplied by a streak multiplier of one plus twenty percent for each consecutive win already banked. A loss or an unlock forfeits the whole accrued amount; the formula changes with the product, so it lives in the domain and never in SQL.

**Authority** `domain`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/game/internal/core/domain/ride.go::BonusDelta` | `domain` | `authoritative` | — |
| `internal/game/internal/core/domain/ride.go::CalculateBonus` | `domain` | `authoritative` | — |

**Verification**

- unit: `internal/game/internal/core/domain/ride_bonus_test.go::TestCalculateBonus`
- unit: `internal/game/internal/core/service/service_test.go::TestService_OnMatchResultRecorded`

#### R-GAME-007 — Each ride command opens only in its phase window

`implemented` · `policy` · authority `domain`

Creating a ride is allowed only before the round's first match; recording a win or a loss only while the round's matches are running; continuing, burning or unlocking only after the last match of the round has ended. A command issued in the wrong window is refused and changes nothing.

**Authority** `domain`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/game/internal/core/domain/ride.go::Ride.Create` | `domain` | `authoritative` | — |
| `internal/game/internal/core/domain/ride.go::Ride.WonPending` | `domain` | `authoritative` | — |
| `internal/game/internal/core/domain/ride.go::Ride.Lost` | `domain` | `authoritative` | — |
| `internal/game/internal/core/domain/ride.go::Ride.Lock` | `domain` | `authoritative` | — |
| `internal/game/internal/core/domain/ride.go::Ride.Burn` | `domain` | `authoritative` | — |
| `internal/game/internal/core/domain/ride.go::Ride.Unlock` | `domain` | `authoritative` | — |

**Refusal** `synthetic` → _(unnamed)_ → `ErrInvalidRoundPhase` → `409`

**Verification**

- unit: `internal/game/internal/core/domain/ride_test.go::TestRide_Create`
- unit: `internal/game/internal/core/domain/ride_test.go::TestRide_WonPending`
- unit: `internal/game/internal/core/domain/ride_test.go::TestRide_Lost`
- unit: `internal/game/internal/core/domain/ride_test.go::TestRide_Lock`
- unit: `internal/game/internal/core/domain/ride_test.go::TestRide_Burn`
- unit: `internal/game/internal/core/domain/ride_test.go::TestRide_Unlock`
- unit: `internal/game/internal/core/service/service_test.go::TestService_RideCommandPhaseRejection`
- e2e: `internal/e2e/rides_test.go::TestRideCommandHandlers`

#### R-GAME-008 — A ride write requires an open round at the write instant

`implemented` · `orchestration` · authority `service`

A ride command may only write while the round it touches is open, and that fact is pinned at the moment of the write: the calendar's share-locked guard runs inside the command's transaction, so a concurrent round close either commits before the command reads (and the command is refused) or waits for it.

**Authority** `service`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/game/internal/core/service/ride_commands.go::Service.RideCreate` | `service` | `authoritative` | — |
| `internal/game/internal/core/service/ride_commands.go::Service.RideLock` | `service` | `authoritative` | — |
| `internal/game/internal/core/service/ride_commands.go::Service.RideBurn` | `service` | `authoritative` | — |
| `internal/game/internal/core/service/ride_commands.go::Service.RideUnlock` | `service` | `authoritative` | — |
| `internal/game/internal/adapter/outbound/schedule_acl.go::ScheduleACL.RoundOpenTx` | `repository` | `defense-in-depth` | — |
| `migrations/000018_context_roles.up.sql` | `repository` | `defense-in-depth` | — |

**Refusal** `synthetic` → _(unnamed)_ → `ErrRoundNotOpen` → `409`

**Verification**

- unit: `internal/game/internal/core/service/rides_concurrency_test.go::TestService_RideLockRacesRoundClose`
- unit: `internal/game/internal/core/service/rides_concurrency_test.go::TestService_RideCreateRacesRoundClose`
- unit: `internal/game/internal/adapter/outbound/postgres/ride_repo_test.go::TestRideRepository_DecisionGuardedTx`
- e2e: `internal/e2e/rides_test.go::TestCreateRideHandler`

#### R-GAME-009 — Round completion force-resolves undecided rides

`implemented` · `policy` · authority `service`

When a round completes, every ride still waiting on a decision is resolved by the system: on rounds one to thirty-seven it is unlocked and the accrued bonus is released; on the season's final round it is burned and the accrued bonus is kept, because a chase option no longer exists. Still-locked rides of the round's matches are settled first, and all of it happens in the one claimed settlement transaction.

**Authority** `service`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/game/internal/core/service/settlement.go::Service.OnRoundCompleted` | `service` | `authoritative` | — |
| `internal/game/internal/core/service/settlement.go::Service.runEvent` | `service` | `defense-in-depth` | — |
| `internal/schedule/internal/service/round_lifecycle.go::RoundService.Close` | `service` | `authoritative` | — |

**Refusal** `P0001` → _(unnamed)_ → `ErrInvalidTransition` → `409`

**Verification**

- unit: `internal/game/internal/core/service/service_test.go::TestService_OnRoundCompleted`
- unit: `internal/game/internal/core/service/rides_concurrency_test.go::TestSettlementHandlersSerializeOnTheRound`

**Expert ref** `docs/league-tokens-learning-phases/phase-08-transactional-outbox-and-events.md`

#### R-GAME-010 — A settled ride agrees with the committed match result

`implemented` · `invariant` · authority `service`

A locked ride may only arrive at won_pending when the committed result of its match crowns the ride's team, and at lost when it does not; a draw crowns nobody. A settlement payload that claims the wrong destination is refused and the whole settlement transaction rolls back.

**Authority** `service` · scope `cross-table`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/game/internal/core/service/settlement.go::resultAgreement` | `service` | `authoritative` | — |
| `internal/game/internal/core/service/settlement.go::Service.settleRideFromResult` | `service` | `defense-in-depth` | `scoreStateRule` |
| `internal/schedule/internal/model/match.go::Match.Winner` | `domain` | `defense-in-depth` | `scoreStateRule` |

**Refusal** `synthetic` → _(unnamed)_ → `ErrInvalidTransition` → `409`

**Verification**

- unit: `internal/game/internal/core/service/score_state_rule_test.go::TestWinnerScoreStateRule`
- unit: `internal/game/internal/core/service/score_state_rule_test.go::TestScoreStateRuleDB`
- unit: `internal/schedule/internal/model/matches_winner_test.go::TestWinner`
- unit: `internal/game/internal/core/service/service_test.go::TestService_OnMatchResultRecorded`
- unit: `internal/game/internal/adapter/outbound/postgres/ride_repo_test.go::TestRideRepository_ArriveTx`

**Expert ref** `docs/league-tokens-learning-phases/phase-05-modular-monolith-boundaries.md`

**Notes**

> stale-pointer: the rule holds and is fixture-pinned (scoreStateRule: three score pairings × both sides, asserted against all three encodings), but the three change-detector comments that lead a reader between them are stale. 000011 and 000013 name "Match.Winner (internal/game/matches.go)" and "the CASE in RideStore.UpdateAllForMatchTx (internal/game/rides.go)", and 000017 says the check verifies "through the SchedulePort (Store.MatchResultTx)"; after the modular-monolith refactor the site is internal/schedule/internal/model/match.go::Match.Winner, the payload derivation is settlement.go::Service.settleRideFromResult (there is no CASE in SQL any more), the enforcement is settlement.go::resultAgreement, and the port method is Schedule.Match. internal/schedule/internal/model/match.go's own comment still points at "internal/game/score_state_rule_test.go", which no longer exists either. The rule's content is unchanged, so the status stays `implemented` (gate A2 / D2: a rotted pointer is not drift); fix the comments at the current sites (do not rewrite applied migrations), and this note surfaces in the Phase B misalignment report. Authority is the service, deviating from the invariant default on purpose: 000017 moved the check out of rides_state_gate because the trigger read schedule's matches table — the cross-context read the boundary rule removes — so the same refusal now happens in Go inside the same transaction, before the arrival write. The two historical refusal names survive as text in the Go errors (ride_result_scores_missing / ride_result_disagreement) with no SQLSTATE. Since Phase G (§6 item #11) the committed snapshot the agreement re-reads also carries the odds: the payload's rate claim is verified against it (refusal text ride_result_odds_disagreement, the same ErrInvalidTransition + rollback as the other two refusals), and the payout rate is read from the committed row with the side picked by the committed team assignment (a side-swapping payload that passed a bare pair-compare could not misprice a won ride). The loud refusal is this rule's claim-refusal pattern applied to the rate, and R-GAME-026's "unverified"/"loud error" language is the same choice.

#### R-GAME-015 — A forfeit is posted before the ride's state moves

`implemented` · `orchestration` · authority `service`

Whenever a ride forfeits its accrued bonus — a player unlock, a lost result, the season-close force-unlock — the money is released first and the state changes after, both inside one transaction. The order is what makes the arrival legal: a ride cannot reach unlocked or lost still carrying a bonus.

**Authority** `service`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/game/internal/core/service/ride_commands.go::Service.RideUnlock` | `service` | `authoritative` | — |
| `internal/game/internal/core/service/settlement.go::Service.settleRideFromResult` | `service` | `authoritative` | — |
| `internal/game/internal/core/service/settlement.go::Service.OnRoundCompleted` | `service` | `authoritative` | — |
| `migrations/000013_add_rides_money_state_coupling.up.sql` | `db-constraint` | `defense-in-depth` | — |

**Refusal** `23514` → `rides_money_state_coupling` → `ErrInvalidTransition` → `409`

**Verification**

- unit: `internal/game/internal/core/service/service_test.go::TestService_RideUnlock`
- unit: `internal/game/internal/core/service/service_test.go::TestService_OnMatchResultRecorded`
- unit: `internal/game/internal/core/service/service_test.go::TestService_OnRoundCompleted`
- unit: `internal/game/internal/core/service/constraints_test.go::TestRideMoneyStateCoupling`

**Knowledge ref** `docs/league-tokens-learning-phases/phase-06-ledger-and-accounting.md`

#### R-GAME-016 — A settlement reaches each ride once

`implemented` · `orchestration` · authority `repository`

A settlement arrival only lands on a ride that is still in the state the handler read and at the version it read; a ride another writer already moved is left alone and the arrival reports the invalid-transition conflict. A settlement therefore never overwrites a decision, a replay or a sibling settlement.

**Authority** `repository` · scope `row-local`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/game/internal/adapter/outbound/postgres/ride_repo.go::RideRepository.ArriveTx` | `repository` | `authoritative` | — |
| `internal/game/internal/adapter/outbound/postgres/ride_repo.go::RideRepository.classifyArrivalRefusal` | `repository` | `authoritative` | — |
| `internal/game/internal/adapter/outbound/postgres/ride_repo.go::RideRepository.LockedForMatchTx` | `repository` | `authoritative` | — |

**Refusal** `synthetic` → _(unnamed)_ → `ErrInvalidTransition` → `409`

**Verification**

- unit: `internal/game/internal/core/service/rides_concurrency_test.go::TestSettlementHandlersSerializeOnTheRound`
- unit: `internal/game/internal/core/service/service_test.go::TestService_OnMatchResultRecorded`
- unit: `internal/game/internal/core/service/service_test.go::TestService_OnRoundCompleted`
- e2e: `internal/e2e/matches_test.go::TestResolveMatchSettlesRowOnce`

**Knowledge ref** `docs/league-tokens-learning-phases/phase-04-concurrency-and-idempotency.md`

#### R-GAME-020 — Ride requests are framed before the command runs

`implemented` · `interface` · authority `handler`

A ride command carries an idempotency key and, for creation, a well-formed body whose player, team and match are named and whose stake is a whole positive number of tokens. A request that fails framing changes nothing: no key is claimed, no ride is written, and the caller is told which field was wrong.

**Authority** `handler`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/game/internal/adapter/inbound/http/rides.go::Handler.createRide` | `handler` | `authoritative` | — |
| `internal/game/internal/adapter/inbound/http/rides.go::Handler.createRideToken` | `handler` | `authoritative` | — |
| `internal/game/internal/adapter/inbound/http/rides.go::Handler.rideCommandToken` | `handler` | `authoritative` | — |
| `internal/game/internal/adapter/inbound/http/rides.go::Handler.listRides` | `handler` | `authoritative` | — |
| `internal/kernel/idempotency/idempotency.go::ValidateIdempotencyToken` | `validator` | `authoritative` | — |
| `internal/game/internal/core/domain/ride.go::ValidateRide` | `validator` | `authoritative` | — |

**Refusal** `synthetic` → _(unnamed)_ → `ValidationError` → `422`

**Verification**

- unit: `internal/game/internal/core/domain/ride_validation_test.go::TestValidateRide`
- unit: `internal/game/internal/core/domain/ride_validation_test.go::TestValidateRideState`
- unit: `internal/kernel/idempotency/idempotency_test.go::TestValidateIdempotencyToken`
- e2e: `internal/e2e/rides_test.go::TestCreateRideHandler`
- e2e: `internal/e2e/rides_test.go::TestRideCommandHandlers`
- e2e: `internal/e2e/rides_test.go::TestCreateRideHandlerBodyTooLarge`
- e2e: `internal/e2e/rides_test.go::TestListRidesHandler`

#### R-GAME-023 — The two settlements of one round run back to back

`implemented` · `orchestration` · authority `repository`

A match settlement and a round settlement of the same round never run beside each other: each takes the round's settlement lock before reading any ride, so the second one sees the first one's committed transitions instead of racing them. Without that order the later settlement would lose its write, roll back — its event claim with it — and the event would be dropped.

**Authority** `repository`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/game/internal/adapter/outbound/postgres/transactor.go::Transactor.LockSettlementRound` | `repository` | `authoritative` | — |
| `internal/game/internal/core/service/settlement.go::Service.runEvent` | `service` | `defense-in-depth` | — |

**Verification**

- unit: `internal/game/internal/core/service/rides_concurrency_test.go::TestSettlementHandlersSerializeOnTheRound`

**Knowledge ref** `docs/league-tokens-learning-phases/phase-05-modular-monolith-boundaries.md`

#### R-GAME-024 — A refused ride write names its cause in a fixed order

`implemented` · `refusal-mapping` · authority `repository`

When a ride write is refused, the reason is classified in a fixed order — the row is gone, the state or version the command claimed no longer holds, then the database's own refusal — so the client always gets the refusal that actually happened. A write that matched nothing while every predicate still holds is reported as a defect, never dressed up as a business refusal.

**Authority** `repository` · scope `row-local`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/game/internal/adapter/outbound/postgres/ride_repo.go::RideRepository.classifyDecisionRefusal` | `repository` | `authoritative` | — |
| `internal/game/internal/adapter/outbound/postgres/ride_repo.go::RideRepository.classifyArrivalRefusal` | `repository` | `authoritative` | — |
| `internal/game/internal/adapter/outbound/postgres/ride_repo.go::RideRepository.DecisionGuardedTx` | `repository` | `authoritative` | — |

**Refusal** `synthetic` → _(unnamed)_ → `ErrInvalidTransition` → `409`

**Verification**

- unit: `internal/game/internal/core/service/rides_concurrency_test.go::TestService_RideCommandsRaceOnOneRide`
- unit: `internal/game/internal/core/service/rides_concurrency_test.go::TestService_RideBurnRacesExactlyOnce`
- unit: `internal/game/internal/adapter/outbound/postgres/ride_repo_test.go::TestRideRepository_DecisionGuardedTx`

**Notes**

> This is the catalog's first refusal-mapping rule (the category the schema reserves for rules *about* the mapping). The order is: the statement's own SQLSTATEs first (a trigger refusal and the money-state check both mean the state machine refused → 409; a foreign-key refusal is the parent gone → 404), then, for a zero-row write, a follow-up read on the pool: no row → not found, row moved (version, or state no longer the claimed pre-state) → invalid transition, otherwise a wrapped error with no sentinel → 500. Two authoritative classifier sites because the two guarded writes have different pre-states (a decision claims won_pending; an arrival claims its own from-state, which may be locked *or* won_pending) — disjoint cases of one statement. The design point worth preserving: the follow-up read may legally see a snapshot from before the concurrent commit, so the "everything still holds" branch must not be folded into a named refusal — that is the loud error. The round's facts are deliberately absent from this classification: a close that slipped in between the gate and the write is the round refusal, decided before the write (R-GAME-008). Renumbered at merge (was provisional R-GAME-025): the request-identity fragment went into R-KERNEL-002 and R-GAME-027 merged into R-SCHEDULE-026, so the game block was renumbered densely. stale-pointer: two comments in the ride adapter still name the old shape — DecisionGuardedTx's doc comment says a destination deleted between the port gate and the write "surfaces as the FK 23503 -> ErrNotFound", while the inline comment two screens down says (correctly) that since 000016 no FK is left on rides and the branch is only the generic race-mapping shape; ride_repo_test.go's comments still cite classifyUpdateRefusal, a symbol that no longer exists (the classifiers are classifyDecisionRefusal, classifyArrivalRefusal and classifyResolveRefusal).

#### R-GAME-025 — A ride is re-pointed at another match only by continuing it

`implemented` · `invariant` · authority `database`

A ride may change the match it is on only when a won ride continues onto a later match; no other write may move it, so a ride can never be silently transplanted onto somebody else's fixture. A state-preserving rewrite keeps the same match and is allowed.

**Authority** `database` · mechanism `trigger` · scope `row-local`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `migrations/000017_app_level_result_agreement.up.sql` | `db-trigger` | `authoritative` | — |
| `internal/game/internal/core/domain/ride.go::Ride.Lock` | `domain` | `defense-in-depth` | — |

**Refusal** `P0001` → `ride_match_id_immutable_outside_continuation` → `ErrInvalidTransition` → `409`

**Verification**

- unit: `internal/game/internal/adapter/outbound/postgres/ride_repo_test.go::TestRideRepository_DecisionGuardedTx`
- unit: `internal/game/internal/core/domain/ride_test.go::TestRide_Lock`

**Knowledge ref** `docs/league-tokens-learning-phases/phase-02-domain-model-and-state-machines.md`

#### R-GAME-026 — The calendar is reached through game's own vocabulary, never its tables

`implemented` · `boundary` · authority `composition-root`

game learns calendar facts only through the calendar's game-facing operations and its events, and every refusal arrives as one of game's own errors; a phase or a value game does not recognize is a loud error, not a guess. The one thing game may not do — read or lock the calendar's rows itself — is enforced by the database, with the two write-instant round guards as the only exceptions and the calendar's own functions as their implementation.

**Authority** `composition-root` · scope `cross-table`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/game/internal/adapter/outbound/schedule_acl.go::ScheduleACL` | `repository` | `authoritative` | — |
| `internal/game/internal/adapter/outbound/schedule_events.go::Subscribe` | `repository` | `authoritative` | — |
| `migrations/000018_context_roles.up.sql` | `db-constraint` | `authoritative` | — |

**Refusal** `synthetic` → _(unnamed)_ → `ErrNotFound` → `404`

**Verification**

- unit: `internal/game/internal/core/service/service_test.go::TestService_RideCreateUnknownParents`
- unit: `internal/game/internal/core/service/service_test.go::TestService_RideLock`
- unit: `internal/game/internal/core/service/service_test.go::TestService_RideCreate`

**Expert ref** `docs/league-tokens-learning-phases/phase-05-modular-monolith-boundaries.md`

#### R-GAME-027 — The round-close sweep is served by the undecided-rides partial index

`implemented` · `persistence-fact` · authority `repository`

The query that collects a closing round's undecided rides is the one shape the database is tuned for: it filters rides by match and by the single undecided state, and a partial index covers exactly that subset. The index stays small because rides leave that state at their round's close, so the round-close cost does not grow with the lifetime ride history.

**Authority** `repository` · scope `row-local`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `migrations/000009_add_rides_won_pending_partial_index.up.sql` | `repository` | `authoritative` | — |
| `internal/game/internal/adapter/outbound/postgres/ride_repo.go::RideRepository.UndecidedForMatchesTx` | `repository` | `authoritative` | — |

**Verification**

- unit: `internal/game/internal/core/service/service_test.go::TestService_OnRoundCompleted`

**Knowledge ref** `docs/league-tokens-learning-phases/phase-01-postgresql-persistence.md`

**Notes**

> A persistence fact, not a business rule: nothing is refused if the index disappears — the sweep just gets slower — which is why the refusal is null and the verification is a functional pointer at the sweep rather than a performance assertion. The stated guarantee is a matching obligation in one direction: the index (`match_id`, where the state is the undecided one) serves the sweep only while the sweep keeps that predicate — a query that started filtering, say, on a joined round id would silently lose the index. The other consumer the index was measured for is gone. stale-pointer: migration 000009's header says the predicate serves "the rounds_close_gate EXISTS backstop and the auto-unlock/auto-burn UPDATE ... FROM". Both moved: 000014 dropped rounds_close_gate's unresolved-rides check (undecided rides in a closing round are now the designed intermediate state) and the auto-unlock statement left the close transaction for game's settlement handler in internal/game (000014's header says so explicitly). The index still holds and still serves the query it serves; only the prose naming its consumers is stale. The applied migration is immutable, so the fix is this note plus a comment at the current query site, not a migration rewrite. Renumbered at merge (was provisional R-GAME-029): the request-identity fragment went into R-KERNEL-002 and R-GAME-027 merged into R-SCHEDULE-026, so the game block was renumbered densely.

#### R-GAME-028 — Taking the tokens and creating the ride are one operation

`specified-only` · `orchestration` · authority `service`

Locking a ride and taking the player's tokens are one operation: the token balance drops by the locked amount in the same transaction that creates the locked ride, so a ride can never exist without its tokens having been taken.

**Authority** `service`

**Expert ref** `docs/league-tokens-learning-phases/phase-03-transactions-and-atomic-operations.md`

#### R-GAME-029 — Burning a ride settles its money in the same transaction

`specified-only` · `orchestration` · authority `service`

Burning a ride settles the money in one step: the ride reaches the burned state, its locked tokens are destroyed and their value is credited as TB, and any currency owed back is returned — all in the transaction that flips the state.

**Authority** `service`

**Expert ref** `docs/league-tokens-learning-phases/phase-03-transactions-and-atomic-operations.md`

#### R-GAME-031 — A ride past its deadline is burned by the system

`specified-only` · `policy` · authority `service`

A ride whose decision window has expired is burned by the system itself, with no player command: a background worker picks up the due rides on its own schedule and applies the auto-burn.

**Authority** `service`

**Expert ref** `docs/league-tokens-learning-phases/phase-07-background-workers.md`

#### R-GAME-032 — The auto-burn is safe to run twice

`specified-only` · `orchestration` · authority `service`

Running the auto-burn more than once — two ticks, a restart, two processes — never burns or rewards twice: the operation recognises state it has already processed and does nothing.

**Authority** `service`

**Expert ref** `docs/league-tokens-learning-phases/phase-07-background-workers.md`

#### R-GAME-033 — A burn is announced as a ride.burned event

`specified-only` · `orchestration` · authority `service`

A burned ride is announced as a ride.burned event carrying the ride's identity, so downstream consumers react to the committed burn instead of being called from inside the burn.

**Authority** `service`

**Expert ref** `docs/league-tokens-learning-phases/phase-08-transactional-outbox-and-events.md`

### Entity `team`

#### R-GAME-030 — Resolving a match updates the team's base price

`specified-only` · `orchestration` · authority `service`

Resolving a match updates the affected team's base price in the same transaction that records the result and settles the rides, so a committed result can never sit next to a stale base price.

**Authority** `service`

**Expert ref** `docs/league-tokens-learning-phases/phase-03-transactions-and-atomic-operations.md`

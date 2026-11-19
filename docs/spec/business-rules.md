<!-- GENERATED — do not edit. -->
# Business rules

> **GENERATED — do not edit.** This file is a readable view of the canonical,
> hand-edited catalog `docs/spec/rules.yaml`. Change the YAML, never this file, then
> regenerate:

```sh
go run ./cmd/spec gen --rules docs/spec/rules.yaml --out docs/spec/business-rules.md
```

The catalog's schema, enums and conventions live in `rules.schema.md`;
the catalog's own invariants are checked by `go run ./cmd/spec check`.

## Dashboard

**126 rules** across 6 modules.

### By module

| Module | Rules |
| --- | ---: |
| `game` | 33 |
| `identity` | 22 |
| `kernel` | 17 |
| `ledger` | 7 |
| `platform` | 17 |
| `schedule` | 30 |
| **total** | **126** |

### By status

| Status | Rules |
| --- | ---: |
| `implemented` | 107 |
| `specified-only` | 19 |
| **total** | **126** |

### By category

| Category | Rules |
| --- | ---: |
| `boundary` | 8 |
| `interface` | 12 |
| `invariant` | 37 |
| `orchestration` | 41 |
| `persistence-fact` | 2 |
| `policy` | 25 |
| `refusal-mapping` | 1 |
| **total** | **126** |

### By authority

| Authority | Rules |
| --- | ---: |
| `composition-root` | 8 |
| `database` | 34 |
| `domain` | 7 |
| `handler` | 25 |
| `repository` | 14 |
| `service` | 38 |
| **total** | **126** |

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

## Module `identity`

### Entity `module`

#### R-IDENTITY-021 — Identity's tables are reachable only through identity's own database role

`implemented` · `boundary` · authority `composition-root`

Who logs in and what they may do lives in its own schema, writable and readable only by the role that speaks for that context. Every other context's role is denied on those tables, and identity's role is denied on theirs, so a cross-context read cannot sneak in through SQL even if someone forgets the import rule.

**Authority** `composition-root`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `migrations/000018_context_roles.up.sql` | `repository` | `authoritative` | — |
| `migrations/000015_split_context_schemas.up.sql` | `repository` | `defense-in-depth` | — |

**Refusal** `42501` → _(unnamed)_ → _(unmapped)_ → _(falls through to 500)_

**Verification**

- unit: `internal/testdb/boundary_test.go::TestIdentityAppBoundary`
- unit: `internal/testdb/boundary_test.go::TestIdentityAppInsertAllowed`

**Expert ref** `docs/league-tokens-learning-phases/phase-05-modular-monolith-boundaries.md`

### Entity `permission`

#### R-IDENTITY-013 — Every permission code is unique and every grant is one role-permission pair

`implemented` · `invariant` · authority `database`

A permission is identified by its code, and a code exists once: there is one row per thing a caller may do. A grant says "this role carries this permission", at most once per pair, and both sides of the pair must exist. So "does this role carry this code" has exactly one answer.

**Authority** `database` · mechanism `constraint` · scope `row-local`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `migrations/000004_add_permissions.up.sql` | `db-constraint` | `authoritative` | — |

**Refusal** `23505` → `permissions_code_key`, `roles_permissions_pkey` → _(unmapped)_ → _(falls through to 500)_

**Verification**

- e2e: `internal/e2e/auth_test.go::TestRequirePermissionMiddleware`

#### R-IDENTITY-014 — Two roles and eleven permission codes are seeded reference data

`implemented` · `policy` · authority `database`

The vocabulary of what a caller may do is fixed: eleven codes covering reading and writing the calendar, the players and the teams, plus the right to manage other people's players. Two roles exist — administrator, which holds all eleven, and the standard user, which holds the read codes plus the right to manage its own player. Nothing in the running service adds, removes or re-grants a permission.

**Authority** `database` · mechanism `constraint` · scope `row-local`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `migrations/000004_add_permissions.up.sql` | `db-constraint` | `authoritative` | — |

**Refusal** `23505` → `permissions_code_key`, `roles_name_key` → _(unmapped)_ → _(falls through to 500)_

**Verification**

- e2e: `internal/e2e/auth_test.go::TestRequirePermissionMiddleware`
- e2e: `internal/e2e/rides_test.go::TestRideRoutesPermissionGate`

#### R-IDENTITY-015 — A caller's permissions are its role's, resolved on every request

`implemented` · `policy` · authority `repository`

Permissions hang off roles, never off accounts: there is no per-account grant and no cached set, so two accounts holding the same role are indistinguishable to the gate and a grant change takes effect on the next request. An account whose role carries nothing resolves to an empty set rather than to an error.

**Authority** `repository`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/identity/internal/repository/permission_repo.go::PermissionRepository.GetAllForRole` | `repository` | `authoritative` | — |

**Verification**

- e2e: `internal/e2e/auth_test.go::TestRequirePermissionMiddleware`

#### R-IDENTITY-017 — A gated route requires the exact permission code it names

`implemented` · `policy` · authority `handler`

A gated route is served only to a principal whose role carries the one permission code the route names; anything else is refused before the handler runs. The resolved set travels with the request so a handler can ask about further codes without a second lookup, and the codes are matched exactly, one at a time.

**Authority** `handler`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/kernel/httpx/middleware.go::Middleware.RequirePermission` | `handler` | `authoritative` | — |
| `internal/kernel/httpx/context.go::Permissions.Include` | `handler` | `authoritative` | — |
| `internal/kernel/httpx/errors.go::MissingPermission` | `handler` | `authoritative` | — |
| `internal/game/internal/adapter/inbound/http/routes.go::Handler.RegisterRoutes` | `handler` | `defense-in-depth` | — |

**Refusal** `synthetic` → _(unnamed)_ → _(unmapped)_ → `403`

**Verification**

- e2e: `internal/e2e/auth_test.go::TestRequirePermissionMiddleware`
- e2e: `internal/e2e/rides_test.go::TestRideRoutesPermissionGate`

### Entity `user`

#### R-IDENTITY-001 — Identity requests are framed before anything else happens

`implemented` · `interface` · authority `handler`

A registration must carry a non-empty name of at most five hundred bytes and an email that is non-empty and syntactically well formed. Every request that carries an email — registration, activation-token request and login alike — is held to the same email shape. A request that fails is refused with the field errors and creates or changes nothing.

**Authority** `handler`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/identity/internal/model/user.go::ValidateRegistration` | `validator` | `authoritative` | — |
| `internal/identity/internal/model/user.go::ValidateEmail` | `validator` | `authoritative` | — |

**Refusal** `synthetic` → _(unnamed)_ → _(unmapped)_ → `422`

**Verification**

- unit: `internal/identity/internal/model/user_test.go::TestValidateRegistration`
- e2e: `internal/e2e/users_test.go::TestRegisterUserHandler`

#### R-IDENTITY-002 — A password is eight to seventy-two bytes, checked before it is hashed

`implemented` · `interface` · authority `handler`

A password presented at registration or login must be at least eight bytes and at most seventy-two bytes long. The ceiling is not cosmetic: the password hashing step refuses anything longer than seventy-two bytes outright, while the comparison step ignores everything past the seventy-second — so an over-long password is either a server error or silently interchangeable with its own prefix. Both bounds are checked before any hashing runs, which turns the over-long case into a field refusal instead.

**Authority** `handler`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/identity/internal/model/user.go::ValidatePasswordPlaintext` | `validator` | `authoritative` | — |

**Refusal** `synthetic` → _(unnamed)_ → _(unmapped)_ → `422`

**Verification**

- unit: `internal/identity/internal/model/user_test.go::TestValidateRegistration`
- e2e: `internal/e2e/users_test.go::TestRegisterUserHandler`

#### R-IDENTITY-003 — A password is stored only as its hash, and never leaves the process

`implemented` · `invariant` · authority `domain`

The password a user types is never stored and never written down anywhere: the account row holds a deliberately slow, salted hash (bcrypt, work factor twelve) and nothing else. The cleartext exists only in memory while the request that carried it is in flight.

**Authority** `domain`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/identity/internal/model/user.go::password.Set` | `domain` | `authoritative` | — |
| `internal/identity/internal/repository/user_repo.go::UserRepository.Insert` | `repository` | `defense-in-depth` | — |
| `internal/identity/internal/repository/user_repo.go::UserRepository.update` | `repository` | `defense-in-depth` | — |
| `migrations/000002_create_users_table.up.sql` | `db-constraint` | `defense-in-depth` | — |

**Refusal** `synthetic` → _(unnamed)_ → _(unmapped)_ → `500`

**Verification**

- unit: `internal/identity/internal/model/user_test.go::TestPasswordHashRoundTrip`
- unit: `internal/identity/internal/repository/user_repo_test.go::TestInsert`
- e2e: `internal/e2e/users_test.go::TestRegisterUserHandlerSuccessState`

#### R-IDENTITY-004 — An account response never carries the hash or the internal version

`implemented` · `boundary` · authority `handler`

What the service says about an account is its id, when it was created, its name, its email, whether it is activated and which role it holds. The stored hash and the internal version counter are never part of a response.

**Authority** `handler`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/identity/internal/handler/dto.go::newUserResponse` | `handler` | `authoritative` | — |

**Verification**

- e2e: `internal/e2e/users_test.go::TestRegisterUserHandlerSuccessState`
- e2e: `internal/e2e/users_test.go::TestRegisterUserHandler`

#### R-IDENTITY-010 — A user row is only rewritten by a writer holding the version it read

`implemented` · `invariant` · authority `repository`

Every write to an account carries the version the writer read, the store bumps it, and a write whose claim no longer matches the stored row is refused instead of overwriting. Two writers that read the same version cannot both win, so no update is silently lost.

**Authority** `repository`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/identity/internal/repository/user_repo.go::UserRepository.update` | `repository` | `authoritative` | — |
| `internal/identity/internal/service/activation.go::Service.Activate` | `service` | `defense-in-depth` | — |

**Refusal** `synthetic` → _(unnamed)_ → `ErrEditConflict` → `409`

**Verification**

- unit: `internal/identity/internal/repository/user_repo_test.go::TestUpdateTx`
- unit: `internal/identity/internal/service/activation_test.go::TestActivate`

#### R-IDENTITY-011 — An email address identifies exactly one account, whatever its case

`implemented` · `invariant` · authority `database`

Two accounts can never share an email address, and letter case does not distinguish them: registering ALICE@EXAMPLE.COM after alice@example.com is the same identity being asked for twice. The store refuses the second registration and the service answers it as a field error on the email, so the client sees one clear refusal rather than a server error. Logins and lookups compare the same way, case-insensitively.

**Authority** `database` · mechanism `constraint` · scope `row-local`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `migrations/000002_create_users_table.up.sql` | `db-constraint` | `authoritative` | — |
| `internal/identity/internal/repository/user_repo.go::UserRepository.Insert` | `repository` | `defense-in-depth` | — |
| `internal/identity/internal/repository/user_repo.go::UserRepository.update` | `repository` | `defense-in-depth` | — |
| `internal/identity/internal/repository/user_repo.go::UserRepository.GetByEmail` | `repository` | `defense-in-depth` | — |

**Refusal** `23505` → `users_email_key` → `ErrDuplicateEmail` → `422`

**Verification**

- unit: `internal/identity/internal/repository/user_repo_test.go::TestInsert`
- unit: `internal/identity/internal/repository/user_repo_test.go::TestUpdateTx`
- unit: `internal/identity/internal/repository/user_repo_test.go::TestGetByEmail`
- e2e: `internal/e2e/users_test.go::TestRegisterUserHandlerDuplicateIsolatedFromValidation`

#### R-IDENTITY-012 — Registration assigns the standard user role, and a client cannot choose one

`implemented` · `policy` · authority `repository`

An account always holds exactly one role, and an account created through registration always holds the standard user role. The client never supplies, hints at or upgrades the role — administrator rights are given, never self-granted — and a body that tries to carry one is refused before it reaches the store.

**Authority** `repository`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/identity/internal/repository/user_repo.go::UserRepository.Insert` | `repository` | `authoritative` | — |
| `internal/identity/internal/handler/dto.go::registerUserInput` | `handler` | `defense-in-depth` | — |
| `migrations/000004_add_permissions.up.sql` | `db-constraint` | `defense-in-depth` | — |

**Refusal** `synthetic` → _(unnamed)_ → _(unmapped)_ → `400`

#### R-IDENTITY-016 — An unactivated account is refused at every gated route

`implemented` · `policy` · authority `handler`

An account that has not completed activation may authenticate, but it may do nothing else: every gated route refuses it before any permission is even resolved, with a refusal that says the account must be activated. Activation is therefore the gate between holding a credential and being allowed to act.

**Authority** `handler`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/kernel/httpx/middleware.go::Middleware.RequirePermission` | `handler` | `authoritative` | — |
| `internal/kernel/httpx/errors.go::AuthenticationRequired` | `handler` | `defense-in-depth` | — |

**Refusal** `synthetic` → _(unnamed)_ → _(unmapped)_ → `403`

**Verification**

- e2e: `internal/e2e/auth_test.go::TestRequirePermissionMiddleware`
- e2e: `internal/e2e/rides_test.go::TestRideRoutesPermissionGate`

#### R-IDENTITY-019 — An account is born unactivated

`implemented` · `invariant` · authority `service`

A newly registered account is never active: activation is a separate, token-driven step that the user completes later from the mail. The flag a caller supplies is overwritten on the way in, so no registration — however framed — can create a working account.

**Authority** `service`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/identity/internal/service/activation.go::Service.Register` | `service` | `authoritative` | — |
| `internal/identity/internal/handler/dto.go::registerUserInput` | `handler` | `defense-in-depth` | — |
| `internal/identity/internal/repository/user_repo.go::UserRepository.Insert` | `repository` | `defense-in-depth` | — |

**Refusal** `synthetic` → _(unnamed)_ → _(unmapped)_ → `400`

**Verification**

- e2e: `internal/e2e/users_test.go::TestRegisterUserHandlerSuccessState`
- e2e: `internal/e2e/users_test.go::TestRegisterUserHandler`

#### R-IDENTITY-020 — An account's identity facts are stamped by the store

`implemented` · `persistence-fact` · authority `database`

An account's id, its creation time and its version counter are produced by the store, never by the caller — a client cannot choose who it is or how old its row looks. The store hands them back with the write, so the service never has to invent one.

**Authority** `database` · mechanism `constraint` · scope `row-local`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `migrations/000002_create_users_table.up.sql` | `db-constraint` | `authoritative` | — |
| `internal/identity/internal/repository/user_repo.go::UserRepository.Insert` | `repository` | `defense-in-depth` | — |
| `internal/identity/internal/handler/dto.go::registerUserInput` | `handler` | `defense-in-depth` | — |

**Refusal** `synthetic` → _(unnamed)_ → _(unmapped)_ → `400`

**Verification**

- unit: `internal/identity/internal/repository/user_repo_test.go::TestInsert`
- e2e: `internal/e2e/users_test.go::TestRegisterUserHandler`

**Knowledge ref** `docs/league-tokens-learning-phases/phase-01-postgresql-persistence.md`

#### R-IDENTITY-022 — Activation happens once and asking discloses nothing

`implemented` · `policy` · authority `service`

Activation happens once. An account that is already active cannot be activated again, and a token that was already spent, has expired or was minted for another purpose is no longer an activation credential. Asking for a fresh activation mail is answered the same way whoever asks: an address no account holds and an address whose account is already active receive exactly the answer a genuine request receives — the same status, the same body — and neither mints a token nor sends a mail, so the endpoint never says whether an address is registered or whether its account is active.

**Authority** `service`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/identity/internal/service/activation.go::Service.RequestActivationToken` | `service` | `authoritative` | — |
| `internal/identity/internal/handler/user_tokens.go::Handler.createActivationToken` | `handler` | `defense-in-depth` | — |
| `internal/identity/internal/service/activation.go::Service.Activate` | `service` | `defense-in-depth` | — |

**Refusal** `synthetic` → _(unnamed)_ → `ErrNotFound` → `422`

**Verification**

- unit: `internal/identity/internal/service/activation_test.go::TestActivate`
- unit: `internal/identity/internal/service/activation_test.go::TestRequestActivationToken`

### Entity `user_token`

#### R-IDENTITY-005 — A token is twenty-six random characters

`implemented` · `invariant` · authority `domain`

Every token the service mints is twenty-six characters drawn from the crypto-grade random source — a hundred and twenty-eight bits and a bit of slack, so a token cannot be guessed and two tokens never collide. A presented credential that is not that shape is refused before any lookup runs.

**Authority** `domain`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/identity/internal/model/user_token.go::GenerateUserToken` | `domain` | `authoritative` | — |
| `internal/identity/internal/model/user_token.go::ValidateUserTokenPlaintext` | `validator` | `defense-in-depth` | — |
| `internal/identity/identity.go::Module.UserForToken` | `handler` | `advisory-copy` | — |

**Refusal** `synthetic` → _(unnamed)_ → `ErrInvalidToken` → `401`

**Verification**

- unit: `internal/identity/internal/model/user_token_test.go::TestGenerateUserToken`
- e2e: `internal/e2e/auth_test.go::TestAuthenticateMiddleware`

#### R-IDENTITY-006 — Only a token's hash is ever stored

`implemented` · `invariant` · authority `database`

The service keeps only a fingerprint of each token it issues; the token text itself exists in memory, travels to the user once, and is never written to the database. A presented token is fingerprinted the same way before the lookup, so a stolen copy of the table reveals nothing that works as a credential.

**Authority** `database` · mechanism `constraint` · scope `row-local`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `migrations/000003_create_user_tokens_table.up.sql` | `db-constraint` | `authoritative` | — |
| `internal/identity/internal/repository/user_repo.go::UserRepository.getForToken` | `repository` | `defense-in-depth` | — |
| `internal/identity/internal/repository/user_token_repo.go::UserTokenRepository.insert` | `repository` | `defense-in-depth` | — |

**Refusal** `synthetic` → _(unnamed)_ → `ErrNotFound` → `401`

**Verification**

- unit: `internal/identity/internal/repository/user_token_repo_test.go::TestNewAndGetForToken`
- unit: `internal/identity/internal/model/user_token_test.go::TestGenerateUserToken`

#### R-IDENTITY-007 — Activation tokens live three days, authentication tokens one day

`implemented` · `policy` · authority `service`

A token minted to activate an account is good for three days — long enough for the user to find the mail — and a token minted by a successful login is good for one day. The lifetime is decided once per flow and stamped on the row at mint time, so every token of a kind lives exactly as long as its flow says.

**Authority** `service`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/identity/internal/service/activation.go::Service.Register` | `service` | `authoritative` | — |
| `internal/identity/internal/service/activation.go::Service.RequestActivationToken` | `service` | `authoritative` | — |
| `internal/identity/internal/service/activation.go::Service.Authenticate` | `service` | `authoritative` | — |

#### R-IDENTITY-008 — A token only works for what it was minted for, while it is fresh

`implemented` · `policy` · authority `repository`

A presented token resolves to its account only if it was minted for the purpose the presenter is using it for and its lifetime has not run out. Both facts are part of the same lookup, so a spent, wrong-purpose or expired token is not a distinguishable state — it is simply "no such token", and the caller is refused as unauthenticated.

**Authority** `repository` · scope `row-local`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/identity/internal/repository/user_repo.go::UserRepository.getForToken` | `repository` | `authoritative` | — |
| `internal/identity/identity.go::Module.UserForToken` | `handler` | `defense-in-depth` | — |

**Refusal** `synthetic` → _(unnamed)_ → `ErrNotFound` → `401`

**Verification**

- unit: `internal/identity/internal/repository/user_token_repo_test.go::TestNewAndGetForToken`
- e2e: `internal/e2e/auth_test.go::TestAuthenticateMiddleware`

#### R-IDENTITY-009 — Activating an account spends every activation token it holds

`implemented` · `orchestration` · authority `service`

An activation token is single-use. The moment an account is activated, the activation flag is flipped and every activation token that account holds is deleted in the same unit of work — so a second presentation of the same token finds nothing, and a crash halfway through leaves neither the flip nor the cleanup behind.

**Authority** `service`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/identity/internal/service/activation.go::Service.Activate` | `service` | `authoritative` | — |
| `internal/identity/internal/repository/user_repo.go::UserRepository.UpdateTx` | `repository` | `defense-in-depth` | — |
| `internal/identity/internal/repository/user_token_repo.go::UserTokenRepository.DeleteAllForUserTx` | `repository` | `defense-in-depth` | — |

**Refusal** `synthetic` → _(unnamed)_ → `ErrNotFound` → `422`

**Verification**

- unit: `internal/identity/internal/service/activation_test.go::TestActivate`
- unit: `internal/identity/internal/repository/user_token_repo_test.go::TestDeleteAllForUserTx`

#### R-IDENTITY-018 — A failed login is one refusal, whichever half failed

`implemented` · `policy` · authority `service`

An email that no account holds and a password that does not match the account produce the same refusal: the same status, the same body, and the same sentence. The service never says which half was wrong, so a caller cannot use login to learn whether an address is registered.

**Authority** `service`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/identity/internal/service/activation.go::Service.Authenticate` | `service` | `authoritative` | — |

**Refusal** `synthetic` → _(unnamed)_ → `ErrInvalidCredentials` → `401`

**Verification**

- unit: `internal/identity/internal/model/user_test.go::TestPasswordHashRoundTrip`
- unit: `internal/identity/internal/service/authenticate_test.go::TestAuthenticate`

## Module `kernel`

### Entity `cors_origin`

#### R-KERNEL-010 — Cross-origin access is an exact allowlist, silently withheld otherwise

`implemented` · `policy` · authority `handler`

Only origins named in the deployment's allowlist are granted cross-origin access, by exact match. An unlisted origin receives no cross-origin permission at all — the request still completes, and the browser withholds the response — and a preflight from an allowed origin is answered immediately, before any throttle token is spent and before any credential is read.

**Authority** `handler`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/kernel/httpx/middleware.go::Middleware.EnableCORS` | `handler` | `authoritative` | — |

**Refusal** `synthetic` → _(unnamed)_ → _(unmapped)_ → _(falls through to 500)_

**Verification**

- e2e: `internal/e2e/cors_test.go::TestEnableCORS`

### Entity `db_budget`

#### R-KERNEL-005 — The database budget pays for storage, not for hashing

`implemented` · `orchestration` · authority `service`

A request's storage work is measured by one budget of three seconds, and that budget opens only around the database calls it pays for. Work that is not storage — a password hash on registration, a password comparison on login — runs outside it, so a slow machine cannot turn a healthy write into a timeout.

**Authority** `service`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/kernel/persistence/consts.go::DBTimeout` | `repository` | `defense-in-depth` | — |
| `internal/identity/internal/service/activation.go::Service.Register` | `service` | `authoritative` | — |
| `internal/identity/internal/service/activation.go::Service.Authenticate` | `service` | `authoritative` | — |
| `internal/kernel/httpx/middleware.go::Middleware.Authenticate` | `handler` | `defense-in-depth` | — |
| `internal/kernel/httpx/middleware.go::Middleware.RequirePermission` | `handler` | `defense-in-depth` | — |

**Verification**

- unit: `internal/identity/internal/service/db_budget_test.go::TestRegisterHashesOutsideDBBudget`
- unit: `internal/identity/internal/service/db_budget_test.go::TestAuthenticateComparesOutsideDBBudget`

### Entity `domain_event`

#### R-KERNEL-013 — An event is a post-commit, same-process notification

`implemented` · `orchestration` · authority `service`

A domain event is delivered only after the publishing operation has committed, to subscribers inside the same process, in the order they subscribed. Nothing is stored and nothing is retried: a subscriber that fails, or a process that dies between the commit and the delivery, drops that event — the publisher's committed write stands and must not be rolled back.

**Authority** `service`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/kernel/eventbus/eventbus.go::Bus.Publish` | `service` | `authoritative` | — |
| `internal/kernel/eventbus/eventbus.go::Bus.Subscribe` | `service` | `authoritative` | — |
| `internal/schedule/internal/service/service.go::publish` | `service` | `authoritative` | — |
| `internal/schedule/internal/service/resolve_match.go::Service.ResolveMatch` | `service` | `authoritative` | — |
| `internal/schedule/internal/service/round_lifecycle.go::RoundService.Close` | `service` | `authoritative` | — |

**Verification**

- unit: `internal/kernel/eventbus/eventbus_test.go::TestBus_SubscribeAndPublish`
- unit: `internal/kernel/eventbus/eventbus_test.go::TestBus_PublishReturnsFirstHandlerError`
- e2e: `internal/e2e/matches_test.go::TestResolveMatchSettlesRowOnce`

#### R-KERNEL-014 — A subscriber claims the event before it acts

`implemented` · `orchestration` · authority `service`

A subscriber records the event's identity before it writes anything, so an event delivered twice is handled once: the second delivery finds the claim and completes as a no-op. A subscriber that fails leaves no claim behind, so the same event may be delivered and processed again.

**Authority** `service`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/game/internal/core/service/settlement.go::Service.runEvent` | `service` | `authoritative` | — |
| `internal/game/internal/core/service/settlement.go::Service.OnMatchResultRecorded` | `service` | `authoritative` | — |
| `internal/game/internal/core/service/settlement.go::Service.OnRoundCompleted` | `service` | `authoritative` | — |
| `internal/kernel/idempotency/idempotency.go::Repository.Claim` | `repository` | `defense-in-depth` | — |

**Verification**

- unit: `internal/game/internal/core/service/service_test.go::TestService_OnMatchResultRecorded`
- unit: `internal/game/internal/core/service/service_test.go::TestService_OnRoundCompleted`
- e2e: `internal/e2e/matches_test.go::TestResolveMatchSettlesRowOnce`

**Expert ref** `docs/league-tokens-learning-phases/phase-08-transactional-outbox-and-events.md`

### Entity `http_request`

#### R-KERNEL-006 — Request validation is collected, not fail-fast

`implemented` · `interface` · authority `handler`

A malformed request reports every offending field at once, one message per field, so the caller can fix the whole request in one round trip. A request that fails no field check is the only one that reaches the operation behind the handler.

**Authority** `handler`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/kernel/validator/validator.go::Validator.AddError` | `validator` | `authoritative` | — |
| `internal/kernel/validator/validator.go::Validator.Check` | `validator` | `authoritative` | — |

**Refusal** `synthetic` → _(unnamed)_ → _(unmapped)_ → `422`

**Verification**

- e2e: `internal/e2e/teams_test.go::TestCreateTeamHandler`
- e2e: `internal/e2e/rides_test.go::TestCreateRideHandler`

#### R-KERNEL-008 — A malformed credential is refused before the handler

`implemented` · `interface` · authority `handler`

A request that presents a credential must present it as a bearer token; anything else — an unknown scheme, a missing value — is refused as unauthenticated before any handler runs. A request that presents no credential at all is not refused here: it proceeds as an anonymous caller and is judged by the permission gate.

**Authority** `handler`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/kernel/httpx/middleware.go::Middleware.Authenticate` | `handler` | `authoritative` | — |
| `internal/kernel/httpx/errors.go::InvalidAuthenticationToken` | `handler` | `authoritative` | — |
| `internal/kernel/httpx/context.go::SetAuthenticatedUser` | `handler` | `authoritative` | — |

**Refusal** `synthetic` → _(unnamed)_ → `ErrInvalidToken` → `401`

**Verification**

- e2e: `internal/e2e/auth_test.go::TestAuthenticateMiddleware`

#### R-KERNEL-011 — A panic is answered as a server error, never explained

`implemented` · `interface` · authority `handler`

A failure inside the request chain is answered with the generic server-error message and the connection is closed; the cause is recorded in the log and never returned to the caller. No internal detail — a stack, a driver message, a query — reaches a client through this path.

**Authority** `handler`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/kernel/httpx/middleware.go::Middleware.RecoverPanic` | `handler` | `authoritative` | — |
| `internal/kernel/httpx/errors.go::ServerError` | `handler` | `authoritative` | — |

**Refusal** `synthetic` → _(unnamed)_ → _(unmapped)_ → `500`

**Verification**

- unit: `internal/kernel/httpx/middleware_test.go::TestRecoverPanicAnswersServerError`

#### R-KERNEL-012 — A request body is read under a one-megabyte cap

`implemented` · `interface` · authority `handler`

No request body is read beyond one megabyte. A body over that limit is refused as too large before anything is decoded or deduplicated, so an oversized request claims no idempotency key and a retry with a normal body executes normally.

**Authority** `handler`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/kernel/httpx/json.go::MaxBodyBytes` | `handler` | `authoritative` | — |
| `internal/kernel/httpx/json.go::ReadJSON` | `handler` | `authoritative` | — |
| `internal/game/internal/adapter/inbound/http/rides.go::Handler.createRideToken` | `handler` | `authoritative` | — |

**Refusal** `synthetic` → _(unnamed)_ → _(unmapped)_ → `413`

**Verification**

- e2e: `internal/e2e/rides_test.go::TestCreateRideHandlerBodyTooLarge`

### Entity `idempotency_key`

#### R-KERNEL-001 — An idempotent command must carry a usable idempotency key

`implemented` · `interface` · authority `handler`

Every command that promises retry safety must arrive with a key that names one logical request: a non-empty value of at most 255 bytes. A request without one is refused as a malformed request, and no writer — a script, a future service — can store a key outside those bounds.

**Authority** `handler` · scope `row-local`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/kernel/idempotency/idempotency.go::ValidateIdempotencyToken` | `validator` | `authoritative` | — |
| `migrations/000010_add_idempotency_keys_table.up.sql` | `db-constraint` | `defense-in-depth` | — |

**Refusal** `synthetic` → `idempotency_keys_key_check` → _(unmapped)_ → `422`

**Verification**

- unit: `internal/kernel/idempotency/idempotency_test.go::TestValidateIdempotencyToken`
- e2e: `internal/e2e/rides_test.go::TestCreateRideHandler`

**Knowledge ref** `docs/league-tokens-learning-phases/phase-04-concurrency-and-idempotency.md`

#### R-KERNEL-002 — A request's dedup identity is a digest of the request itself

`implemented` · `orchestration` · authority `handler`

Two requests are the same logical request exactly when they carry the same method, the same request URI and — for a command with a body — the same raw body bytes. The identity is a SHA-256 digest of exactly those bytes, and a body is capped before it is hashed so that the bytes hashed are the bytes decoded: a retry of an identical request is recognized as a replay, any changed field makes it a different request, and an oversized body claims no key at all.

**Authority** `handler` · scope `row-local`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/game/internal/adapter/inbound/http/rides.go::Handler.rideCommandToken` | `handler` | `authoritative` | — |
| `internal/game/internal/adapter/inbound/http/rides.go::Handler.createRideToken` | `handler` | `authoritative` | — |
| `migrations/000010_add_idempotency_keys_table.up.sql` | `db-constraint` | `defense-in-depth` | — |

**Verification**

- unit: `internal/game/internal/core/service/idempotency_test.go::TestService_RideCreateIdempotent`
- e2e: `internal/e2e/rides_test.go::TestCreateRideIdempotentHandler`
- e2e: `internal/e2e/rides_test.go::TestRideLockIdempotentHandler`
- e2e: `internal/e2e/rides_test.go::TestCreateRideHandlerBodyTooLarge`

#### R-KERNEL-003 — An idempotency key names exactly one logical request

`implemented` · `policy` · authority `repository`

Once a key has been used by one caller, that same caller reusing it for a different request is refused as key misuse and the second request never executes. The refusal is a conflict, not a retry: a genuine retry resends the identical request and receives the stored answer. A key belongs to its caller — two callers that happen to choose the same key hold two independent claims, so one caller's key can neither collide with another's nor come back with another's answer. A claim is remembered for one day: a key whose claim is older than that is forgotten, and a command sent under it executes again instead of replaying.

**Authority** `repository` · scope `row-local`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/kernel/idempotency/idempotency.go::Repository.Claim` | `repository` | `authoritative` | — |
| `migrations/000022_scope_idempotency_keys_per_caller.up.sql` | `db-constraint` | `authoritative` | — |
| `migrations/000010_add_idempotency_keys_table.up.sql` | `db-constraint` | `defense-in-depth` | — |
| `internal/game/internal/adapter/inbound/http/errors.go::Handler.idempotencyConflictResponse` | `handler` | `advisory-copy` | — |

**Refusal** `synthetic` → _(unnamed)_ → `ErrIdempotencyConflict` → `409`

**Verification**

- unit: `internal/game/internal/core/service/idempotency_test.go::TestService_RideLockIdempotency`
- unit: `internal/game/internal/core/service/idempotency_test.go::TestService_RideCreateIdempotent`
- e2e: `internal/e2e/rides_test.go::TestRideLockIdempotentHandler`
- e2e: `internal/e2e/rides_test.go::TestCreateRideIdempotentHandler`

**Knowledge ref** `docs/league-tokens-learning-phases/phase-04-concurrency-and-idempotency.md`

#### R-KERNEL-004 — A claim commits with its command, never before or after it

`implemented` · `orchestration` · authority `service`

The claim on a key and the stored answer are written inside the command's own transaction, so a command that fails leaves no claim behind (failures are never cached) and a command that succeeds commits its claim together with its effect. A concurrent duplicate waits on the claim and then receives the committed answer instead of executing a second time.

**Authority** `service` · scope `row-local`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/game/internal/core/service/ride_commands.go::Service.RideCreate` | `service` | `authoritative` | — |
| `internal/game/internal/core/service/ride_commands.go::Service.RideLock` | `service` | `authoritative` | — |
| `internal/game/internal/core/service/ride_commands.go::Service.RideBurn` | `service` | `authoritative` | — |
| `internal/game/internal/core/service/ride_commands.go::Service.RideUnlock` | `service` | `authoritative` | — |
| `internal/kernel/idempotency/idempotency.go::Repository.Claim` | `repository` | `defense-in-depth` | — |
| `internal/kernel/idempotency/idempotency.go::Repository.Save` | `repository` | `defense-in-depth` | — |

**Verification**

- unit: `internal/game/internal/core/service/idempotency_test.go::TestService_RideLockIdempotency`
- unit: `internal/game/internal/core/service/idempotency_test.go::TestService_RideCreateIdempotent`
- e2e: `internal/e2e/rides_test.go::TestCreateRideIdempotentHandler`
- e2e: `internal/e2e/rides_test.go::TestRideLockIdempotentHandler`

**Knowledge ref** `docs/league-tokens-learning-phases/phase-04-concurrency-and-idempotency.md`

### Entity `list_query`

#### R-KERNEL-007 — A list request is bounded, and its sort never becomes client SQL

`implemented` · `interface` · authority `handler`

A list request selects a bounded window of results — at least one row, at most one hundred per page, and a page number small enough to be addressable — and it may sort only by a column the resource publishes. A sort name outside that list is refused as a malformed request rather than handed to the database.

**Authority** `handler`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/kernel/persistence/filters.go::ValidateFilters` | `validator` | `authoritative` | — |
| `internal/kernel/persistence/filters.go::Filters.SortColumn` | `repository` | `defense-in-depth` | — |

**Refusal** `synthetic` → _(unnamed)_ → _(unmapped)_ → `422`

**Verification**

- e2e: `internal/e2e/teams_test.go::TestListTeamsHandler`
- e2e: `internal/e2e/rides_test.go::TestListRidesHandler`

### Entity `outbox_event`

#### R-KERNEL-015 — An event is committed with the change that caused it

`specified-only` · `orchestration` · authority `service`

The fact that an event must be published is committed in the same transaction as the state change itself: if the change commits the event is owed, and if the change rolls back the event was never owed.

**Authority** `service`

**Expert ref** `docs/league-tokens-learning-phases/phase-08-transactional-outbox-and-events.md`

#### R-KERNEL-016 — A relay delivers committed events and marks them published

`specified-only` · `orchestration` · authority `service`

Events owed by committed transactions are read from the outbox by a relay that runs outside the request, delivered to their consumers, and marked published only after the delivery, so a crash leaves the event to be delivered again.

**Authority** `service`

**Expert ref** `docs/league-tokens-learning-phases/phase-08-transactional-outbox-and-events.md`

#### R-KERNEL-017 — Delivery is at-least-once and never claimed exactly-once

`specified-only` · `orchestration` · authority `service`

An event a transaction committed to publish is eventually delivered at least once, and the system makes no exactly-once claim: a consumer must tolerate the same event arriving twice.

**Authority** `service`

**Expert ref** `docs/league-tokens-learning-phases/phase-08-transactional-outbox-and-events.md`

### Entity `rate_limit`

#### R-KERNEL-009 — Each client gets a token bucket, and overdraw is a 429

`implemented` · `policy` · authority `handler`

Requests are throttled per client address: a client may burst a configured number of requests and is then refilled at a configured rate. A client over its allowance is refused with a rate-limit answer that tells it how long to wait, and the operation behind the gate never runs.

**Authority** `handler`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/kernel/httpx/ratelimiter.go::RateLimiter.Allow` | `handler` | `authoritative` | — |
| `internal/kernel/httpx/ratelimiter.go::RateLimiter.ClientKey` | `handler` | `authoritative` | — |
| `internal/kernel/httpx/middleware.go::Middleware.RateLimit` | `handler` | `authoritative` | — |
| `internal/kernel/httpx/ratelimiter.go::RateLimiter.Sweep` | `handler` | `authoritative` | — |

**Refusal** `synthetic` → _(unnamed)_ → _(unmapped)_ → `429`

**Verification**

- unit: `internal/kernel/httpx/ratelimiter_test.go::TestRateLimitMiddlewareDenial`
- unit: `internal/kernel/httpx/ratelimiter_test.go::TestRateLimiterSustainedCapped`
- unit: `internal/kernel/httpx/ratelimiter_test.go::TestRateLimiterIPsIsolated`
- unit: `internal/kernel/httpx/ratelimiter_test.go::TestRateLimiterStaleBucketsEvicted`
- unit: `internal/kernel/httpx/ratelimiter_proxy_test.go::TestClientKeyTrustedProxyUsesForwardedClient`
- unit: `internal/kernel/httpx/ratelimiter_proxy_test.go::TestClientKeyTrustedProxyTakesRightmostUntrustedHop`
- unit: `internal/kernel/httpx/ratelimiter_proxy_test.go::TestClientKeyUntrustedPeerIgnoresForwardedFor`
- unit: `internal/kernel/httpx/ratelimiter_proxy_test.go::TestClientKeyEmptyTrustedListIgnoresForwardedFor`
- unit: `internal/kernel/httpx/ratelimiter_proxy_test.go::TestClientKeyMalformedForwardedForFallsBack`
- unit: `internal/kernel/httpx/ratelimiter_proxy_test.go::TestRateLimitMiddlewareKeysOnForwardedClientBehindProxy`

## Module `ledger`

### Entity `account`

#### R-LEDGER-004 — Every balance sits in an account of a known kind

`specified-only` · `invariant` · authority `database`

Money is not held as a free-standing number: every balance belongs to an account, and every account is one of the ledger's kinds — a player's currency, a player's tokens for a team, a team reserve, or the common pool.

**Authority** `database` · mechanism `constraint` · scope `row-local`

**Expert ref** `docs/league-tokens-learning-phases/phase-06-ledger-and-accounting.md`

### Entity `balance`

#### R-LEDGER-001 — No balance goes negative

`specified-only` · `invariant` · authority `database`

A player's currency or token balance can never fall below zero. A debit that would overdraw is refused before any journal entry or balance projection is written, so a negative balance is not a repairable state — it is unrepresentable.

**Authority** `database` · mechanism `constraint` · scope `row-local`

**Expert ref** `docs/league-tokens-learning-phases/phase-06-ledger-and-accounting.md`

#### R-LEDGER-003 — Only the ledger moves money

`specified-only` · `boundary` · authority `composition-root`

A balance changes only through a ledger operation. Other modules state what should happen to the money — deduct, lock, burn — and never compute or write a balance themselves, so there is exactly one place where the corruption rule can be enforced.

**Authority** `composition-root`

**Expert ref** `docs/league-tokens-learning-phases/phase-06-ledger-and-accounting.md`

#### R-LEDGER-005 — A balance is the journal's projection

`specified-only` · `invariant` · authority `database`

A balance is only ever an explanation of what the journal says. Every balance change is accompanied, in the same transaction, by the journal entries for it, so a player's visible balance can always be traced back to the postings that produced it.

**Authority** `database` · mechanism `trigger` · scope `cross-table`

**Expert ref** `docs/league-tokens-learning-phases/phase-06-ledger-and-accounting.md`

### Entity `journal_entry`

#### R-LEDGER-002 — Every money movement posts a balanced journal pair

`specified-only` · `invariant` · authority `database`

Every movement of money is recorded as a pair of journal entries whose debits equal their credits, in the same transaction as the balance projection update. A single-sided entry never commits, so the journal always explains every balance.

**Authority** `database` · mechanism `trigger` · scope `cross-row`

**Expert ref** `docs/league-tokens-learning-phases/phase-06-ledger-and-accounting.md`

#### R-LEDGER-006 — A money operation repeats without moving money twice

`specified-only` · `orchestration` · authority `service`

A ledger operation that is retried — after a crash, a restart or a duplicate request — applies its movement once: the retry is recognised as the same operation and changes no balance and no journal entry.

**Authority** `service`

**Expert ref** `docs/league-tokens-learning-phases/phase-06-ledger-and-accounting.md`

#### R-LEDGER-007 — Buying tokens posts both sides of the purchase

`specified-only` · `orchestration` · authority `service`

Buying tokens is one movement with two sides: the player pays currency into the common pool and receives tokens from the team reserve, and both sides are posted together, so no leg of a purchase can survive on its own.

**Authority** `service`

**Expert ref** `docs/league-tokens-learning-phases/phase-06-ledger-and-accounting.md`

## Module `platform`

### Entity `configuration`

#### R-PLATFORM-001 — Configuration is resolved flag over environment over default

`implemented` · `orchestration` · authority `handler`

A deployment's settings come from its environment, and an operator may override them per run with a flag; when neither says anything, a built-in default applies. Values that are deployment data rather than per-run operator choices — the cross-origin allowlist, the build version — are not overridable at all.

**Authority** `handler`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/platform/config/config.go::Parse` | `handler` | `authoritative` | — |
| `internal/platform/config/config.go::readEnv` | `handler` | `authoritative` | — |

**Verification**

- unit: `internal/platform/config/config_test.go::TestParse`

#### R-PLATFORM-002 — Startup refuses a configuration it cannot honour, and names the setting

`implemented` · `orchestration` · authority `handler`

A configuration value that is missing, unparseable or out of its allowed range stops the process before it serves anything, and the message names the setting that must change — both its environment variable and its command-line flag, because the same value can arrive either way. Every bad environment value is reported in one refusal instead of one at a time, and nothing degrades into a silent default.

**Authority** `handler`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/platform/config/config.go::Parse` | `handler` | `authoritative` | — |
| `internal/platform/config/config.go::readEnv` | `handler` | `authoritative` | — |

**Refusal** `synthetic` → _(unnamed)_ → _(unmapped)_ → _(falls through to 500)_

**Verification**

- unit: `internal/platform/config/config_test.go::TestParse`
- unit: `internal/platform/config/config_test.go::TestValidate`

#### R-PLATFORM-003 — Every setting has a bound, and the bound holds when the feature is off

`implemented` · `orchestration` · authority `handler`

Each setting is either usable or refused: the listen port and the mail port are real ports, the outbound sender is an address mail can be sent from, the environment is one of the three the deployment knows, a database connection string is present and parseable, the pool is at least one connection with a positive idle limit, and the throttle rates are positive. A setting is judged on its own merits — a throttle that is switched off must still be configured with usable values, because config that is only correct while something else is off fails unpredictably when it is switched on.

**Authority** `handler`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/platform/config/config.go::validate` | `validator` | `authoritative` | — |
| `internal/platform/config/config.go::parseLogLevel` | `validator` | `authoritative` | — |

**Refusal** `synthetic` → _(unnamed)_ → _(unmapped)_ → _(falls through to 500)_

**Verification**

- unit: `internal/platform/config/config_test.go::TestValidate`
- unit: `internal/platform/config/config_test.go::TestParse`

#### R-PLATFORM-004 — A log level is an enumerated name, never a silent fallback

`implemented` · `orchestration` · authority `handler`

The logging level is one of the four names the deployment documents — debug, info, warn, error — matched without regard to case. An unrecognized level stops startup and the message names where the bad value came from, because a fallback would either flood or silence a deployment whose level was mistyped.

**Authority** `handler`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/platform/config/config.go::parseLogLevel` | `validator` | `authoritative` | — |
| `internal/platform/config/config.go::readEnv` | `validator` | `authoritative` | — |

**Refusal** `synthetic` → _(unnamed)_ → _(unmapped)_ → _(falls through to 500)_

**Verification**

- unit: `internal/platform/config/config_test.go::TestParse`

### Entity `context_dependency`

#### R-PLATFORM-013 — Contexts depend one way and meet only at facades

`implemented` · `boundary` · authority `composition-root`

A context uses another context only through the other's public operations, in one allowed direction, and never reaches into its tables. The context that owns the fact answers; the context that needs it declares what it needs and has it satisfied from outside; and where the two vocabularies meet, the translation happens in one place so neither context speaks the other's error language.

**Authority** `composition-root` · scope `cross-table`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `cmd/api/main.go::main` | `service` | `authoritative` | — |
| `cmd/api/gamerefs.go::gameRefs.DeletePlayersForSeasonTx` | `service` | `authoritative` | — |
| `internal/identity/identity.go::Module` | `handler` | `authoritative` | — |
| `internal/kernel/httpx/middleware.go::UserLookup` | `handler` | `defense-in-depth` | — |
| `internal/kernel/httpx/middleware.go::PermissionLookup` | `handler` | `defense-in-depth` | — |

**Verification**

- unit: `internal/arch/arch_test.go::TestImportBoundaries`
- unit: `internal/arch/arch_test.go::TestNonModulesNeverImportModules`
- unit: `internal/arch/arch_test.go::TestModulesNeverImportPlatform`
- unit: `internal/arch/arch_test.go::TestKernelImportsOnlyKernel`
- e2e: `internal/e2e/teams_test.go::TestDeleteTeamHandler`

**Expert ref** `docs/league-tokens-learning-phases/phase-05-modular-monolith-boundaries.md`

**Notes**

> Authority is the composition root (ADR-028): it is the only place that may import everything, so it is where every direction is chosen — the game context receives the calendar facade (wrapped by its own anti-corruption layer), the identity facade (as a consumer-side lookup), the ledger and the idempotency engine; the calendar receives the game-reference port from outside rather than importing game. Layer note: the wiring has no layer of its own in the frozen enum; it is recorded as service. The pinned direction itself lives in internal/arch/arch_test.go's allowedImports matrix (test-time enforcement, so it appears under verification, not as a site): a module reaches another module's root only, nothing below the modules reaches a module, no module reaches the platform, the kernel reaches the kernel only. The gamerefs adapter is the single vocabulary translator (game's record-in-use becomes the calendar's), which is what keeps the delete refusals byte-identical. Merged with provisional R-IDENTITY-021 (Phase B3): identity's own statement of this rule — other parts reach identity through its published surface only (the two lookups httpx declares and the account read the player flow needs), never its models, repositories or tables, and the transport layer holds no identity import — is the same composition-root decision seen from one edge, so it is one rule with the union of sites and identity's slot is freed. The three identity sites are the facade the root builds once (identity.Module, authoritative) and the two consumer-side shapes that let httpx stay identity-free (defense-in-depth). Identity's facade also re-exports the model's types and sentinels as aliases, so callers name identity's vocabulary without importing its internals; game's player handler mapping identity.ErrNotFound onto its own validation error is the only caller that depends on one of those sentinels. Tie: the copies cannot drift silently — TestImportBoundaries walks every import in the tree against internal/arch's allowedImports, so an httpx -> identity edge or a schedule -> identity edge fails the build's test first. No refusal of its own: the drift is a compile- or test-time failure, never a status a client sees — which is also why the layer value is approximate (the frozen enum has no composition-root layer; `handler` is the seam the facade satisfies). R-GAME-026 is the distinct publisher-language half of the same boundary at game's edge. stale-pointer: phase-05 §"As built" names `cmd/server` as the composition root and the hoisted `internal/idempotency`, and lists "api — the HTTP gateway" as a component; the binary is cmd/api, the idempotency engine is internal/kernel/idempotency and there is no api package (each module owns its transport). The doc's §"Suggested ownership" is already flagged in the extraction as superseded by its own §"As built".

### Entity `context_schema`

#### R-PLATFORM-012 — Each context owns one schema and speaks only as its own role

`implemented` · `boundary` · authority `composition-root`

Each bounded context owns one database schema and nothing else: the calendar its teams, seasons, rounds and matches; the game its rides, players and idempotency keys; identity its users, tokens, roles and permissions. A context reaches the database as its own role, which holds privileges on its own schema alone, so an attempt to read or write another context's tables is refused by the database itself rather than by code review.

**Authority** `composition-root` · scope `cross-table`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `migrations/000015_split_context_schemas.up.sql` | `db-constraint` | `authoritative` | — |
| `migrations/000018_context_roles.up.sql` | `db-constraint` | `authoritative` | — |

**Refusal** `42501` → _(unnamed)_ → _(unmapped)_ → _(falls through to 500)_

**Verification**

- unit: `internal/testdb/boundary_test.go::TestGameAppBoundary`
- unit: `internal/testdb/boundary_test.go::TestScheduleAppBoundary`
- unit: `internal/testdb/boundary_test.go::TestIdentityAppBoundary`

**Knowledge ref** `docs/league-tokens-learning-phases/phase-05-modular-monolith-boundaries.md`

#### R-PLATFORM-015 — A context's pool speaks as that context's role

`implemented` · `boundary` · authority `repository`

The connection pool a context is given runs every one of its statements as that context's role, and no context is ever handed another context's pool. The role is bound when a connection is established, together with the search path that resolves the context's own unqualified table names, so no statement can run with broader privileges than the context it serves.

**Authority** `repository` · scope `cross-table`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `cmd/api/main.go::openContextPool` | `repository` | `authoritative` | — |
| `cmd/api/main.go::openPools` | `repository` | `authoritative` | — |

**Refusal** `42501` → _(unnamed)_ → _(unmapped)_ → _(falls through to 500)_

**Verification**

- unit: `internal/testdb/context_roles_test.go::TestContextPoolsRunAsContextRole`

**Knowledge ref** `docs/league-tokens-learning-phases/phase-05-modular-monolith-boundaries.md`

### Entity `cors_origin`

#### R-PLATFORM-005 — The cross-origin allowlist holds bare origins, never a wildcard

`implemented` · `orchestration` · authority `handler`

The deployment lists the origins it serves cross-origin, one bare origin each; a wildcard is refused at startup rather than degraded into "allow everything". Origins are normalized to lowercase scheme and host with any trailing slash removed, a value carrying a path, query or fragment is refused as not an origin, duplicates collapse, and an empty list means cross-origin access is simply off.

**Authority** `handler`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/platform/config/config.go::parseCORSOrigins` | `validator` | `authoritative` | — |

**Refusal** `synthetic` → _(unnamed)_ → _(unmapped)_ → _(falls through to 500)_

**Verification**

- unit: `internal/platform/config/config_test.go::TestParseCORSOrigins`
- unit: `internal/platform/config/config_test.go::TestParse`

### Entity `email_delivery`

#### R-PLATFORM-006 — Mail cannot be sent from an incomplete configuration

`implemented` · `orchestration` · authority `handler`

If the outbound mail settings are incomplete — no host, no credentials, no sender — the process refuses to start instead of accepting requests it cannot honour. The refusal is distinguished from a delivery failure, so a configuration mistake is never retried as if it were a network problem.

**Authority** `handler`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/platform/mailer/mailer.go::New` | `handler` | `authoritative` | — |

**Refusal** `synthetic` → _(unnamed)_ → `ErrMissingCredentials` → _(falls through to 500)_

**Verification**

- unit: `internal/platform/mailer/mailer_test.go::TestNewRefusesIncompleteConfig`
- unit: `internal/platform/mailer/mailer_test.go::TestNewAcceptsCompleteConfig`
- unit: `internal/platform/mailer/mailer_test.go::TestSendFailureIsNotConfigError`

#### R-PLATFORM-007 — A failed delivery is retried a bounded number of times, then reported

`implemented` · `policy` · authority `service`

One email delivery is attempted at most three times, with a growing pause between attempts that is randomized so that many failing senders do not retry in a synchronized wave. A cancelled request stops the ladder at once instead of letting it run on after the caller and the process have given up.

**Authority** `service`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/platform/mailer/mailer.go::Mailer.Send` | `service` | `authoritative` | — |

**Verification**

- unit: `internal/platform/mailer/mailer_test.go::TestSendRetriesBoundedAttempts`
- unit: `internal/platform/mailer/mailer_test.go::TestSendJitterIsRandomized`
- unit: `internal/platform/mailer/mailer_test.go::TestSendStopsOnCancellation`

### Entity `http_request`

#### R-PLATFORM-014 — The request chain order is fixed

`implemented` · `orchestration` · authority `composition-root`

Every request traverses the same chain in the same order: panic recovery outermost, then cross-origin handling, then throttling, then credential resolution, then the route. That order is what guarantees a panic is always answered, a preflight never spends a throttle token and never needs a credential, and a caller is throttled before any work is done on its behalf.

**Authority** `composition-root`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/platform/httpchain/httpchain.go::Build` | `composition-root` | `authoritative` | — |
| `cmd/api/routes.go::routes` | `handler` | `authoritative` | — |

**Verification**

- unit: `internal/platform/httpchain/httpchain_test.go::TestBuildOrder`
- e2e: `internal/e2e/cors_test.go::TestEnableCORS`
- e2e: `internal/e2e/auth_test.go::TestAuthenticateMiddleware`

### Entity `http_server`

#### R-PLATFORM-009 — Every connection is bounded in time

`implemented` · `orchestration` · authority `handler`

The HTTP server bounds how long it will spend on a connection: a read must finish within five seconds, a write within ten, and an idle connection is closed after a minute. A client that cannot meet those bounds does not hold a server resource indefinitely.

**Authority** `handler`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/platform/server/server.go::Server.Serve` | `handler` | `authoritative` | — |

**Verification**

- unit: `internal/platform/server/server_test.go::TestServeReadTimeoutClosesSilentConnection`
- unit: `internal/platform/server/server_test.go::TestServeWriteTimeoutClosesStreamingResponse`
- unit: `internal/platform/server/server_test.go::TestServeIdleTimeoutClosesKeepAlive`

### Entity `log_stream`

#### R-PLATFORM-008 — One structured log stream per process

`implemented` · `orchestration` · authority `handler`

The process emits one machine-readable log stream on standard output at the level the deployment chose, and everything that logs — the application and the database driver — writes into that same stream. Call-site source locations are attached in development only, so production records carry no path bulk.

**Authority** `handler`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/platform/logging/logging.go::New` | `handler` | `authoritative` | — |
| `internal/platform/logging/logging.go::newLogger` | `handler` | `authoritative` | — |
| `internal/platform/logging/logging.go::Pgx` | `handler` | `authoritative` | — |

**Verification**

- unit: `internal/platform/logging/logging_test.go::TestNewJSONOutput`
- unit: `internal/platform/logging/logging_test.go::TestNewLevelFiltering`
- unit: `internal/platform/logging/logging_test.go::TestNewSourceShortFileInDevelopment`
- unit: `internal/platform/logging/logging_test.go::TestPgxLevelMapping`
- unit: `internal/platform/logging/logging_test.go::TestPgxDataBecomesSortedAttributes`

### Entity `process_lifecycle`

#### R-PLATFORM-010 — Shutdown is bounded and never hangs

`implemented` · `orchestration` · authority `service`

When the process is asked to stop, it stops accepting work, gives in-flight requests a fixed grace period to finish, and force-closes whatever is left when that period expires. A second stop request during the drain closes connections immediately, and after the drain the process waits a bounded time for background work before exiting. A drain that had to be forced is still a successful stop, not a failure.

**Authority** `service`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/platform/server/server.go::Server.runServer` | `service` | `authoritative` | — |
| `internal/platform/server/server.go::Server.shutdown` | `service` | `authoritative` | — |
| `internal/platform/server/consts.go::ShutdownGracePeriod` | `service` | `authoritative` | — |
| `internal/platform/server/consts.go::BackgroundTaskBudget` | `service` | `authoritative` | — |

**Verification**

- unit: `internal/platform/server/shutdown_test.go::TestDrainTimeoutForceCloses`
- unit: `internal/platform/server/shutdown_test.go::TestSecondSignalForceClosesDuringDrain`
- unit: `internal/platform/server/shutdown_test.go::TestBackgroundTaskBudgetBoundsWait`

**Knowledge ref** `docs/league-tokens-learning-phases/phase-07-background-workers.md`

#### R-PLATFORM-011 — Background work may fail without taking the process down

`implemented` · `policy` · authority `service`

Work that runs after a response has been sent — a mail delivery, a background sweep — is tracked by the server and runs under the process's cancellation root. If it fails or panics, the failure is recorded and the process keeps serving: once a response has been committed, no downstream task may kill it.

**Authority** `service`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/platform/server/server.go::Server.Background` | `service` | `authoritative` | — |
| `cmd/api/main.go::main` | `service` | `authoritative` | — |

**Verification**

- unit: `internal/platform/server/server_test.go::TestBackgroundPanicIsContainedAndRecorded`

**Knowledge ref** `docs/league-tokens-learning-phases/phase-07-background-workers.md`

### Entity `worker`

#### R-PLATFORM-016 — A failing background pass never kills the process

`specified-only` · `orchestration` · authority `composition-root`

A background pass that fails is logged and retried later: the failure never takes the process down and never stops the next pass from running.

**Authority** `composition-root`

**Expert ref** `docs/league-tokens-learning-phases/phase-07-background-workers.md`

#### R-PLATFORM-017 — A background worker stops on cancellation and is waited for

`specified-only` · `orchestration` · authority `composition-root`

A background worker runs under the process's cancellation context and is waited for at shutdown: on a signal it stops taking new work, the process waits for what it already started, and only then exits.

**Authority** `composition-root`

**Expert ref** `docs/league-tokens-learning-phases/phase-07-background-workers.md`

## Module `schedule`

### Entity `match`

#### R-SCHEDULE-002 — A fixture is scheduled forward in time

`implemented` · `policy` · authority `domain`

A match may only be created for a start that lies strictly in the future. Calendar entries are announcements, so a past or exactly-now start is refused at creation; editing an existing match may still adjust a start that has already passed.

**Authority** `domain` · scope `row-local`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/schedule/internal/model/match.go::ValidateNewMatch` | `validator` | `authoritative` | — |

**Refusal** `synthetic` → _(unnamed)_ → _(unmapped)_ → `422`

**Verification**

- unit: `internal/schedule/internal/model/matches_validation_test.go::TestValidateNewMatch`
- e2e: `internal/e2e/matches_test.go::TestCreateMatchHandler`

#### R-SCHEDULE-005 — A started match freezes the whole hierarchy

`implemented` · `invariant` · authority `database`

Once any match has started, nothing above it may move backwards: the match cannot return to created and cannot be postponed, its round cannot return to created or open, and its season cannot return to created or open. The calendar's history is durable the moment the first whistle blows.

**Authority** `database` · mechanism `trigger` · scope `cross-row`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `migrations/000001_create_initial_game_models.up.sql` | `db-trigger` | `authoritative` | — |
| `migrations/000019_add_status_rank_gates.up.sql` | `db-trigger` | `authoritative` | — |

**Refusal** `P0001` → `started_match_blocks_status_regression`, `started_match_blocks_round_regression`, `started_match_blocks_season_regression` → `ErrRecordInUse` → `409`

**Verification**

- unit: `internal/schedule/internal/repository/gates_test.go::TestMatchFreezeGate`
- unit: `internal/schedule/internal/repository/gates_test.go::TestRoundFreezeGate`
- unit: `internal/schedule/internal/repository/gates_test.go::TestSeasonFreezeGate`
- e2e: `internal/e2e/matches_test.go::TestPostponeStartedMatchFrozen`

#### R-SCHEDULE-006 — A match carries a score exactly while it is playing or finished

`implemented` · `invariant` · authority `database`

A match has either no score at all or both halves of one, and never a negative number. Scores exist only from the moment the match is in progress or closed; a created or postponed match carries none.

**Authority** `database` · mechanism `constraint` · scope `row-local`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `migrations/000001_create_initial_game_models.up.sql` | `db-constraint` | `authoritative` | — |
| `internal/schedule/internal/model/match.go::validateMatchShape` | `validator` | `advisory-copy` | — |

**Refusal** `23514` → `matches_status_score_check`, `matches_score_complete_check`, `matches_home_score_check`, `matches_away_score_check` → _(unmapped)_ → _(falls through to 500)_

**Verification**

- unit: `internal/schedule/internal/repository/constraints_test.go::TestMatchConstraints`
- unit: `internal/schedule/internal/model/matches_validation_test.go::TestValidateNewMatch`
- unit: `internal/schedule/internal/model/matches_validation_test.go::TestValidateMatchUpdate`

#### R-SCHEDULE-007 — A closed match records when it ended

`implemented` · `invariant` · authority `database`

A match carries an end time if and only if it is closed, and that end is later than its start. An in-progress or scheduled match has no end, so a reader can never mistake a running match for a finished one.

**Authority** `database` · mechanism `constraint` · scope `row-local`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `migrations/000005_add_matches_ended_at.up.sql` | `db-constraint` | `authoritative` | — |
| `internal/schedule/internal/model/match.go::validateMatchShape` | `validator` | `advisory-copy` | — |

**Refusal** `23514` → `matches_ended_at_check` → _(unmapped)_ → _(falls through to 500)_

**Verification**

- unit: `internal/schedule/internal/repository/constraints_test.go::TestMatchConstraints`

#### R-SCHEDULE-008 — The end of a match is stamped once by the database

`implemented` · `invariant` · authority `database`

The end time of a match is written once, on the single transition into closed, and never rewritten afterwards. The resolve flow supplies the match's actual end time — a client fact the time policy (R-SCHEDULE-020) validates — and the database keeps it; when a writer supplies no end time, the database stamps its own clock. Editing a finished match cannot move its end time.

**Authority** `database` · mechanism `trigger` · scope `cross-row`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `migrations/000005_add_matches_ended_at.up.sql` | `db-trigger` | `authoritative` | — |
| `migrations/000020_respect_supplied_match_ended_at.up.sql` | `db-trigger` | `authoritative` | — |

**Verification**

- unit: `internal/schedule/internal/repository/constraints_test.go::TestMatchConstraints`
- e2e: `internal/e2e/matches_test.go::TestResolveMatchHandler`

#### R-SCHEDULE-009 — Odds are a decimal above one, recorded to two places

`implemented` · `invariant` · authority `database`

A match's home and away odds are both strictly greater than one — a book never offers a stake back for nothing — and are recorded to at most two decimal places, the precision the bonus arithmetic is derived from.

**Authority** `database` · mechanism `constraint` · scope `row-local`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `migrations/000001_create_initial_game_models.up.sql` | `db-constraint` | `authoritative` | — |
| `migrations/000007_alter_matches_odds_precision.up.sql` | `db-constraint` | `authoritative` | — |
| `internal/schedule/internal/model/match.go::validateMatchShape` | `validator` | `advisory-copy` | — |

**Refusal** `23514` → `matches_home_odds_check`, `matches_away_odds_check` → _(unmapped)_ → _(falls through to 500)_

**Verification**

- unit: `internal/schedule/internal/repository/constraints_test.go::TestMatchConstraints`
- unit: `internal/schedule/internal/model/matches_validation_test.go::TestValidateNewMatch`

**Notes**

> Two halves, one operand contract (ADR-021's scale chain: bonus_acc scale 4 = tokens 0 + odds 2 + streak-rate budget 2). The bound half is a real refusal (23514, app-mirrored, unmapped in the repository so a bypassing writer sees a 500); 000007 narrowed numeric(6,3) to numeric(4,2). stale-pointer: one finding against 000007's prose. (1) The migration says a 3-dp value is "unrepresentable, not rounded on arrival" — that is false: a numeric typmod coerces by *rounding*, so an odds of 1.234 arrives as 1.23 silently, and nothing (no CHECK on round(odds,2)) refuses it. The rule as the database actually enforces it is "at most two places are *stored*", not "a third place is refused"; a CHECK would be needed to make the stated claim true. The create path therefore echoes the stored odds, not the requested ones: MatchRepository.Insert's RETURNING carries home_odds/away_odds and the 201 reports the row's rounded value (1.234 in, 1.23 out — the pattern 000020 set for ended_at on the resolve path), so the discrepancy between request and row is visible in the response instead of hidden behind an in-memory copy. Third, unremarked: precision 4 caps odds at 99.99, and a larger value surfaces as 22003 (numeric overflow), which is unmapped -> 500.

#### R-SCHEDULE-010 — A team never plays itself

`implemented` · `invariant` · authority `database`

The two sides of a match are two different teams. A calendar entry with the same club on both sides is not a fixture and cannot be stored.

**Authority** `database` · mechanism `constraint` · scope `row-local`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `migrations/000001_create_initial_game_models.up.sql` | `db-constraint` | `authoritative` | — |
| `internal/schedule/internal/model/match.go::validateMatchShape` | `validator` | `advisory-copy` | — |

**Refusal** `23514` → `matches_teams_differ_check` → _(unmapped)_ → _(falls through to 500)_

**Verification**

- unit: `internal/schedule/internal/repository/constraints_test.go::TestMatchConstraints`

#### R-SCHEDULE-011 — A match belongs to a round of its own season

`implemented` · `invariant` · authority `database`

A match's season and its round are the same season: the round it names must itself belong to the season it names, and both must exist. A calendar can therefore never hold a fixture that sits in one season while pointing at another season's round.

**Authority** `database` · mechanism `composite-fk` · scope `cross-table`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `migrations/000001_create_initial_game_models.up.sql` | `db-constraint` | `authoritative` | — |
| `internal/schedule/internal/handler/matches.go::createMatch` | `handler` | `advisory-copy` | — |

**Refusal** `23503` → `matches_season_id_round_id_fkey` → _(unmapped)_ → _(falls through to 500)_

**Verification**

- unit: `internal/schedule/internal/repository/constraints_test.go::TestMatchConstraints`
- e2e: `internal/e2e/matches_test.go::TestCreateMatchHandler`

#### R-SCHEDULE-020 — A match may only be resolved after it has ended

`implemented` · `policy` · authority `service`

A result is accepted only with an end time that lies in the past. Resolving a match with an end that has not happened yet is refused before anything is written, so no result can be recorded from the future.

**Authority** `service` · scope `row-local`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/schedule/internal/service/resolve_match.go::Service.ResolveMatch` | `service` | `authoritative` | — |
| `internal/schedule/internal/handler/matches.go::resolveMatch` | `handler` | `advisory-copy` | — |

**Refusal** `synthetic` → _(unnamed)_ → `ErrEndedAtInFuture` → `422`

**Verification**

- e2e: `internal/e2e/matches_test.go::TestResolveMatchHandler`

#### R-SCHEDULE-021 — A match is resolved once, against the version the caller holds

`implemented` · `orchestration` · authority `repository`

A result may only be written for a match that is in progress, has already started, has no end yet, whose round is still open, and whose version is the one the caller was shown. A second resolution, a stale caller or a closed round is refused and changes nothing.

**Authority** `repository` · scope `row-local`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/schedule/internal/repository/match_repo.go::MatchRepository.ResolveTx` | `repository` | `authoritative` | — |
| `internal/schedule/internal/repository/match_repo.go::MatchRepository.classifyResolveRefusal` | `repository` | `advisory-copy` | — |
| `internal/schedule/internal/service/resolve_match.go::Service.ResolveMatch` | `service` | `defense-in-depth` | — |

**Refusal** `synthetic` → _(unnamed)_ → `ErrEditConflict` → `409`

**Verification**

- unit: `internal/game/internal/core/service/rides_concurrency_test.go::TestResolveMatchRaces`
- e2e: `internal/e2e/matches_test.go::TestResolveMatchHandler`
- e2e: `internal/e2e/matches_test.go::TestResolveMatchSettlesRowOnce`

**Knowledge ref** `docs/league-tokens-learning-phases/phase-04-concurrency-and-idempotency.md`

**Notes**

> Authority deviates from the orchestration default (service) because the predicate needs the write instant: it is spelled as the UPDATE's own WHERE (status = 'in_progress' AND starts_at < now() AND ended_at IS NULL AND version = $n AND EXISTS(round still open)), so a blocked write re-evaluates on lock release and 0 rows is the refusal — path-dependent by definition, with no call-set fixture, so a new resolve statement that forgot the guard would pass every test. The service site is the transaction boundary and the ordering (time policy first, then the guarded write) and is listed as defense-in-depth. Tie: the service site fails loudly — a wrong boundary loses the serialization the race test asserts, so the refused write surfaces there first. Zero rows is ambiguous by construction (unknown match, stale version, wrong status, closed round), so classifyResolveRefusal reads beside the transaction and names the cause: absent row -> ErrRecordNotFound (404), everything else -> ErrEditConflict (409). The classification is advisory in the strict sense — the refusal already happened, the read only chooses the status code. stale-pointer: ResolveMatch's comment still says the payload's winner "is re-checked ... through the rides_state_gate", but 000017 removed that cross-context check from the trigger; the re-check is now game's resultAgreement in Go (R-GAME-010). The guarded UPDATE's RETURNING now carries ended_at, so ResolveTx returns the committed row rather than a pre-seeded request copy; the whole 200 body (and the post-commit MatchResultRecorded) is the row's values, which makes future trigger drift visible at the wire.

#### R-SCHEDULE-024 — Deleting a season or a round takes its matches with it

`implemented` · `invariant` · authority `database`

Removing a season removes its rounds and their matches; removing a round removes its matches. Calendar children never outlive their parent, so no match can point at a round or season that is gone.

**Authority** `database` · mechanism `constraint` · scope `cross-table`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `migrations/000001_create_initial_game_models.up.sql` | `db-constraint` | `authoritative` | — |

**Verification**

- unit: `internal/schedule/internal/repository/constraints_test.go::TestMatchConstraints`
- e2e: `internal/e2e/rounds_test.go::TestDeleteOpenRoundCascadesMatches`
- e2e: `internal/e2e/seasons_test.go::TestDeleteCreatedSeasonHandler`

### Entity `round`

#### R-SCHEDULE-012 — A schedule record never changes parents

`implemented` · `invariant` · authority `repository`

A round stays in the season it was created in, and a match stays in the season and round it was created in. Re-parenting is not an edit; it is a different record.

**Authority** `repository` · scope `cross-table`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/schedule/internal/repository/round_repo.go::RoundRepository.Update` | `repository` | `authoritative` | — |
| `internal/schedule/internal/repository/match_repo.go::MatchRepository.Update` | `repository` | `authoritative` | — |
| `migrations/000001_create_initial_game_models.up.sql` | `db-constraint` | `defense-in-depth` | — |

**Refusal** `23503` → `matches_season_id_round_id_fkey` → _(unmapped)_ → _(falls through to 500)_

**Verification**

- e2e: `internal/e2e/rounds_test.go::TestUpdateRoundHandler`
- e2e: `internal/e2e/matches_test.go::TestUpdateMatchHandler`

**Notes**

> Authority deviates from the invariant default (database) on purpose: the guarantee is the *absence* of season_id/round_id from the two UPDATE statements plus the absence of the fields from the update DTOs — a path-dependent guarantee, not a predicate any constraint can state (a CHECK cannot see "unchanged", and the DTO is the wire half). The database is a partial backstop only: re-parenting a round that already owns matches violates the composite FK, but a round with no matches can be re-parented in SQL and no CHECK notices. Tie: the DB copy fails loudly — a repository path that started writing season_id would trip matches_season_id_round_id_fkey (23503) for any round with matches. Coverage gap: the two e2e pointers prove the field is rejected on the wire (`{"season_id":2}` is a 400 unknown key) but nothing pins the SQL omission itself — there is no call-set fixture, so a new update statement that included the parent would fail no test. stale-pointer: RoundRepository.Update's own comment states the wrong reason for the omission (it says re-parenting would break the composite FK for every match "in it"; that is the DB backstop, not the reason the column is absent).

#### R-SCHEDULE-013 — A round's number is one to thirty-eight

`implemented` · `invariant` · authority `database`

A round is numbered within the season's fixture list: at least one, at most thirty-eight — ten matches a round, twenty teams home and away. There is no representable round zero or round thirty-nine.

**Authority** `database` · mechanism `constraint` · scope `row-local`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `migrations/000001_create_initial_game_models.up.sql` | `db-constraint` | `authoritative` | — |
| `internal/schedule/internal/model/round.go::ValidateRound` | `validator` | `advisory-copy` | — |

**Refusal** `23514` → `rounds_number_check` → _(unmapped)_ → _(falls through to 500)_

**Verification**

- unit: `internal/schedule/internal/repository/constraints_test.go::TestRoundAndSeasonConstraints`
- e2e: `internal/e2e/rounds_test.go::TestUpdateRoundHandler`
- e2e: `internal/e2e/rounds_test.go::TestCreateRoundHandler`

#### R-SCHEDULE-014 — A season has one round per number

`implemented` · `invariant` · authority `database`

Within a season each round number is used once. Two rounds may share a number only if they belong to different seasons, so a season's fixture list has no duplicates.

**Authority** `database` · mechanism `constraint` · scope `cross-row`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `migrations/000001_create_initial_game_models.up.sql` | `db-constraint` | `authoritative` | — |
| `internal/schedule/internal/repository/round_repo.go::RoundRepository.Insert` | `repository` | `advisory-copy` | — |
| `internal/schedule/internal/repository/round_repo.go::RoundRepository.updateRound` | `repository` | `advisory-copy` | — |

**Refusal** `23505` → `rounds_season_id_number_key` → `ErrDuplicateRecord` → `409`

**Verification**

- unit: `internal/schedule/internal/repository/constraints_test.go::TestRoundAndSeasonConstraints`
- e2e: `internal/e2e/rounds_test.go::TestCreateRoundDuplicateNumber`
- e2e: `internal/e2e/rounds_test.go::TestUpdateRoundHandler`

#### R-SCHEDULE-015 — A round only goes live under a live season with an unbroken sequence

`implemented` · `invariant` · authority `database`

A round may only become open or closed while its season is open or in progress, and only when every earlier round of that season is already closed: the highest closed round must be exactly the predecessor. The season can never be one round ahead of its own fixture list, which is what makes the last-round flip sound.

**Authority** `database` · mechanism `trigger` · scope `cross-row`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `migrations/000008_add_round_lifecycle_gates.up.sql` | `db-trigger` | `authoritative` | — |
| `migrations/000019_add_status_rank_gates.up.sql` | `db-trigger` | `authoritative` | — |

**Refusal** `P0001` → `round_progress_season_not_live`, `round_sequence_incomplete` → `ErrRecordInUse` → `409`

**Verification**

- unit: `internal/schedule/internal/service/rounds_close_test.go::TestRoundClose_MidTxFailureLeavesNoTrace`
- e2e: `internal/e2e/rounds_test.go::TestUpdateRoundHandler`

**Notes**

> rounds_progress_gate fires on INSERT beyond created, on every forward transition (created -> open/closed, open -> closed) and on any renumbering of a *live or closed* round (OLD.number IS DISTINCT FROM NEW.number, inside the NEW.status IN ('open','closed') branch), which is what stops a writer from renaming its way around the order; a created round renumbers freely, subject only to the per-season unique number. Both halves read sibling rows (seasons, sibling rounds), so a trigger. Two migration sites, because the function's current body is 000019's: 000008 created it, and 000019 gave the same function R-SCHEDULE-003's rank half — round_status_regression — checked before this gate's own two, so the rank message wins when both could fire (that name belongs to R-SCHEDULE-003's refusal list, not this one's). Two findings. (1) Verification gap: the season-live half is pinned by the close test, the *sequence* half has no direct test anywhere — the two schedule suites instead disable the trigger to build fixtures ("rounds_progress_gate requires an unbroken closed sequence, which no realistic fixture builds"), so the rule's central half is unpinned and the escape hatch is the only evidence it exists. (2) Refusal UX defect: every round-update refusal maps to ErrRecordInUse and the handler answers the *frozen* message ("a match has already started, so the hierarchy cannot move to an earlier stage"). A round going live under a created or closed season trips round_progress_season_not_live, and a close over unclosed matches trips rounds_close_gate, both with no started match anywhere, and both still get that message; those causes are indistinguishable to a client. misalignment-report §6 item 5 (docs/spec/history/misalignment-report.md) is therefore a false premise: it claims a created round in a created season cannot be renumbered, but the gate's renumber branch requires NEW.status IN ('open','closed'), so such a renumber is allowed and only the per-season unique number can refuse it (§6 item 4 repeats the same example). stale-pointer: round_repo.go::updateRound's IsTriggerViolation comment names only rounds_freeze_gate as the cause of the P0001 it maps to ErrRecordInUse, but the same SQLSTATE also comes from rounds_progress_gate (000008) and rounds_close_gate; the mapping is by SQLSTATE, not by raise name, so the comment's list of causes is one short (and the handler's frozen-hierarchy message is what makes the gap visible).

#### R-SCHEDULE-016 — A round closes only when every match in it is closed

`implemented` · `invariant` · authority `database`

A round may not be declared finished while any of its matches is still scheduled, running or postponed. Closing is the statement that the round's schedule is history, so every fixture in it must already have a result.

**Authority** `database` · mechanism `trigger` · scope `cross-row`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `migrations/000008_add_round_lifecycle_gates.up.sql` | `db-trigger` | `authoritative` | — |
| `migrations/000014_relax_rounds_close_gate.up.sql` | `db-trigger` | `authoritative` | — |

**Refusal** `P0001` → `round_close_has_unclosed_matches` → `ErrRecordInUse` → `409`

**Verification**

- unit: `internal/schedule/internal/service/rounds_close_test.go::TestRoundClose`
- e2e: `internal/e2e/rounds_test.go::TestUpdateRoundHandler`

#### R-SCHEDULE-017 — Opening a round starts its season

`implemented` · `orchestration` · authority `service`

The moment a round is opened, its season starts running: the round's flip from created to open and the season's flip to in progress commit together or not at all. A live round therefore never sits under a season that has not started.

**Authority** `service` · scope `cross-table`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/schedule/internal/service/round_lifecycle.go::RoundService.UpdateRound` | `service` | `authoritative` | — |
| `internal/schedule/internal/repository/round_repo.go::RoundRepository.Open` | `repository` | `defense-in-depth` | — |

**Refusal** `P0001` → _(unnamed)_ → `ErrRecordInUse` → `409`

**Verification**

- e2e: `internal/e2e/rounds_test.go::TestOpenRoundStartsSeason`
- e2e: `internal/e2e/rounds_test.go::TestOpenRoundSeasonFlipIsIdempotent`

#### R-SCHEDULE-019 — A round close announces its results and settles nothing itself

`implemented` · `orchestration` · authority `service`

Finishing a round commits the round and then announces the round's complete result set to the rest of the system. The close itself writes no ride and no player: settling what was riding on the round is the game context's own, later transaction, so undecided rides never block a close and a closed round may still carry them.

**Authority** `service` · scope `cross-table`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/schedule/internal/service/round_lifecycle.go::RoundService.Close` | `service` | `authoritative` | — |
| `internal/schedule/internal/repository/match_repo.go::MatchRepository.ResultsForRound` | `repository` | `defense-in-depth` | — |

**Verification**

- unit: `internal/schedule/internal/service/rounds_close_test.go::TestRoundClose`

#### R-SCHEDULE-022 — A round's gameplay phase follows its match times

`implemented` · `policy` · authority `domain`

A round is in its action phase until one hour before its first match starts, in its match phase from then until one hour after its last match ends, and in its decision phase afterwards. A round with no ended match yet can never be in the decision phase, so the decision window cannot open on a match that is still running.

**Authority** `domain` · scope `cross-table`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/schedule/internal/model/round_phase.go::Phase` | `domain` | `authoritative` | — |

**Verification**

- unit: `internal/schedule/internal/model/round_phase_test.go::TestPhase`

#### R-SCHEDULE-023 — A season or round that has started is durable

`implemented` · `invariant` · authority `database`

A round that is closed, or any round with a match that has already started, cannot be deleted; neither can a season that is in progress, closed, or has a started match. Deleting would take the history with it, so the calendar's past is not removable.

**Authority** `database` · mechanism `trigger` · scope `cross-row`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `migrations/000001_create_initial_game_models.up.sql` | `db-trigger` | `authoritative` | — |

**Refusal** `P0001` → `round_lifecycle_blocks_delete`, `season_lifecycle_blocks_delete` → `ErrRecordInUse` → `409`

**Verification**

- unit: `internal/schedule/internal/repository/gates_test.go::TestRoundDeleteGateRejectsMatchHistory`
- unit: `internal/schedule/internal/repository/gates_test.go::TestSeasonDeleteGateRejectsMatchHistory`
- e2e: `internal/e2e/rounds_test.go::TestDeleteRoundHandler`
- e2e: `internal/e2e/seasons_test.go::TestDeleteSeasonHandler`
- e2e: `internal/e2e/seasons_test.go::TestDeleteSeasonRefusalKeepsSeasonPlayers`

### Entity `season`

#### R-SCHEDULE-001 — A new schedule record is born in its created state

`implemented` · `interface` · authority `handler`

A season, a round and a match always come into existence in their created state. The client cannot choose it — the create request carries no status — so no organiser can plant a live or finished season, round or match with a single call.

**Authority** `handler` · scope `row-local`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/schedule/internal/handler/seasons.go::createSeason` | `handler` | `authoritative` | — |
| `internal/schedule/internal/handler/rounds.go::createRound` | `handler` | `authoritative` | — |
| `internal/schedule/internal/handler/matches.go::createMatch` | `handler` | `authoritative` | — |
| `internal/schedule/internal/model/season.go::ValidateNewSeason` | `validator` | `defense-in-depth` | — |
| `internal/schedule/internal/model/round.go::ValidateNewRound` | `validator` | `defense-in-depth` | — |
| `internal/schedule/internal/model/match.go::ValidateNewMatch` | `validator` | `defense-in-depth` | — |

**Refusal** `synthetic` → _(unnamed)_ → _(unmapped)_ → `422`

**Verification**

- unit: `internal/schedule/internal/model/matches_validation_test.go::TestValidateNewMatch`
- e2e: `internal/e2e/seasons_test.go::TestCreateSeasonHandler`
- e2e: `internal/e2e/rounds_test.go::TestCreateRoundHandler`
- e2e: `internal/e2e/matches_test.go::TestCreateMatchHandler`

#### R-SCHEDULE-003 — A schedule lifecycle never moves backwards

`implemented` · `policy` · authority `database`

A season, a round and a match only ever advance through their vocabularies: created, then open (or in progress), then closed. A status may not return to an earlier stage, a same-status rewrite is allowed, and a closed match is final — it never changes state again, even back to in_progress.

**Authority** `database` · mechanism `trigger` · scope `row-local`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `migrations/000019_add_status_rank_gates.up.sql` | `db-trigger` | `authoritative` | — |
| `internal/schedule/internal/model/status.go::checkStatusRegression` | `domain` | `advisory-copy` | — |
| `internal/schedule/internal/model/season.go::ValidateSeasonUpdate` | `validator` | `advisory-copy` | — |
| `internal/schedule/internal/model/round.go::ValidateRoundUpdate` | `validator` | `advisory-copy` | — |
| `internal/schedule/internal/model/match.go::ValidateMatchUpdate` | `validator` | `advisory-copy` | — |

**Refusal** `P0001` → `match_status_regression`, `round_status_regression`, `season_status_regression` → `ErrRecordInUse` → `409`

**Verification**

- unit: `internal/schedule/internal/model/lifecycle_test.go::TestValidateSeasonUpdate`
- unit: `internal/schedule/internal/model/lifecycle_test.go::TestValidateRoundUpdate`
- unit: `internal/schedule/internal/model/matches_validation_test.go::TestValidateMatchUpdate`
- unit: `internal/schedule/internal/repository/constraints_test.go::TestStatusRankGatesMirrorGoRanks`
- unit: `internal/schedule/internal/repository/gates_test.go::TestRoundFreezeGate`
- unit: `internal/schedule/internal/repository/gates_test.go::TestSeasonFreezeGate`
- e2e: `internal/e2e/rounds_test.go::TestUpdateRoundHandler`
- e2e: `internal/e2e/matches_test.go::TestUpdateMatchHandler`

**Knowledge ref** `docs/league-tokens-learning-phases/phase-02-domain-model-and-state-machines.md`

**Notes**

> One rule, three vocabularies: each entity owns a rank map and the shared statusRegresses verdict rejects a lower rank (disjoint cases, one statement; the generic signature makes a cross-vocabulary comparison a compile error). The rank maps are why a same-status rewrite passes. Since migration 000019 the database is the authoritative home: the three gate functions mirror the Go ranks and refuse every regression with the three P0001 names (match_status_regression / round_status_regression / season_status_regression), including a closed match moved back to in_progress, a closed round with nothing started moved back to open, and a live season moved back a stage — all reachable by a direct writer before. The rank check runs BEFORE the started-freeze rule, so a row tripping both reports the rank message deterministically. The Go rank maps stay the authority the DB copies must not drift from: the model's three exported StatusRegresses functions are the oracle and the drift matrix (TestStatusRankGatesMirrorGoRanks) probes every ordered status pair, asserting the exact rank message on regressions and acceptance elsewhere (fixtures isolate the rank rule from the freeze / season-live / sequence gates). The advisory validators still run first, so the API path keeps its friendly 422 and the DB's 409 is reachable only by writers that skip them. stale-pointer: migration 000008's header ("Regressions ... are NOT gated here — they belong to rounds_freeze_gate, which already refuses them") overstates the gate — resolved by 000019: it refused regressions only once a match of the round had started, and the rank half now lives in the same function's 000019 body, so the 000008 header is historical, not current; 000008 is applied and stays byte-identical.

#### R-SCHEDULE-004 — The status vocabularies are closed sets shared by code and database

`implemented` · `invariant` · authority `database`

Every season, round and match status is one of the values the league recognises, and the database accepts exactly the set the code knows. A status word nobody defined is unrepresentable, so no reader ever has to guess at an unknown state.

**Authority** `database` · mechanism `constraint` · scope `row-local`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `migrations/000001_create_initial_game_models.up.sql` | `db-constraint` | `authoritative` | — |
| `internal/schedule/internal/model/season.go::ValidateSeasonStatus` | `validator` | `advisory-copy` | — |
| `internal/schedule/internal/model/round.go::ValidateRoundStatus` | `validator` | `advisory-copy` | — |
| `internal/schedule/internal/model/match.go::ValidateMatchStatus` | `validator` | `advisory-copy` | — |

**Refusal** `23514` → `seasons_status_check`, `rounds_status_check`, `matches_status_check` → _(unmapped)_ → _(falls through to 500)_

**Verification**

- unit: `internal/schedule/internal/repository/constraints_test.go::TestStatusVocabularyIsInSync`
- unit: `internal/schedule/internal/model/matches_validation_test.go::TestValidateNewMatch`
- e2e: `internal/e2e/rounds_test.go::TestUpdateRoundHandler`

#### R-SCHEDULE-018 — Closing the season's last round closes the season

`implemented` · `orchestration` · authority `repository`

Closing round thirty-eight ends the season in the same transaction. That one transition is the season's own last step, so a completed fixture list and a finished season can never disagree.

**Authority** `repository` · scope `cross-table`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/schedule/internal/repository/round_repo.go::RoundRepository.Close` | `repository` | `authoritative` | — |

**Refusal** `P0001` → _(unnamed)_ → `ErrRecordInUse` → `409`

**Verification**

- unit: `internal/schedule/internal/service/rounds_close_test.go::TestRoundClose`

#### R-SCHEDULE-027 — Deleting a season also deletes its players

`implemented` · `orchestration` · authority `service`

Removing a season removes its players first, through the game context, and only then the season row. The result is the old cascade's: the season's players and its calendar go together, and a ride still hanging on one of those players still refuses the whole delete.

**Authority** `service` · scope `cross-table`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/schedule/internal/service/delete_policy.go::Service.DeleteSeason` | `service` | `authoritative` | — |

**Refusal** `23503` → `rides_player_id_fkey` → `ErrRecordInUse` → `409`

**Verification**

- e2e: `internal/e2e/seasons_test.go::TestDeleteSeasonHandler`

#### R-SCHEDULE-028 — An update only lands on the version the caller was shown

`implemented` · `orchestration` · authority `repository`

Every edit is conditional on the version the client last saw. If the record moved in between, the write is refused as a conflict instead of silently overwriting the other editor's change.

**Authority** `repository` · scope `row-local`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/schedule/internal/repository/season_repo.go::SeasonRepository.Update` | `repository` | `authoritative` | — |
| `internal/schedule/internal/repository/round_repo.go::RoundRepository.updateRound` | `repository` | `authoritative` | — |
| `internal/schedule/internal/repository/match_repo.go::MatchRepository.Update` | `repository` | `authoritative` | — |
| `internal/schedule/internal/repository/team_repo.go::TeamRepository.Update` | `repository` | `authoritative` | — |
| `internal/schedule/internal/handler/rounds.go::updateRound` | `handler` | `advisory-copy` | — |
| `internal/schedule/internal/handler/matches.go::updateMatch` | `handler` | `advisory-copy` | — |

**Refusal** `synthetic` → _(unnamed)_ → `ErrEditConflict` → `409`

**Verification**

- e2e: `internal/e2e/rounds_test.go::TestUpdateRoundHandler`
- e2e: `internal/e2e/matches_test.go::TestUpdateMatchHandler`
- e2e: `internal/e2e/teams_test.go::TestUpdateTeamHandler`
- e2e: `internal/e2e/seasons_test.go::TestUpdateSeasonHandler`

**Knowledge ref** `docs/league-tokens-learning-phases/phase-04-concurrency-and-idempotency.md`

### Entity `team`

#### R-SCHEDULE-025 — A team kept in the calendar by its matches cannot be deleted

`implemented` · `invariant` · authority `database`

A team that any match refers to cannot be removed. Match history is durable, so the calendar's teams are protected by the fixtures that name them, not by a check the application remembers to run.

**Authority** `database` · mechanism `constraint` · scope `cross-table`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `migrations/000001_create_initial_game_models.up.sql` | `db-constraint` | `authoritative` | — |
| `internal/schedule/internal/repository/team_repo.go::TeamRepository.Delete` | `repository` | `advisory-copy` | — |

**Refusal** `23001` → `matches_home_team_id_fkey`, `matches_away_team_id_fkey` → `ErrRecordInUse` → `409`

**Verification**

- unit: `internal/schedule/internal/repository/constraints_test.go::TestTeamDeleteRestricted`
- e2e: `internal/e2e/teams_test.go::TestDeleteTeamHandler`
- e2e: `internal/e2e/teams_test.go::TestDeleteUnreferencedTeamHandler`

**Notes**

> ON DELETE RESTRICT (not NO ACTION) on both sides of matches, with the intent spelled out in 000001: "we don't want to delete matches history if we delete a team". The repository's copy maps 23001/23503 to ErrRecordInUse so the handler can answer 409 instead of a raw FK 500 — advisory, the FK refuses either way. Scope note: after the context split this is only the *schedule* half of "a team still in use"; the players and rides references moved to the composed delete policies (R-SCHEDULE-026). stale-pointer: TeamRepository.Delete's comment still says "teams is referenced with ON DELETE RESTRICT by matches and players" — the players half was dropped by migration 000016 (cross-context FKs), so only matches remain; the comment's rule is right, its list is one item too long.

#### R-SCHEDULE-026 — A calendar record the game context still references is not deleted

`implemented` · `orchestration` · authority `service`

The calendar may not delete a team, a match or a season that the game context still points at: before deleting it asks game through schedule's own port, and refuses with the in-use conflict when any ride, player favourite or registration still references the record. For a season the question is asked of its registrations too, so a registration that still carries rides keeps the whole delete refused.

**Authority** `service` · scope `cross-table`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/schedule/internal/service/delete_policy.go::Service.DeleteTeam` | `service` | `authoritative` | — |
| `internal/schedule/internal/service/delete_policy.go::Service.DeleteMatch` | `service` | `authoritative` | — |
| `internal/schedule/internal/service/delete_policy.go::Service.DeleteSeason` | `service` | `authoritative` | — |
| `cmd/api/gamerefs.go::gameRefs` | `service` | `authoritative` | — |
| `internal/game/internal/adapter/outbound/postgres/ride_repo.go::RideRepository.ExistsForMatch` | `repository` | `defense-in-depth` | — |
| `internal/game/internal/adapter/outbound/postgres/ride_repo.go::RideRepository.ExistsForTeam` | `repository` | `defense-in-depth` | — |
| `internal/game/internal/adapter/outbound/postgres/player_repo.go::PlayerRepository.DeleteForSeasonTx` | `repository` | `defense-in-depth` | — |
| `migrations/000016_drop_cross_context_foreign_keys.up.sql` | `db-constraint` | `defense-in-depth` | — |
| `migrations/000021_add_game_delete_players_for_season.up.sql` | `db-constraint` | `defense-in-depth` | — |

**Refusal** `synthetic` → _(unnamed)_ → `ErrRecordInUse` → `409`

**Verification**

- unit: `internal/schedule/internal/service/rounds_close_test.go::TestMatchDeleteRestricted`
- e2e: `internal/e2e/matches_test.go::TestDeleteMatchHandler`
- e2e: `internal/e2e/teams_test.go::TestDeleteTeamHandler`
- e2e: `internal/e2e/seasons_test.go::TestDeleteSeasonHandler`
- e2e: `internal/e2e/seasons_test.go::TestDeleteSeasonRefusalKeepsSeasonPlayers`

**Notes**

> Merged rule (Phase B3): the calendar sweep (provisional R-SCHEDULE-026) and the game sweep (provisional R-GAME-027) found one boundary fact from its two ends, so it is one rule with the union of sites — module schedule, because the side that performs the delete owns the delete policy, and game's slots are freed for dense renumbering. The decision is per record (team: player favourites and rides; match: rides; season: its registrations and their rides) and the order is the record's own: team and match ask game, then delete; a season deletes its registrations through game first and the season row second, both inside schedule's one transaction (000021's definer function + SeasonRepository.DeleteTx), which is why the authority is the use case. The question is asked through GameUsage, a consumer-side port declared in schedule's service package so that game -> schedule stays the only legal import direction; the composition root adapts game's facade to it (cmd/api/gamerefs.go::gameRefs) and translates game's "still referenced" sentinel into schedule's ErrRecordInUse, so the handlers only ever see one vocabulary — that translation is the fourth authoritative site. Category orchestration rather than the game sweep's boundary: the port's answers are facts and the refusal is still the use case's, so the merged rule keeps one authority layer instead of splitting into a wiring rule plus a policy rule (no composition-root authority is claimed here; the wiring itself is R-PLATFORM-013's). Refusal surface: both refusals are Go-only (no SQLSTATE — the cross-context FKs that used to produce them were dropped by 000016) and the same 409 the FKs used to give; the one surviving same-context FK, rides_player_id_fkey RESTRICT, raises 23503 on the season path and is translated there. Two recorded gaps: (1) the answer is a check-then-delete, so a ride locked or a player created between the read and the write slips through — accepted, and exactly the window the old delete-then-FK flow had; (2) an unwired port is a 500 (errGameRefsNotWired), not a refusal. The game-side EXISTS queries and DeleteForSeasonTx are facts, not decisions (Tie: they cannot drift into a wrong answer without failing the calendar's own delete test), and the composed check cannot silently disappear because the port is required at wiring time. TestMatchDeleteRestricted is the deliberate opposite assertion — it proves the store no longer refuses, pinning the boundary that this policy is the only thing holding the rule. The season half is pinned end to end since Phase G4: TestDeleteSeasonRefusalKeepsSeasonPlayers plants a ride on one registration (this rule's game-side refusal) and a started match (R-SCHEDULE-023's gate), and asserts the 409 and that the registrations survive — the same one transaction rolls both refusals back, so a refused season delete cannot leave the season alive with its players already gone. The row-outcome half of the season path (the players really go with the season, and a refused delete rolls them back) stays R-SCHEDULE-027; the durability gates stay R-SCHEDULE-023 and the same-context team half R-SCHEDULE-025. stale-pointer: 000016's header still describes the replacement as "the api layer (api holds the identity store)" and "the api season-delete handler" / "PlayerStore"; internal/api no longer exists — the delete policies are internal/schedule/internal/service/delete_policy.go, the adapter is cmd/api/gamerefs.go and the store is game's own repository.

#### R-SCHEDULE-029 — A team carries a name, a badge and a description

`implemented` · `invariant` · authority `database`

A team always has a name, a badge and a description, each non-empty, with the name and the description bounded in length. A half-described team is not a representable record.

**Authority** `database` · mechanism `constraint` · scope `row-local`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `migrations/000001_create_initial_game_models.up.sql` | `db-constraint` | `authoritative` | — |
| `internal/schedule/internal/model/team.go::ValidateTeam` | `validator` | `advisory-copy` | — |

**Refusal** `23514` → `teams_name_check`, `teams_logo_check`, `teams_description_check` → _(unmapped)_ → _(falls through to 500)_

**Verification**

- unit: `internal/schedule/internal/repository/constraints_test.go::TestTeamConstraints`
- e2e: `internal/e2e/teams_test.go::TestCreateTeamHandler`

#### R-SCHEDULE-030 — A team badge is an image link

`implemented` · `interface` · authority `domain`

A team's badge is a web address ending in png, jpg, jpeg, svg or webp. A calendar cannot carry a badge nobody can display, so anything else is refused when the team is created or edited.

**Authority** `domain` · scope `row-local`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/schedule/internal/model/team.go::ValidateTeam` | `validator` | `authoritative` | — |
| `internal/schedule/internal/model/team.go::validImageURL` | `validator` | `authoritative` | — |

**Refusal** `synthetic` → _(unnamed)_ → _(unmapped)_ → `422`

**Verification**

- e2e: `internal/e2e/teams_test.go::TestCreateTeamHandler`

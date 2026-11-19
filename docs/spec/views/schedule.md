<!-- GENERATED — do not edit. -->
# Business rules — `schedule`

> **GENERATED — do not edit.** This is the `schedule` module's slice of the readable
> catalog view. The canonical, hand-edited catalog is `docs/spec/rules.yaml`.
>
> Change the YAML, never this file, then regenerate from the dashboard:

```sh
go run ./cmd/spec gen --rules docs/spec/rules.yaml --out docs/spec/business-rules.md
```

The rules themselves live in `../features/schedule/`; the format authority is
`rules.schema.md` and the checker is `go run ./cmd/spec check`.

**30 rules** — 30 implemented.

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

> rounds_progress_gate fires on INSERT beyond created, on every forward transition (created -> open/closed, open -> closed) and on any renumbering of a *live or closed* round (OLD.number IS DISTINCT FROM NEW.number, inside the NEW.status IN ('open','closed') branch), which is what stops a writer from renaming its way around the order; a created round renumbers freely, subject only to the per-season unique number. Both halves read sibling rows (seasons, sibling rounds), so a trigger. Two migration sites, because the function's current body is 000019's: 000008 created it, and 000019 gave the same function R-SCHEDULE-003's rank half — round_status_regression — checked before this gate's own two, so the rank message wins when both could fire (that name belongs to R-SCHEDULE-003's refusal list, not this one's). Two findings. (1) Verification gap: the season-live half is pinned by the close test, the *sequence* half has no direct test anywhere — the two schedule suites instead disable the trigger to build fixtures ("rounds_progress_gate requires an unbroken closed sequence, which no realistic fixture builds"), so the rule's central half is unpinned and the escape hatch is the only evidence it exists. (2) Refusal UX defect: every round-update refusal maps to ErrRecordInUse and the handler answers the *frozen* message ("a match has already started, so the hierarchy cannot move to an earlier stage"). A round going live under a created or closed season trips round_progress_season_not_live, and a close over unclosed matches trips rounds_close_gate, both with no started match anywhere, and both still get that message; those causes are indistinguishable to a client. misalignment-report §6 item 5 is therefore a false premise: it claims a created round in a created season cannot be renumbered, but the gate's renumber branch requires NEW.status IN ('open','closed'), so such a renumber is allowed and only the per-season unique number can refuse it (§6 item 4 repeats the same example). stale-pointer: round_repo.go::updateRound's IsTriggerViolation comment names only rounds_freeze_gate as the cause of the P0001 it maps to ErrRecordInUse, but the same SQLSTATE also comes from rounds_progress_gate (000008) and rounds_close_gate; the mapping is by SQLSTATE, not by raise name, so the comment's list of causes is one short (and the handler's frozen-hierarchy message is what makes the gap visible).

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

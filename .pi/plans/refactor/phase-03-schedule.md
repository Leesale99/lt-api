# Phase 3 — schedule module, layered (model/repository/service/handler + facade)

Workers read first: `docs/designs/go-modular-monolith-example.md` §1 (catalog module) + §2. Style: layered — `internal/schedule/internal/{model,repository,service,handler}`.

**CONSTRAINT — game must keep compiling unchanged after this phase.** The game-facing surface (today `internal/schedule/port.go` methods on `*Store`, satisfying `game.SchedulePort` structurally) keeps identical method signatures: same schedule-owned types, same tx params, same persistence sentinel errors — exposed via the facade. game's restructure + error-contract flip happens in phase 4.

## Mappings (source → target)
- `store.go` (aggregate: Pool, Seasons, Rounds, Teams, Matches, NewStore, attach) → **DIES**. Replaced by:
  - facade `internal/schedule/schedule.go`: `type Module struct` (service + repositories + game-facing surface), `NewModule(pool *pgxpool.Pool, bus *eventbus.Bus, log *slog.Logger) *Module`, `RegisterRoutes`, capability interfaces + DTOs for cmd and other modules.
  - the `attach(bus, log)` pattern dies: bus/log become constructor params.
- `matches.go` (599 lines) splits:
  - `model/match.go`: Odds, Score, MatchStatus + consts, Match, `(Match).Winner`, ValidateMatchStatus/NewMatch/ResolveMatch/MatchUpdate, validateMatchShape, matchStatusRank. **No json tags** — the handler DTO replicates them verbatim (including `json:"_"` on EndedAt, `matches.go:43`, with a `// frozen wire format` comment).
  - `model/errors.go`: schedule sentinels (ErrNotFound etc.) — schedule-internal use for now.
  - `repository/match_repo.go`: MatchStore → `MatchRepository` (same methods: Get, Insert, GetAll, Delete, Update, PhaseForMatch, NextForTeam, ResolveTx, ResultsForRound). SQL unchanged. `resultsForRound`'s WinnerTeamID derivation in the scan loop (matches.go:555–599) moves to model (one authoritative copy); the repo maps rows → model results. `classifyResolveRefusal` stays with the repo (error enrichment).
  - `repository/game_facing.go` (from `port.go`): PhaseForMatch, NextForTeam, RoundOpenTx, DestinationOpenTx, Match, Team — SQL unchanged, exposed via the facade. Error contract UNCHANGED this phase.
- `rounds.go` (402 lines) splits:
  - `model/round.go`: RoundStatus + consts, Round, Validate* — no json tags (DTO replicates).
  - `repository/round_repo.go`: RoundRepository (Get, Insert, Update, Open, Close, GetAll, Delete). **CRITICAL:** RoundStore.Close currently publishes RoundCompleted post-commit (rounds.go:284–304) and holds bus+log — the repository must NOT publish. Close returns the closed-round data needed for the event; the SERVICE publishes RoundCompleted post-commit with identical payload/timing. `logCloseGap` moves to the service. `roundsPerSeason`, `roundUpdateSQL`, `seasonOpenToInProgressSQL`, `seasonInProgressToClosedSQL`, `updateRound` move with the repo.
  - `service/round_lifecycle.go`: the use cases `updateRoundHandler` currently dispatches (api/rounds.go:141–249): `UpdateRound(ctx, …)` decides created→Open / open→Close / plain Update — the lifecycle transition dispatch moves OUT of the handler. The X-Expected-Version header read stays in the handler; the version value passes into the service call (preserve the semantics of api/rounds.go:161–166).
- `seasons.go` (226) → `model/season.go` + `repository/season_repo.go` (SeasonRepository, same methods, SQL unchanged).
- `teams.go` (214) → `model/team.go` (Team, ValidateTeam, validImageURL) + `repository/team_repo.go` (TeamRepository).
- `round_phase.go` → `model/round_phase.go` (pure derivation, unchanged).
- `lifecycle.go` (checkStatusRegression) → `model/status.go`.
- `events.go` → `model/events.go` (MatchResult, MatchResultRecorded, RoundCompleted, NewEventID — fat payloads unchanged; events are part of schedule's public contract).
- `service.go` (114) → `service/resolve_match.go`: ErrEndedAtInFuture, ResolveMatch + publish. The service no longer embeds *Store; it takes the repositories ResolveMatch needs, the bus, and an injected clock if it reads time (same behavior). Add the thin CRUD use-case methods the handlers need (api handlers today call `app.Schedule.Seasons/Rounds/Teams/Matches.*` directly on ~20 sites) — thin pass-throughs delegating to repositories are fine; **handlers depend on the service from now on, never on repositories**.
- `service/delete_policy.go` (NEW, from api handlers): DeleteTeam (api/teams.go:172–235: Players.ExistsForTeam + Rides.ExistsForTeam → refuse), DeleteMatch (api/matches.go:199–255: Rides.ExistsForMatch → refuse), DeleteSeason (api/seasons.go:188–241: Players.DeleteForSeason then Seasons.Delete). Cross-context calls go through a consumer-side interface DEFINED HERE (e.g. `type GameUsage interface{…}` with exactly the capabilities used: PlayersExistForTeam, RidesExistForTeam, RidesExistForMatch, DeletePlayersForSeason). Until game's facade exists (phase 4), satisfy it in cmd/api or the api package with a small adapter over the existing game store methods — behavior-identical, INCLUDING the non-transactional cascade and its comment.
- handler package (from internal/api): `handler/seasons.go`, `handler/rounds.go`, `handler/matches.go`, `handler/teams.go` (policy → service, dispatch → service); `handler/dto.go` — request/response DTOs, json tags copied VERBATIM from today's domain structs; `handler/errors.go` — schedule-flavored mappers from api/errors.go; `handler/routes.go` — schedule's route table, paths/methods/middleware identical to baseline. internal/api drops these handlers + its schedule routes + schedule mappers; cmd/api/main.go wires `schedule.NewModule` + RegisterRoutes (hot files — see 00-plan contention rule).
- Update the arch matrix for the new schedule packages (model→validator; repository→persistence/eventbus; service→model/repository/eventbus; handler→httpx/service; facade→own internals + shared).

## Tests
- `lifecycle_test.go`, `matches_validation_test.go`, `matches_winner_test.go`, `round_phase_test.go` → model package.
- `constraints_test.go`, `gates_test.go`, `rounds_store_test.go` + the `testmain_test.go` harness (TestMain, pools, seed, cleanup) → repository package (same DB requirements, same DSN env).
- api handler tests for seasons/rounds/matches/teams stay in api for now; they move to the shared/server E2E suite in phase 6.

## Done when
Invariants hold; internal/schedule contains ONLY the facade + `internal/**` (arch enforces); the game package is untouched and green; schedule's HTTP endpoints serve from the module with identical wire behavior; no `Store` identifier remains under internal/schedule; commits `refactor(3): …` (suggest: model / repository / service / handler+wiring).

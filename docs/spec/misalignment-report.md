# Business-rule catalog — misalignment report (Phase B3)

Merged from `/tmp/lt-spec-fragments-{schedule,game,identity,kernel-platform,expert-docs}.yaml`
(the five extraction sweeps) and `/tmp/lt-spec-docmap.yaml` (the phase-doc coverage map),
against `docs/spec/rules.yaml`'s 12 seed rules and the frozen schema
`docs/spec/rules.schema.md`.

Catalog after the merge: **127 rules** — 108 `implemented`, 19 `specified-only`,
0 `drifted`; **125 after gate B5's two retirements** (106 `implemented`, 19
`specified-only`, 0 `drifted` — no rule changed status). Twelve are the untouched seeds;
the other 115 come from the fragments, with four fragment rules merged away at B3 (see the
merge notes below the counts) and two more retired at gate B5, and the doc-sweep rules
renumbered into each module's dense sequence.

| module | implemented | specified-only | total |
|---|---|---|---|
| schedule | 30 | 0 | 30 |
| game | 25 | 7 | 32 |
| identity | 22 | 0 | 22 |
| ledger | 0 | 7 | 7 |
| kernel | 14 | 3 | 17 |
| platform | 15 | 2 | 17 |
| **total** | **106** | **19** | **125** |

(game's two implemented rules `R-GAME-019` and `R-GAME-022` were retired at gate B5 into
`R-KERNEL-003` and `R-KERNEL-014` — see the last two merge notes; no other count moved.)

Renumbering and dedupe (the decisions this report's counts depend on):

- **R-SCHEDULE-026 ← R-SCHEDULE-026 + R-GAME-027** (one calendar-delete boundary fact,
  two sweeps). Kept in the schedule slot; module schedule because the deleted side owns
  the delete policy; union of sites; `ErrRecordInUse` → 409. **Freed: R-GAME-027.**
- **R-KERNEL-002 ← R-KERNEL-002 + the game request-identity fragment** (the request-digest
  contract, instantiated at the ride edge). Module kernel; union of sites; game's
  instantiation site is cited in the notes. **Freed: a game slot** (the dense renumbering
  below reused it, so no id is cited here).
- **R-KERNEL-009 folded into R-IDENTITY-016 and R-IDENTITY-017** (a conjunction of two
  independently falsifiable facts: the activation branch and the exact-code branch). The
  identity rules kept their ids and gained kernel 009's distinctive sites
  (`errors.go::AuthenticationRequired`, `context.go::Permissions.Include`,
  `errors.go::MissingPermission`) and its `TestRequirePermissionMiddleware` pointer.
  **Freed: R-KERNEL-009.**
- **R-IDENTITY-021 folded into R-PLATFORM-013** (the facade-only-access rule is the same
  composition-root decision seen from identity's edge). **Freed: R-IDENTITY-021.**
- Dense renumbering after the frees: game `020..026 → 019..025`, `028..029 → 026..027`;
  identity `022..023 → 021..022`; kernel `010..015 → 009..014`. Doc-sweep rules land after
  the implemented ones: **R-GAME-201..207 → R-GAME-028..034**, **R-KERNEL-201..203 →
  R-KERNEL-015..017**, **R-PLATFORM-201..202 → R-PLATFORM-016..017** (dense: the frees
  moved these three slots one lower than the brief's nominal 030/016/016).
- Kept apart on purpose, cross-referenced in their notes: R-GAME-016 (per-ride CAS,
  repository) vs R-KERNEL-014 (per-event claim, service); R-GAME-018 (claim-first ordering
  at the command) vs R-KERNEL-004 (claim commits with the command); R-KERNEL-010 (CORS
  matching) vs R-PLATFORM-005 (allowlist parsing); R-LEDGER-006 (money-operation
  idempotency, unbuilt) vs the implemented HTTP/event idempotency rules; R-GAME-025
  (re-point only on continuation) vs seed R-GAME-003 (terminal freeze); R-GAME-026
  (publisher-language ACL) vs R-PLATFORM-013 (facade/direction).
- Two further near-duplicate pairs were **merged at gate B5** (approved dupe policy: one
  statement per rule, N enforcement sites — the kernel owns the generic mechanism, and the
  module call-sites become enforcement sites on the kernel rule): R-GAME-019 ↔
  R-KERNEL-003 (same `Repository.Claim` site, same 409, same tests) and R-GAME-022 ↔
  R-KERNEL-014 (same `runEvent` sites, same tests). Kernel keeps the statement — `003` is
  the claim/replay contract every future context inherits, `014` the event-claim — the
  game sites and notes moved into it, and both game ids were retired: their entries are
  removed from the catalog and recorded in `rules.yaml`'s header note, and the ids are
  never reused. At B3 the two pairs had been kept apart pending this gate; the reviewer
  took the merge (C-pilot confirmed).

---

## 1. Content drift — 0 rules

**Headline: 0 `status: drifted`.** Every fragment's content was re-resolved against this
tree, and no rule states something the code contradicts: where the docs claim something
the code does not do, the rule is `specified-only` (§3), and where the code moved away from
a doc's *pointer* (a file, a symbol, a migration's prose) the rule stays `implemented`
with a `stale-pointer:` note (§2 — gate A2 / D2: rot is not drift).

One debatable borderline, recorded here so gate B5 sees it:

- **R-SCHEDULE-012 — "a schedule record never changes parents"** (`authority: repository`,
  `scope: cross-table`). The rule holds only as a *path-dependent* guarantee: `season_id`
  is absent from the two UPDATE statements and from the update DTOs. Nothing pins the call
  set, so a new update statement that wrote the parent would fail no test (the composite FK
  refuses it only for a round that already owns matches, and a round with no matches can be
  re-parented in SQL unnoticed). Per the copy policy this is a defensible
  `implemented` + "Coverage gap" note rather than `drifted` — the authority is genuinely the
  repository, and the DB is a real backstop for the case that carries history — but it is
  the one rule where a missing fixture is the *only* thing between the catalog and a
  second silent copy. Recommendation: a call-set fixture before the rule is called held.
  **Gate B5 confirmed `implemented` under D2:** no expert document states this rule at all
  (nothing under `docs/league-tokens-learning-phases/` mentions parents or re-parenting),
  so there is no doc-vs-code content disagreement — only rot and a coverage gap. It is in
  §4's no-doc list and §5's e2e-only list, and both stay the finding.

---

## 2. Doc rot / stale pointers — 14 rules, 14 `stale-pointer:` notes, plus 3 findings that belong in this report only

**Headline: 14 rules carry a `stale-pointer:` note — 14 notes in total, one per rule**
(2 of them seeds). `grep -c 'stale-pointer:' docs/spec/rules.yaml` returns **14**; a bare
`grep -c 'stale-pointer'` returns **15** because R-GAME-005's note names the convention a
second time in its own prose ("this `stale-pointer` note surfaces in the Phase B report")
— a mention, not a second note. And 3 further rot findings cannot be recorded in the rule
a reader would look in (one is a frozen seed, one is a doc-internal inconsistency, one is
already covered by fact rather than complaint).
Applied migrations are immutable, so in every case the fix is the catalog note plus a
comment at the current site — never a migration rewrite.

Catalogued `stale-pointer:` notes (rule → where → what it says → what is true today):

| rule | rot site | claims | today |
|---|---|---|---|
| R-GAME-005 (seed) | `000013`'s change-detector prose | domain copies at `internal/game/rides.go`, `rideAutoUnlockSQL` in `internal/game/rounds.go`, probe at `internal/game/constraints_test.go` | `internal/game/internal/core/domain/ride.go`, the copy that reaches the row is `internal/ledger/postgres.go::Repository.ReleaseBonus`, probe is `internal/game/internal/core/service/constraints_test.go` |
| R-GAME-010 (seed) | `000011`/`000013`/`000017` prose + `match.go` comment | `Match.Winner (internal/game/matches.go)`, the `CASE` in `RideStore.UpdateAllForMatchTx (internal/game/rides.go)`, "through the SchedulePort (Store.MatchResultTx)"; `match.go` points at `internal/game/score_state_rule_test.go` | `internal/schedule/internal/model/match.go::Match.Winner`, derivation is `settlement.go::settleRideFromResult` (no CASE in SQL), port method is `Schedule.Match`, fixture lives in `internal/game/internal/core/service/score_state_rule_test.go` |
| R-SCHEDULE-003 | `000008` header | "Regressions (closed → open, → created) are NOT gated here — they belong to rounds_freeze_gate, which already refuses them" | the freeze gate refuses a regression only once a match of the round has *started*; a closed round with no started match does regress (the repository's own gate test proves it), so the app rank check is the only closed-is-terminal guard |
| R-SCHEDULE-009 | `000007` header | a 3-dp odds value is "unrepresentable, not rounded on arrival" | a numeric typmod **rounds**: 1.234 is stored as 1.23 with no refusal; no CHECK tests `round(odds,2)`, and the create path never reads the stored value back |
| R-SCHEDULE-012 | `RoundRepository.Update` comment | `season_id` is omitted because re-parenting would break the composite FK "for every match in it" | that is the DB backstop, not the reason the column is absent (the reason is that re-parenting is not an edit); nothing pins the omission |
| R-SCHEDULE-015 | `round_repo.go::updateRound` | the `IsTriggerViolation` branch is commented as `rounds_freeze_gate` only | the same `P0001`/SQLSTATE also comes from `rounds_progress_gate` (000008) and `rounds_close_gate`; the mapping is by SQLSTATE, so the comment is one cause short — and the handler's "hierarchy cannot move to an earlier stage" message is what makes the gap visible |
| R-SCHEDULE-021 | `resolve_match.go::Service.ResolveMatch` comment | the payload's winner "is re-checked … through the rides_state_gate" | `000017` removed that cross-context check from the trigger; the re-check is game's `resultAgreement` in Go (R-GAME-010) |
| R-SCHEDULE-025 | `team_repo.go::TeamRepository.Delete` | "teams is referenced with ON DELETE RESTRICT by matches **and players**" | `000016` dropped `players_favorite_team_id_fkey`; only the match FKs remain — the rule is right, its list is one item too long |
| R-SCHEDULE-026 | `000016` header | "the api layer (api holds the identity store)", "the api season-delete handler", `PlayerStore.Update` | `internal/api` no longer exists: the delete policies are `internal/schedule/internal/service/delete_policy.go`, the adapter is `cmd/api/gamerefs.go::gameRefs`, the store is game's own repository |
| R-GAME-013 | `ride_repo.go::insertRide` + `player_repo.go::GetAll` | the birth INSERT's FK branch is "the referenced player, **team or match** does not exist"; `favorite_team_id` "is never 0 in the schema (FK)" | after `000016` the only reference left on that INSERT is `rides_player_id_fkey` (team/match existence is the port's check), and the `favorite_team_id` FK is gone (0 still works as the no-filter sentinel) |
| R-GAME-018 | phase-04 doc | "Later, for HTTP commands, add an idempotency key table" | `000010` landed; the doc's future step is now history (superseded sequencing, not disagreement) |
| R-GAME-024 | `DecisionGuardedTx` doc comment + `ride_repo_test.go` | "a destination deleted … surfaces as the FK 23503 -> ErrNotFound"; tests cite `classifyUpdateRefusal` | the inline comment in the same function says correctly that 000016 left no FK on rides; the classifiers are `classifyDecisionRefusal` / `classifyArrivalRefusal` / `classifyResolveRefusal` |
| R-GAME-027 | `000009` header | the `won_pending` partial index serves "the rounds_close_gate EXISTS backstop and the auto-unlock/auto-burn UPDATE … FROM" | `000014` dropped the close gate's undecided-rides check and the auto-unlock/auto-burn left the close transaction for game's settlement handler; the index still serves the sweep, only its prose is stale |
| R-PLATFORM-013 | phase-05 §"As built" | `cmd/server` is the composition root, `internal/idempotency` was hoisted, "**api** — the HTTP gateway, not a context" | the binary is `cmd/api`, the engine is `internal/kernel/idempotency`, and each module owns its transport (`internal/api` is gone) |

Rot that belongs in this report only:

1. **`internal/schedule/internal/model/match.go::Match.Winner`'s comment** — a dead-path
   crop: it names `internal/game/events.go` ("the event consumer of the Game/Schedule
   split"), "that same handler", `the SchedulePort`, and `internal/game/score_state_rule_test.go`
   as the shared fixture. Today the payload derivation is
   `internal/game/internal/core/service/settlement.go::Service.settleRideFromResult`, the
   enforcement is `resultAgreement` in the same file, and the fixture is
   `internal/game/internal/core/service/score_state_rule_test.go`. It cannot be recorded in
   the rule's own notes because that rule is the frozen seed **R-GAME-010** (whose notes
   already record the `score_state_rule_test.go` half).
2. **`000008`'s `rounds_close_gate` prose** — "no ride still in won_pending (undecided
   rides are auto-resolved by the round-close transaction before the flip)" and
   "transitively no locked stragglers once ResolveMatch lands in Phase 04". `000014`
   replaced the function and deliberately allows a close over undecided rides; R-SCHEDULE-016
   records the *fact* (two migration sites, latest body wins) without a rot complaint, and
   R-SCHEDULE-019 owns the replay half.
3. **phase-05 internal inconsistency** — §"Suggested ownership" (game owns base price,
   ledger owns balances) is contradicted by the doc's own §"As built" and by the code
   (schedule owns teams; there is no base-price column at all). R-GAME-030's notes carry
   the open design question; it is a doc-internal supersession, not code drift.

---

## 3. Specified-but-unimplemented — 19 rules (17 merged + 2 seed ledger rules)

**Headline: 19 `specified-only` rules, in 4 phase groups.** All carry `expert-ref`,
`enforcement: []`, no verification and `refusal: null` (none of the four docs names a
refusal surface; each rule's notes say so).

**phase-03 — transactions and atomic operations (4)**

| id | title | what is missing |
|---|---|---|
| R-GAME-028 | Taking the tokens and creating the ride are one operation | the ledger's `LockTokens` intent writes nothing and no wallet/token balance exists; only the ride half is real (and the doc's `Lock` ≠ the code's `/rides/:id/lock`) |
| R-GAME-029 | Burning a ride settles its money in the same transaction | only the state flip is real: `Burn` is a no-op, there is no TB award, currency return or token destruction |
| R-GAME-030 | Resolving a match updates the team's base price | `teams` has no base-price column, no update path, and `rides.base_at_lock` is a hard-coded `1.95` at the HTTP edge |
| R-GAME-034 | A registration is born with its starting currency | the registration half exists; there is no balance/currency table and no grant step anywhere |

**phase-06 — ledger and accounting (7)**

| id | title | what is missing |
|---|---|---|
| R-LEDGER-001 | No balance goes negative (seed) | no balance table at all; `LockTokens`/`Burn` are intent seams that persist nothing |
| R-LEDGER-002 | Every money movement posts a balanced journal pair (seed) | no `journal_entries` table and no paired-append path |
| R-LEDGER-003 | Only the ledger moves money | no ledger schema/role to refuse a foreign write; today the direction is inverted (the ledger's only SQL writes `rides.bonus_acc`) |
| R-LEDGER-004 | Every balance sits in an account of a known kind | no `accounts` table and no kind vocabulary |
| R-LEDGER-005 | A balance is the journal's projection | no balances, no journal, no projection step |
| R-LEDGER-006 | A money operation repeats without moving money twice | the ledger has no idempotency of its own; `AccrueBonus` is a blind increment and would apply twice on redelivery |
| R-LEDGER-007 | Buying tokens posts both sides of the purchase | no reserve, wallet, pool or currency table; `BuyFromReserve`/`TransferCurrency` exist only in doc prose |

**phase-07 — background workers (4)**

| id | title | what is missing |
|---|---|---|
| R-GAME-031 | A ride past its deadline is burned by the system | no worker package, no ticker, no decision-deadline column, no `ProcessDueAutoBurns` |
| R-GAME-032 | The auto-burn is safe to run twice | no auto-burn path to make idempotent (the ingredients — terminal freeze, kernel claim — exist but nothing calls them) |
| R-PLATFORM-016 | A failing background pass never kills the process | `server.Background` exists but no domain job registers on it |
| R-PLATFORM-017 | A background worker stops on cancellation and is waited for | same: the machinery is built for HTTP handlers, no worker runs on it |

**phase-08 — transactional outbox and events (4)**

| id | title | what is missing |
|---|---|---|
| R-KERNEL-015 | An event is committed with the change that caused it | no `outbox_events` table and no same-transaction append; delivery is post-commit and in-process |
| R-KERNEL-016 | A relay delivers committed events and marks them published | no relay/dispatcher and no `published_at` marker |
| R-KERNEL-017 | Delivery is at-least-once and never claimed exactly-once | the idempotent-consumer half is implemented (R-KERNEL-014); the delivery half is at-most-once, so "eventually delivered" cannot hold |
| R-GAME-033 | A burn is announced as a `ride.burned` event | no `ride.*` event type exists; game is a consumer only (schedule publishes `MatchResultRecorded`/`RoundCompleted`) |

---

## 4. Implemented rules with no expert-doc backing — 74 of 106

**Headline: 74 implemented rules are not stated by any phase document** (cross-checked
against every `covers` line in `/tmp/lt-spec-docmap.yaml`; a rule counts as doc-backed only
when a doc line states its fact — sharing a migration or a doc section does not count).
This is a finding about the docs, not an error in the catalog: the sweeps read the code.

Doc-backed implemented rules, for auditability (32): schedule `003, 004, 005, 015, 016,
019, 021` (phase-02 status vocabulary + the 000008/000014 gates; phase-03 atomic resolve;
phase-08 post-commit publish); game `001`–`010` (the seeds, each naming its phase-02/03/04/
05/06/08 source) plus `012`, `013` (phase-03 Register / reference checks), `016`
(phase-04 CAS), `018` (phase-03 idempotency), `023` (phase-05 settlement serialization),
`026` (phase-05 publisher language/ACL); kernel `003`, `004` (phase-04), `006` (phase-00
validation at the edge), `013`, `014` (phase-08 event seam + consumer claim); platform
`010` (phase-00 graceful shutdown), `011` (phase-07 process lifecycle), `012`, `013`
(phase-05 boundaries). The two game rules retired at gate B5 were doc-backed too: the
ride/replay statement (phase-03/04 idempotency) now lives in R-KERNEL-003 and the
settlement event claim (phase-08) in R-KERNEL-014, whose own phase-08 `expert-ref` now
carries it.

No-doc rules, grouped by module:

**schedule — 23**: R-SCHEDULE-001, 002, 006, 007, 008, 009, 010, 011, 012, 013, 014, 017,
018, 020, 022, 023, 024, 025, 026, 027, 028, 029, 030.

**game — 9**: R-GAME-011, 014, 015, 017, 020, 021, 024, 025, 027.

**identity — 22**: R-IDENTITY-001 … R-IDENTITY-022 (every identity rule; phase-00 mentions
authentication only as a deferred item, and phase-05 calls identity "the generic supporting
context" without stating any of its rules).

**kernel — 9**: R-KERNEL-001, 002, 005, 007, 008, 009, 010, 011, 012.

**platform — 11**: R-PLATFORM-001, 002, 003, 004, 005, 006, 007, 008, 009, 014, 015.

`expert-ref` is populated only where a phase document *names* the rule (a seed id or a
precise section); coverage that merely implies a rule — phase-04's idempotency section
implies kernel `003`/`004`, phase-00's "validate before the use case" implies kernel `006` —
is recorded in this section instead, so the field stays a claim about the doc, not about
this report.

Reading of the pattern: the docs describe *what the product does* with money, rides and
lifecycles, and almost nothing about *framing and hardening* (config, mail, logging,
server timeouts, CORS, rate limiting, body caps, panic recovery, credential parsing) or
*identity*. Identity is the largest single gap — 22 rules, zero doc statements — and it is
also the module whose rules the docs treat as out of scope ("Do not build yet:
authentication" in phase-00, later crossed). Either identity gets a phase document, or the
catalog has to be accepted as the only specification of that context.

---

## 5. Verification gaps — 9 rules with no pointer at all; 24 e2e-only; 12 pointers that do not test the assertion

**Headline: 33 of 106 implemented rules have no unit test; 9 of those have no verification
pointer of any kind.** All 9 stay `implemented` per the brief (a coverage gap is not
disagreement); they are the §3-policy-3 violations the schema self-check reports.

**(a) No verification pointer at all — 9**

| rule | subject | why unpinned |
|---|---|---|
| R-IDENTITY-007 | activation TTL 3 d / auth TTL 1 d | every test seeds its own `time.Hour`/`-time.Minute`; the two constants are never read by a test, so 3 days → 3 hours would stay green |
| R-IDENTITY-012 | registration role = `user`, client cannot choose | no test reads `role_id` back; pointing the sub-select at `admin` would stay green |
| R-KERNEL-005 | DB budget 3 s pays for storage, not hashing | no test asserts that bcrypt runs outside the window |
| R-KERNEL-011 | a panic is answered as a generic 500 | nothing exercises `RecoverPanic` anywhere (no recover, no panic fixture, no `Connection: close` assertion) |
| R-PLATFORM-006 | mailer refuses an incomplete configuration | `platform:mailer` has no test file; every suite substitutes a no-op mailer |
| R-PLATFORM-007 | bounded retry with jitter, cancellation stops it | no test — ladder, jitter and cancellation are unexercised |
| R-PLATFORM-009 | server read/write/idle timeouts | no test asserts any of the three; the platform server suite covers shutdown only |
| R-PLATFORM-011 | background work may fail without killing the process | the only background test asserts the wait budget, not survival of a panicking task |
| R-PLATFORM-015 | each pool speaks as its context's role (`SET ROLE`) | no test exercises the hook — the boundary suite connects as the roles directly, so a dropped `SET ROLE` keeps the whole suite green while production runs as the owner |

**(b) E2E-only (no unit test) — 24**

R-SCHEDULE-012, 017, 020, 027, 028, 030 · R-GAME-011, 012, 014, 017, 021 ·
R-IDENTITY-004, 013, 014, 015, 016, 017, 019 · R-KERNEL-006, 007, 008, 010, 012 ·
R-PLATFORM-014.

**Count check (gate B5).** (a) and (b) are disjoint, so **33 = 9 + 24 distinct rules** and
the headline double-counts nothing. Verified against `rules.yaml`: **R-IDENTITY-007 carries
`verification: {}`** — no unit *and* no e2e pointer — so it is in (a) only and is **not**
e2e-only. It is the one rule this section names twice, and only because (c) item 2 cites it
as a rule that *looks* covered; that is a cross-reference, not a second count.

**(c) Pointers that exist but do not exercise the rule's core assertion — 12 findings
(11 rules plus one cross-reference)**

1. **R-IDENTITY-018** (a failed login is one refusal): the only unit pointer is
   `TestPasswordHashRoundTrip`, which pins `password.Matches` — there is **no login test at
   all** (no service test, no e2e for the authentication-token endpoint), so neither half of
   the credentials-equivalence is pinned end to end.
2. **R-IDENTITY-007**: covered in (a) — cited here because the rule *looks* covered.
3. **R-SCHEDULE-015** (unbroken round sequence): the season-live half is pinned by the close
   test, the sequence half by nothing — both schedule suites disable `rounds_progress_gate`
   to build fixtures, so the central half's only evidence is the escape hatch.
4. **R-SCHEDULE-001 / R-SCHEDULE-030** (validators without tests): `ValidateNewSeason`,
   `ValidateNewRound`, `ValidateTeam` and `validImageURL` have no unit test; only their match
   counterpart is pinned, and the badge rule's update path is unpinned entirely.
5. **R-SCHEDULE-026**: `TestMatchDeleteRestricted` asserts the **opposite** of what its name
   suggests — it proves the store no longer refuses, which is the right pin for a
   boundary rule but reads as coverage of the refusal it does not test.
6. **R-GAME-004** (seed): no test probes the four `rides` CHECKs directly; `TestValidateRide`
   pins only the app copy, and the `23514` is unmapped. Deferred at gate A2 (D6).
7. **R-GAME-014**: only the e2e duplicate probe; no direct-SQL constraint probe like
   `TestRideMoneyStateCoupling`.
8. **R-GAME-016 / R-GAME-024**: no unit test calls `ArriveTx`/`DecisionGuardedTx` with a
   stale pre-state directly — the CAS is inferred from the settlement races.
9. **R-KERNEL-001 / R-KERNEL-002**: no test ties the request-digest formula (method + URI +
   body) or the 255-byte key bound to the handler; the replay cases use tokens built by hand,
   so dropping the URI from the hash would replay instead of conflicting.
10. **R-KERNEL-006 / R-KERNEL-007**: the kernel has no `validator_test.go` and no
    `filters_test.go` — the collect-not-fail-fast rule, the 1..100 page-size bounds and the
    panicking `SortColumn` are pinned only through two e2e list/create suites.
11. **R-PLATFORM-014**: the e2e harness (`internal/e2e/compose_test.go::newTestApplication`)
    rebuilds the same chain as `cmd/api/routes.go`; nothing ties the copy to the original, so
    a chain-order change in one leaves the suite green while production differs.
12. **R-SCHEDULE-012 / R-SCHEDULE-028 / R-GAME-016** (repository CAS and call-set rules):
    every pointer is e2e, and none is a call-set fixture — a new guarded statement that
    forgot its `WHERE` would pass the suite.

---

## 6. Defect candidates

Each is a real behaviour a reviewer can check in the tree; severity is a first guess
(H = wrong money/history or missing authorization, M = wrong refusal or leak, L = hygiene).

1. **`ended_at` is discarded on the resolve path — H.** `MatchRepository.ResolveTx`
   (`internal/schedule/internal/repository/match_repo.go`) writes `ended_at = $3`, then
   `matches_set_ended_at_on_close` (`migrations/000005_add_matches_ended_at.up.sql`)
   overwrites it with `now()` on the same statement. Consequences: (a) the returned row (and
   so the HTTP 200) echoes the request's value while the row holds `now()`;
   (b) `MatchResultRecorded.EndedAt` carries the request's value into settlement;
   (c) `TestResolveMatchHandler` asserts the echoed `2020-01-01T12:00:00Z`, locking the
   discrepancy in; (d) the time policy R-SCHEDULE-020 validates a value that is then thrown
   away. Not drift (the rule states the stamp, not the echo) — recorded in R-SCHEDULE-008's
   notes. **Fixed** in Phase G (e657011): the trigger keeps a supplied `ended_at` on the
   transition into closed (migration 000020) and `ResolveTx` returns the committed row's
   value via `RETURNING`, so the 200 and `MatchResultRecorded` echo what the row holds.
2. **Unmapped SQLSTATEs surface as 500 — M, several paths.** Schedule: `23514` from any of
   the match/team/round/season CHECKs, `22003` from an odds value over 99.99, and `23503` at
   match INSERT (the update path maps it, the insert does not) all fall through to
   `ServerError`. Game: `23514` from the four `rides` CHECKs (seed **R-GAME-004**, already
   catalogued) and a `player` DELETE blocked by `rides_player_id_fkey` —
   `player_repo.go::Delete` has no `IsDeleteRestricted` branch (unlike
   `DeleteForSeason`), so a player with rides answers 500 instead of 409. Identity: a
   missing `user` role row makes `UserRepository.Insert`'s sub-select yield NULL and 000004
   refuse with `23502` → 500 on registration. Boundary: `42501` (insufficient privilege) is
   unmapped everywhere, so a boundary violation that ever reached a handler is a generic 500.
3. **Odds round-trip echo — L/M.** `MatchRepository`'s create INSERT returns only
   `id, created_at, ended_at, status, version`, so the 201 echoes the unrounded request odds
   while the row holds `numeric(4,2)`'s rounded value (1.234 → row 1.23, response 1.234).
   Related: `numeric(4,2)` caps odds at 99.99; anything larger is `22003` → 500 (item 2).
4. **Refusal-message conflation — M.** `ErrRecordInUse` carries three unrelated causes on the
   calendar: a referential block (team in use), a durability verdict (started history cannot
   be deleted) and a phase/sequence refusal (round gating), and the handlers render one
   message. A renumber of a created round in a created season trips
   `round_progress_season_not_live` with no started match anywhere and still reads "a match
   has already started, so the hierarchy cannot move to an earlier stage" (R-SCHEDULE-015
   and R-SCHEDULE-023 notes).
5. **Created-round renumber refused — M.** `rounds_progress_gate` fires on
   `OLD.number IS DISTINCT FROM NEW.number` regardless of status, so renumbering a *created*
   round in a *created* season is refused (as `ErrRecordInUse` → the frozen message above):
   the gate implements "cannot rename your way around the sequence" as "cannot rename at
   all", which is stricter than the stated rule.
6. **Season delete is not all-or-nothing — H/M.** `Service.DeleteSeason`
   (`internal/schedule/internal/service/delete_policy.go`) asks game to delete the season's
   players *before* the schedule delete, and only then can the delete gate
   (R-SCHEDULE-023, the `seasons_delete_gate` `BEFORE DELETE` trigger) refuse. A season that
   is refused therefore loses its registrations while it survives: the old single-statement
   cascade was atomic, the composition is not. Nothing narrows the window: there is no
   pre-check — the gate is a trigger that fires on the season `DELETE` itself, so the window
   is the whole delete path (the season's players are deleted, then the delete can still be
   refused, losing registrations while the season survives). The code comment at
   internal/schedule/internal/service/delete_policy.go makes the same wrong "narrows the
   window" claim; the app code is deliberately untouched (spec-work constraint) — noted
   here so the next app-side edit fixes both. **Fixed** in Phase G (b837e69): game's player
   delete for a season is a SECURITY DEFINER function (migration 000021) called on
   schedule's own transaction, so a refused season delete rolls the registrations back.
7. **Closed match/round are DB-regressible — H.** The freeze gates refuse only
   created/postponed returns on a *started* match, and `rounds_progress_gate` explicitly
   does not handle regressions. A direct writer (psql, a backfill, a new repository method)
   can move a closed match back to `in_progress` or a closed round with no started matches
   back to `open`. The advisory rank validator is the only guard, so the "closed is final"
   half of R-SCHEDULE-003 is a policy, not an invariant — the schema's own
   database-vs-repository test says that is not enough for history. **Fixed** in Phase G
   (c4459cc): migration 000019 gives the three gate functions the rank half, mirroring the
   Go rank maps and refusing every regression with the three `P0001` rank names.
8. **Login timing asymmetry — M (enumeration).** `Service.Authenticate` returns before any
   bcrypt comparison when the email is unknown, so the two "invalid credentials" paths have
   measurably different latency; the equivalence R-IDENTITY-018 claims holds in the refusal,
   not in the work. A dummy comparison on the unknown-email path closes it.
9. **Activation endpoint is an account oracle — M (by design, needs a decision).** An unknown
   email at `RequestActivationToken` answers 404 while an already-active account answers 422
   — deliberate existence disclosure (R-IDENTITY-022's notes). Identity is therefore
   enumeration-resistant on login-in-spirit and not on activation; two endpoints, two
   policies.
10. **Ride routes are ungated — H (authorization).** No `/v1/rides*` route carries a
   permission code (R-IDENTITY-014's notes), and `RequirePermission` is where the activation
   check lives (R-IDENTITY-016), so an unactivated — or anonymous — caller reaches ride
   creates and commands. Combined with the next item, anonymous callers can spend a named
   player's tokens. **Fixed** in Phase G (876ca23): the six `/v1/rides*` routes are gated
   with `players:read` (the two reads) and `players:write` (the four commands).
11. **Bonus odds come from an unverified event payload — H/M.** Game verifies the
   *destination* against the committed result (`resultAgreement`) but takes the payout rate
   from `MatchResultRecorded`; the committed match snapshot game re-reads carries teams and
   scores but no odds (R-GAME-026's notes). A wrong payload rate is paid. **Fixed** in Phase
   G (e4ff4d9): `resultAgreement` verifies the payload's odds against the committed snapshot
   (`ride_result_odds_disagreement`) and the bonus is paid at the committed rate for the
   committed side.
12. **Player user-reference check is optional and mislabelled — M.** The only user-existence
   check lives in the handler, runs only when `h.users != nil`, and reports its failure on
   the `favorite_team_id` field (`internal/game/internal/adapter/inbound/http/players.go`).
   With the FK gone (`000016`), a module wired without identity silently plants an orphan
   `user_id`, and the client is told the wrong field.
13. **Idempotency keys: one global namespace, no retention — M.** The claim table's primary
   key is the bare key text (no player/route scoping) and nothing ever deletes a claimed row
   (`migrations/000010` says so). Two clients that pick the same key collide, and the table
   grows forever.
14. **Rate limiter collapses behind a proxy — M.** Buckets are keyed on `r.RemoteAddr` and
   live in memory: behind a load balancer every client shares the balancer's bucket, and
   limits are per process (R-KERNEL-009's notes state both).
15. **A body-less command ignores its body in the digest — L/M.** `rideCommandToken` hashes
   method + URI only, so appending a body to `POST /v1/rides/:id/burn` leaves the dedup
   identity unchanged; an identical retry with a different (ignored) body replays instead of
   conflicting. Harmless while those commands carry no body — a contract trap the day one
   does (R-KERNEL-002's notes).
16. **SMTP settings are under-validated — L.** The port has no bound (0 and negatives pass)
   and the sender is only checked for non-emptiness, so a malformed sender fails at the
   first send, not at boot (R-PLATFORM-003/006 notes).
17. **Config errors name the env var even for flag values — L.**
   `internal/platform/config/config.go::validate` always reports `PORT`,
   `LT_API_DB_MAX_CONNS`, … even when the bad value arrived as `-port`: the operator is told
   to change a variable they did not set (R-PLATFORM-002's notes).
18. **Typo on the 401 path — L.** `internal/kernel/httpx/errors.go::AuthenticationRequired`
   writes "you must be authenticated to **acces** this resource".
19. **The e2e chain duplicates the production chain — M (test fidelity).**
   `internal/e2e/compose_test.go::newTestApplication` rebuilds
   `RecoverPanic(EnableCORS(RateLimit(Authenticate(router))))` independently of
   `cmd/api/routes.go`; a reordering in one place leaves the suite green (R-PLATFORM-014's
   notes).
20. **`crypto/rand.Text` length is hard-coded — L.** `plaintextTokenLength = 26` is derived
   from `crypto/rand.Text`'s documented "at least 128 bits" (32 base32 chars, trimmed to
   26); a stdlib change to the alphabet or the guarantee would silently make the validator
   reject fresh tokens (R-IDENTITY-005's notes).
21. **Dead code — L.** `UserTokenRepository.DeleteAllForUser` (test-only),
   `UserRepository.Update` (no production caller; `UpdateTx` is the path),
   `Service.UserByEmail` (no caller) and `RideRepository.Update` (test-only) are unreachable
   from production; each is documented in its rule's notes but nothing fails when it rots.
22. **Already catalogued / seed-known:** R-GAME-004's `23514 → 500` gap (gate A2, D6), and the
   seed's two `stale-pointer` notes. Listed here so the two lists do not read as a
   contradiction.

---

## Schema gaps observed (three closed at gate B5, one still open)

The first three gaps below were closed at gate B5; the decisions and the schema wording live
in `docs/spec/rules.schema.md` §6 "Gate B5 resolutions". Kept here so each finding and its
resolution sit in one trail.

- **`enforcement[].layer` has no composition-root value.** Boundary and wiring sites are
  approximated as `handler` (the facade seam, the HTTP chain), `repository` (the SQL that
  carries a boundary, e.g. `000018`), `service` (the wiring in `cmd/api`) or `db-constraint`
  (schema/role migrations). Three values are doing one job; a `composition-root` layer (or a
  rule-level "layer: n/a") would let these entries stop apologising in their notes.
  **Closed at B5: `composition-root` added to the layer enum** (cross-context wiring /
  facade binding — where modules are handed their collaborators; distinguished from
  `handler`'s transport framing), and `cmd/spec` validates the 8-value enum.
- **`refusal.code` in schema §2 is illustrative, not closed.** The list is
  `P0001 | 23514 | 23503 | 23001 | synthetic`, but the catalog truthfully records `23505`
  (unique violations, e.g. R-GAME-014, R-IDENTITY-011), `23502` (R-IDENTITY-012's own notes
  on a missing role row), `22003` (odds overflow, §6.2) and `42501` (the context-role
  boundary, R-IDENTITY-021 / R-PLATFORM-012 / R-PLATFORM-015). The reserved word is
  `synthetic` — not `null` — which leaves no honest value for "a real SQLSTATE the house
  table forgot". **Closed at B5: the list is documented as illustrative** — any 5-character
  SQLSTATE plus `synthetic` is valid; `cmd/spec` keeps warning (never erroring) on codes
  outside the illustrative set, which is the intended check (the 8 WARN lines above).
- **No `mechanism` value for seeded reference data.** R-IDENTITY-014 (two roles, eleven
  permission codes, twenty-two grants, inserted by an applied migration and immutable
  thereafter) is catalogued as `mechanism: constraint` because what pins it is the keys plus
  migration immutability. A `seed`/`reference-data` mechanism value would say that directly.
  **Closed at B5: no new value** — seeded reference data stays `mechanism: constraint` (the
  keys refuse a conflicting seed, migration immutability keeps the seeded values true), now
  stated in the schema's mechanism section.
- **Kernel/platform rules with zero tests strain §3 policy 3.** Policy 3 demands at least
  one verification pointer for every `implemented` rule; nine rules have none, and the honest
  reading is that the policy is about *rules that can be verified today*. Either the policy
  grows an explicit `untested: true` escape (with the report as the gate), or the platform
  surface (mailer, timeouts, pool hook, panic recovery) needs tests before policy 3 can be
  claimed as passing. Today the self-check reports those nine as known violations, not as
  errors of the merge.

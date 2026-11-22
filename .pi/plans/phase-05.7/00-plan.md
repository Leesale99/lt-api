# Phase 05.7 — Spec + delegate meta-refactor

- **Status:** A + B complete — execution merged through `232c08f` (see Phase log below). C (docs) pending; expanded in phase-C.md.
- **Scope:** the meta-system (executable specification + delegate workflow + docs). The league-token application and Phase 06 domain work are out of scope.
- **Execution:** each phase runs as one background orchestrator job from this plan; per-phase files carry the task breakdown, acceptance gates, and HANDOFF mode.

## Why

Three goals, in order:
1. Simpler to maintain and extend — kill generation where the source is readable, deduplicate, delete history.
2. Clear architecture — bounded units with one facade each, owned communication formats. Inspiration: the app's module facades, kernel/platform split, and `internal/arch` enforcement.
3. After the refactor — intuitive developer documentation, high level first, then per-part depth.

**Thesis.** The meta-system has exactly one properly engineered contract: the approval constants (`contracts.ts` ↔ `WORKFLOW.md` front matter, equality-enforced by `policy.ts`, fail-closed). Every other seam is prose agreement — eight files carry protocol prose, one copy already stale, the profile table copied in three places with hidden role-name drift. The refactor makes every seam look like the approval contract: **one owner, one parser, fail-closed validation, one doc-of-record.**

**Three defects found during recon (fix, not just refactor):**
1. `check --rule X` filters findings by `Finding.RuleID`, but policy 13 (vocabulary) stores a scenario pointer there and annotation-reverse findings store junk → a worker's scoped gate can never fail on vocabulary or annotation-typo errors. Pinned as intended by `check_scope_test.go`. → fixed in B2.
2. "Which rules does this job own?" has two answers: the approval relay parses the task's `SPEC-SCOPE` line, the merge-path check reads `claims.jsonl` first → a job can be relay-approved yet merge-blocked. → fixed in B3.
3. An unparsable `SPEC-SCOPE:` line is silently ignored (fail-open); `CLAIMS-PATHS:` is documented in no `.md` file. → fixed in B3.

## Phase map

| Phase | Scope | File |
|---|---|---|
| A | Mechanical cleanups: cruft, history, WORKFLOW.md trim, main persona move | phase-A.md |
| B1 | Rules → one markdown file per rule; cmd/spec kernel; loader/scope/finding unification | phase-B1.md |
| B2 | Policy registry; scoped-check behavior fix; brief+queue → `spec work`; parse dedup | phase-B2.md |
| B3 | Extension decomposition: protocol.ts, mergegate.ts, thin root; WORKFLOW `gate_file_paths` | phase-B3.md |
| C | Developer documentation + vault design rewrites (only after B lands) | phase-C.md |

Order: A → B1 → B2 → B3 → C. B1 deliberately precedes Phase 06 so R-LEDGER-001..007 land in the new format.

## Decisions (agreed with the user, 2026-11-22)

1. Rules: one markdown file per rule with YAML front matter, `docs/spec/rules/<module>/<ID>.md`; thin generated dashboard only (`views/*.md` die). The rule statement stays in front matter — ADR-030 preserved.
2. `@known-defect` machinery deleted (tag family, `SPEC_KNOWN_DEFECTS=1`, announcer, docs). `SPEC_FEATURE_PATHS` machinery stays — prove depends on it.
3. WORKFLOW.md: front-matter comments, scanner/allowlist history, "Honest residuals", "Not built yet" cut; residuals → vault `Deferred/`; sandbox profile prose → pointer to `sbx.ts`.
4. Main persona: `.pi/delegate/orchestrator.md` → `.pi/agents/main.md` (the *main session*); mentor/orchestrator reconciled — explaining is conversation (stays with main), executing separable work is delegated; extension reads the file, inline fallback becomes a fail-loud stub.
5. `cmd/spec` stays in the same Go module — the checker ships with the checked; internal decomposition only.
6. App quick wins (`httpx`/ratelimit split, handler tests, composition-root sharing) deferred out of 05.7.
7. Vault-tracked as Phase 05.7; Phase 06 out of scope.
8. `vendor/` kept — empirically load-bearing (hermetic gate-VM/CI builds; `audit/static`'s tidy needs the warmed GOMODCACHE — complementary, not redundant). `bin/` is untracked/gitignored — no action.
9. Defect-1 fix approved as a behavior change: scoped runs keep vocabulary/annotation findings (subject-attributed); `check_scope_test.go` flips.
10. `brief` + `queue` merge into one `spec work` command; `queue --json` dropped (zero consumers; re-add if delegate automation needs packets).
11. Catalog field hygiene rides B1: `scope` → `write_scope` (kill the collision with run-scoping); `stale-pointer:` / `Tie:` notes markers promoted to real fields.
12. Receipts gain `schema_version`; `rulesHash` → `catalogHash` = manifest hash over `docs/spec/rules/**` (one noisy reconciliation day accepted).
13. `WORKFLOW.md` gains `gate_file_paths:` — ships with the `policy.ts` support in the same commit (unknown-field refusal is fail-closed; Phase A trims prose only, never fields).

## Constraints

- WORKFLOW.md front-matter FIELD changes always ship with their `policy.ts` support, in the same commit.
- Recon file:line references are as of baseline HEAD — workers re-verify before editing.
- Every phase exits `make audit/static` green; phases touching `cmd/spec`/`internal/spec` or the catalog run the full `make audit` where feasible (`make db/up` for the DSN).
- Phases touching extension code run `make smoke` (all three harnesses).
- Phase A deletes `.pi/plans/delegate/` and `.pi/plans/refactor/`; keep `.pi/plans/phase-05.7/`.

## Parked calls from Phase A (resolved at landing, 2026-11-22)

- **Blocked-report norm** ("a blocked, well-explained report beats a green-looking summary"): retained in WORKFLOW.md judgment rules (~L113). `main.md` stays lean and does not duplicate it.
- **Host-passthrough retention for future roles**: the doc-of-record is the code — `sbx.ts` comments (~L672/682: "NO role is passthrough today"). No WORKFLOW.md prose needed; consistent with the one-owner thesis.

## Phase log

| Phase | Merged | Head | Notes |
|---|---|---|---|
| A | 2026-11-22 | `8ec10a7` | 43 files, +266/−1780; WORKFLOW.md → 117 lines; persona → `.pi/agents/main.md`; parked calls resolved |
| B1 | 2026-11-22 | `34ce993` | 191 files; 126 per-rule files under `docs/spec/rules/`; round-trip 126/126; ERROR 0 pre/post; prove VERIFIED (R-SCHEDULE-003) |
| B2 | 2026-11-22 | `b49be02` | policy registry (contiguous, refusal=17); defect 1 closed (scoped runs report catalog/feature findings); `spec work` live |
| B3 | 2026-11-22 | `232c08f` | 27 files; root `index.ts` 4,278 → 340; protocol.ts (defect 3 closed) + mergegate.ts (defect 2 closed); `gate_file_paths` live; roster: `worker` (was `go-worker`) |

All three recon defects are closed. Receipts are schema v2 (catalogHash); v1 receipts superseded — expected reconciliation noise, do not "fix".

## Session restart completed; branch strategy added (2026-11-22, user decision)

The session was restarted after B3 (new extension code live). The whole 05.7 effort now ships as **one PR**: branch `workflow/05.7-meta-refactor` carries A+B (and later C) at the original SHAs; `main` was reset to the pre-effort baseline `8a4e35e` (back in sync with `origin/main`). After Phase C lands on the branch: push + open the PR. Phase C's merge-back target is the current checkout branch — never `main`.

The earlier note (kept for the record): a session started before B3 runs the old extension code in memory, while the checkout's WORKFLOW.md declares `gate_file_paths` — the old `policy.ts` refuses to run gates (fail-closed, exactly as designed). That is why the restart was required before Phase C.

## Follow-ups flagged at B1 landing (2026-11-22, merged at `34ce993`)

- `docs/league-tokens-learning-phases/phase-06-ledger-and-accounting.md:171` still names `rules.yaml` — update when Phase 06 starts so R-LEDGER-001..007 land in the per-file format guidance.
- Cosmetic carryover into B2: `brief.go` labels the write-scope row `| scope |` — fix during the `spec work` rewrite.
- Expected one-time noise (decision 12): next reconciliation pass reports today's v1 receipts as superseded. Do not "fix" it.

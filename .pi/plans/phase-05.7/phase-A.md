# Phase A — mechanical cleanups

HANDOFF=confirm. SPEC-SCOPE: none for every task (no catalog rules owned; the one catalog touch in A2 — a note sentence — falls back to the full check).

Phase acceptance: `make audit` green (A2 touches catalog prose and `internal/spec`), `make smoke` green (A4 touches `index.ts`), and `grep -ri 'known-defect\|docs/spec/history\|bashscan\|gate-allowlist'` clean across tracked files (excluding `.pi/plans/phase-05.7/**`).

## A1 — delete historical artifacts

- Delete `.pi/plans/delegate/` (11 files, incl. `workflow-s1-proposal/`) and `.pi/plans/refactor/` (8 files) — keep `.pi/plans/phase-05.7/`.
- Delete `scratch/` (edit-confirm probe files).
- Trim `.pi/sandboxes/README.md` ~L494–702: keep build/usage instructions; cut the v2-kit-relative narrative ("Behavior deltas vs. the v2 kit", "Evidence", benchmark-log prose). Local-only: `rm -rf .pi/sandboxes/dist/` (gitignored build logs).
- Verify no tracked file references the deleted paths (`grep -r '\.pi/plans/delegate\|\.pi/plans/refactor\|scratch/probe'` over tracked files).

## A2 — purge history references and dead @known-defect machinery

- `internal/spec/`: delete `knowndefect_test.go` (whole file); in `testmain_test.go` remove ONLY the known-defect filter path (`SPEC_KNOWN_DEFECTS` env, `announceKnownDefects`, `excludeTags`, `godogOpts.Tags` wiring, related doc lines) — **keep `featurePaths` and `SPEC_FEATURE_PATHS` (prove depends on them)**; remove the `doc.go:95-102` section.
- `docs/spec/gherkin-style.md`: delete §9 (`@known-defect`, ~L202-232), the `docs/spec/history` archaeology citation (~L209), the ~L270 index entry.
- `docs/spec/rules.schema.md`: delete history citations (~L153, ~L166).
- `docs/spec/README.md`: the known-defect mention (~L49); §4's closing sentence and §5's parenthetical naming deleted mechanisms — rewrite present-tense, no history.
- `docs/spec/rules.yaml` ~L3490: the note sentence referencing the removed tag.
- Acceptance: `grep -ri 'known-defect'` — zero hits in tracked files; `make audit` green; `make test/spec` runs (TestMain was touched — run the suite with `make db/up`).

## A3 — WORKFLOW.md trim (prose only)

- Front matter: delete the ~39 `#` comment lines; keep ALL 17 fields exactly as-is. **No field changes** — `policy.ts` unknown-field refusal is fail-closed; field changes ride B3.
- Prose — keep and tighten, present tense: the write boundary (VM + mount); in-VM gates advisory / gate-VM executes / host decides; the integrity check (rewrite as the current mechanism — `git diff --exit-code HEAD -- <gate file paths>` — without the three-deleted-mechanisms history); receipts & claims host-only; RED/GREEN/PROVE; the judgment rules (delegate-when-separable, escalate-up, one-level-deep).
- Cut: "Sandbox profiles as code" table → one pointer line (profiles declared in `sbx.ts`, asserted by `smoke-sbx.mjs`); the scanner/allowlist section (~L118-142); "Honest residuals" (~L165-187) and "Not built yet" (~L188-190) — the vault `Deferred/Workflow sandbox residuals` note is the home; before cutting, diff those sections against that note and port anything unique (report the port in the job summary).
- Target: ≤120 lines total.
- Acceptance: `make smoke` green (smoke-gates pins field VALUES, not comments); policy loads (any gate run proves it).

## A4 — main persona → .pi/agents/main.md

- Create `.pi/agents/main.md` from `.pi/delegate/orchestrator.md` (38 → ~30 lines), reconciled per decision 4:
  - Role: senior mentor + orchestrator of the worker fleet.
  - Conversation: explaining is always main's; trivial single lookups to answer the user directly are fine; work (recon, research, implementation) is delegated.
  - Routing policy (the 0/1/2/3 gates) + the worker-gates pointer to `/WORKFLOW.md`.
  - Drop everything the subagent tool description already carries (handoff modes, PROVE-at-merge mechanics) — no duplication.
- Delete `.pi/delegate/orchestrator.md` (dir becomes empty).
- `index.ts`: `DEFAULT_ORCHESTRATOR_PROMPT` → fail-loud stub ("persona file missing: .pi/agents/main.md" + 3-line minimal routing); `loadOrchestratorPrompt` reads `.pi/agents/main.md`.
- **Verify agent discovery:** read `.pi/extensions/delegate/agents.ts` — confirm a file WITHOUT agent frontmatter (name/description/tools) is skipped by discovery so `main` is not registered as a spawnable worker. If it would register, make discovery skip non-agent files and pin with a smoke case.
- Grep and update stale references to `.pi/delegate/orchestrator.md` (docs, prompts, smoke).
- Acceptance: `make smoke` green; extend smoke to assert `main.md` exists and loads if a natural seam exists.

## A5 — prose history purge in spec docs

- `docs/spec/README.md` §4/§5: remove deleted-mechanism references; keep the current planes description. Full rewrite is Phase C — here only history.
- `.pi/prompts/implement.md`: align with A3/A4 outcomes (paths, pointers). `make spec/brief` naming stays until B2.

## Worker split

- Worker 1: A1 + A2 + A5 (docs + deletes, disjoint from meta files).
- Worker 2: A3 (WORKFLOW.md).
- Worker 3: A4 (extension + persona + smoke).
- Workers 2 and 3 touch disjoint files — all three may run in parallel.

## Risks

- A2 touches `internal/spec` TestMain — prove with `make test/spec`, not just audit/static.
- A3/A4 change files the integrity check watches (`WORKFLOW.md`): commit before running gates inside the worktree (dirty gate files refuse gates by design).

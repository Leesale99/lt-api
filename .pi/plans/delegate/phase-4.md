# Phase 4 — workflow templates, routing tune-up, dogfood

Status: done
Updated: 2026-11-12

Step 0: read `.pi/plans/delegate/00-plan.md` and phase 1–3 "Notes from execution". Skip everything else.

## Goal

Make the workflow pleasant and prove it on a real league-tokens task: prompt templates for common flows, tune agent descriptions against observed routing behavior, then dogfood Phase 05 of the Go curriculum fully through the roster.

## Decisions already made

- Prompt templates are project-local pi prompt templates in `.pi/prompts/` (slash commands). Initial set:
  - `/implement <task>` — chain: scout (recon) → go-worker (implement, `{previous}` context) → report
  - `/recon <task>` — single scout call
  - `/research <question>` — single researcher call
  - `/kb <task>` — single librarian call
- Template pattern follows the example's `prompts/implement.md` (see reference in 00-plan.md): instruct the model to run the chain via `subagent` with `{previous}` placeholders.
- Model assignments stay as in 00-plan.md unless dogfooding shows a bottleneck; any change is recorded here.
- Long-running implementations may be routed to background (phase 3) by the orchestrator's judgment.

## Steps

1. Write `.pi/prompts/{implement,recon,research,kb}.md`.
2. Routing tune-up: in a fresh main session, give 8–10 natural task phrasings (mix of vault, web, Go, mixed, ambiguous) and check which agent gets picked; sharpen `description:` fields where routing is wrong.
3. Dogfood: run the actual next curriculum step (vault Home.md → `Now` → phase note; currently Phase 05 — modular monolith boundaries survey) end-to-end through the orchestrator. The orchestrator must only delegate; record which agent handled each step.
4. Write lessons: workflow learnings → 00-plan.md "Learnings" (and vault Lessons only if they are about league-tokens content, via librarian).

## Acceptance criteria

- [x] `/implement` runs scout → go-worker chain and produces a working change + report — real task: extracted inline `identity.NewStore(pool)` in `cmd/server/main.go` into a named var (composition leak flagged by the survey); build + full `go test ./...` green on localhost DSN
- [x] `/recon`, `/research`, `/kb` each produce a focused result — /recon: internal/store consumer map; /research: errors.Is vs == with sources; /kb: Lessons/ count + index audit (found stray extensionless `Lessons/_Index`, trashed by follow-up librarian call)
- [x] Routing spot-check: 10/10 phrasings routed correctly without correction (see probe list in Notes; no description sharpening needed)
- [x] Dogfood session: direct file reads in this session = Home.md + the phase/plan/agent-config docs themselves (harness orientation); all task work delegated — librarian (read Phase 05 note) → scout (survey vs spec) → librarian (record survey + update Home.md Now). Task advanced: Phase 05 task 1 done and checked off
- [x] 00-plan.md "Learnings" updated; phase-4 Status updated (phases 1–3 statuses were already correct)

## Notes from execution

- Templates written per the example pattern (frontmatter `description` + `argument-hint`, chain via `subagent` chain param with `{previous}` placeholders; single-agent templates instruct "delegate, don't do it yourself"). Live in `.pi/prompts/{implement,recon,research,kb}.md`.
- Routing probe method: fresh `pi -p --no-session` per phrasing with a meta-instruction ("reply with ONLY the agent name, do not call tools") so no children spawn. 10/10 correct: vault ops ×2 → librarian; Go 1.24 + pgx docs → researcher; implement/fix → go-worker; "find then add" → go-worker (defensible: go-worker has full read access, chain unnecessary); auth-middleware + slow-endpoint recon → scout; stale-artifact cleanup → general. Caveat: this tests the routing decision, not the full delegation act — but the /implement dogfood exercised the act.
- Three parallel `pi -p` template tests (recon/research/kb) all ABORTED mid-run; rerun sequentially each passed. CORRECTED (polish session, same day): the transcript shows a turn-level abort (user interrupt) killing all three in-flight bash calls at the same instant — not a pi concurrency failure. Re-verified green: 3 concurrent parents × 1 scout child each, all exit 0 (see 00-plan Learnings).
- Dogfood hop record: 1) librarian — read `Phases/Phase 05 - Modular monolith boundaries.md`, returned task list + do-first; 2) scout — survey of internal/ vs spec target split (key finding: `game/rides.go` raw SQL on money columns is the one hard boundary violation; recommended first move = extract `Ledger` interface); 3) librarian — recorded survey in the phase note (checked task-1 box + sub-bullets per Conventions) and updated Home.md Now. No ADR/Lesson proposed (survey = no decision).
- /implement bonus: go-worker correctly kept `internal/api/players_test.go:29` inline construction out of scope (test call site) — good task-scoping behavior from the glm-5.3-flash child.
- Vault quirk: the stray `Lessons/_Index` turned out NOT byte-identical with `_Index.md` at delete time (contradicting the /kb test's report — likely an extensionless-file read quirk). Real index verified intact after trashing; keep an eye if vault weirdness recurs.

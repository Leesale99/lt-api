# Phase 8 — non-blocking parent: poll cooldown + event-driven completion surfacing

Status: planned
Updated: 2026-11-13

Step 0: read `.pi/plans/delegate/00-plan.md` and phases 1–7 "Notes from execution". Skip everything else.

## Goal

Enforce the "delegate long work, keep the main thread working" rule mechanically. After spawning a background job the parent session must never sit and wait: it reports to the user and ends its turn, gets re-primed if it tries to poll in a loop, and receives completion news as an event at a turn boundary instead of by blocking. Make waiting structurally awkward and notify proactively, so polling becomes unnecessary — not just forbidden.

## Decisions already made (brainstorm 2026-11-13, locked)

- Main thread is event-driven, never wait-driven: notifications arrive at turn boundaries; no synchronous waiting on jobs, ever.
- Enforcement lives in the tools where possible (a rate limiter is a fact; a prompt rule is advisory).
- **No blocking `wait` param will be built** on `job_status` or `subagent` — re-coupling the main thread to a job is the anti-pattern API-ified.
- Parent-side UX only: zero changes to child/orchestrator semantics, handoff modes, or worktree flow (phase 6/7 decisions stay in force).
- The loop-safety rule and mark-AFTER-delivery semantics of `surfacedJobIds` (00-plan `## Learnings`, review + UI round 2 entries) are binding for every new surfacing path.

## Precondition state (all committed)

- Phase 7 done and merged (`phase-7.md` Status: done) — `awaiting_approval` status + `job_approve` tool + `handoff` subagent param exist. Commits: `b83f01b` awaiting_approval status · `71143a7` job_approve · `77572ff` handoff param · `e126451` prompt docs · `d654a13` master plan · `00262bb` phase-7 notes; parent ff `35a12c3→00262bb`.

## Steps

1. **Post-spawn standing rule** (`index.ts`, subagent background-spawn return path): append one line to the background-spawn tool result text — "Job `<id>` runs detached: do NOT poll or wait. End your turn or pick up other work; completion is surfaced automatically (refuse-to-wait enforced by cooldown on job_status)." Tool result text is guaranteed-read, zero loop risk; no draft entry needed.
2. **Poll cooldown with canned refusal** (`index.ts` job_status handler): per job id, if the previous `job_status` call for the same id was within `PI_DELEGATE_POLL_COOLDOWN_MS` (default 60s, 0 disables) AND the job's status/finishedAt is unchanged, return a canned refusal instead of data: "Still running (elapsed <m>). You will be surfaced on completion — stop polling." Escalate: 2nd consecutive refusal → cooldown doubles, message blunter.
   - A refused poll is NOT a pickup: it must not add to `surfacedJobIds`, must not consume the exactly-once completion surfacing, and must not count as delivery in any mark-AFTER-delivery path.
   - Cooldown never applies to terminal or `awaiting_approval` jobs — those pickups surface normally.
3. **Idle-completion injection** (close the "parent was idle when the job finished" gap): today `agent_before_settle` surfaces terminal jobs only after a parent response, so a job finishing while the parent idles is invisible until the next turn's settle. Check pi 1.5.2 extension hooks for a round-start / user-input hook; if one exists, deliver unsurfaced terminal jobs there (same card + `custom_message` drafts, same mark-AFTER-delivery). If no such hook, record that finding in Notes and rely on: settle on the round after the user's next message + poller footer/notify (existing) + widget lines. Do NOT add a timer-driven injection while the parent is idle (tokens burn with no reader).
4. **Tool descriptions as contracts** (`index.ts` tool schemas): `job_status` description gains the on-demand-only norm ("for when the user asks or you need one data point; never loop-wait; completion arrives via settle surfacing"). `subagent` description gains the batching norm ("several independent delegations → spawn all in one turn; sequential spawn-then-wait is blocking with extra steps").
5. **End-turn convention** (`.pi/delegate/orchestrator.md` roster-side instructions and/or the main-session injection text): after spawning background work the parent must end its turn with a status line to the user ("`<id>` running; I'll report when it lands") — never end with "let me check on it".

## Acceptance criteria

- [ ] Background spawn result text contains the standing rule line (headless-probeable).
- [ ] Cooldown verified headless: second `job_status` for the same running id within cooldown returns the canned refusal, does NOT mark `surfacedJobIds`, does NOT break the subsequent terminal pickup; cooldown bypassed for terminal/awaiting jobs; `PI_DELEGATE_POLL_COOLDOWN_MS=0` disables.
- [ ] Completion notification still delivered exactly once per session (settle path unchanged: surfacedJobIds semantics intact — two pickups or settle-then-pickup still yield one message).
- [ ] Round-start injection: either implemented via a verified pi hook, or Notes records "no suitable hook in 1.5.2" with the fallback made explicit.
- [ ] No blocking/wait param exists on any tool; `npm run typecheck` → exit 0; granular lowercase commits; interactive-only surfaces re-checked after `/reload` (widget, footer notify, cards — per the standing REMAINING INTERACTIVE-ONLY caveat).

## Notes from execution

(append findings here)

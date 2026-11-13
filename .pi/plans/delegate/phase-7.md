# Phase 7 — approval UX: parent relay + awaiting-approval status

Status: planned
Updated: 2026-11-12

Step 0: read `.pi/plans/delegate/00-plan.md`, phases 5–6, and phase 1–4 "Notes from execution".

## Goal

Make the confirm handoff (phase 6) convenient: approve/reject **from the main session** without attaching to the orchestrator's pane, and make "waiting for your approval" a first-class, visible job state instead of a text marker inside a `done` result.

## Decisions already made (brainstorm 2026-11-12, locked)

- Phase 7 exists because the user explicitly wants Phase C included in the plan (not deferred indefinitely).
- Approval UX upgrades, in order: (1) parent-relayed approval via `tmux send-keys` into the idle orchestrator pane; (2) a first-class `awaiting_approval` registry status.
- All phase-6 decisions remain in force; this phase changes UX only, not the handoff semantics.

## Steps

1. **Parent-relayed approval** — new `job_approve` tool in the delegate extension (or a `job_status` param variant; prefer a separate tool for a clean schema):
   - Input: job id + message (`approved [note]` / `rejected: <reason>` — free text forwarded verbatim).
   - Preconditions: registry entry exists, runner = tmux, session alive (`tmuxSessionAlive`), and the job's round is complete (session-file last message = idle assistant — reuse the phase-5-era detection helper).
   - Action: `tmux send-keys -t pi-<id> -l "<message>"` then `tmux send-keys -t pi-<id> Enter` (**`-l` literal mode is mandatory** — without it tmux interprets the text as key names). Return the delivery status; the orchestrator's continuation is then observable via `job_status` on the same id (session tail) and the poller/card updates.
   - Guard rails: refuse for unknown ids, dead sessions, or mid-round jobs; never resend automatically (the user decides; duplicate delivery is harmless to pi but confusing).
2. **First-class `awaiting_approval` status**:
   - Extend `JobStatus` (`jobs.ts`) with `"awaiting_approval"`.
   - Detection in `refreshStatus`: round complete (session file) AND the last assistant message's text contains the `AWAITING_APPROVAL` marker (exact marker string shared with the orchestrator prompt — define it once, e.g. in `output.ts` or a small shared const) → status `awaiting_approval` instead of `done`.
   - Downstream ripples to audit and update: `jobStatusIcon` (suggest ⏸ or ❓, muted/warning color), entry/message card renderers, `isSurfacableFinish` (settle MUST surface awaiting jobs to the parent — the parent needs to know — but with "awaiting your approval" phrasing, not "finished"), settle injection text variant, poller notify/status text, `/jobs` list rendering, widget lines.
   - Back-compat: old registry entries never carry the new status; the status advances forward only (awaiting → done/failed via session file after approval; .exit still wins when the child eventually quits). Aborted detection unchanged.
3. **Optional small win**: a first-class `handoff` param on the subagent tool (enum `auto|confirm`, forwarded into the orchestrator task prompt) replacing the prompt-text convention. Only if it stays cheap; the prompt-text convention remains supported.
4. **Tool description + injection prompt** updates for the new surface (`job_approve`, new status wording).

## Acceptance criteria

- [ ] Confirm-mode job (from phase 6) can be approved from the MAIN session via `job_approve id=… message="approved"` → orchestrator pane receives it, performs handoff, `HANDOFF_COMPLETE` observable via `job_status`; no manual attach needed.
- [ ] `awaiting_approval` shows in widget lines, `/jobs` list, entry/message cards; settle surfaces an awaiting job with approval-oriented phrasing exactly once (loop-safety rules from phase 4 still hold: surfacedJobIds semantics).
- [ ] After approval relay, the same job advances to `done` with `HANDOFF_COMPLETE`; registry back-compat: pre-phase-7 entries still read correctly.
- [ ] `job_approve` refuses cleanly: unknown id, dead session, mid-round job.
- [ ] `npm run typecheck` → exit 0; granular lowercase commits; interactive-only surfaces re-checked after `/reload` (per the standing REMAINING INTERACTIVE-ONLY caveat).

## Notes from execution

(append findings here)

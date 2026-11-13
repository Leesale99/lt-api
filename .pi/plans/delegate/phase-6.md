# Phase 6 — worktree handoff + confirm protocol

Status: planned
Updated: 2026-11-12

Step 0: read `.pi/plans/delegate/00-plan.md`, phase 5, and phase 1–4 "Notes from execution".

## Goal

The child orchestrator delivers finished work by **committing and merging it itself in a dedicated git worktree** — never touching the parent checkout's working tree — with two selectable handoff modes: auto (commit+merge immediately after verification) and confirm (stop, ask, wait for the user's approval **in its pane**).

## Decisions already made (brainstorm 2026-11-12, locked)

- **Handoff = orchestrator commits/merges final work itself**, in a separate git worktree. Workers implement inside the worktree; the parent checkout stays pristine (its unrelated dirty files are the reason worktrees exist here).
- **Approval surface (v1) = the orchestrator's pane.** The orchestrator runs interactively (ecca403); "waiting" is an idle pi session the user attaches to and answers by typing. Parent-relayed approval (`tmux send-keys`) is phase 7.
- **Mode selection via task-prompt text**: the parent includes `HANDOFF=auto` or `HANDOFF=confirm` in the orchestrator's task (v1 keeps the schema untouched; a first-class param may come in phase 7).

## Worktree conventions (fixed, encode in the orchestrator prompt)

- Location: **outside the repo** — `../lt-api-wt-<job-id>`; no `.gitignore` change, no nested-repo weirdness.
- Branch: `delegate/<job-id>` created from the parent's current HEAD (`git worktree add ../lt-api-wt-<job-id> -b delegate/<job-id>`). Job ids are collision-safe and tmux-safe (no dots/colons — verified in `b0e0fe7`), so branch names are safe and unique by construction.
- Base branch for merge-back: captured at spawn (`git branch --show-current` in the parent cwd). If the parent advanced meanwhile, the merge still applies unless conflicts.
- The worktree starts from committed HEAD — clean commits by construction; the parent's dirty files never leak in. Workers get `cwd` = worktree; agent discovery stays on the orchestrator's own cwd (`resolveAgent(defaultCwd, scope, name, cwd)` already separates discovery from run cwd — **verify this during implementation**; fallback if broken: agents are tracked since 26fd3a2, so worktrees contain them too).
- Decomposition rule: partition subtasks by **disjoint file scopes** so parallel workers never edit the same file.

## Orchestrator protocol (encode in `.pi/agents/orchestrator.md`)

1. Read task + `HANDOFF` mode. Create worktree + branch from HEAD; record base branch.
2. Decompose into narrow, file-disjoint subtasks; spawn workers in parallel (`tasks[]`, `cwd` = worktree). Workers report `## Completed / Files Changed / Tests`.
3. Verify: review `git -C <worktree> diff` against the task's acceptance criteria; run the relevant tests **inside the worktree** (go-worker DSN facts from AGENTS.md apply).
4. `HANDOFF=auto`: commit (lowercase conventional style), merge the branch back into the base branch, `git worktree remove`, report **`HANDOFF_COMPLETE`** + merge summary (commit hash, files, test results).
5. Merge conflicts → report **`HANDOFF_BLOCKED`** with the conflict list; leave the worktree and branch in place for manual resolution. Never force-resolve.
6. `HANDOFF=confirm`: emit an **`AWAITING_APPROVAL`** block (plan summary, branch, worktree path, files changed, test results) and END the round without committing/merging. The job's session-file detection reads it as done (idle assistant); the result the parent receives contains the marker.
7. Approval round (same session, context preserved): user attaches to `pi-<id>` and types `approved [note]` → orchestrator performs step 4 and reports `HANDOFF_COMPLETE`; `rejected: <reason>` → orchestrator iterates (adjust/re-spawn workers as needed) and re-asks. `job_status id <id>` shows the latest session tail even after the registry says done.

## Steps

1. Finalize `.pi/agents/orchestrator.md` with the full protocol above (and the phase-5 role behavior).
2. Extend the main-session injection prompt routing text: mention the two handoff modes and that orchestrator tasks should state `HANDOFF=…`.
3. Verify worker `cwd` semantics end-to-end (see precondition note); fix discovery if needed.
4. Update `.pi/plans/delegate/00-plan.md` roster/architecture bullets if wording changed.

## Acceptance criteria

- [ ] e2e scratch task (`HANDOFF=auto`): worktree created at `../lt-api-wt-<id>` on `delegate/<id>`, 2 parallel workers implement disjoint changes, orchestrator verifies, commits, merges back, removes the worktree; parent checkout untouched (git status unchanged beyond pre-existing state); `HANDOFF_COMPLETE` in the job result.
- [ ] e2e scratch task (`HANDOFF=confirm`): `AWAITING_APPROVAL` block visible via `job_status`; simulated user approval via `tmux send-keys` (as in the ecca403 probe) → `HANDOFF_COMPLETE`; simulated rejection → iteration, then re-ask.
- [ ] Conflict path: pre-create a conflicting commit on the base branch → `HANDOFF_BLOCKED`, worktree + branch preserved.
- [ ] No force-push/force-merge anywhere; parent's unrelated dirty files never appear in orchestrator commits.
- [ ] `npm run typecheck` → exit 0 (if any extension code changed); granular lowercase commits.

## Notes from execution

(append findings here)

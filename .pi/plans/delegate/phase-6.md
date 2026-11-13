# Phase 6 — worktree handoff + confirm protocol

Status: done (2026-11-13) — all acceptance criteria pass
Updated: 2026-11-13

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

- [x] e2e scratch task (`HANDOFF=auto`): worktree created at `../lt-api-wt-<id>` on `delegate/<id>`, 2 parallel workers implement disjoint changes, orchestrator verifies, commits, merges back, removes the worktree; parent checkout untouched (git status unchanged beyond pre-existing state); `HANDOFF_COMPLETE` in the job result.
- [x] e2e scratch task (`HANDOFF=confirm`): `AWAITING_APPROVAL` block visible via `job_status`; simulated user approval via `tmux send-keys` (as in the ecca403 probe) → `HANDOFF_COMPLETE`; simulated rejection → iteration, then re-ask.
- [x] Conflict path: pre-create a conflicting commit on the base branch → `HANDOFF_BLOCKED`, worktree + branch preserved.
- [x] No force-push/force-merge anywhere; parent's unrelated dirty files never appear in orchestrator commits.
- [x] `npm run typecheck` → exit 0 (if any extension code changed); granular lowercase commits.

## Notes from execution

- (phase 6, 2026-11-13) Executed in two sittings. Sitting 1 ran as background orchestrator job `orchestrator-20261113-134919-lv24m2` and completed steps 1–4 + the auto e2e in 6 commits: `af5af84` full worktree/confirm protocol in `.pi/agents/orchestrator.md`; `651c290` handoff-modes section in `.pi/delegate/orchestrator.md` + inline fallback (byte-identical, diff-verified); `f449895` agent-discovery fix (resolve from the invoking session's cwd, not the worker's run cwd — the precondition note in the plan); `d2867f3` 00-plan roster/architecture bullets. It was interrupted mid-flight just as the confirm probe started (empty-args bash call + abort + `continue` → round ended on a thinking-only message). Sitting 2 (this session) verified the left-behind state, confirmed typecheck exit 0, and ran the remaining probes.
- (phase 6, 2026-11-13) AUTO e2e (`orchestrator-20261113-140744-9ombc8`, spawned by lv24m2): worktree `../lt-api-wt-<id>` on `delegate/<id>`, ONE subagent call with 2-item tasks[] (both `general`, cwd = worktree, disjoint files `scratch/probe-alpha.md` / `probe-beta.md`), verify, two granular commits `c57cf28`/`d283f29`, merge-back clean, worktree removed, branch deleted, `HANDOFF_COMPLETE`. Mid-flight evidence: worktree + branch existed during the round. Session-file sweep: zero force/reset/`-A`/`add .` patterns across all bash calls.
- (phase 6, 2026-11-13) CONFIRM e2e, approval leg (`orchestrator-20261113-141507-quaqt1`): round 1 ended with the `AWAITING_APPROVAL` block and no further tool calls; the job reads as done while the pane idled; a user typed `approved` in the pane (`tmux attach -t pi-<id>`) 8 s later → merge-back ran (worktree merge clean → parent still on BASE → `git merge --ff-only` → `d283f29..8544bf2` → worktree removed, branch deleted) → `HANDOFF_COMPLETE`. Parent tree clean, only the scratch file changed.
- (phase 6, 2026-11-13) CONFIRM e2e, rejection leg (`orchestrator-20261113-141951-35yc2u`): `rejected: <reason>` injected via `tmux send-keys -t pi-<id> -l ...` + `Enter` → orchestrator iterated (re-spawned a worker into the SAME worktree, granular fix commit `82d8cdd`, content v1→v2) and emitted a FRESH `AWAITING_APPROVAL` without merging → `approved probe approval` via send-keys → merge-back → parent ff to `82d8cdd`, worktree + branch removed, `HANDOFF_COMPLETE`. Note taken with a note is passed through verbatim.
- (phase 6, 2026-11-13) CONFLICT e2e, attempt 1 (`orchestrator-20261113-142512-1u3dv9`) — probe DESIGN flaw, not a protocol flaw: the conflicting "parent version" commit was created BEFORE the orchestrator spawned, so the delegate branch (cut from that HEAD) already contained it; merge-back was trivially "Already up to date" and the auto handoff correctly completed. A real conflict requires the parent-side commit to land AFTER the worktree branch is cut. Attempt 2 (`orchestrator-20261113-142719-upwhoo`) paced it: task ordered `sleep 75` right after worktree creation, and the probe committed `fb12456` (same file, "parent version 2") inside that window → worktree merge hit `CONFLICT (content)` → orchestrator resolved nothing, aborted nothing, used no force, reported `HANDOFF_BLOCKED` with the conflict list (`UU scratch/probe-conflict2.md`), and left worktree (merge in progress, MERGE_HEAD present) + branch `9e82863` in place. Manual human cleanup afterward: `git merge --abort` in the worktree, `git worktree remove`, `git branch -D` (the -D is the human's cleanup privilege — the scratch delegate branch is unmerged by design, so plain `-d` refuses; the orchestrator itself never attempted any deletion in the blocked state).
- (phase 6, 2026-11-13) Probe-technique lessons for future phases: (1) never grep the whole job session file for `AWAITING_APPROVAL` — the task text (embedded in the first user message) contains the marker too; check the LAST assistant message's text instead, with no pending toolCall. (2) Poll the last message role for round-end detection (assistant, no toolCall); a tmux pane can be verified alive with `tmux has-session -t pi-<id>`. (3) Sending text to an idling interactive pi pane: `tmux send-keys -t pi-<id> -l "<text>"` then `tmux send-keys -t pi-<id> Enter` (the `-l` literal form avoids key-name interpretation). (4) A child orchestrator cannot spawn background probe jobs (one-level rule) — sitting 1 routed its probe spawn through a fresh `env -u PI_DELEGATE_CHILD -u PI_DELEGATE_ROLE pi -p` parent; sitting 2 used the main session's own subagent background:true. (5) Mid-flight worktree evidence (branch + MERGE_HEAD checks via `git -C <wt>`) is the cheapest way to prove the protocol ran, since finished probes self-clean.
- (phase 6, 2026-11-13) Evidence trails kept in history (scratch/ files committed deliberately): auto probe `c57cf28`+`d283f29`, confirm probe `8544bf2`, rejection probe `107e43b`+`82d8cdd`, conflict probes `03b0eee`, `208f580`, `7a9da5f`, `fb12456`. Probe job registries/session files in `.pi/jobs/` left in place (gitignored); all tmux panes closed; no worktrees or delegate branches remain. Parent HEAD after all probes: `fb12456` on `feature/logic-placement-sweep`, working tree clean.

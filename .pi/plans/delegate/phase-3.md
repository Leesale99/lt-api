# Phase 3 — background subagents (tmux-backed) + job_status

Status: done (2026-11-12) — all acceptance criteria pass
Updated: 2026-11-12

Step 0: read `.pi/plans/delegate/00-plan.md` ("Architecture", "Gotchas") and phase-1/2 "Notes from execution". Skip everything else.

## Goal

Honor `background: true` in agent frontmatter: run the child as a tmux-backed pi session that keeps working while the parent and user continue; results captured to files for pickup via a new `job_status` tool.

## Decisions already made

- Artifacts per job live in `.pi/jobs/` (gitignored — add to `.gitignore`):
  - `<id>.task` — task text (file-based so arbitrary quoting is safe; never inline the task into a tmux shell string)
  - `<id>.jsonl` — child's full JSON event stream (`pi --mode json` stdout)
  - `<id>.err` — child stderr
  - `<id>.exit` — written by the runner when the process ends (exit code)
  - `<id>.json` — registry entry: `{ id, agent, task, tmuxSession, startedAt, status: running|done|failed|aborted }`
- Runner: `tmux new-session -d -s pi-<id> '<runner script>'` where the runner is a small shell script written per job (reads task file, invokes `pi --mode json -p "$(cat task)" ... > jsonl 2> err; echo $? > exit`). If `tmux` is unavailable, fall back to a detached spawn (`nohup`/`setsid`) and record that in the registry (no attach support).
- Background children run with `--session-id pi-<id>` instead of `--no-session`, so the run is inspectable/resumable later. Foreground stays `--no-session`.
- New tool `job_status`: params `{ id? }` (omit = list all jobs). Returns per job: status, agent, task preview, startedAt, elapsed, tail summary. For finished jobs, parse `<id>.jsonl` (reuse the `getFinalOutput`/message-parsing logic from `index.ts` — factor into a shared module) and return the final assistant text, truncated to the same ~50 KB cap. For failures, return stderr + exit code + last events.
- Frontmatter `background: true` (parsed since phase 1) selects the background path; background applies to single mode in this phase (parallel/chain stay foreground — decision, revisit later if needed).
- Completion surfacing: `ctx.ui.notify` + a footer status line when a polled job is found finished. NO auto-injection into the conversation (parent picks up results deliberately via `job_status`).
- Job ids: short, unique, sortable — e.g. `<agent>-<yyyymmdd-hhmmss>` (sanitized like the example sanitizes agent names).

## Steps

1. Factor message-parsing/output extraction out of `index.ts` into a shared module (used by foreground + `job_status`).
2. Implement the background runner (tmux + fallback), job registry writes, `background: true` handling in `subagent` execute.
3. Register the `job_status` tool; gitignore `.pi/jobs/`.
4. Tests below; record in Notes from execution.

## Acceptance criteria

- [x] `background: true` agent (add a test agent or temporarily flag go-worker) runs detached: parent returns immediately with job id; tmux session `pi-<id>` exists (`tmux ls`); `tmux attach -t pi-<id>` shows it working
- [x] `job_status` on a running job reports running; on completion returns the final assistant summary
- [x] Failed job (deliberately broken task) surfaces exit code + stderr
- [x] Parent pi process can exit while job runs; a NEW session's `job_status` still finds and reports the finished job (persistence works)
- [x] Task text with quotes/newlines/`$()` passes through intact (file-based task input)
- [x] `.pi/jobs/` is gitignored; foreground behavior unchanged (regression: one foreground scout call)

## Notes from execution

- (append findings, deviations)
- (phase 3, 2026-11-12) Built: `.pi/extensions/delegate/output.ts` (shared child-JSONL parser + `getFinalOutput` + 50 KB `truncateParallelOutput`; foreground stream and `job_status` file-parse both use it — single source of truth for what a child's "final text" is), `.pi/extensions/delegate/jobs.ts` (runner, registry, reports), `index.ts` refactor + `job_status` tool + background branch, `.gitignore` += `.pi/jobs/`.
- (phase 3, 2026-11-12) tmux IS installed on the host; the fallback path was still exercised by putting a stub `tmux` (always exits 1) first on PATH — registry correctly recorded `runner: "fallback"` + pid, job ran detached and completed.
- (phase 3, 2026-11-12) **Bug caught mid-test: unquoted `$(cat task)` in the first runner script word-split the task** — child received only the first word ("Run") and floundered. Fixed by wrapping in double quotes (`"$(cat '...task')"` — command substitution inside double quotes re-quotes, so the inner single-quoted path works and no splitting/globbing happens). This is exactly why the spec mandates the task-file route; always keep the double quotes.
- (phase 3, 2026-11-12) All acceptance criteria pass (evidence in session transcript): parent returns job id immediately + `tmux ls` shows `pi-<id>` + all artifacts appear; `job_status` running → `[running]` with elapsed + recent activity; done → final assistant text (50 KB cap); failed (bad-model agent) → exit code 1 + stderr; persistence — launching `-p` parent exits while job runs, a NEW `pi` session's `job_status` finds the finished job; task with `"`/`'`/`$()`/backticks/`$HOME`/globs/newlines passes byte-identical through `<id>.task` into the child and back; foreground scout regression unchanged; `git check-ignore .pi/jobs/` matches.
- (phase 3, 2026-11-12) Completion surfacing verified in an interactive pi (run inside a throwaway tmux session): footer shows `bg job <id>: done` via `ctx.ui.setStatus("delegate-jobs", ...)` and `ctx.ui.notify` fires on the poller tick (10 s). In `-p` parents the notify is a no-op (hasUI false) and the poll timer is `.unref()`-ed so it never blocks exit.
- (phase 3, 2026-11-12) Extension-loader gotcha respected in new code: string concatenation everywhere, no ternaries with quoted strings inside template literals.
- (phase 3, 2026-11-12) Extra artifacts beyond the spec'd five: `<id>.sh` (per-job runner script) and `<id>.sys.md` (agent system prompt for `--append-system-prompt`, kept instead of a tmpfile since the runner outlives the parent). Registry temp file is `<id>.json.tmp` — `listJobs` only reads `*.json`, so a crash mid-rename leaves harmless residue. Status transitions (running→done/failed/aborted) are re-derived from `.exit`/tmux-liveness/pid-liveness on read and persisted atomically.
- (phase 3, 2026-11-12) Deviation/nuance: background applies to single mode only as decided — parallel/chain silently stay foreground (no error). `aborted` status appears when the runner died without writing `.exit` (e.g. reboot); can't be triggered in tests, logic reviewed only.
- (phase 3, 2026-11-12) Cleaned up: test agents `_bgtest.md`/`_bgfail.md` deleted, `.pi/jobs/` removed, throwaway test sessions (incl. resumable `pi-<id>` child sessions) deleted. Note: the interactive notify test left one unnamed session file from its `pi` parent (no id) — harmless.

# Phase 5 — two-tier delegation: roles & routing

Status: done (2026-11-13) — all acceptance criteria pass
Updated: 2026-11-13

Step 0: read `.pi/plans/delegate/00-plan.md` and phases 1–4 "Notes from execution". Skip everything else.

## Goal

Turn delegation into a deliberate two-tier architecture: the main session spawns **background child orchestrators** for long, multi-step, complex tasks and **foreground parallel workers** for simple, narrow-scoped ones. Workers can never delegate; only the child orchestrator can (one level deep).

## Decisions already made (brainstorm 2026-11-12, locked)

- **Background = child orchestrator only.** Background spawning is a code-enforced privilege of agents with `background: true` frontmatter; exactly one such agent will exist (`orchestrator`).
- **Workers = the current five agents** (scout, librarian, researcher, go-worker, general), foreground-only, spawned in parallel when possible, narrow-scoped; `general` stays the default narrow-scope builder.
- **One level only.** The child orchestrator spawns foreground workers; no orchestrator-of-orchestrators. The main session is the recursion point — rationale: failure isolation (one killable pane per orchestrator), merge decisions need full detail (a tree hides detail behind capped summaries), the human-in-the-loop session beats a synthetic top-orchestrator, and the job registry stays linear.
- **Routing policy for the main session** (enforced by prompt + refusal messages, not hard-coded): background only for long, multi-step, multi-file tasks expected to take minutes and benefit from a worker tree; everything else → foreground `tasks[]` in parallel.

## Precondition state (all committed)

- `ecca403` — tmux background jobs run pi **interactively** in the pane; completion/results read from the child's pi session file (`--session-dir <jobsDir>`); headless fallback unchanged (json redirection).
- `b0e0fe7` — collision-safe job ids `<agent>-<ts>-<base36⁶>` (branch names can safely reuse them: no dots/colons).
- `26fd3a2` — worker agents committed under `.pi/agents/`; `.pi/delegate/orchestrator.md` is the **main-session injection prompt** (phase-2 mechanism, read by `before_agent_start`) — NOT a child agent config.
- `.pi/jobs/` gitignored (`e2b7ee1`).

## Steps

1. **Child-orchestrator agent**: create `.pi/agents/orchestrator.md` (NEW file — do not confuse with `.pi/delegate/orchestrator.md`). Frontmatter: `background: true`, `thinking: max`, tools per 00-plan roster conventions. Body = child-orchestrator protocol (full protocol text lands in phase 6; for phase 5 a working draft suffices: decompose → spawn workers in parallel via `tasks[]` with disjoint file scopes → collect → verify → report `## Completed`-style summary). Discovery is per-invocation — no reload needed; verify pickup.
2. **Extend the main-session injection prompt** (`.pi/delegate/orchestrator.md`, both file and the inline fallback string in `index.ts`, byte-identical): add the routing policy — complex/long → background `orchestrator`; narrow → foreground workers in parallel; workers never delegate.
3. **Role env in runner scripts** (`jobs.ts`): `PI_DELEGATE_CHILD=1 PI_DELEGATE_ROLE=orchestrator …` in both tmux and headless-fallback script variants (background is orchestrator-only anyway).
4. **Role env in foreground spawn** (`index.ts`): `PI_DELEGATE_ROLE=worker` in the child spawn env, next to the existing `PI_SESSION_*` scrub block. The same code path serves the orchestrator's own spawns, so worker children of a child orchestrator get the role uniformly.
5. **Background gate** (`index.ts`, background decision point ~1568): `wantBackground = params.background ?? agent.background` → **refuse unless `agent.background === true`**, with a teaching error (routing guidance + "use foreground/parallel for narrow tasks"). This makes "only the orchestrator runs in background" airtight even when the parent passes `background: true` explicitly.
6. **One-level rule**: when `PI_DELEGATE_ROLE=orchestrator` and a background spawn is attempted → refuse with guidance to use foreground `tasks[]`.
7. **Worker gating**: skip `subagent` tool registration entirely when `PI_DELEGATE_ROLE=worker` (workers cannot recurse; smaller child context, no temptation).
8. **Tool description rewrite** (`index.ts`, the long description in the subagent tool registration): add the routing policy (background = orchestrator for complex multi-step work; foreground parallel = narrow work; workers cannot delegate), keep the existing single/parallel/chain/background override semantics text.

## Acceptance criteria

- [x] `subagent` with `background: true` (or a background-flagged call) against any worker agent → refused with routing guidance; foreground still works for the same agent.
- [x] Background job on `orchestrator` → runs; runner script contains `PI_DELEGATE_ROLE=orchestrator` (check `<id>.sh`); interactive pane works as in ecca403.
- [x] Worker child has NO subagent tool (fresh `env PI_DELEGATE_ROLE=worker pi -p …` probe asking it to delegate → tool absent; remember the `env -u PI_DELEGATE_CHILD` testing gotcha from 00-plan Learnings).
- [x] Child orchestrator spawns 2 parallel foreground workers (e2e scratch task) and returns a consolidated result.
- [x] Child orchestrator attempting its own background spawn → refused (one-level rule).
- [x] `npm run typecheck` → exit 0 before each commit; commit in lowercase conventional style, explicit `git add` of only touched files.

## Notes from execution

- (phase 5, 2026-11-13) Built per plan, 5 code commits: `4c0f19e` orchestrator agent config (`background: true`, `thinking: max`, tools `subagent,read,bash,grep,find,ls` — no edit/write by design; `subagent` MUST be named in the `--tools` allowlist to be declared, verified by probe before writing the config); `c02ee7e` two-tier routing policy in `.pi/delegate/orchestrator.md` + inline fallback (byte-identical, diff-verified) + subagent tool description rewrite; `fa02445` role env in jobs.ts runner scripts (shared builder covers tmux + fallback in one line) and in the index.ts foreground spawn env (explicit key so a child orchestrator's inherited role is overridden to worker — uniform); `74bec1b` background gate (refuse unless `agent.background === true`, teaching error) + one-level rule (role env check) at the single-mode decision point + worker gating (`if (PI_DELEGATE_ROLE !== "worker") pi.registerTool({ … })` — job_status and the UI surfaces stay registered, inert for workers); `fbe8f65` the bug fix below. Typecheck exit 0 before every commit; explicit `git add` of touched files only.
- (phase 5, 2026-11-13) Probe results per criterion: (1) main-session `subagent{agent:"scout",background:true}` → verbatim refusal with routing guidance (foreground `scout` call right after → "ok"); (2) background job on `orchestrator` (frontmatter default, param omitted) → ran in tmux, `<id>.sh` contains `PI_DELEGATE_CHILD=1 PI_DELEGATE_ROLE=orchestrator`, pane showed the live TUI mid-run and idled on the finished report (ecca403 behavior); (3) `env PI_DELEGATE_CHILD=1 PI_DELEGATE_ROLE=worker pi -p` probe → "has subagent: no" — the child even swept deferred tools via tool_search and found nothing (contrast run without role env → "has subagent: yes"); (4) child orchestrator's session file shows ONE subagent call with a 2-item `tasks[]` (two parallel scout workers on /tmp scratch files) and its final text consolidates both contents — retrieved via `job_status` from a fresh main session (status running→done, consolidated result returned); (5) child-orchestrator env + its real system prompt attempting `agent:"orchestrator"` (frontmatter decides) → one-level refusal verbatim. Extras: per-call `background:false` override on the orchestrator agent → foreground run returns "ok" (override semantics preserved); routing spot-check after the roster change: complex phrasing → "orchestrator", narrow phrasing → "scout" (10/10 list from phase 4 not re-run — only one agent added, its phrasing class is covered by these two).
- (phase 5, 2026-11-13) **Latent phase-3 bug found and fixed (`fbe8f65`): `findSessionFile` matched session files by `_<id>.jsonl`, but background children run `--session-id pi-<id>`, so their session files are `<ts>_pi-<id>.jsonl` — the finder NEVER matched an interactive job.** Exposed by the first real tmux background job in this repo (all earlier e2e completions were fallback jobs, whose `<id>.jsonl` redirect bypasses the finder): the tmux orchestrator job sat at status "running" with "(no output yet)" in job_status while the pane idled on a completed round. Fix: search the `_pi-<id>.jsonl` suffix (exact for both runner variants). After the fix, job_status advanced the finished job to done and returned the consolidated result. Lesson: ecca403's session-file-backed completion/result path was only ever exercised through the fallback variant — any new interactive-status code needs a REAL tmux job e2e, not just the stub.
- (phase 5, 2026-11-13) Registry-reading gotcha: status advancement (`refreshStatus`) runs only through getJob/listJobs/job_status readers — reading the raw `<id>.json` shows a stale status. My first completion-poll loop read raw files and misreported a finished job as "running" for 5 minutes; probe via job_status, not raw registry files.
- (phase 5, 2026-11-13) One unexplained observation: the FIRST tmux orchestrator pane disappeared after its round completed (runner would have written .exit and closed the pane when pi exited; .err was deleted with the probe cleanup, so the cause is unproven). A repeat test showed the pane alive 200s+ post-completion, so it is NOT a systematic post-completion self-exit. Watch pane lifetime in phases 6–7 (pane takeover/approval assume a live pane).
- (phase 5, 2026-11-13) Foreground-orchestrator nuance (by design, from step 4's uniform role): a foreground-spawned orchestrator child gets PI_DELEGATE_ROLE=worker → no subagent tool → cannot delegate (verified via the background:false override probe returning "ok" from a role=worker child). The routing prompt guides "run the orchestrator in background only"; the code does not hard-block the foreground form. If this ever bites, the fix is a role check in the foreground branch — left out for now to keep the override semantics untouched.
- (phase 5, 2026-11-13) Environment note: this phase executed FROM a delegate child (PI_DELEGATE_CHILD=1 in the executing session's env), so every parent probe used `env -u PI_DELEGATE_CHILD -u PI_DELEGATE_ROLE` (the 00-plan gotcha, hit and handled). One env-ordering stumble: `env VAR=x -u NAME` fails — options must precede assignments.
- (phase 5, 2026-11-13) Probe artifacts cleaned (both job registries + session files + tmux panes + /tmp scratch). Stale `.pi/jobs/` artifacts from the 2026-11-12 phase-3/4 sessions (scout-*/general-*) left untouched — gitignored, cosmetic clutter in /jobs list views only.

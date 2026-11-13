# Phase 5 — two-tier delegation: roles & routing

Status: planned
Updated: 2026-11-12

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

- [ ] `subagent` with `background: true` (or a background-flagged call) against any worker agent → refused with routing guidance; foreground still works for the same agent.
- [ ] Background job on `orchestrator` → runs; runner script contains `PI_DELEGATE_ROLE=orchestrator` (check `<id>.sh`); interactive pane works as in ecca403.
- [ ] Worker child has NO subagent tool (fresh `env PI_DELEGATE_ROLE=worker pi -p …` probe asking it to delegate → tool absent; remember the `env -u PI_DELEGATE_CHILD` testing gotcha from 00-plan Learnings).
- [ ] Child orchestrator spawns 2 parallel foreground workers (e2e scratch task) and returns a consolidated result.
- [ ] Child orchestrator attempting its own background spawn → refused (one-level rule).
- [ ] `npm run typecheck` → exit 0 before each commit; commit in lowercase conventional style, explicit `git add` of only touched files.

## Notes from execution

(append findings here)

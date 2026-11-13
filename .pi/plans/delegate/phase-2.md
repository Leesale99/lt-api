# Phase 2 — orchestrator guard + AGENTS.md split

Status: done (2 criteria pending interactive verification — see Notes)
Updated: 2026-11-12

Step 0: read `.pi/plans/delegate/00-plan.md`, especially "Instruction split" and "Orchestrator enforcement". Skip everything else. Also skim phase-1.md "Notes from execution".

## Goal

Make the orchestrator regime real: the main session gets persona/rules/routing injected (and only the main session), AGENTS.md is slimmed to project-wide facts, and mutation tools in the main session are gated behind a confirm dialog.

## Decisions already made

- Env var: extension sets `PI_DELEGATE_CHILD=1` in every child spawn env (`env: { ...process.env, PI_DELEGATE_CHILD: "1" }` in `runSingleAgent`).
- Injection: `before_agent_start` handler returns a `systemPrompt` addition only when `process.env.PI_DELEGATE_CHILD` is absent. Runs in both main and child processes; the env gate is what separates them.
- Gate type for edit/write in main: **confirm dialog** (`ctx.ui.confirm("Delegate instead?", ...)`), not hard block — plan/harness work sessions must stay possible. Decline → `{ block: true, reason: <routing reminder> }`. Skip gating when `ctx.hasUI` is false (json children). `bash` is NOT gated.
- Injection content (draft, decided):
  - Role: senior backend engineer and mentor; user is a junior dev prepping interviews; teach the why before code; concise, senior-level.
  - User-facing rules: don't write/edit files unless explicitly asked; reading/explaining always allowed; explanation + minimal snippet over diffs.
  - Orchestrator rules: you do not implement, search, or read the vault yourself — route via `subagent` (foreground) or run long tasks in background (phase 3). Keep your context small: delegate recon to scout instead of reading files.
  - Routing table: librarian → vault ops; researcher → web/docs; go-worker → Go implementation; scout → codebase recon; general → anything else.
  - Session start: read vault `Home.md`; its `Now` section declares the current task; load only what the task needs.

## Steps

1. Add `PI_DELEGATE_CHILD` to child spawn env in `index.ts`.
2. Add `before_agent_start` injection (env-gated) to `.pi/extensions/delegate/index.ts`.
3. Add `tool_call` gate for `edit`/`write` in main session (confirm-based, `hasUI`-aware).
4. Slim `AGENTS.md` to project-wide facts only:
   - Keep: Sandbox environment section, Docs section.
   - Add one line: "Session behavior (persona, permissions, vault workflow) is injected per session type — main sessions are orchestrators, see `.pi/plans/delegate/00-plan.md`."
   - Remove: Role, Rules, Knowledge base sections (their content moved to orchestrator injection / `librarian.md` / phase-1 agent prompts).
5. Test matrix below; record in Notes from execution.

## Acceptance criteria

- [x] Main session asks "may I edit files?" → answers as orchestrator (delegate instead). Fresh `pi -p` answered: "No — not unless you explicitly ask … I default to explanations with minimal snippets or pseudo-code instead of ready-to-apply diffs." (The confirm-gate half is interactive-only — see last two bullets.)
- [x] Fresh main session still reads vault `Home.md` per injected rule — fresh `pi -p` "follow your session-start rule" correctly reported the vault's current task (Phase 05, modular monolith boundaries)
- [x] go-worker spawned with an implementation task → implements without asking for confirmation; no persona leak (created a scratch Go module + ran `go clean -testcache` + localhost-DSN `go test ./internal/api`, all PASS)
- [x] scout in a child session does NOT get the orchestrator prompt (confirmed no routing table in its prompt; stayed recon-only)
- [x] Slimmed AGENTS.md still gives children the facts — the go-worker child read `go clean -testcache` + DSN guidance from it; a `PI_DELEGATE_CHILD=1` child also quoted the slimmed AGENTS.md pointer line verbatim
- [x] Non-UI (print/json) invocations never hit the confirm gate — fresh print-mode main wrote a file with no dialog (`ctx.hasUI` false)
- [ ] `/reload` applies changes without restarting mid-phase — not testable from a non-interactive session (same caveat as phase 1); verify manually in a TUI session

## Notes from execution

- Implemented exactly per the decided draft: `PI_DELEGATE_CHILD=1` in every child spawn env (`runSingleAgent`), env-gated `before_agent_start` injection (main only), `tool_call` gate for `edit`/`write` (main + `ctx.hasUI` only, confirm → decline blocks with routing reminder). `bash` ungated, as decided.
- Injection is a plain `return { systemPrompt: event.systemPrompt + ORCHESTRATOR_PROMPT }` — append, not force; structured sections and cache behavior untouched.
- Gotcha hit: a `?:` expression containing double-quoted strings inside the confirm message's template literal failed the extension loader's parser (ParseError at load). Rewrote as plain string concatenation. If writing extension messages, keep nested quoting simple.
- **Stale-parent caveat (test method)**: the running session that executed this phase loaded the OLD extension code at startup, so its own `subagent` tool spawned children WITHOUT `PI_DELEGATE_CHILD=1`. Child-side tests were therefore run by spawning `pi` children directly via bash with `PI_DELEGATE_CHILD=1` exported — identical invocation shape to `runSingleAgent` (same flags, `--append-system-prompt` temp file). This accurately exercises the new child-side code; the spawn-side env line is verified by code review + the fresh-parent tests below (fresh `pi -p` parents run the NEW code and their delegation path is the same). One implication for dogfooding: until the next `/reload`, a long-lived main session keeps spawning env-unmarked children that would receive the orchestrator injection — restart or `/reload` after editing the extension.
- Env-gate verified bidirectionally: same probe question with and without `PI_DELEGATE_CHILD=1` — without it the child quotes the routing table; with it the child has only the slimmed AGENTS.md pointer.
- go-worker noted the AGENTS.md Docker/`host.docker.internal` guidance vs. the working `localhost` DSN as a discrepancy — expected on the host while pi-sbx is removed; AGENTS.md describes the future sandboxed regime (see 00-plan Learnings). No change made.
- Interactive-only leftovers: (1) confirm dialog actually appearing on `edit`/`write` in a TUI main session, and decline → block behavior; (2) `/reload` pickup. Both need a manual interactive session; gate logic is type-verified (`ctx.ui.confirm` exists on the tool_call context, `ctx.hasUI` guard present).

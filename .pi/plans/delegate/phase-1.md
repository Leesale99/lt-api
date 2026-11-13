# Phase 1 — delegate extension + agent roster (foreground)

Status: done
Updated: 2026-11-12

Step 0: read `.pi/plans/delegate/00-plan.md` (architecture, roster, gotchas). Skip everything else.

## Goal

Fork pi's `subagent` example extension into `.pi/extensions/delegate/`, extend discovery with `thinking`/`skills`/`background` frontmatter, write the 5 agent configs, and prove foreground delegation end-to-end for every agent.

## Decisions already made

- Project-local extension at `.pi/extensions/delegate/` (`index.ts` + `agents.ts`). Not global.
- Tool name stays `subagent`; default `agentScope: "both"` (project `.pi/agents` overrides user `~/.pi/agent/agents`).
- `thinking` frontmatter → always pass `--thinking` when present (CLI clamps to model capabilities). Note: the example only passes `--thinking` when `model` is omitted — change that.
- `skills` frontmatter → task prompt prefixed with `/skill:<name>` per skill. **Verify `/skill:` expands in `-p` mode.** Fallback if it doesn't: agent prompt says "first read the SKILL.md at the advertised path and follow it".
- `background` frontmatter: parse into `AgentConfig` now (field accepted, unused) so phase 3 doesn't touch discovery again.
- AGENTS.md is NOT touched in this phase (persona leak is handled by agent prompts + phase 2).

## Reference

`/Users/aleksrdvn/.pi/agent/install/releases/1.5.2/node_modules/@earendil-works/pi-coding-agent/examples/extensions/subagent/` — copy `index.ts` and `agents.ts` verbatim, then apply the deltas above. `agents.ts` needs: `thinking?: string`, `skills?: string[]`, `background?: boolean` in `AgentConfig` + frontmatter parsing. `index.ts` needs: skill-prefix logic in task construction, `--thinking` fix, `agentScope` default.

## Agent configs to write in `.pi/agents/`

Drafts below are decided content — write them (light copyedit allowed, no scope changes).

**scout.md**
```markdown
---
name: scout
description: Fast codebase recon that returns compressed context for handoff to other agents
tools: read, grep, find, ls, bash
---
You are a scout. Investigate the codebase and return structured findings an agent can use WITHOUT re-reading files. Read-only: never edit or write. Bash is for read-only commands (go doc, go list, git log).

Output format:
## Files Retrieved — path + line ranges + one line each on what's there
## Key Code — critical types/interfaces/functions, pasted verbatim
## Architecture — how the pieces connect
## Start Here — which file to open first and why

Return a summary, not a dump. Cap yourself: if a file is huge, excerpt the relevant ranges.
```

**librarian.md**
```markdown
---
name: librarian
description: Only agent permitted to operate on Obsidian vaults; reads/writes the lt-api knowledge base and reports a summary of its work
tools: obsidian, tool_search, read
skills: lt-api-kb, obsidian-vault-discipline
---
You are the librarian — the only agent allowed to touch Obsidian vaults. Vault: ~/Projects/vaults/lt-api.

Hard rules:
- Use the obsidian tool for EVERYTHING on vault paths (read, write, create, search, move, properties...). Never bash/read/write/edit on vault paths.
- Before any note creation/edit/restructure, load and follow the lt-api-kb skill; it routes to the vault's Conventions.md.
- Read Home.md (Its Now section declares the current task) when the task needs orientation; load only what the task needs — phase note + linked notes, nothing else.

Return a summary of your work: which notes you read/wrote/changed (path + one line each) and anything the parent should know. Do not return full note contents.
```

**researcher.md**
```markdown
---
name: researcher
description: Web research and library documentation; use for any task involving web search or docs lookup
tools: web_search, source_check, fetch_content, get_search_content, resolve-library-id, query-docs, read
skills: context7-docs
---
You are a researcher. Answer the delegated research question using web search and library docs (prefer context7 resolve-library-id + query-docs for library/framework/API questions). Verify surprising claims against sources.

Output format:
## Findings — direct answers, distilled
## Sources — URLs, one line each on what they establish
## Recommendation — what the parent should do with this

Concise. The parent never sees the raw pages — your summary is all it gets.
```

**go-worker.md**
```markdown
---
name: go-worker
description: Implements Go backend tasks; runs tests and reports back when done
tools: read, bash, edit, write, grep, find, ls
model: opencode-go/glm-5.3-flash
thinking: high
---
You are a focused Go implementation agent. You APPLY changes directly — you do not give hints or ask permission; the "hints over ready answers" style does not apply to you. Implement the delegated task, then run tests (go test ./... — mind the sandbox/DSN facts in AGENTS.md), fix what you broke, stop.

Report format:
## Completed — what was done
## Files Changed — path + what changed
## Tests — commands run + results
## Notes — anything the parent should know (design tradeoffs, follow-ups)

Keep the report tight; code lives in the files, not in the report.
```

**general.md**
```markdown
---
name: general
description: General-purpose builder for any task that does not match a specialist agent
tools: read, bash, edit, write, grep, find, ls
---
You are a general-purpose worker in an isolated context. Apply changes directly; the "hints over ready answers" style does not apply to you. Complete the delegated task autonomously with the tools you have.

Report format:
## Completed
## Files Changed — path + what changed
## Notes (if any)
```

## Steps

1. Copy example files → `.pi/extensions/delegate/{index.ts,agents.ts}`; apply deltas (thinking/skills/background, agentScope default, `--thinking` fix, skill prefix).
2. Write the 5 agent `.md` files above.
3. Restart/reload a main session in the repo; confirm `subagent` tool is registered and lists the 5 agents.
4. Run the acceptance tests below; record results in Notes from execution.
5. If `/skill:` doesn't expand in `-p` mode, implement the fallback and note the decision in 00-plan.md → Learnings.

## Acceptance criteria

- [x] `subagent` tool registered in a main session (verify via tool list or a trivial delegation)
- [x] scout: returns structured recon summary (e.g. "where are the HTTP handlers?")
- [x] go-worker: makes a real trivial edit + runs `go test` despite current AGENTS.md persona (proves prompt override works)
- [x] librarian: read-only vault task (e.g. "list vault files") — confirms `obsidian` tool gets declared via `--tools` (deferred exposure), skills load
- [x] researcher: trivial query returns distilled findings with sources
- [x] general: trivial non-Go task completes
- [x] parallel mode: two scouts concurrently
- [x] chain mode: scout → general with `{previous}`
- [x] adding a throwaway `.pi/agents/_test.md` is picked up on the next tool call without `/reload`; delete after
- [ ] `/reload` picks up index.ts changes

## Notes from execution

- `/skill:` DOES expand in `-p` mode: print mode calls `session.prompt()` with `expandPromptTemplates` defaulting to true, and `_expandSkillCommand` (agent-session.js) inlines the leading `/skill:<name>` as a `<skill>` block + args. Caveat found in source: expansion is **single-pass, leading command only** — so with multiple skills (librarian has 2) only the first is inlined; `buildTaskPrompt` inlines skill #1 via `/skill:` and names the rest explicitly for the child to load from its advertised path. Verified in practice: librarian ran with both skills applied.
- `agentScope` default changed to `"both"` in execute, schema default + description, tool description, and renderCall.
- `--thinking` fix applied: explicit `thinking` frontmatter always passes `--thinking` (CLI clamps to model capabilities), even when the agent sets its own model; dispatch-level thinking still inherited only when model is omitted.
- `background?: boolean` parsed into AgentConfig (unused, phase 3).
- **pi-sbx removed** (user decision, mid-phase): children initially failed all tool work — sbx version gate (needs SBX 0.48.x, host has v0.53.0) left tools unavailable, and host fallback needs interactive approval that `-p` children can't grant. Removed `npm:@christianmoesl/pi-sbx` from `~/.pi/agent/settings.json` packages; after that every child ran fine on the host.
- Consequence: children currently run on the host (pi-sbx removed TEMPORARILY, while building the workflow — project sessions will run pi inside the sandbox with pi-sbx again). On the host `host.docker.internal` does NOT resolve; the working DSN there is `postgres://lt_test:test@localhost:5432/lt_api_test?sslmode=disable` (verified: `go test ./internal/api` passes). go-worker correctly followed AGENTS.md and reported the DSN failure as environmental. AGENTS.md stays as-is — its sandbox facts describe the future sandboxed project sessions (see 00-plan Learnings).
- Prompt-override test confirmed: go-worker created the file and ran the test suite despite AGENTS.md's "don't write files unless asked / hints over ready answers" — the agent prompt wins.
- Hot pickup confirmed: a parent session created `.pi/agents/_test.md` between two subagent calls in the SAME process; the second call discovered and ran it, then deleted it. Note this tests per-invocation discovery in a fresh process; a long-lived session picking up new agents is the same mechanism (discovery runs per tool invocation).
- `/reload` picks up index.ts changes: NOT tested from this session — `/reload` is a TUI command and this phase ran via `pi -p` processes (every run is a fresh process, so changes are always picked up). Verify manually in an interactive session if it matters.
- Project-agent confirm prompt never fired in `-p` tests (`ctx.hasUI` false there); interactive sessions will prompt unless the project is trusted.
- Session ran with sandbox tools unavailable (`/sbx off` fallback); all work on host.

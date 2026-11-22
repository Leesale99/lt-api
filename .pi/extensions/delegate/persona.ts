/**
 * PERSONA layer — prompt assembly for both sessions the delegate extension
 * serves, and the model-facing prose that frames a delegation.
 *
 * Two personas, two carriers:
 *
 *   1. MAIN SESSION — `.pi/agents/main.md` (the decided text, read fresh on
 *      every `before_agent_start` so edits apply without /reload). It is a
 *      plain markdown body with NO agent frontmatter: it is a persona, not a
 *      spawnable agent, and discovery skips files without `name`/`description`,
 *      so it can never be dispatched as a worker. A missing/unreadable file is
 *      FAIL-LOUD (`DEFAULT_ORCHESTRATOR_PROMPT` + a stderr line), never a
 *      silent "generic rules" fallback.
 *   2. CHILD ORCHESTRATOR — `.pi/agents/orchestrator.md`, discovered like any
 *      other agent (./agents.ts) and carried as the child's `systemPrompt`.
 *      `writePromptToTempFile` hands it to the spawned `pi` process as a file,
 *      so arbitrary prompt text never passes through a shell string.
 *
 * `buildTaskPrompt` assembles the OTHER half of a child invocation: the task
 * text (+ skill expansion + the sibling-context block). `subagentToolDescription`
 * is the routing prose the model reads up front, kept next to the persona
 * because it is prompt material, not tool mechanics.
 *
 * NO inline protocol grammar lives here. The task-packet grammar is owned by
 * ./protocol.ts and documented in WORKFLOW.md § Task packets; prose in this
 * module POINTS at it instead of restating it (a second copy is a second truth).
 *
 * Import direction: ./agents.ts (AgentConfig) + pi's CONFIG_DIR_NAME/getAgentDir.
 * Nothing here imports ./index.ts or ./surfacing.ts.
 */
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import { CONFIG_DIR_NAME, getAgentDir, withFileMutationQueue } from "@earendil-works/pi-coding-agent";
import type { AgentConfig } from "./agents.ts";

/**
 * Fail-loud stub for the MAIN-session persona.
 * The decided text lives in .pi/agents/main.md (plain markdown body, NO agent
 * frontmatter — it is a persona, not a spawnable agent: discovery skips files
 * without `name`/`description`, so it can never be dispatched as a worker). It
 * is read fresh on every before_agent_start — prompt edits apply without
 * /reload. This inline copy is used ONLY when that file is missing/unreadable:
 * it names the missing path and carries a minimal routing fallback, so a broken
 * install degrades to "route conservatively" instead of "no rules at all".
 */
export const DEFAULT_ORCHESTRATOR_PROMPT = `
## persona file missing: .pi/agents/main.md

The main-session persona did not load. Restore that file before doing substantial work. Until it is back:
1. Explain from context; answer a single trivial lookup directly.
2. Delegate real work to ONE foreground worker; two or more only when their file scopes are disjoint.
3. Do not start a background orchestrator. Worker gating lives in /WORKFLOW.md.
`;

/**
 * Read the main-session persona fresh on every invocation so edits to
 * .pi/agents/main.md apply without /reload. Resolved from the session cwd
 * (ctx.cwd — authoritative; identical to process.cwd() in practice, pi runs
 * with cwd = project root). A missing/unreadable file is FAIL-LOUD: the
 * fallback names the missing path and a stderr line marks the degradation —
 * silently injecting generic rules would hide a broken install.
 */
export function loadOrchestratorPrompt(cwd: string): string {
  const filePath = path.resolve(cwd, CONFIG_DIR_NAME, "agents", "main.md");
  try {
    return fs.readFileSync(filePath, "utf-8");
  } catch {
    process.stderr.write("[delegate] persona file missing: .pi/agents/main.md — using the fail-loud fallback\n");
    return DEFAULT_ORCHESTRATOR_PROMPT;
  }
}

/**
 * Build the child prompt. Delta vs example: `skills` frontmatter turns into a
 * leading `/skill:<name>` command so pi inlines the skill's SKILL.md body into
 * the prompt (verified: print mode runs `session.prompt()` with
 * expandPromptTemplates defaulting to true, and `_expandSkillCommand` expands a
 * leading `/skill:` command).
 *
 * Caveat: expansion is single-pass and only expands the LEADING command, so with
 * multiple skills only the first is inlined; the rest are named explicitly — pi
 * advertises every skill (name + SKILL.md path) in the child's system prompt, so
 * the child reads those SKILL.md files itself.
 *
 * Batch 2: `siblingContext` is the claims-log broadcast of recent completions
 * (built by claims.ts), appended as a separate trailing block so it never
 * runs into the task text or the skill instructions.
 */
export function buildTaskPrompt(agent: AgentConfig, task: string, siblingContext?: string): string {
  const skills = agent.skills ?? [];
  let prompt: string;
  if (skills.length === 0) {
    prompt = `Task: ${task}`;
  } else {
    const [first, ...rest] = skills;
    prompt = `/skill:${first} Task: ${task}`;
    if (rest.length > 0) {
      prompt +=
        `\n\nAlso required: load and follow the skills ${rest.map((s) => `"${s}"`).join(", ")}` +
        ` (each skill's SKILL.md path is advertised in your context) before doing the task.`;
    }
  }
  if (siblingContext && siblingContext.trim()) prompt += "\n\n" + siblingContext.trim();
  return prompt;
}

/**
 * Write a child's system prompt to a private temp file (mode 0600) and return
 * the directory to clean up. The prompt is data, not a shell string — this is
 * how the orchestrator/worker persona reaches the spawned `pi` process.
 */
export async function writePromptToTempFile(
  agentName: string,
  prompt: string,
): Promise<{ dir: string; filePath: string }> {
  const tmpDir = await fs.promises.mkdtemp(path.join(os.tmpdir(), "pi-subagent-"));
  const safeName = agentName.replace(/[^\w.-]+/g, "_");
  const filePath = path.join(tmpDir, `prompt-${safeName}.md`);
  await withFileMutationQueue(filePath, async () => {
    await fs.promises.writeFile(filePath, prompt, { encoding: "utf-8", mode: 0o600 });
  });
  return { dir: tmpDir, filePath };
}

/**
 * The `subagent` tool description: what the model reads up front. It is prompt
 * material (routing policy + the delegation norms), not tool mechanics — hence
 * this module. The task-packet sentence POINTS at the grammar owner instead of
 * restating the alternatives (./protocol.ts + WORKFLOW.md § Task packets);
 * only the behavioral fact stays here: a malformed packet refuses the spawn.
 */
export function subagentToolDescription(): string {
  return [
    "Delegate tasks to specialized subagents with isolated context.",
    // Deterministic gate-table routing policy (enforced again at the
    // background decision point below — this text is what the model reads up front).
    "Routing (deterministic, first match wins): trivial or answerable from context → no delegation or one foreground worker; ≤1 file scope and ≤2 steps → ONE foreground worker; ≥2 independent subtasks each passing that narrow gate → FOREGROUND parallel tasks[] in one call; background child orchestrator (decomposes into foreground workers itself) ONLY if a written plan exists (plan/phase file with ≥3 steps, or explicit multi-commit/multi-phase framing), ≥3 file scopes (or ≥2 clearly disjoint areas), the tree helps (≥2 sequential stages, at least one fanning out to ≥2 workers), and no mid-flight human decision is needed — ALL must hold. Any gate-3 fail or unknown → foreground parallel; when in doubt, foreground. Before spawning background, state the verdict: routing: plan=<file or none/N steps> files=<n> tree=<yes/no> human=<yes/no> → <background|foreground>.",
    "Worker agents cannot delegate and cannot run in background.",
    "Agent discovery always runs in the invoking session's cwd; a per-call cwd only sets the worker's process cwd (e.g. a git worktree) — the resolved roster is identical for every cwd.",
    "Modes: single (agent + task), parallel (tasks array), chain (sequential with {previous} placeholder).",
    "Single mode: background=true forces the detached tmux-backed job (results via job_status), background=false forces foreground, omitted = the agent config's background frontmatter decides; rejected with tasks/chain.",
    "Batching norm: several independent delegations → spawn all in one turn (parallel tasks[] for foreground work, one background spawn for the long orchestrator task); sequential spawn-then-wait is blocking with extra steps.",
    "Handoff (single background mode): handoff:\"auto\"|\"confirm\" is forwarded into the orchestrator task prompt as HANDOFF=..., overriding a HANDOFF mention in the task text; omitted keeps the task-text convention (no mention = confirm). Rejected with tasks/chain and foreground runs.",
    "Task packets: a task text may carry at most ONE `SPEC-SCOPE:` line and one `CLAIMS-PATHS:` line (grammar: WORKFLOW.md § Task packets); a malformed packet REFUSES the spawn with an error naming the offending line.",
    `Default agent scope is "both": project-local agents (${CONFIG_DIR_NAME}/agents) override user agents (${path.join(getAgentDir(), "agents")}) with the same name.`,
    `Set agentScope: "user" to ignore project-local agents.`,
  ].join(" ");
}

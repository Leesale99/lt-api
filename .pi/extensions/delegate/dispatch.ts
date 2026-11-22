/**
 * DISPATCH layer — the `subagent` tool (and its two job-side siblings).
 *
 * What lives here (moved verbatim out of index.ts by B3.4, behavior unchanged):
 *
 *   1. THE TOOL SCHEMA + DESCRIPTION — `SubagentParams` and the model-facing
 *      description (./persona.ts `subagentToolDescription`), so the contract the
 *      model sees and the execute body that enforces it sit together.
 *   2. MODE BRANCHING — single (`agent` + `task`, foreground or detached into a
 *      tmux-backed background job), parallel (`tasks[]`, bounded concurrency),
 *      chain (`chain[]`, `{previous}` substitution, stop on the first failure).
 *   3. THE TASK-PACKET BOUNDARY (B3.1) — the ONE parse of the task-packet
 *      grammar (./protocol.ts owns it). Every task text in one call is parsed
 *      BEFORE agent resolution, claim writes, sandbox creation or any child
 *      spawn; a malformed packet refuses the whole call. The normalized scope
 *      env is threaded into ./spawn.ts, so nothing downstream re-parses.
 *   4. CLAIMS — prepare before the first child launches, release exactly once
 *      per claim on EVERY exit path (a completed claim is what makes it
 *      inactive; an orphaned active claim would block the next worker).
 *   5. REFUSALS — the tool-result shapes for bad parameters, an unknown agent,
 *      a malformed packet, a claim conflict and a background/handoff misuse.
 *
 * Also here because they are the same tool surface: `job_status` (poll cooldown
 * included) and `job_approve` (the parent-relayed approval). Their REGISTRY and
 * relay logic stay in ./jobs.ts; this module only owns the pi wiring.
 *
 * Import direction: ./agents.ts, ./claims.ts, ./jobs.ts, ./output.ts,
 * ./persona.ts, ./policy.ts, ./protocol.ts, ./spawn.ts, ./surfacing.ts (the
 * renderers registered with the tools) and ./tui/. Nothing here imports the
 * root, so the graph stays acyclic: index.ts → dispatch.ts → spawn.ts.
 *
 * Loader gotcha (pi 1.5.2 extension parser): no `?:` ternaries with
 * double-quoted strings inside a template literal's ${} — this module sticks to
 * string concatenation throughout.
 */

import * as fs from "node:fs";
import * as path from "node:path";
import type { AgentToolResult } from "@earendil-works/pi-agent-core";
import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";
import { Type } from "typebox";
import { type AgentConfig, type AgentScope, discoverAgents } from "./agents.ts";
import {
  type ClaimRecord,
  type SpawnRequest,
  completeSpawnClaim,
  formatClaimConflict,
  jobIdForEnv,
  prepareSpawnClaims,
} from "./claims.ts";
import {
  type JobEntry,
  type JobStatus,
  formatElapsed,
  formatJobDetail,
  formatJobList,
  getJob,
  jobsDirFor,
  listJobEntries,
  listJobs,
  peekJobEntry,
  relayApprovalDecision,
  startBackgroundJob,
} from "./jobs.ts";
import { emptyUsage, getFinalOutput, truncateParallelOutput } from "./output.ts";
import { buildTaskPrompt, subagentToolDescription } from "./persona.ts";
import { loadWorkflowPolicy } from "./policy.ts";
import { type TaskPacket, formatTaskPacketRefusal, parseTaskPacket } from "./protocol.ts";
import {
  type DispatchDefaults,
  type OnUpdateCallback,
  type ResolvedAgent,
  type SingleResult,
  type SubagentDetails,
  formatHostVerification,
  getResultOutput,
  resolveAgent,
  runSingleAgent,
  truncateModelOutput,
  withHostVerification,
} from "./spawn.ts";
import {
  beginJobPickup,
  buildJobResultInjection,
  cardDataOf,
  emitJobCard,
  endJobPickup,
  hasSurfacedJob,
  isFailedResult,
  isJobPickupInFlight,
  markJobSurfaced,
  renderJobApproveCall,
  renderJobApproveResult,
  renderJobStatusCall,
  renderJobStatusResult,
  renderSubagentCall,
  renderSubagentResult,
} from "./surfacing.ts";
import { refreshJobWidget } from "./tui/index.ts";

const MAX_PARALLEL_TASKS = 8;
/**
 * Batch 3: cap on tasks[] in one subagent call. The WORKFLOW.md field
 * `max_concurrent_workers` wins when present; otherwise today's constant.
 */
function maxParallelTasksFor(cwd: string): number {
  const loaded = loadWorkflowPolicy(cwd);
  if (loaded.ok && loaded.declared.has("max_concurrent_workers")) {
    return Math.max(1, loaded.policy.maxConcurrentWorkers);
  }
  return MAX_PARALLEL_TASKS;
}

const MAX_CONCURRENCY = 4;
/** Poll interval (ms) for background-job completion notifications. */
const JOB_POLL_MS = 10_000;
/** Default job_status poll cooldown (ms) per running job id (phase 8). */
const POLL_COOLDOWN_MS_DEFAULT = 60_000;
/** Consecutive-refusal doublings cap: cooldown at most 32x base (~32 min at default) so a
 * genuinely stuck job stays inspectable via /jobs and the artifacts; polling itself stays awkward. */
const POLL_COOLDOWN_MAX_DOUBLINGS = 5;

/** How long a job_status poll cooldown lasts: env override, 0 disables, invalid/negative → default. */
function pollCooldownMs(): number {
  const raw = Number(process.env.PI_DELEGATE_POLL_COOLDOWN_MS);
  if (!Number.isFinite(raw) || raw < 0) return POLL_COOLDOWN_MS_DEFAULT;
  return raw;
}

async function mapWithConcurrencyLimit<TIn, TOut>(
  items: TIn[],
  concurrency: number,
  fn: (item: TIn, index: number) => Promise<TOut>,
): Promise<TOut[]> {
  if (items.length === 0) return [];
  const limit = Math.max(1, Math.min(concurrency, items.length));
  const results: TOut[] = new Array(items.length);
  let nextIndex = 0;
  const workers = new Array(limit).fill(null).map(async () => {
    while (true) {
      const current = nextIndex++;
      if (current >= items.length) return;
      results[current] = await fn(items[current], current);
    }
  });
  await Promise.all(workers);
  return results;
}

const TaskItem = Type.Object({
  agent: Type.String({ description: "Name of the agent to invoke" }),
  task: Type.String({ description: "Task to delegate to the agent" }),
  cwd: Type.Optional(Type.String({ description: "Working directory for the agent process" })),
});

const ChainItem = Type.Object({
  agent: Type.String({ description: "Name of the agent to invoke" }),
  task: Type.String({ description: "Task with optional {previous} placeholder for prior output" }),
  cwd: Type.Optional(Type.String({ description: "Working directory for the agent process" })),
});

// Typebox-native so the static type resolves to the AgentScope union from
// ./agents.ts (pi-ai's StringEnum wraps pi-schema Schema, which Type.Optional
// reduces to {}).
// Keeps the same runtime JSON schema { type: "string", enum: [...] } — provider-compatible.
const AgentScopeSchema = Type.Unsafe<AgentScope>({
  type: "string",
  enum: ["user", "project", "both"],
  description: 'Which agent directories to use. Default: "both" — project-local agents (.pi/agents) override user agents with the same name.',
  default: "both",
});

const SubagentParams = Type.Object({
  agent: Type.Optional(Type.String({ description: "Name of the agent to invoke (for single mode)" })),
  task: Type.Optional(Type.String({ description: "Task to delegate (for single mode)" })),
  tasks: Type.Optional(Type.Array(TaskItem, { description: "Array of {agent, task} for parallel execution" })),
  chain: Type.Optional(Type.Array(ChainItem, { description: "Array of {agent, task} for sequential execution" })),
  agentScope: Type.Optional(AgentScopeSchema),
  confirmProjectAgents: Type.Optional(
    Type.Boolean({ description: "Prompt before running project-local agents. Default: true.", default: true }),
  ),
  cwd: Type.Optional(Type.String({ description: "Working directory for the agent process (single mode)" })),
  background: Type.Optional(
    Type.Boolean({
      description:
        "Single mode only: true forces the detached background path (tmux-backed job, results via job_status), false forces foreground. Omit to let the agent config's background frontmatter decide. Rejected together with tasks or chain.",
    }),
  ),
  handoff: Type.Optional(
    Type.Unsafe<"auto" | "confirm">({
      type: "string",
      enum: ["auto", "confirm"],
      description:
        'Handoff mode for a single-mode background job (the orchestrator): forwarded into the task prompt as HANDOFF=..., overriding any HANDOFF mention in the task text. Omitted → the task-text convention applies (no mention = confirm). Rejected with tasks/chain and with foreground runs.',
    }),
  ),
});

const JobStatusParams = Type.Object({
  id: Type.Optional(Type.String({ description: "Job id to inspect. Omit to list all background jobs." })),
});

const JobApproveParams = Type.Object({
  id: Type.String({ description: "Job id of the awaiting_approval background job" }),
  message: Type.String({ description: 'Decision forwarded verbatim into the orchestrator pane: "approved [note]" approves the pending handoff; "rejected: <reason>" sends the orchestrator back to iterate. Single line.' }),
});

/**
 * Details for the single-mode background branch (phase 3).
 */
interface BackgroundJobDetails {
  mode: "background";
  job: JobEntry;
  /** Start-failure path only: lets the tool_result hook map runtime error state. */
  isError?: boolean;
}

/** Either shape a tool result can carry; the renderers branch on `mode`. */
export type AnyToolDetails = SubagentDetails | BackgroundJobDetails;

/** Last job_status poll of one job id: when, what the registry read, consecutive refusals. */
interface PollRecord {
  at: number;
  status: JobStatus;
  finishedAt?: string;
  refusals: number;
}
/** Per-job-id poll history for the cooldown (phase 8). Small session-scale map — entries are
 * deleted as soon as a poll observes a non-running status, so no pruning is needed. */
const pollRecords = new Map<string, PollRecord>();

/**
 * Phase 3: detach a single-mode run into a background job.
 *
 * The full child prompt (skill-expanded) is written to `<id>.task` so arbitrary
 * quoting is safe — the task text never passes through a shell string. The
 * child runs `--session-id pi-<id>` (resumable), NOT `--no-session`.
 * Returns immediately with the job id; a poller notifies the UI (ctx.ui.notify
 * + footer status line) when the job finishes. Results reach the session as
 * delegate-job-result context messages: explicitly via job_status (pickup) and
 * automatically at the settle boundary for jobs finished this session.
 *
 * Phase 7: the optional `handoff` param ("auto" | "confirm", single-mode
 * background jobs only) is appended to the prompt as a HANDOFF=... line AFTER
 * the task text, overriding any HANDOFF mention there; omitted keeps the
 * task-text convention (no mention = confirm).
 */
async function startBackgroundRun(
  dispatchDefaults: DispatchDefaults,
  agent: AgentConfig,
  task: string,
  ctx: ExtensionContext,
  handoff?: "auto" | "confirm",
): Promise<AgentToolResult<AnyToolDetails>> {
  const model = agent.model ?? dispatchDefaults.model;
  let thinking: string | undefined;
  if (agent.thinking) thinking = agent.thinking;
  else if (!agent.model && dispatchDefaults.thinkingLevel) thinking = dispatchDefaults.thinkingLevel;

  let prompt = buildTaskPrompt(agent, task);
  if (handoff) {
    // Phase 7: the handoff param is first-class now; it is appended AFTER the
    // task text so it wins over any HANDOFF mention there (the orchestrator
    // protocol reads the mode from the task text; omitted there → confirm).
    prompt += "\n\nHANDOFF=" + handoff + " (handoff mode parameter; overrides any earlier HANDOFF mention in the task text above)";
  }

  let entry: JobEntry;
  try {
    entry = await startBackgroundJob({
      cwd: ctx.cwd,
      agent,
      task,
      prompt,
      model,
      thinking,
    });
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    return {
      content: [{ type: "text", text: "Failed to start background job: " + message }],
      details: {
        mode: "background",
        isError: true,
        job: {
          id: "unknown",
          agent: agent.name,
          task,
          startedAt: new Date().toISOString(),
          runner: "fallback",
          status: "failed",
        },
      },
    };
  }

  // Jobs widget: one line per background job; fire-and-forget (guarded by hasUI
  // inside) so the tool result path never breaks in -p/json mode.
  void refreshJobWidget(ctx);
  // Phase 4: a "running" card for the fresh job (the finish adds a second one).
  emitJobCard(ctx, entry);

  // Completion surfacing: poll until the job leaves "running", then notify and
  // set a footer status line. The interval DOES tick in -p/json mode, but the
  // notify/status/card/widget calls inside are no-ops there (no-op UI context,
  // hasUI-gated emitJobCard). The timer is unref'd so it cannot keep a process
  // alive. Each tick also refreshes the jobs widget so running elapsed times
  // stay current.
  let settled = false;
  const pollTimer = setInterval(() => {
    void refreshJobWidget(ctx);
    if (settled) return;
    void getJob(ctx.cwd, entry.id)
      .then((current) => {
        if (!current || current.status === "running" || settled) return;
        settled = true;
        clearInterval(pollTimer);
        let kind: "info" | "warning" | "error" = "info";
        let statusLine: string = current.status;
        if (current.status === "awaiting_approval") {
          // Phase 7: the wait for the user's decision is its own status —
          // notify with approval-oriented phrasing, warning-colored.
          kind = "warning";
          statusLine = "awaiting_approval — approve via job_approve (or in the pane)";
        } else if (current.status !== "done") {
          kind = "error";
        }
        try {
          ctx.ui.notify("Background job " + entry.id + " " + statusLine, kind);
          ctx.ui.setStatus("delegate-jobs", "bg job " + entry.id + ": " + current.status);
        } catch {
          /* UI may be gone; best effort */
        }
        emitJobCard(ctx, current);
        void refreshJobWidget(ctx);
      })
      .catch(() => {
        /* transient read errors — try again next tick */
      });
  }, JOB_POLL_MS);
  pollTimer.unref();

  const text =
    "Background job started: " + entry.id +
    "\nagent: " + entry.agent +
    "\nrunner: " + entry.runner + (entry.tmuxSession ? " (tmux session: " + entry.tmuxSession + ")" : "") +
    (entry.runner === "tmux"
      ? "\nWatch live: tmux attach -t " + entry.tmuxSession + " (pi runs interactively; quit pi in the pane when done)"
      : "") +
    "\nThe task runs detached; retrieve results with the job_status tool using id \"" + entry.id + "\"." +
    "\nArtifacts are in .pi/jobs/ (" + entry.id + ".task/.jsonl/.err/.exit/.json)." +
    // Phase 8 standing rule, part of the guaranteed-read tool result text: the
    // parent must never wait on the job. Plain string concatenation (extension
    // loader rejects nested quoting in template literals).
    "\nJob `" + entry.id + "` runs detached: do NOT poll or wait. End your turn or pick up other work; completion is surfaced automatically (refuse-to-wait enforced by cooldown on job_status).";
  return {
    content: [{ type: "text", text }],
    details: { mode: "background", job: entry },
  };
}

/**
 * Register the `subagent` tool. The CALLER guards the worker case (a worker
 * child gets no subagent tool at all, so it cannot recurse); everything the
 * tool does lives here.
 */
export function registerSubagentTool(pi: ExtensionAPI): void {
  pi.registerTool({
    name: "subagent",
    label: "Subagent",
    description: subagentToolDescription(),
    parameters: SubagentParams,

    async execute(_toolCallId, params, signal, onUpdate, ctx) {
      const agentScope: AgentScope = params.agentScope ?? "both";
      const dispatchDefaults: DispatchDefaults = {
        model: ctx.model ? `${ctx.model.provider}/${ctx.model.id}` : undefined,
        thinkingLevel: ctx.thinkingLevel,
      };
      const discovery = discoverAgents(ctx.cwd, agentScope);
      const agents = discovery.agents;
      const confirmProjectAgents = params.confirmProjectAgents ?? true;

      const hasChain = (params.chain?.length ?? 0) > 0;
      const hasTasks = (params.tasks?.length ?? 0) > 0;
      const hasSingle = Boolean(params.agent && params.task);
      const modeCount = Number(hasChain) + Number(hasTasks) + Number(hasSingle);

      const makeDetails =
        (mode: "single" | "parallel" | "chain", forceError = false) =>
          (results: SingleResult[]): SubagentDetails => ({
            mode,
            results,
            isError: forceError || results.some(isFailedResult),
          });

      if (modeCount !== 1) {
        const available = agents.map((a) => `${a.name} (${a.source})`).join(", ") || "none";
        return {
          content: [
            {
              type: "text",
              text: `Invalid parameters. Provide exactly one mode.\nAvailable agents: ${available}`,
            },
          ],
          details: makeDetails("single", true)([]),
        };
      }

      if (params.background !== undefined && (hasChain || hasTasks)) {
        const available = agents.map((a) => `${a.name} (${a.source})`).join(", ") || "none";
        return {
          content: [
            {
              type: "text",
              text: "Invalid parameters. background applies to single mode only.\nAvailable agents: " + available,
            },
          ],
          details: makeDetails(hasChain ? "chain" : hasTasks ? "parallel" : "single", true)([]),
        };
      }

      if (params.handoff !== undefined && (hasChain || hasTasks)) {
        const available = agents.map((a) => `${a.name} (${a.source})`).join(", ") || "none";
        return {
          content: [
            {
              type: "text",
              text: "Invalid parameters. handoff applies to single mode only.\nAvailable agents: " + available,
            },
          ],
          details: makeDetails(hasChain ? "chain" : hasTasks ? "parallel" : "single", true)([]),
        };
      }

      // TASK-PACKET BOUNDARY (B3.1): the ONE parse of the task-packet grammar
      // (./protocol.ts owns it). Every task text in this call — single, each
      // tasks[] entry, each chain step — is parsed here, before agent
      // resolution, claim writes, sandbox creation or any child spawn. A
      // malformed SPEC-SCOPE / CLAIMS-PATHS packet REFUSES the whole call and
      // the tool result names the offending line (fail-closed at spawn). The
      // normalized scope env computed here is threaded into runSingleAgent, so
      // nothing downstream re-parses the task text.
      const packetErrors: string[] = [];
      const packetOf = (label: string, task: string): TaskPacket | undefined => {
        const parsed = parseTaskPacket(task);
        if (parsed.ok) return parsed.packet;
        for (const error of parsed.errors) packetErrors.push(label + ": " + error);
        return undefined;
      };
      const chainPackets = (params.chain ?? []).map((step, index) =>
        packetOf("chain step " + String(index + 1) + " (" + step.agent + ")", step.task),
      );
      const taskPackets = (params.tasks ?? []).map((entry, index) =>
        packetOf("tasks[" + String(index) + "] (" + entry.agent + ")", entry.task),
      );
      const singlePacket = params.agent && params.task
        ? packetOf("task (" + params.agent + ")", params.task)
        : undefined;
      if (packetErrors.length > 0) {
        return {
          content: [{ type: "text", text: formatTaskPacketRefusal(packetErrors) }],
          details: makeDetails(hasChain ? "chain" : hasTasks ? "parallel" : "single", true)([]),
        };
      }

      const resolvedChain = (params.chain ?? []).map((step) =>
        resolveAgent(ctx.cwd, agentScope, step.agent, step.cwd),
      );
      const resolvedTasks = (params.tasks ?? []).map((task) =>
        resolveAgent(ctx.cwd, agentScope, task.agent, task.cwd),
      );
      const resolvedSingle = params.agent ? resolveAgent(ctx.cwd, agentScope, params.agent, params.cwd) : undefined;

      if ((agentScope === "project" || agentScope === "both") && confirmProjectAgents && ctx.hasUI) {
        let selectedAgents: ResolvedAgent[] = [];
        if (hasChain) selectedAgents = resolvedChain;
        else if (hasTasks) selectedAgents = resolvedTasks;
        else if (resolvedSingle) selectedAgents = [resolvedSingle];
        const projectAgentsByPath = new Map<string, AgentConfig>();
        for (const resolved of selectedAgents) {
          if (resolved.agent?.source === "project") {
            projectAgentsByPath.set(resolved.agent.filePath, resolved.agent);
          }
        }

        const trustedProjectAgentsDir =
          ctx.isProjectTrusted() && discovery.projectAgentsDir
            ? fs.realpathSync(discovery.projectAgentsDir)
            : undefined;

        const projectAgentsRequested = Array.from(projectAgentsByPath.values()).filter(
          (agent) =>
            !trustedProjectAgentsDir || fs.realpathSync(path.dirname(agent.filePath)) !== trustedProjectAgentsDir,
        );
        if (projectAgentsRequested.length > 0) {
          const sources = projectAgentsRequested.map((agent) => `- ${agent.name}: ${agent.filePath}`).join("\n");
          const ok = await ctx.ui.confirm(
            "Run project-local agents?",
            `Sources:\n${sources}\n\nProject agents are repo-controlled. Only continue for trusted repositories.`,
          );
          if (!ok) {
            return {
              content: [{ type: "text", text: "Canceled: project-local agents not approved." }],
              details: makeDetails(hasChain ? "chain" : hasTasks ? "parallel" : "single")([]),
            };
          }
        }
      }

      // Batch 2: claims log + overlap blocking. Foreground single and tasks[]
      // spawns claim their SPEC-SCOPE / CLAIMS-PATHS before any child
      // launches; an overlapping ACTIVE claim blocks the WHOLE call (no child
      // starts, no claim is written). Background orchestrator spawns are not
      // claimed here: their lifetime is tracked by the job registry, not by
      // this tool result, so a claim would stay active for the whole job.
      // Parameter errors that return before a child exists are handled ABOVE
      // (the MAX_PARALLEL_TASKS guard below too): a claim must never outlive a
      // refused call.
      if (hasTasks && params.tasks && params.tasks.length > maxParallelTasksFor(ctx.cwd)) {
        const maxTasks = maxParallelTasksFor(ctx.cwd);
        return {
          content: [
            {
              type: "text",
              text: `Too many parallel tasks (${params.tasks.length}). Max is ${maxTasks}.`,
            },
          ],
          details: makeDetails("parallel", true)([]),
        };
      }
      const claimsJobId = jobIdForEnv();
      const claimsSessionId = process.env.PI_SESSION_ID ?? "";
      const singleAgentForClaims = resolvedSingle?.agent;
      const singleIsBackground = Boolean(
        hasSingle && singleAgentForClaims && (params.background ?? singleAgentForClaims.background),
      );
      const spawnRequests: SpawnRequest[] = hasTasks
        ? (params.tasks ?? []).map((entry) => ({ worker: entry.agent, task: entry.task }))
        : hasSingle && singleAgentForClaims && !singleIsBackground && params.agent && params.task
          ? [{ worker: params.agent, task: params.task }]
          : [];
      let spawnClaims: ClaimRecord[] = [];
      const releasedClaimIds = new Set<string>();
      // Release exactly once per claim: the completion record is what makes a
      // claim inactive, so it must be appended on every exit path (a crashed
      // spawn included) — an orphaned active claim would block the next worker.
      const releaseClaim = async (claim: ClaimRecord | undefined, ok: boolean): Promise<void> => {
        if (!claim || releasedClaimIds.has(claim.claimId)) return;
        releasedClaimIds.add(claim.claimId);
        try {
          await completeSpawnClaim({ cwd: ctx.cwd, jobId: claimsJobId, claim, ok });
        } catch (error) {
          console.error("[delegate] claim completion write failed:", error);
        }
      };
      if (spawnRequests.length > 0) {
        const prepared = await prepareSpawnClaims({
          cwd: ctx.cwd,
          jobId: claimsJobId,
          sessionId: claimsSessionId,
          requests: spawnRequests,
        });
        if (prepared.blocked.length > 0) {
          const text = prepared.blocked.map((conflict) => formatClaimConflict(conflict)).join("\n\n");
          return {
            content: [{ type: "text", text }],
            details: makeDetails(hasTasks ? "parallel" : "single", true)([]),
          };
        }
        spawnClaims = prepared.claims;
      }

      if (params.chain && params.chain.length > 0) {
        const results: SingleResult[] = [];
        let previousOutput = "";

        for (let i = 0; i < params.chain.length; i++) {
          const step = params.chain[i];
          const taskWithContext = step.task.replace(/\{previous\}/g, previousOutput);

          // Create update callback that includes all previous results
          const chainUpdate: OnUpdateCallback | undefined = onUpdate
            ? (partial) => {
              // Combine completed results with current streaming result
              const currentResult = partial.details?.results[0];
              if (currentResult) {
                const allResults = [...results, currentResult];
                onUpdate({
                  content: partial.content,
                  details: makeDetails("chain")(allResults),
                });
              }
            }
            : undefined;

          const result = await runSingleAgent(
            dispatchDefaults,
            resolvedChain[i],
            step.agent,
            taskWithContext,
            i + 1,
            signal,
            chainUpdate,
            makeDetails("chain"),
            ctx,
            chainPackets[i]?.scopeEnv,
          );
          results.push(result);

          const isError = isFailedResult(result);
          if (isError) {
            const errorMsg = getResultOutput(result);
            return {
              content: [
                {
                  type: "text",
                  text: truncateModelOutput(
                    `Chain stopped at step ${i + 1} (${step.agent}): ${errorMsg}`,
                  ) + (formatHostVerification(result) ? "\n\n" + formatHostVerification(result) : ""),
                },
              ],
              details: makeDetails("chain")(results),
            };
          }
          previousOutput = getFinalOutput(result.messages);
        }
        return {
          content: [
            {
              type: "text",
              text: truncateModelOutput(
                withHostVerification(
                  getFinalOutput(results[results.length - 1].messages) || "(no output)",
                  ...results,
                ),
              ),
            },
          ],
          details: makeDetails("chain")(results),
        };
      }

      if (params.tasks && params.tasks.length > 0) {
        const maxTasks = maxParallelTasksFor(ctx.cwd);
        if (params.tasks.length > maxTasks)
          return {
            content: [
              {
                type: "text",
                text: `Too many parallel tasks (${params.tasks.length}). Max is ${maxTasks}.`,
              },
            ],
            details: makeDetails("parallel", true)([]),
          };

        // Track all results for streaming updates
        const allResults: SingleResult[] = new Array(params.tasks.length);

        // Initialize placeholder results
        for (let i = 0; i < params.tasks.length; i++) {
          allResults[i] = {
            agent: params.tasks[i].agent,
            agentSource: "unknown",
            task: params.tasks[i].task,
            status: "pending",
            exitCode: null,
            messages: [],
            stderr: "",
            usage: emptyUsage(),
          };
        }

        const emitParallelUpdate = () => {
          if (onUpdate) {
            const running = allResults.filter((result) => result.status === "running").length;
            const pending = allResults.filter((result) => result.status === "pending").length;
            const done = allResults.length - running - pending;
            onUpdate({
              content: [
                {
                  type: "text",
                  text: `Parallel: ${done}/${allResults.length} done, ${running} running, ${pending} pending...`,
                },
              ],
              details: makeDetails("parallel")([...allResults]),
            });
          }
        };

        const results = await mapWithConcurrencyLimit(params.tasks, MAX_CONCURRENCY, async (t, index) => {
          let result: SingleResult;
          try {
            result = await runSingleAgent(
              dispatchDefaults,
              resolvedTasks[index],
              t.agent,
              t.task,
              undefined,
              signal,
              // Per-task update callback
              (partial) => {
                if (partial.details?.results[0]) {
                  allResults[index] = partial.details.results[0];
                  emitParallelUpdate();
                }
              },
              makeDetails("parallel"),
              ctx,
              taskPackets[index]?.scopeEnv,
            );
          } catch (error) {
            // A thrown spawn must still release its claim (fail-open telemetry).
            await releaseClaim(spawnClaims[index], false);
            throw error;
          }
          allResults[index] = result;
          emitParallelUpdate();
          // Batch 2: release the claim with the paths that actually changed.
          await releaseClaim(spawnClaims[index], !isFailedResult(result));
          return result;
        });

        const successCount = results.filter((r) => !isFailedResult(r)).length;
        const summaries = results.map((r) => {
          const output = truncateParallelOutput(getResultOutput(r));
          const status =
            isFailedResult(r) && r.stopReason && r.stopReason !== "end"
              ? `${r.status} (${r.stopReason})`
              : r.status;
          return withHostVerification(`### [${r.agent}] ${status}\n\n${output}`, r);
        });
        return {
          content: [
            {
              type: "text",
              text: `Parallel: ${successCount}/${results.length} succeeded\n\n${summaries.join("\n\n---\n\n")}`,
            },
          ],
          details: makeDetails("parallel")(results),
        };
      }

      if (params.agent && params.task && resolvedSingle) {
        // Phase 3 + polish: single-mode runs detach into a tmux-backed job when
        // the per-call `background` param (true = force background, false =
        // force foreground) or the agent's `background: true` frontmatter asks
        // for it (parallel/chain stay foreground by decision).
        // Phase 5 two-tier: background spawning is a code-enforced privilege of
        // agents with `background: true` frontmatter (roster-wise: the child
        // orchestrator) — a background request on any other agent is refused
        // with routing guidance, even when the parent passes background:true
        // explicitly. One level deep: a child orchestrator (role env) cannot
        // background-spawn either; the main session is the recursion point.
        const singleAgent = resolvedSingle.agent;
        if (params.handoff !== undefined && !(singleAgent && (params.background ?? singleAgent.background))) {
          return {
            content: [
              {
                type: "text",
                text: "Invalid parameters. handoff applies to single-mode background jobs only (the orchestrator agent, background: true).",
              },
            ],
            details: makeDetails("single", true)([]),
          };
        }
        if (singleAgent && (params.background ?? singleAgent.background)) {
          if (!singleAgent.background) {
            const available = agents.map((a) => `${a.name} (${a.source})`).join(", ") || "none";
            return {
              content: [
                {
                  type: "text",
                  text:
                    "Refused: background jobs are reserved for agents with 'background: true' frontmatter (roster-wise: the orchestrator). " +
                    singleAgent.name + " is a foreground worker — delegate narrow tasks via foreground subagent calls (single, or independent items together in ONE parallel tasks[] call). " +
                    "Background is only for long, multi-step, multi-file work that will take minutes; route those to the orchestrator agent." +
                    "\nAvailable agents: " + available,
                },
              ],
              details: makeDetails("single", true)([]),
            };
          }
          if (process.env.PI_DELEGATE_ROLE === "orchestrator") {
            return {
              content: [
                {
                  type: "text",
                  text:
                    "Refused: one level deep — a child orchestrator cannot start background jobs. " +
                    "Delegate via foreground subagent calls (parallel tasks[] for independent work) instead; background spawning happens only in the main session, which is the recursion point.",
                },
              ],
              details: makeDetails("single", true)([]),
            };
          }
          return await startBackgroundRun(dispatchDefaults, singleAgent, params.task, ctx, params.handoff);
        }
        let result: SingleResult;
        try {
          result = await runSingleAgent(
            dispatchDefaults,
            resolvedSingle,
            params.agent,
            params.task,
            undefined,
            signal,
            onUpdate,
            makeDetails("single"),
            ctx,
            singlePacket?.scopeEnv,
          );
        } catch (error) {
          // A thrown spawn must still release its claim (fail-open telemetry).
          await releaseClaim(spawnClaims[0], false);
          throw error;
        }
        // Batch 2: release the claim with the paths that actually changed.
        await releaseClaim(spawnClaims[0], !isFailedResult(result));
        const isError = isFailedResult(result);
        if (isError) {
          const errorMsg = getResultOutput(result);
          return {
            content: [
              {
                type: "text",
                text: truncateModelOutput(
                  `Agent ${result.stopReason || result.status}: ${errorMsg}` +
                    (formatHostVerification(result) ? "\n\n" + formatHostVerification(result) : ""),
                ),
              },
            ],
            details: makeDetails("single")([result]),
          };
        }
        return {
          content: [
            {
              type: "text",
              text: truncateModelOutput(
                withHostVerification(getFinalOutput(result.messages) || "(no output)", result),
              ),
            },
          ],
          details: makeDetails("single")([result]),
        };
      }

      const available = agents.map((a) => `${a.name} (${a.source})`).join(", ") || "none";
      return {
        content: [{ type: "text", text: `Invalid parameters. Available agents: ${available}` }],
        details: makeDetails("single", true)([]),
      };
    },

    renderCall: renderSubagentCall,
    renderResult: renderSubagentResult,

  });
}

/**
 * Phase 3: pick up background job results on demand. No id → list all jobs;
 * id → per-job report (running/done/failed/aborted) from the .pi/jobs artifacts.
 * Phase 4: a specific FINISHED job additionally injects its result as a
 * delegate-job-result custom message (LLM context, no turn triggered) —
 * exactly once per job per session (surfacedJobIds).
 */
export function registerJobStatusTool(pi: ExtensionAPI): void {
  pi.registerTool({
    name: "job_status",
    label: "Job status",
    description:
      "Inspect background subagent jobs (artifacts in .pi/jobs/). " +
      "Omit id to list all jobs (id, status, agent, task preview, elapsed). " +
      "With id: for done jobs returns the final assistant output; for failed jobs exit code + stderr + last events; for running jobs current activity; for awaiting_approval jobs (confirm handoff) returns the orchestrator's approval request — check its `HEAD SHA: <sha>` line, then decide via job_approve (which refuses if the worktree HEAD moved). " +
      "On-demand only: for when the user asks or you need one data point — never loop-wait on a job. A repeat poll of the same unchanged running job is refused by a cooldown (doubling on consecutive refusals), and completion arrives automatically as an injected message at the next turn boundary.",
    parameters: JobStatusParams,
    async execute(_toolCallId, params, _signal, _onUpdate, ctx) {
      // Every inspection also refreshes the jobs widget (fire-and-forget; no-op
      // without UI).
      void refreshJobWidget(ctx);
      if (params.id) {
        const requested = params.id.trim();
        // Raw registry read BEFORE getJob(): getJob's refreshStatus advances a
        // stale "running" entry to its terminal status; the diff is what emits
        // this inspection's finish card (deduped against poller/settle cards).
        const before = await peekJobEntry(ctx.cwd, requested);
        const entry = await getJob(ctx.cwd, requested);
        if (!entry) {
          const all = await listJobs(ctx.cwd);
          const known = all.map((e) => e.id).join(", ") || "none";
          return {
            content: [{ type: "text", text: "No job with id " + requested + ". Known jobs: " + known }],
            details: { jobs: all.map(cardDataOf) },
          };
        }
        // Phase 8 poll cooldown: a repeat job_status poll of the SAME unchanged
        // running job within the cooldown window gets a canned refusal instead
        // of data — make waiting structurally awkward, don't just forbid it.
        // Cooldown never applies to terminal or awaiting_approval jobs: their
        // pickups (and the approval flow) must keep working.
        // A refused poll is NOT a pickup: it returns before the
        // result-injection branch below, so it never touches surfacedJobIds,
        // never consumes the exactly-once completion surfacing, and does not
        // count as delivery in any mark-AFTER-delivery path. It also skips the
        // card transition below (status is unchanged, so nothing transitioned).
        if (entry.status === "running") {
          const base = pollCooldownMs();
          if (base > 0) {
            const prev = pollRecords.get(entry.id);
            const now = Date.now();
            const unchanged =
              prev !== undefined && prev.status === entry.status && prev.finishedAt === entry.finishedAt;
            // Escalation: refusal #1 keeps the base window; the window DOUBLES
            // from the 2nd consecutive refusal on (and again per further
            // consecutive refusal, capped), so hammering job_status meets
            // progressively longer silences. An allowed poll resets the count.
            const multiplier = prev
              ? Math.pow(2, Math.min(Math.max(prev.refusals - 1, 0), POLL_COOLDOWN_MAX_DOUBLINGS))
              : 1;
            if (unchanged && prev && now - prev.at < base * multiplier) {
              prev.refusals += 1;
              prev.at = now;
              const elapsed = formatElapsed(entry.startedAt);
              const text = prev.refusals === 1
                ? "Still running (elapsed " + elapsed + "). You will be surfaced on completion — stop polling."
                : "Refused again. Job " + entry.id + " is unchanged since your last poll (elapsed " + elapsed + "). Completion is surfaced automatically at the next turn boundary — further polls are refused with a doubling cooldown. Pick up other work or end your turn.";
              return {
                content: [{ type: "text", text }],
                details: { jobs: [cardDataOf(entry)], refused: true },
              };
            }
            pollRecords.set(entry.id, {
              at: now,
              status: entry.status,
              finishedAt: entry.finishedAt,
              refusals: 0,
            });
          }
        } else {
          // Cooldown bookkeeping ends with the running state; terminal and
          // awaiting_approval polls are always allowed (and drop the record).
          pollRecords.delete(entry.id);
        }

        if (before && before.status === "running" && entry.status !== "running") {
          emitJobCard(ctx, entry);
        }
        if (
          entry.status !== "running" &&
          !process.env.PI_DELEGATE_CHILD &&
          !hasSurfacedJob(entry.id) &&
          !isJobPickupInFlight(entry.id)
        ) {
          // Synchronous guard FIRST (before any await): parallel same-turn
          // job_status calls for the same id must not both inject (phase 8
          // probe caught exactly that double delivery).
          beginJobPickup(entry.id);
          try {
            const injection = await buildJobResultInjection(ctx.cwd, entry);
            // No options: when idle this only stores the message; while this
            // run is streaming it steers (delivered before the next LLM call) —
            // never triggers a turn of its own.
            pi.sendMessage({
              customType: "delegate-job-result",
              content: injection.content,
              display: true,
              details: injection.details,
            });
            // Mark AFTER delivery: sendMessage is synchronous (void return), so
            // reaching this line means the message was handed to the session.
            // A throw above leaves the id unmarked — safe, because job_status
            // runs only when the model calls it (no auto-retry loop here).
            markJobSurfaced(entry.id);
          } catch (error) {
            console.error(
              "[delegate] job_status result pickup failed for " + entry.id + "; id left unmarked:",
              error,
            );
            /* pickup is best effort; the tool report below still returns */
          } finally {
            endJobPickup(entry.id);
          }
        }
        const report = await formatJobDetail(entry, jobsDirFor(ctx.cwd));
        return { content: [{ type: "text", text: report }], details: { jobs: [cardDataOf(entry)] } };
      }
      const entries = await listJobEntries(ctx.cwd);
      if (entries.length === 0) {
        return { content: [{ type: "text", text: "No background jobs (.pi/jobs/ has no registry entries)." }], details: { jobs: [] } };
      }
      return { content: [{ type: "text", text: formatJobList(entries) }], details: { jobs: entries.map(cardDataOf) } };
    },
    renderCall: renderJobStatusCall,
    renderResult: renderJobStatusResult,
  });
}

/**
 * Phase 7: parent-relayed approval. A confirm-handoff job idles in its pane
 * after emitting AWAITING_APPROVAL; this tool types the decision into that
 * pane from the MAIN session (tmux send-keys -l + Enter), so nobody has to
 * attach. Preconditions and refusal messages live in jobs.ts
 * (relayApprovalDecision); duplicates are never sent automatically — the
 * user decides, and a duplicate delivery is harmless but confusing.
 */
export function registerJobApproveTool(pi: ExtensionAPI): void {
  pi.registerTool({
    name: "job_approve",
    label: "Job approve",
    description:
      "Relay an approval decision to a background orchestrator job that is waiting in confirm handoff mode (status awaiting_approval). " +
      "SHA-BOUND: when the approval block carries a `HEAD SHA: <sha>` line (the job registry records it), the decision is only relayed while the job's worktree HEAD still matches — a moved HEAD makes the approval stale and the relay is refused (the orchestrator emits a fresh AWAITING_APPROVAL block to review). " +
      'On a match the relayed message gets " (bound to <sha>)" appended, so the child pane also sees the binding. Blocks without a HEAD SHA line (legacy) relay unbound. ' +
      "The message is typed verbatim into the job's idle tmux pane (tmux send-keys -l + Enter), exactly as if the user typed it there. " +
      'message "approved [note]" → the orchestrator performs its merge-back and reports HANDOFF_COMPLETE (the job then advances to done); "rejected: <reason>" → it iterates and emits a fresh approval request. ' +
      "Refuses: unknown id, non-tmux job, dead tmux session, mid-round job, a job whose last message carries no approval request, a worktree HEAD that moved since the request, or an approval whose task SPEC-SCOPE names rules that have no VERIFIED prove receipt yet (missing ids are listed; relay `rejected: missing VERIFIED prove receipts for <ids>` so the orchestrator runs the scoped prove and re-requests). " +
      "After delivery, follow up with job_status on the same id to watch the continuation.",
    parameters: JobApproveParams,
    async execute(_toolCallId, params, _signal, _onUpdate, ctx) {
      void refreshJobWidget(ctx);
      const outcome = await relayApprovalDecision(ctx.cwd, params.id, params.message);
      if (!outcome.ok) {
        return { content: [{ type: "text", text: "Refused: " + (outcome.reason ?? "unknown reason") }], details: { delivered: false, id: params.id } };
      }
      const text =
        "Delivered to tmux session " + outcome.session + " (job " + params.id + "): \"" + (outcome.deliveredMessage ?? params.message) + "\"." +
        (outcome.boundSha ? "\nBound to HEAD " + outcome.boundSha + " (the worktree HEAD still matched the approved block)." : "\nNo HEAD SHA recorded in the approval block — relayed unbound (legacy block).") +
        "\nThe orchestrator pane received your decision; follow up with job_status id \"" + params.id + "\" to watch it advance (approval → HANDOFF_COMPLETE, job done; rejection → a fresh approval request).";
      return { content: [{ type: "text", text }], details: { delivered: true, id: params.id, session: outcome.session, boundSha: outcome.boundSha } };
    },
    renderCall: renderJobApproveCall,
    renderResult: renderJobApproveResult,
  });
}

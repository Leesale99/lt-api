/**
 * Subagent Tool - Delegate tasks to specialized agents
 *
 * Spawns a separate `pi` process for each subagent invocation,
 * giving it an isolated context window.
 *
 * Supports three modes:
 *   - Single: { agent: "name", task: "..." } — foreground, or detached in a
 *     tmux-backed background job; a per-call `background` param overrides the
 *     agent frontmatter (true = force background, false = force foreground)
 *   - Parallel: { tasks: [{ agent: "name", task: "..." }, ...] }
 *   - Chain: { chain: [{ agent: "name", task: "... {previous} ..." }, ...] }
 *
 * Uses JSON mode to capture structured output from subagents.
 *
 * Phase 3 adds the `job_status` tool for picking up background job results
 * (runner + registry live in ./jobs.ts; shared child-output parsing in
 * ./output.ts).
 *
 * Phase 4 adds the session-level surfaces: one-line `delegate-job` entry cards
 * (TUI-only, never reach the LLM), `delegate-job-result` context messages
 * injected on explicit job_status pickup of a finished job, and auto-surfacing
 * of finished jobs at the agent_before_settle boundary (continue:true only when
 * something new was surfaced — loop-guarded).
 *
 * Phase 7 adds the `job_approve` tool (parent-relayed approval into a
 * confirm-handoff job's idle pane) and the first-class `awaiting_approval`
 * registry status (refreshStatus in jobs.ts detects the AWAITING_APPROVAL
 * marker; the UI surfaces and the settle injection phrase it as
 * waiting-for-approval).
 *
 * Batch 1 (worker gates + content-bound approvals, all logic in ./gates.ts):
 *   - POST-WRITE gate: a HOST-PASSTHROUGH worker child that successfully
 *     edits/writes a .go, .feature, .yaml or docs/spec/ file gets
 *     `make audit/static` + the scoped spec check appended to its tool result on
 *     failure (debounced ~10s). Sandboxed workers never run this: their
 *     instance is inert and the HOST runs the authoritative ladder after the
 *     sandbox exits.
 *   - WORKER-SETTLE gate: the same two commands at settle, up to 3 attempts
 *     (continue:true is bounded by the attempt counter), then a hard stop.
 *   - Receipts: the EXTENSION appends one JSON line per gate run to
 *     `<host-root>/<receipts_dir>/<iso-date>.jsonl` (commands, exit codes,
 *     rules hash, HEAD). Host-only: an in-sandbox instance writes none.
 *   - PROVE receipts: a bash run of the DECLARED prove command (WORKFLOW.md
 *     `prove_cmd`) carrying `--rule <id>` gets its own receipt, recorded in the
 *     HOST SESSION'S OWN receipts dir (a leading `cd <worktree>` no longer
 *     redirects it — a worker could pre-forge a VERIFIED receipt there).
 *   - TASK PACKETS: the task text's `SPEC-SCOPE` / `CLAIMS-PATHS` lines are
 *     parsed ONCE at the subagent boundary (./protocol.ts, the grammar owner);
 *     a malformed packet refuses the spawn before any claim/sandbox/child
 *     exists. A valid scope is recorded in the claims log at spawn and its
 *     normalized form is exported as PI_DELEGATE_SPEC_SCOPE into the sandbox's
 *     `-e` env (no line = no env var = full spec check).
 *   - job_approve binds the relayed decision to the `HEAD SHA:` recorded in the
 *     job's approval block and refuses when the worktree HEAD moved; for a job
 *     whose recorded scope names rules it also refuses while any scoped rule
 *     lacks a VERIFIED prove receipt (./mergegate.ts `checkProveCoverage`, fed
 *     by the single job-scope resolver there — the same call the merge-path
 *     block makes).
 *
 * Sandbox era (this file):
 *   - Every foreground worker runs inside a per-task Docker Sandbox micro-VM
 *     (./sbx.ts owns the whole CLI): the worktree is mounted per the role's
 *     profile (`researcher` is mountless), the pi invocation is exec'd
 *     foreground, and the stall watchdog owns teardown (`sbx stop`, then
 *     `rm --force`).
 *   - The HOST session is the verifier plane: after a sandboxed exec exits it
 *     runs the integrity check FIRST (`checkWorktreeIntegrity`), refuses the
 *     gates on a dirty result, and otherwise runs the authoritative ladder and
 *     writes the receipts.
 *   - The extension ALSO loads inside the VM and is INERT there
 *     (`isSandboxedChild`): it registers no enforcing hook, no receipts, no
 *     claims, no subagent tool and no job surfaces. It only appends
 *     clearly-marked ADVISORY notes from `runInVmAdvisoryGate`.
 *   - The mount is the boundary, and the integrity check is the net at the
 *     merge boundary; nothing classifies paths or inspects commands.
 *   - MERGE-PATH PROVE: in an orchestrator host session a bash hook blocks a
 *     merge command when the job's recorded SPEC-SCOPE names rules without
 *     VERIFIED host-side prove receipts — independent of how the approval
 *     arrived.
 *
 * Gate-VM era (this file):
 *   - The AUTHORITATIVE gate commands (declared in WORKFLOW.md) no longer run
 *     on the host. They run in a second, locked-down `pi-gate:v1` VM
 *     (./sbx.ts `createGateSandbox`): the host spawns `sbx exec` and nothing
 *     else. A gate command typed into a host bash session is recognised and
 *     REWRITTEN to the equivalent `sbx exec` line (after the host integrity
 *     check and, for `make audit`/`make prove`, the Postgres pre-sequence); the
 *     extension-driven ladder/settle/reconciliation paths call the gate VM
 *     directly. The host keeps only the integrity check (`git`) and receipts.
 *   - Gate VMs are session-scoped and keyed by worktree path (a background
 *     job's fresh worktree gets its own VM); all are torn down at
 *     `session_shutdown`.
 *
 * Module layout (B3.4 — this file is the extension ROOT: env switches, hooks,
 * tool schemas + their execute bodies, and the claim/gate/prove wiring):
 *   - ./persona.ts   prompt assembly — the main-session persona
 *                    (`.pi/agents/main.md`), the child prompt
 *                    (`buildTaskPrompt`, `writePromptToTempFile`) and the
 *                    model-facing `subagent` tool description. NO protocol
 *                    grammar lives there: ./protocol.ts owns it, WORKFLOW.md §
 *                    Task packets documents it.
 *   - ./surfacing.ts the chat surface — job cards, the ONE result-injection
 *                    builder, the exactly-once surfacing bookkeeping, the
 *                    ambient live-plane footer, and every transcript renderer
 *                    (registered here, drawn there).
 *   - ./tui/         the live UI — badges.ts (pure execution-plane helpers),
 *                    widgets.ts (the jobs widget), viewers.ts (/jobs, /agents,
 *                    the scrollable viewer). See ./tui/index.ts.
 *   - ./protocol.ts  the task-packet grammar (one parse, at the spawn boundary).
 *   - ./mergegate.ts THE job-scope resolver + THE prove-receipt policy.
 *   - ./gates.ts, ./sbx.ts, ./jobs.ts, ./claims.ts, ./policy.ts, ./runtime.ts,
 *     ./output.ts, ./agents.ts, ./contracts.ts — the existing planes.
 *
 * Loader gotcha (pi 1.5.2 extension parser), applies to every module here and
 * to the ones above: no `?:` ternaries with double-quoted strings inside a
 * template literal's ${} — stick to string concatenation.
 */

import { spawn } from "node:child_process";
import * as fs from "node:fs";
import * as path from "node:path";
import type { AgentToolResult, ThinkingLevel } from "@earendil-works/pi-agent-core";
import type { ImageContent, Message, TextContent } from "@earendil-works/pi-ai";
import {
  DEFAULT_MAX_BYTES,
  DEFAULT_MAX_LINES,
  type ExtensionAPI,
  type ExtensionContext,
  type SessionBoundaryDraft,
  type ToolResultEvent,
  truncateHead,
} from "@earendil-works/pi-coding-agent";
import type { AutocompleteItem } from "@earendil-works/pi-tui";
import { Type } from "typebox";
import { type AgentConfig, type AgentScope, discoverAgents } from "./agents.ts";
import {
  type ClaimRecord,
  type SpawnRequest,
  completeSpawnClaim,
  formatClaimConflict,
  jobIdForEnv,
  prepareSpawnClaims,
  readClaims,
  siblingContextFor,
} from "./claims.ts";
import {
  type GateRun,
  type IntegrityCheckResult,
  type ReconciliationResult,
  bumpSettleGateAttempts,
  checkWorktreeIntegrity,
  detectMergeBackCommands,
  formatGateReport,
  formatSettleEscalation,
  formatSettleTrail,
  integrityRefusalReason,
  isGateRelevantWrite,
  logGateDebug,
  markPostWriteGateEnd,
  markPostWriteGateStart,
  planSettleGateAttempt,
  prepareGateDbVm,
  receiptCommandsOf,
  recentSettleFailures,
  reconciliationGateRun,
  recordSettleGateAttempt,
  resetSettleGateAttempts,
  resolveDeclaredGateCommand,
  runInVmAdvisoryGate,
  runReconciliation,
  runScopedGate,
  settleBackoffBaseMs,
  settleGateMaxAttempts,
  settleGateStateFor,
  shouldRunPostWriteGate,
  splitCommandLine,
  splitTopLevelSegments,
  tailLines,
  gateVmExecEnv,
  gateVmShellCommand,
  parseGateVmShellCommand,
  appendGateDbOverride,
  writeReceipt,
} from "./gates.ts";
import {
  type TaskPacket,
  formatTaskPacketRefusal,
  parseTaskPacket,
} from "./protocol.ts";
import {
  checkProveCoverage,
  isProveSegment,
  proveRuleIdsIn,
  resolveJobRuleIds,
} from "./mergegate.ts";
import {
  type SandboxExecResult,
  type SandboxHandle,
  type SandboxMount,
  SANDBOX_ENV_FLAG,
  createWorkerSandbox,
  ensureGateSandbox,
  execInSandbox,
  gateSandboxFor,
  isSandboxedChild,
  profileForRole,
  teardownGateSandboxes,
  teardownSandbox,
} from "./sbx.ts";
import { formatStallReason, startStallWatchdog, workerStallTimeoutMs } from "./runtime.ts";
import {
  createChildStreamParser,
  emptyUsage,
  getFinalOutput,
  truncateParallelOutput,
} from "./output.ts";
import {
  type JobEntry,
  type JobStatus,
  formatElapsed,
  formatJobDetail,
  formatJobList,
  getJob,
  jobsDirFor,
  listJobEntries,
  listJobIdsSync,
  listJobs,
  peekJobEntry,
  relayApprovalDecision,
  startBackgroundJob,
} from "./jobs.ts";
import { loadWorkflowPolicy } from "./policy.ts";
import { buildTaskPrompt, loadOrchestratorPrompt, subagentToolDescription, writePromptToTempFile } from "./persona.ts";
import {
  type JobCardData,
  type JobResultDetails,
  beginJobPickup,
  beginLiveRun,
  buildJobResultInjection,
  cardDataOf,
  emitJobCard,
  endJobPickup,
  endLiveRun,
  hasSurfacedJob,
  isFailedResult,
  isJobPickupInFlight,
  markJobSurfaced,
  registerSurfacingHooks,
  renderJobApproveCall,
  renderJobApproveResult,
  renderJobCardEntry,
  renderJobResultMessage,
  renderJobStatusCall,
  renderJobStatusResult,
  renderSubagentCall,
  renderSubagentResult,
  setSurfacingApi,
  sleepMs,
} from "./surfacing.ts";
import { openAgentsView, openJobViewer, openJobsView, refreshJobWidget } from "./tui/index.ts";

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
const TRUNCATION_NOTICE_RESERVE = 256;
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

/** True in a spawned worker child (foreground spawns only — background children are orchestrators). */
function isWorkerChild(): boolean {
  return process.env.PI_DELEGATE_CHILD === "1" && process.env.PI_DELEGATE_ROLE === "worker";
}

/**
 * The worker gate ladder with the batch-1 fail-closed crash fallback: a throw
 * in the runner becomes a failed GateRun, never a silent pass.
 */
async function runWorkerGate(cwd: string, scopeEnv: string | undefined): Promise<GateRun> {
  try {
    return await runScopedGate({ cwd, scopeEnv });
  } catch (error) {
    console.error("[delegate] worker gate crashed:", error);
    return {
      ok: false,
      steps: [
        {
          command: "(worker gate)",
          argv: [],
          ok: false,
          exitCode: null,
          timedOut: false,
          durationMs: 0,
          stdout: "",
          stderr: error instanceof Error ? error.message : String(error),
          spawnError: null,
          reason: "gate runner threw",
        },
      ],
    };
  }
}

/** Receipt append for a settle/worker attempt; a failure never changes a verdict. */
async function writeSettleReceipt(input: {
  cwd: string;
  agent: string;
  commands: ReturnType<typeof receiptCommandsOf>;
  ok: boolean;
  note: string;
  sandbox?: string;
}): Promise<string | null> {
  try {
    return await writeReceipt({
      cwd: input.cwd,
      agent: input.agent,
      session: process.env.PI_SESSION_ID,
      commands: input.commands,
      ok: input.ok,
      note: input.note,
      ...(input.sandbox ? { sandbox: input.sandbox } : {}),
    });
  } catch (error) {
    console.error("[delegate] settle receipt failed:", error);
    return null;
  }
}

/**
 * Append a note to a tool result's content without dropping the original text:
 * the LAST text part is extended in place (a new text part when there is none).
 * Used by the scoped gates to report failures/skips on top of the tool output.
 */
function appendToolResultNote(
  content: (TextContent | ImageContent)[],
  note: string,
): (TextContent | ImageContent)[] {
  const copy: (TextContent | ImageContent)[] = [...content];
  for (let index = copy.length - 1; index >= 0; index--) {
    const part = copy[index];
    if (part.type === "text") {
      copy[index] = { type: "text", text: part.text + note };
      return copy;
    }
  }
  copy.push({ type: "text", text: note.replace(/^\n+/, "") });
  return copy;
}

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

type ChildStatus = "pending" | "running" | "completed" | "failed" | "aborted";
export type UsageStats = ReturnType<typeof emptyUsage>;

/**
 * HOST-side verification of a sandboxed worker, run AFTER the sandbox exited
 * and BEFORE anything in the result is read as green:
 *
 *   1. integrity check (gate files == HEAD) — a dirty result REFUSES the gates;
 *   2. the authoritative gate ladder (only when the child exited normally);
 *   3. receipts, written to the HOST session's receipts dir, never the worktree.
 *
 * `refused` is the loud path: no gate command ran, and the batch routes to
 * human review.
 */
interface HostVerification {
  integrity: IntegrityCheckResult;
  /** The authoritative ladder (undefined when the integrity check refused). */
  gates?: GateRun;
  /** Receipt files written by this verification (host-side). */
  receipts: string[];
  /** true ⇒ the integrity check refused to run any gate. */
  refused: boolean;
  /** Whether the ladder actually ran. */
  gatesRan: boolean;
}

/**
 * Instance truth of the VM a run occupied, persisted in tool-result `details`
 * (additive: resume/branching replays it) so the TUI can say WHERE the work
 * ran. Policy half (role → profile) is derived at render time, never stored.
 */
export interface SandboxDetails {
  /** The `sbx` VM name this run occupied (`pi-<role>-…`). */
  name: string;
  /** The role the profile was resolved for (the agent name actually spawned). */
  role: string;
  /**
   * Which reference produced the VM — `template` or `kit` (SandboxHandle.
   * createdFrom). Image provenance, not a host path: the mounted paths are
   * deliberately NOT persisted (they are machine-local and the mounts line
   * already says what the VM could see).
   */
  createdFrom: string;
  /** Mounts as declared by the profile (what the VM could actually see). */
  mounts: readonly SandboxMount[];
  /** Egress hosts the profile allowed (default-deny proxy, empty ⇒ none). */
  networkHosts: readonly string[];
  /** Disposable rw primary workspace, when the profile needed one (scout). */
  scratchPath?: string;
  /** Set once teardown ran: `ok` clean, `errors` one or more failures, absent ⇒ live. */
  teardown?: "ok" | "errors";
  /** One line per teardown failure (TUI only — model text keeps its own block). */
  teardownErrors?: string[];
}

/**
 * One child run. Exported because the surfacing/TUI layers render it (type-only
 * import — erased at runtime); the spawn path in this file produces it.
 */
export interface SingleResult {
  agent: string;
  agentSource: "user" | "project" | "unknown";
  task: string;
  status: ChildStatus;
  exitCode: number | null;
  messages: Message[];
  stderr: string;
  usage: UsageStats;
  model?: string;
  stopReason?: string;
  errorMessage?: string;
  terminationSignal?: NodeJS.Signals;
  step?: number;
  /** Batch 2: the run was killed by the stall watchdog (see runtime.ts). */
  stallTimeout?: boolean;
  /** The sandbox VM this run occupied (absent for host-passthrough roles). */
  sandboxName?: string;
  /** One line per teardown failure (empty/undefined ⇒ clean teardown). */
  sandboxTeardownErrors?: string[];
  /**
   * Which plane the spawn actually used, recorded at the routing decision.
   * Absent on results that never started (unknown agent, pre-start abort) — a
   * rendered badge then stays silent instead of guessing.
   */
  plane?: "host" | "sandbox";
  /** Instance truth of the VM (sandboxed runs only). */
  sandbox?: SandboxDetails;
  /** Host-passthrough runs only: the cwd the child actually ran in. */
  hostCwd?: string;
  /**
   * The `sbx` CLI itself could not run the command (exit 125/127, spawn error,
   * timeout, abort). Distinct from the child's own status and from a gate
   * failure: the remediation is a host/VM fix, not a code fix.
   */
  infraFailure?: boolean;
  /** Host-side post-exit verification (sandboxed runs only). */
  hostVerification?: HostVerification;
}

interface SubagentDetails {
  mode: "single" | "parallel" | "chain";
  results: SingleResult[];
  isError: boolean;
}

/** Details for the single-mode background branch (phase 3). */
interface BackgroundJobDetails {
  mode: "background";
  job: JobEntry;
  /** Start-failure path only: lets the tool_result hook map runtime error state. */
  isError?: boolean;
}
/** Either shape a tool result can carry; the renderers branch on `mode`. */
export type AnyToolDetails = SubagentDetails | BackgroundJobDetails;

function getFailureEvidence(result: SingleResult): string {
  const evidence: string[] = [];
  const seen = new Set<string>();
  const add = (label: string, value: string | undefined) => {
    const text = value?.trim();
    if (!text || seen.has(text)) return;
    seen.add(text);
    evidence.push(`${label}: ${text}`);
  };

  add("Error", result.errorMessage);
  add("Signal", result.terminationSignal);
  add("stderr", result.stderr);
  add("Host verification", formatHostVerification(result));
  add("Assistant output", getFinalOutput(result.messages));
  return evidence.join("\n\n") || "(no output)";
}

function getResultOutput(result: SingleResult): string {
  return isFailedResult(result) ? getFailureEvidence(result) : getFinalOutput(result.messages) || "(no output)";
}

function truncateModelOutput(output: string): string {
  const initial = truncateHead(output);
  if (!initial.truncated) return output;

  const body = truncateHead(output, {
    maxLines: DEFAULT_MAX_LINES - 2,
    maxBytes: DEFAULT_MAX_BYTES - TRUNCATION_NOTICE_RESERVE,
  });
  const omittedBytes = initial.totalBytes - body.outputBytes;
  const notice = `[Output truncated: ${omittedBytes} bytes omitted. Full output preserved in tool details.]`;
  return body.content ? `${body.content}\n\n${notice}` : notice;
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

function getPiInvocation(args: string[]): { command: string; args: string[] } {
  const currentScript = process.argv[1];
  // Bun-compiled binaries report their embedded entry as argv[1]: "/$bunfs/root/..." on
  // POSIX and "B:/~BUN/root/..." on Windows. fs.existsSync() resolves these inside Bun.
  const isBunVirtualScript =
    currentScript !== undefined &&
    (currentScript.startsWith("/$bunfs/root/") || /^[A-Za-z]:[\\/]~BUN[\\/]root[\\/]/.test(currentScript));
  if (currentScript && !isBunVirtualScript && fs.existsSync(currentScript)) {
    return { command: process.execPath, args: [currentScript, ...args] };
  }

  const execName = path.basename(process.execPath).toLowerCase();
  const isGenericRuntime = /^(node|bun)(\.exe)?$/.test(execName);
  if (!isGenericRuntime) {
    return { command: process.execPath, args };
  }

  return { command: "pi", args };
}

type OnUpdateCallback = (partial: AgentToolResult<SubagentDetails>) => void;

interface DispatchDefaults {
  model?: string;
  thinkingLevel?: ThinkingLevel;
}

interface ProcessOutcome {
  code: number | null;
  signal: NodeJS.Signals | null;
  spawnError?: Error;
}

interface ResolvedAgent {
  agent?: AgentConfig;
  available: AgentConfig[];
  cwd: string;
  /**
   * The INVOKING session's cwd — the host root. Receipts and claims resolve
   * against it, never against a per-call cwd (a worktree spawn must not redirect
   * the host's own evidence into the tree a worker can write).
   */
  hostRoot: string;
}

function resolveAgent(defaultCwd: string, scope: AgentScope, name: string, cwd?: string): ResolvedAgent {
  // Phase 6: agent discovery runs in the INVOKING session's cwd (defaultCwd),
  // never in the per-call run cwd — a worker spawned into a git worktree
  // (../lt-api-wt-<job-id>) must resolve the same roster as its orchestrator,
  // and the trusted-project realpath check must compare one and the same
  // discovery dir (a run-cwded discovery mismatches it and would raise a
  // spurious project-agent confirm dialog in the orchestrator's pane mid-run).
  const discovery = discoverAgents(defaultCwd, scope);
  return {
    agent: discovery.agents.find((candidate) => candidate.name === name),
    available: discovery.agents,
    cwd: path.resolve(defaultCwd, cwd ?? "."),
    hostRoot: path.resolve(defaultCwd),
  };
}

function createUnstartedResult(
  agent: string,
  task: string,
  step: number | undefined,
  status: "aborted" | "failed",
  errorMessage?: string,
): SingleResult {
  return {
    agent,
    agentSource: "unknown",
    task,
    status,
    exitCode: null,
    messages: [],
    stderr: "",
    usage: emptyUsage(),
    errorMessage,
    step,
  };
}

/**
 * The env a sandboxed pi gets via `sbx exec -e`. A strict allowlist on purpose:
 * the host process env never crosses into the VM, so pi's own session identity
 * (`PI_SESSION_ID`, `PI_PROVIDER`, …) and any host secret stay host-side. The
 * model API key is NOT here — it reaches the VM through the `sbx` secret
 * sentinel/proxy (see SANDBOX_SECRET_SENTINEL_ENVS in ./sbx.ts).
 */
function sandboxChildEnv(specScopeEnv?: string): Record<string, string> {
  const env: Record<string, string> = {
    PI_DELEGATE_CHILD: "1",
    PI_DELEGATE_ROLE: "worker",
    [SANDBOX_ENV_FLAG]: "1",
  };
  // Batch 1 + B3.1: the worker's spec scope travels as env, never re-parsed
  // from the child's context. The normalized value was produced ONCE by the
  // task-packet boundary in the subagent tool (./protocol.ts owns the grammar);
  // no scope line means NO env var, so the allowlist above keeps any inherited
  // value from narrowing the worker's gate.
  if (specScopeEnv) env.PI_DELEGATE_SPEC_SCOPE = specScopeEnv;
  return env;
}

/** Receipt append that never throws and never changes a verdict. */
async function safeHostReceipt(input: {
  cwd: string;
  agent: string;
  commands: ReturnType<typeof receiptCommandsOf>;
  ok: boolean;
  note: string;
  sandbox?: string;
}): Promise<string | null> {
  try {
    return await writeReceipt({
      cwd: input.cwd,
      agent: input.agent,
      session: process.env.PI_SESSION_ID,
      commands: input.commands,
      ok: input.ok,
      note: input.note,
      ...(input.sandbox ? { sandbox: input.sandbox } : {}),
    });
  } catch (error) {
    console.error("[delegate] host receipt failed:", error);
    return null;
  }
}

/**
 * HOST-SIDE post-exit verification of one sandboxed worker. Runs in the HOST
 * session (this process), never in the VM:
 *
 *   1. integrity check FIRST (`git diff --exit-code HEAD` over the gate files,
 *      worktree + index). Dirty/unverifiable ⇒ REFUSE to run any gate, write a
 *      receipt saying so, and fail the result loudly (human review).
 *   2. otherwise, when the child exited normally, the authoritative ladder
 *      (`make audit/static` + the scoped spec check) IN THE GATE VM (mounted at
 *      the worktree path; the host only spawns `sbx exec`), and its receipt —
 *      in the HOST's receipts dir, not the worktree's.
 */
async function verifySandboxedWorker(input: {
  hostRoot: string;
  worktreePath: string;
  agent: string;
  scopeEnv?: string;
  runGates: boolean;
}): Promise<HostVerification> {
  const receipts: string[] = [];
  let integrity: IntegrityCheckResult;
  try {
    integrity = await checkWorktreeIntegrity(input.worktreePath);
  } catch (error) {
    // Fail closed: an internal error means the gate files were not verified.
    integrity = {
      clean: false,
      refused: true,
      report:
        "\u2500\u2500 INTEGRITY CHECK (host, pre-gate) \u2500\u2500\nintegrity check crashed: " +
        (error instanceof Error ? error.message : String(error)),
      steps: [],
      paths: [],
    };
  }

  const integrityReceipt = await safeHostReceipt({
    cwd: input.hostRoot,
    agent: input.agent,
    commands: integrity.steps.map((step) => ({
      command: step.command,
      exitCode: step.exitCode,
      timedOut: step.timedOut,
      durationMs: step.durationMs,
    })),
    ok: integrity.clean,
    note: integrity.clean
      ? "integrity check (host, pre-gate): gate files match HEAD"
      : "integrity check (host, pre-gate): REFUSED \u2014 no gate command ran",
  });
  if (integrityReceipt) receipts.push(integrityReceipt);

  if (integrity.refused) {
    return { integrity, receipts, refused: true, gatesRan: false };
  }
  if (!input.runGates) {
    return { integrity, receipts, refused: false, gatesRan: false };
  }

  let gates: GateRun;
  try {
    gates = await runScopedGate({ cwd: input.worktreePath, scopeEnv: input.scopeEnv });
  } catch (error) {
    gates = {
      ok: false,
      steps: [
        {
          command: "(host gate)",
          argv: [],
          ok: false,
          exitCode: null,
          timedOut: false,
          durationMs: 0,
          stdout: "",
          stderr: error instanceof Error ? error.message : String(error),
          spawnError: null,
          reason: "host gate runner threw",
        },
      ],
    };
  }
  const gateReceipt = await safeHostReceipt({
    cwd: input.hostRoot,
    agent: input.agent,
    commands: receiptCommandsOf(gates),
    ok: gates.ok,
    note: "host gate (authoritative, post-exit; executed in the gate VM)",
    sandbox: gates.steps.find((step) => step.gateSandbox)?.gateSandbox,
  });
  if (gateReceipt) receipts.push(gateReceipt);
  return { integrity, receipts, refused: false, gatesRan: true, gates };
}

/** Append the HOST VERIFICATION block(s) of the given results to model-facing text. */
function withHostVerification(text: string, ...results: SingleResult[]): string {
  const blocks = results
    .map((result) => formatHostVerification(result))
    .filter((block) => block.length > 0);
  if (blocks.length === 0) return text;
  return text + "\n\n" + blocks.join("\n\n");
}

/**
 * The model-facing HOST VERIFICATION block: "" when the run was not sandboxed
 * (host passthrough keeps its old result text) or when everything was clean.
 */
function formatHostVerification(result: SingleResult): string {
  const verification = result.hostVerification;
  if (!verification) return "";
  const lines: string[] = ["\u2500\u2500 HOST VERIFICATION (authoritative, post-exit) \u2500\u2500"];
  lines.push(verification.integrity.report);
  if (verification.refused) {
    lines.push("gates: NOT RUN \u2014 the integrity check refused them (human review required).");
  } else if (!verification.gatesRan) {
    lines.push("gates: not run (the worker did not exit normally \u2014 no verdict was produced).");
  } else if (verification.gates) {
    const run = verification.gates;
    if (run.ok) {
      lines.push("gates: PASSED \u2014 " + run.steps.map((step) => step.command).join(" \u00b7 "));
    } else {
      lines.push(
        formatGateReport(run, {
          header: "\u2500\u2500 HOST GATE FAILED \u2500\u2500",
          footer: "The host, not the worker, decides green: this batch is NOT green.",
          maxLines: 20,
        }),
      );
    }
  }
  if (verification.receipts.length > 0) {
    lines.push("receipts: " + verification.receipts.join(", "));
  }
  if (result.sandboxName) lines.push("sandbox: " + result.sandboxName + " (torn down)");
  if (result.sandboxTeardownErrors && result.sandboxTeardownErrors.length > 0) {
    lines.push("teardown: " + result.sandboxTeardownErrors.join("; "));
  }
  return lines.join("\n");
}

async function runSingleAgent(
  dispatchDefaults: DispatchDefaults,
  resolvedAgent: ResolvedAgent,
  agentName: string,
  task: string,
  step: number | undefined,
  signal: AbortSignal | undefined,
  onUpdate: OnUpdateCallback | undefined,
  makeDetails: (results: SingleResult[]) => SubagentDetails,
  // Ambient footer status lives on ctx.ui: the run counts itself in/out around
  // the spawn so the footer shows live sandboxed vs host workers.
  runCtx: Pick<ExtensionContext, "hasUI" | "ui">,
  // Normalized PI_DELEGATE_SPEC_SCOPE value from the task-packet boundary in
  // the subagent tool (undefined = the packet carried no SPEC-SCOPE line).
  specScopeEnv?: string,
): Promise<SingleResult> {
  if (signal?.aborted) {
    return createUnstartedResult(agentName, task, step, "aborted", "Subagent was aborted before starting");
  }

  const effectiveCwd = resolvedAgent.cwd;
  const agent = resolvedAgent.agent;
  if (!agent) {
    const available = resolvedAgent.available.map((candidate) => `"${candidate.name}"`).join(", ") || "none";
    return createUnstartedResult(
      agentName,
      task,
      step,
      "failed",
      `Unknown agent: "${agentName}". Available agents: ${available}.`,
    );
  }

  const args: string[] = ["--mode", "json", "-p", "--no-session"];
  const inheritsDispatchConfig = !agent.model;
  const model = agent.model ?? dispatchDefaults.model;
  if (model) args.push("--model", model);
  // Delta vs example: explicit `thinking` frontmatter always wins (the CLI clamps
  // the level to the model's capabilities), even when the agent sets its own model.
  if (agent.thinking) {
    args.push("--thinking", agent.thinking);
  } else if (inheritsDispatchConfig && dispatchDefaults.thinkingLevel) {
    args.push("--thinking", dispatchDefaults.thinkingLevel);
  }
  if (agent.tools && agent.tools.length > 0) args.push("--tools", agent.tools.join(","));

  let tmpPromptDir: string | null = null;
  let tmpPromptPath: string | null = null;
  // Shared JSONL parser (phase 3): the same message/usage logic job_status
  // uses when reading a finished job's on-disk event stream.
  const stream = createChildStreamParser();
  const currentResult: SingleResult = {
    agent: agentName,
    agentSource: agent.source,
    task,
    status: "running",
    exitCode: null,
    messages: stream.messages,
    stderr: "",
    usage: stream.usage,
    model,
    step,
  };

  const emitUpdate = () => {
    onUpdate?.({
      content: [{ type: "text", text: getFinalOutput(currentResult.messages) || "(running...)" }],
      details: makeDetails([currentResult]),
    });
  };

  try {
    beginLiveRun(agent.name, runCtx);
    // Sandbox routing. Every role today runs inside its own micro-VM (no role
    // is host-passthrough — the mechanism is retained in sbx.ts for a future
    // deliberate host role, and this branch is its only spawn seam). The pi
    // invocation is otherwise the same one the local path uses; three deltas
    // are forced by the mount boundary:
    //   - the system prompt goes INLINE: `--append-system-prompt` resolves its
    //     argument as a file only when that path exists, and a host temp path
    //     does not exist inside the VM;
    //   - `--approve` is appended: project-local `.pi/extensions` load only
    //     after project trust, and the VM's HOME (/home/agent) has no
    //     `trust.json` and print mode cannot show the trust prompt — without
    //     the override pi silently skips the delegate extension in the VM, so
    //     the IN-VM ADVISORY gate never runs at all (found by the S1 e2e
    //     dogfood: a misformatted write produced no ADVISORY note until the
    //     same write was run with `-a`). The override loads the INERT instance,
    //     which by design only appends advisory notes;
    //   - `--no-skills` is appended (no shared skills store in the VM; pi has
    //     no `--skills=off` flag — unknown `--key=value` flags are silently
    //     collected, so that spelling left skills ON).
    const sandboxProfile = profileForRole(agent.name);
    const useSandbox = !sandboxProfile.hostPassthrough;
    // What we ATTEMPTED is instance truth: a VM that fails to create still
    // reports `[sbx]` (no name invented), never a silent host run.
    currentResult.plane = useSandbox ? "sandbox" : "host";
    if (!useSandbox) currentResult.hostCwd = effectiveCwd;
    if (agent.systemPrompt.trim()) {
      if (useSandbox) {
        args.push("--append-system-prompt", agent.systemPrompt);
      } else {
        const tmp = await writePromptToTempFile(agent.name, agent.systemPrompt);
        tmpPromptDir = tmp.dir;
        tmpPromptPath = tmp.filePath;
        args.push("--append-system-prompt", tmpPromptPath);
      }
    }

    if (signal?.aborted) {
      currentResult.status = "aborted";
      currentResult.errorMessage = "Subagent was aborted before starting";
      return currentResult;
    }

    args.push(buildTaskPrompt(agent, task, await siblingContextFor(resolvedAgent.hostRoot, jobIdForEnv())));
    emitUpdate();
    let wasAborted = false;
    // Batch 2 stall guard — and, in the sandbox era, the OWNER of teardown:
    // every parsed child event (and every raw stdout chunk — a superset,
    // deliberately forgiving) refreshes lastActivityMs; a referenced 1s
    // watchdog aborts the exec once the silence exceeds the (policy/env tunable,
    // 0-disabled) timeout, and the finally below removes the VM on EVERY exit
    // path (stall, abort, spawn error, normal finish). There is no local TTL:
    // the host owns wall-clock.
    const stallTimeoutMs = workerStallTimeoutMs(process.env, effectiveCwd);
    const activity = { lastMs: Date.now() };
    let stalled = false;
    let stallReason = "";
    let sandbox: SandboxHandle | null = null;
    let sandboxExec: SandboxExecResult | null = null;
    let sandboxTeardown: Awaited<ReturnType<typeof teardownSandbox>> | null = null;
    let outcome: ProcessOutcome;

    const processLine = (line: string) => {
      activity.lastMs = Date.now();
      if (stream.processLine(line)) emitUpdate();
    };
    let buffer = "";
    const onStdoutChunk = (text: string) => {
      activity.lastMs = Date.now();
      buffer += text;
      const lines = buffer.split("\n");
      buffer = lines.pop() || "";
      for (const line of lines) processLine(line);
    };
    const onStderrChunk = (text: string) => {
      currentResult.stderr += text;
    };

    /**
     * The HOST-PASSTHROUGH spawn path, unchanged in shape: `getPiInvocation` + a
     * local child, its env scrubbed of pi's own session identity, and the
     * caller's abort signal mapped to SIGTERM→SIGKILL. No role uses it today
     * (the researcher is a mountless sandbox); it is the retained seam for a
     * future deliberate host role.
     */
    const runLocalSpawn = async (): Promise<ProcessOutcome> => {
      return await new Promise<ProcessOutcome>((resolve) => {
        const invocation = getPiInvocation(args);
        let settled = false;
        let exited = false;
        let closed = false;
        let killTimer: ReturnType<typeof setTimeout> | undefined;

        let proc: ReturnType<typeof spawn>;
        const finish = (result: ProcessOutcome) => {
          if (settled) return;
          settled = true;
          closed = true;
          if (killTimer) clearTimeout(killTimer);
          execAbort.signal.removeEventListener("abort", abortProcess);
          resolve(result);
        };
        const abortProcess = () => {
          if (closed || exited || proc.exitCode !== null || proc.signalCode !== null) return;
          if (!proc.kill("SIGTERM")) return;
          wasAborted = true;
          killTimer = setTimeout(() => {
            if (!closed && !exited) proc.kill("SIGKILL");
          }, 5000);
        };

        // pi's bash tool exports PI_SESSION_ID / PI_SESSION_FILE / PI_PROVIDER /
        // PI_MODEL / PI_REASONING_LEVEL into tool subprocesses; scrub them so
        // the parent's session identity never leaks into a subagent's env.
        // Phase 5: every foreground spawn is a worker — including the child
        // orchestrator's own spawns, whose inherited PI_DELEGATE_ROLE is
        // overridden here so worker children of a child orchestrator get the
        // role uniformly. (Background children are role=orchestrator via the
        // runner script in jobs.ts.)
        const childEnv: NodeJS.ProcessEnv = {
          ...process.env,
          PI_DELEGATE_CHILD: "1",
          PI_DELEGATE_ROLE: "worker",
        };
        delete childEnv.PI_SESSION_ID;
        delete childEnv.PI_SESSION_FILE;
        delete childEnv.PI_PROVIDER;
        delete childEnv.PI_MODEL;
        delete childEnv.PI_REASONING_LEVEL;
        delete childEnv[SANDBOX_ENV_FLAG];
        // Batch 1: the worker's spec scope travels as env, never re-parsed from
        // the child's context (the child cannot see its own tool_result input
        // reliably enough to scope a gate). The value was validated and
        // normalized at the task-packet boundary; otherwise the variable is
        // removed, so no inherited value can ever narrow a worker's gate.
        if (specScopeEnv) childEnv.PI_DELEGATE_SPEC_SCOPE = specScopeEnv;
        else delete childEnv.PI_DELEGATE_SPEC_SCOPE;
        try {
          proc = spawn(invocation.command, invocation.args, {
            cwd: effectiveCwd,
            shell: false,
            stdio: ["ignore", "pipe", "pipe"],
            // Delta vs example: mark children so env-gated hooks (orchestrator
            // injection, edit/write gate) know they are running in a child.
            env: childEnv,
          });
        } catch (error) {
          finish({
            code: null,
            signal: null,
            spawnError: error instanceof Error ? error : new Error(String(error)),
          });
          return;
        }

        proc.stdout?.on("data", (data) => {
          onStdoutChunk(data.toString());
        });
        proc.stderr?.on("data", (data) => {
          onStderrChunk(data.toString());
        });
        proc.on("exit", () => {
          exited = true;
        });
        proc.on("close", (code, terminationSignal) => {
          if (buffer.trim()) processLine(buffer);
          finish({ code, signal: terminationSignal });
        });
        proc.on("error", (error) => {
          finish({ code: null, signal: null, spawnError: error });
        });

        if (execAbort.signal.aborted) abortProcess();
        else execAbort.signal.addEventListener("abort", abortProcess, { once: true });
      });
    };

    // One abort channel for BOTH paths: the watchdog and the caller's signal
    // only ever request a stop; the transport decides how to enforce it.
    const execAbort = new AbortController();
    const requestAbort = () => {
      if (execAbort.signal.aborted) return;
      wasAborted = true;
      execAbort.abort();
    };
    if (signal) {
      if (signal.aborted) requestAbort();
      else signal.addEventListener("abort", requestAbort, { once: true });
    }
    const watchdog = startStallWatchdog({
      timeoutMs: stallTimeoutMs,
      getLastActivityMs: () => activity.lastMs,
      onStall: (decision) => {
        stalled = true;
        stallReason = formatStallReason(decision.idleMs, stallTimeoutMs);
        if (!execAbort.signal.aborted) execAbort.abort();
      },
    });

    try {
      if (useSandbox) {
        let createError: string | null = null;
        try {
          sandbox = await createWorkerSandbox({
            role: agent.name,
            worktreePath: effectiveCwd,
            cwd: resolvedAgent.hostRoot,
          });
          currentResult.sandboxName = sandbox.name;
          currentResult.sandbox = {
            name: sandbox.name,
            role: sandbox.role,
            createdFrom: sandbox.createdFrom,
            mounts: sandbox.profile.mounts,
            networkHosts: sandbox.networkHosts,
            ...(sandbox.scratchPath ? { scratchPath: sandbox.scratchPath } : {}),
          };
        } catch (error) {
          createError = error instanceof Error ? error.message : String(error);
        }
        if (sandbox) {
          try {
            sandboxExec = await execInSandbox(
              sandbox.name,
              ["pi", ...args, "--no-skills", "--approve"],
              {
                worktreePath: effectiveCwd,
                env: sandboxChildEnv(specScopeEnv),
                signal: execAbort.signal,
                onStdout: onStdoutChunk,
                onStderr: onStderrChunk,
              },
            );
            outcome = {
              code: sandboxExec.exitCode,
              signal: null,
              spawnError: sandboxExec.spawnError ? new Error(sandboxExec.spawnError) : undefined,
            };
          } catch (error) {
            outcome = {
              code: null,
              signal: null,
              spawnError: error instanceof Error ? error : new Error(String(error)),
            };
          }
        } else {
          // The sandbox could not be created: NO local fallback. A host-side
          // spawn would hand the worker host reach behind the profile's back.
          outcome = {
            code: null,
            signal: null,
            spawnError: new Error("sandbox create failed: " + createError),
          };
        }
      } else {
        outcome = await runLocalSpawn();
      }
    } finally {
      // The stall watchdog / abort path lands here too: the VM never outlives
      // its worker on ANY exit path. Teardown is best-effort and never throws.
      watchdog.stop();
      if (signal) signal.removeEventListener("abort", requestAbort);
      if (sandbox) {
        sandboxTeardown = await teardownSandbox(sandbox.name, sandbox.scratchPath);
      }
    }

    currentResult.exitCode = outcome.code;
    currentResult.terminationSignal = outcome.signal ?? undefined;
    if (!currentResult.model) currentResult.model = stream.model;
    currentResult.stopReason = stream.stopReason;
    currentResult.errorMessage ||= stream.errorMessage;

    if (stalled) {
      // Fail closed: the run produced no verdict, so the worker result is an
      // error, and the extension (not the worker) records what happened.
      currentResult.status = "failed";
      currentResult.stallTimeout = true;
      currentResult.errorMessage = stallReason;
      await safeHostReceipt({
        cwd: resolvedAgent.hostRoot,
        agent: agentName,
        commands: [],
        ok: false,
        note: "stall-timeout: " + stallReason + (sandbox ? " (sandbox " + sandbox.name + " torn down)" : ""),
      });
    } else if (wasAborted) {
      currentResult.status = "aborted";
      currentResult.errorMessage ||= "Subagent was aborted";
    } else if (sandboxExec && sandboxExec.classification.infraFailure) {
      // Distinct from the child's own status AND from a gate failure: the CLI
      // could not run the command (125/127/spawn/timeout). No verdict was
      // produced; the remediation is a host/VM fix.
      currentResult.status = "failed";
      currentResult.infraFailure = true;
      currentResult.errorMessage =
        "sandbox infrastructure failure (no verdict produced): " +
        sandboxExec.classification.reason +
        (sandboxExec.sandbox ? " [sandbox " + sandboxExec.sandbox + "]" : "");
    } else if (outcome.spawnError) {
      currentResult.status = "failed";
      currentResult.errorMessage = `Failed to spawn subagent: ${outcome.spawnError.message}`;
    } else if (outcome.signal) {
      currentResult.status = "failed";
      currentResult.errorMessage ||= `Subagent terminated by signal ${outcome.signal}`;
    } else if (outcome.code !== 0) {
      currentResult.status = "failed";
      currentResult.errorMessage ||= `Subagent exited with code ${outcome.code}`;
    } else if (currentResult.stopReason === "aborted") {
      currentResult.status = "aborted";
    } else if (currentResult.stopReason === "error") {
      currentResult.status = "failed";
    } else {
      currentResult.status = "completed";
    }

    if (sandbox) {
      currentResult.sandboxName = sandbox.name;
      if (currentResult.sandbox) {
        const teardownErrors = sandboxTeardown?.errors ?? [];
        currentResult.sandbox.teardown = teardownErrors.length > 0 ? "errors" : "ok";
        if (teardownErrors.length > 0) currentResult.sandbox.teardownErrors = [...teardownErrors];
      }
      if (sandboxTeardown && sandboxTeardown.errors.length > 0) {
        currentResult.sandboxTeardownErrors = sandboxTeardown.errors;
      }
      // HOST-SIDE authoritative verification, in this order: integrity check →
      // (only if clean) the gate ladder. Never in the VM, never after the fact.
      const verification = await verifySandboxedWorker({
        hostRoot: resolvedAgent.hostRoot,
        worktreePath: effectiveCwd,
        agent: agentName,
        scopeEnv: specScopeEnv,
        runGates: currentResult.status === "completed",
      });
      currentResult.hostVerification = verification;
      if (verification.refused) {
        currentResult.status = "failed";
        currentResult.errorMessage =
          (currentResult.errorMessage ? currentResult.errorMessage + "\n" : "") +
          integrityRefusalReason(verification.integrity);
      } else if (verification.gates && !verification.gates.ok) {
        currentResult.status = "failed";
        const failed = verification.gates.steps.filter((entry) => !entry.ok);
        const summary = failed
          .map((entry) => entry.command + " (" + (entry.exitCode === null ? "no exit code" : "exit " + String(entry.exitCode)) + ")")
          .join(", ");
        currentResult.errorMessage =
          (currentResult.errorMessage ? currentResult.errorMessage + "\n" : "") +
          "host gate failed after the sandbox exited: " + (summary || "unknown step") +
          " — the host, not the worker, decides green.";
      }
    }
    return currentResult;
  } finally {
    endLiveRun(agent.name);
    if (tmpPromptPath) {
      try {
        fs.unlinkSync(tmpPromptPath);
      } catch {
        /* ignore */
      }
    }
    if (tmpPromptDir) {
      try {
        fs.rmdirSync(tmpPromptDir);
      } catch {
        /* ignore */
      }
    }
  }
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

// ── UI surface: /jobs, /agents, the jobs widget, ctrl+\ ────────────────────
// The widget, the viewers and the badge helpers live in ./tui/ (registered
// below, drawn there). What stays here is the routing reminder the edit/write
// gate returns.

/**
 * Routing reminder returned whenever the edit/write gate stops a call. The
 * non-UI block prefixes it (see tool_call below); the confirm-decline path
 * appends the dialog hint.
 */
const DELEGATE_REASON =
  "Orchestrator does not implement. Delegate this to a subagent (go-worker for Go, general for anything else) via the subagent tool";

// ── PROVE receipts (the extension writes them, never the worker) ────────────
//
// The receipt POLICY (which rule ids a prove argv names, what "this command is
// the declared prove command" means, and how a receipt is read back as MERGE-BACK
// evidence) lives in ./mergegate.ts, one copy. This section keeps only what the
// RECEIPT WRITER needs: identifying the command's leading `cd` and the exit code
// of the bash tool result it records.

/**
 * The directory of a leading `cd <dir>` segment, or null. Only the FIRST
 * segment counts: the orchestrator's handoff form is
 * `cd ../lt-api-wt-<id> && make prove args='--rule <id>'`, and a cd later in
 * the chain says nothing about the command that followed.
 *
 * IDENTIFICATION ONLY. In the sandbox era a `cd` never redirects the receipt
 * (receipts resolve against the host session's own root): routing evidence into
 * the worktree would let a worker pre-forge a VERIFIED receipt inside its mount.
 */
function leadingCdTarget(segment: string): string | null {
  const words = splitCommandLine(segment);
  if (words.length !== 2 || words[0] !== "cd") return null;
  return words[1];
}

/** Non-negative integer or null for anything else (never assume a code). */
function exitCodeNumber(value: unknown): number | null {
  if (typeof value === "number" && Number.isInteger(value)) return value;
  return null;
}

/**
 * Exit code of a bash tool result. pi 1.5.2 carries it in the result's
 * `structuredContent.exit_code`; the details object is typed without it, so
 * every location is read defensively and a status line in the content is the
 * last resort. Unreadable ⇒ null (a null exitCode is never ok:true), never 0.
 */
function bashExitCodeOf(event: ToolResultEvent): number | null {
  const details = event.details as { exitCode?: unknown; exit_code?: unknown } | undefined;
  const fromDetails = exitCodeNumber(details?.exitCode) ?? exitCodeNumber(details?.exit_code);
  if (fromDetails !== null) return fromDetails;
  const structured = event.structuredContent as { exit_code?: unknown } | undefined;
  const fromStructured = exitCodeNumber(structured?.exit_code);
  if (fromStructured !== null) return fromStructured;
  for (const part of event.content) {
    if (part.type !== "text") continue;
    const match = /Command exited with code (-?\d+)\s*$/.exec(part.text.trim());
    if (match) return Number(match[1]);
  }
  return null;
}

/**
 * IN-VM (sandboxed) registration: ADVISORY ONLY.
 *
 * The extension also loads inside the worker's micro-VM. There it must register
 * nothing that enforces or records — no enforcing hook, no receipts, no claims,
 * no subagent tool, no job surfaces, no merge hook. The only thing it does is
 * append clearly-marked ADVISORY notes after a gate-relevant write and at
 * settle, so the worker's own loop is shorter and it knows a gate is due. Every
 * note says the host re-runs the authoritative gates after exit — an in-VM green
 * is not a verdict.
 *
 * The ladder is `runInVmAdvisoryGate`: the declared scoped spec check plus a
 * direct `gofmt -l` over the changed Go files. `make audit/static` is never run
 * here (its gofmt step enumerates `git ls-files`, and the mount carries no
 * usable `.git`).
 */
function registerInVmAdvisory(pi: ExtensionAPI): void {
  const ADVISORY_FOOTER =
    "This is ADVISORY only: the host re-runs the authoritative gates after your sandbox exits, " +
    "and its verdict — not this note — decides. Never quote this note as evidence of green.";

  pi.on("tool_result", async (event, ctx) => {
    if (event.toolName !== "edit" && event.toolName !== "write") return;
    if (event.isError) return;
    const input = event.input as { path?: unknown };
    if (typeof input.path !== "string" || input.path.length === 0) return;
    if (!isGateRelevantWrite(ctx.cwd, input.path)) return;

    let run: GateRun;
    try {
      run = await runInVmAdvisoryGate({
        cwd: ctx.cwd,
        scopeEnv: process.env.PI_DELEGATE_SPEC_SCOPE,
        changedPaths: [input.path],
      });
    } catch (error) {
      return {
        content: appendToolResultNote(
          event.content,
          "\n[ADVISORY] in-VM gate check crashed (" +
            (error instanceof Error ? error.message : String(error)) +
            "). \n" + ADVISORY_FOOTER,
        ),
      };
    }
    if (run.ok) return;
    const report = formatGateReport(run, {
      header: "\u2500\u2500 ADVISORY GATE (in-VM \u2014 not a verdict) \u2500\u2500",
      footer: ADVISORY_FOOTER,
      maxLines: 20,
    });
    return { content: appendToolResultNote(event.content, "\n" + report) };
  });

  // Settle advisory: same ladder, no forced continuation (nothing here may
  // consume a turn or block the run) and no receipt.
  pi.on("agent_before_settle", async (event, ctx) => {
    if (event.outcome !== "completed") return;
    let run: GateRun;
    try {
      run = await runInVmAdvisoryGate({ cwd: ctx.cwd, scopeEnv: process.env.PI_DELEGATE_SPEC_SCOPE });
    } catch (error) {
      return {
        entries: [
          ...event.entries,
          {
            type: "custom_message",
            customType: "delegate-gate",
            content:
              "[ADVISORY] in-VM settle check crashed: " +
              (error instanceof Error ? error.message : String(error)) + "\n" + ADVISORY_FOOTER,
            display: true,
          },
        ],
      };
    }
    if (run.ok) return;
    const report = formatGateReport(run, {
      header: "\u2500\u2500 ADVISORY SETTLE GATE (in-VM \u2014 not a verdict) \u2500\u2500",
      footer: ADVISORY_FOOTER,
      maxLines: 20,
    });
    return {
      entries: [
        ...event.entries,
        { type: "custom_message", customType: "delegate-gate", content: report, display: true },
      ],
    };
  });
}

/**
 * One-shot: the settle gate emitted its HARD STOP message with continue:true
 * (the worker's terminal turn, for its verbatim failure report). The NEXT
 * agent_before_settle consumes it: no receipt, no gate run, return undefined.
 *
 * Why the hard stop is NOT an entries-only return: pi's
 * `_runBeforeSettleBoundary` commits the drafts and, with `continue` false,
 * ends the run with that committed CUSTOM message as the last message. Print
 * mode derives stdout from `state.messages[last].role === "assistant"`, so a
 * custom/user-role draft last means exit 0 with EMPTY stdout and no failure
 * report (observed live, deterministic). Ending on a real assistant turn keeps
 * the buffered output and hands the parent the worker's report.
 * Cleared on read; a PASSING gate run also clears it.
 *
 * (Gate state, not surfacing state: it stays with the worker-settle gate.)
 */
let settleGateTerminal = false;

export default function (pi: ExtensionAPI) {
  // Sandboxed (in-VM) instance: inert. Register the advisory notes and stop —
  // nothing below may run inside a worker VM (no enforcement, no records, no
  // teardown, no tool surfaces).
  if (isSandboxedChild()) {
    registerInVmAdvisory(pi);
    return;
  }

  setSurfacingApi(pi);

  // Phase 2: env-gated orchestrator injection. Main sessions get persona/rules/routing
  // appended to the system prompt; children (PI_DELEGATE_CHILD set) get nothing.
  pi.on("before_agent_start", (event, ctx) => {
    if (process.env.PI_DELEGATE_CHILD) return;
    // Opt-in delegate-only mode: PI_DELEGATE_ONLY set in the main session leaves subagent as its only callable tool.
    if (process.env.PI_DELEGATE_ONLY) pi.setActiveTools(["subagent"]);
    return { systemPrompt: event.systemPrompt + loadOrchestratorPrompt(ctx.cwd) };
  });

  // Phase 2: hard-layer gate. edit/write in a MAIN session must not run
  // unimpeded: UI present → confirm dialog, declining blocks with a routing
  // reminder, confirming allows harness/plan work. Non-UI main sessions (-p)
  // get a HARD block instead — nobody can approve a dialog there, and without
  // this the orchestrator could silently implement. bash is not gated (prompt
  // layer discourages it). Children (PI_DELEGATE_CHILD set) skip THIS gate so
  // delegated work runs unattended; a sandboxed worker has no write barrier in
  // the VM by design (the mount is the boundary), and the host integrity check
  // at the merge boundary is the net.
  pi.on("tool_call", async (event, ctx) => {
    if (process.env.PI_DELEGATE_CHILD) return;
    if (event.toolName !== "edit" && event.toolName !== "write") return;
    if (!ctx.hasUI) {
      return {
        block: true,
        reason: "Non-UI main session: edit/write is blocked; delegate to a subagent. " + DELEGATE_REASON + ".",
      };
    }
    const ok = await ctx.ui.confirm("Delegate instead?", "The orchestrator routes implementation work to subagents. Proceed with the " + event.toolName + " anyway?");
    if (!ok) {
      return {
        block: true,
        reason: DELEGATE_REASON + ", or confirm the edit in the dialog if this is harness/plan work.",
      };
    }
  });

  // MERGE-PATH PROVE enforcement (sandbox era). This hook closes the
  // typed-in-pane bypass: a confirm-handoff user can type "approved" straight
  // into the orchestrator's tmux pane, which never passes through job_approve.
  // The precondition therefore lives on the MERGE EXECUTION PATH, not on the
  // approval path: in an orchestrator host session, any `git merge …` (including
  // the `git -C <worktree> merge` form) is blocked when the job's RECORDED spec
  // scope names rules that lack VERIFIED prove receipts host-side. A job with no
  // recorded SPEC-SCOPE merges freely. Host-side only — an in-VM instance never
  // registers this (the inert-mode early return above).
  //
  // The scope is `./mergegate.ts` `resolveJobRuleIds` — THE resolver (claims
  // first, registry task text second), the same call the approval relay in
  // jobs.ts makes, so this block and a relay approval can never disagree about
  // the job's scope (defect 2).
  pi.on("tool_call", async (event, ctx) => {
    if (process.env.PI_DELEGATE_ROLE !== "orchestrator") return;
    if (isSandboxedChild()) return;
    if (event.toolName !== "bash") return;
    const input = event.input as { command?: unknown };
    if (typeof input.command !== "string" || input.command.trim().length === 0) return;
    const merges = detectMergeBackCommands(input.command).filter((entry) => !entry.controlOnly);
    if (merges.length === 0) return;

    const jobId = jobIdForEnv();
    const rules = await resolveJobRuleIds(ctx.cwd, jobId);
    if (rules.length === 0) return;

    let coverage: Awaited<ReturnType<typeof checkProveCoverage>>;
    try {
      coverage = await checkProveCoverage(ctx.cwd, rules, { worktreePath: merges[0].worktree ?? undefined });
    } catch (error) {
      // Fail closed: an internal error is a refusal, never a pass.
      coverage = {
        ok: false,
        covered: [],
        missing: rules,
        report:
          "PROVE coverage check crashed: " + (error instanceof Error ? error.message : String(error)) +
          " (scoped rules: " + rules.join(", ") + ")",
      };
    }
    if (coverage.ok) return;

    return {
      block: true,
      reason:
        "MERGE BLOCKED \u2014 PROVE receipts missing for this merge-back (HANDOFF_BLOCKED). " +
        "The job's recorded SPEC-SCOPE names " + rules.join(", ") + ", so the merge execution path " +
        "requires a VERIFIED host-side prove receipt for EVERY scoped rule \u2014 no matter how the " +
        "approval arrived (typed in the pane or relayed via job_approve). Missing: " +
        coverage.missing.join(", ") + ". " +
        "Run `make prove args='--rule <id>'` once per scoped rule (the bash gate hook " +
        "dispatches it into the gate VM; the extension records the receipt in the host receipts dir), " +
        "then retry the merge.\n" + coverage.report,
    };
  });

  // GATE-VM EXECUTION (host shell → gate VM). The authoritative gates used to
  // be typed into the host's bash tool, so the host shell executed the
  // worktree's Makefile / cmd/spec directly. They now run in the locked-down
  // gate VM instead: a declared gate command typed here is recognised
  // (`resolveDeclaredGateCommand`), the host integrity check runs FIRST (the
  // trust anchor — dirty gate files ⇒ refuse, never execute), the DB-backed
  // gates get the Postgres pre-sequence, and the bash input is REWRITTEN to the
  // equivalent `sbx exec` line. The host therefore executes only `sbx`; the
  // gate argv is passed verbatim to the VM. A compound command (pipe, redirect,
  // several segments) is not rewritten: it is not the declared invocation.
  pi.on("tool_call", async (event, ctx) => {
    if (isSandboxedChild()) return;
    if (event.toolName !== "bash") return;
    const input = event.input as { command?: unknown };
    if (typeof input.command !== "string" || input.command.trim().length === 0) return;
    const resolved = resolveDeclaredGateCommand(input.command, ctx.cwd);
    if (!resolved) return;

    // 1. Host-side integrity check — unchanged, and still BEFORE any gate
    // execution: the four gate paths must match HEAD in this worktree.
    let integrity: IntegrityCheckResult;
    try {
      integrity = await checkWorktreeIntegrity(resolved.cwd);
    } catch (error) {
      integrity = {
        clean: false,
        refused: true,
        report:
          "\u2500\u2500 INTEGRITY CHECK (host, pre-gate) \u2500\u2500\nintegrity check crashed: " +
          (error instanceof Error ? error.message : String(error)),
        steps: [],
        paths: [],
      };
    }
    if (integrity.refused || !integrity.clean) {
      await safeHostReceipt({
        cwd: ctx.cwd,
        agent: process.env.PI_DELEGATE_ROLE ?? "agent",
        commands: integrity.steps.map((step) => ({
          command: step.command,
          exitCode: step.exitCode,
          timedOut: step.timedOut,
          durationMs: step.durationMs,
        })),
        ok: false,
        note: "integrity check (host, pre-gate VM): REFUSED \u2014 gate command not executed",
      });
      return {
        block: true,
        reason:
          integrityRefusalReason(integrity) +
          "\n\nThe gate command was NOT executed anywhere (no gate VM exec): " + resolved.segment,
      };
    }

    // 2. DB-backed gates only: start Postgres, wait, reset the test DB. A
    // failure here stops the gate with a named step instead of running it
    // against a broken database.
    try {
      if (resolved.db) {
        const prep = await prepareGateDbVm(resolved.cwd);
        if (!prep.ok && prep.step) {
          const failing = prep.step;
          return {
            block: true,
            reason:
              "GATE VM DB PRE-SEQUENCE FAILED \u2014 the gate command was not run (no verdict).\n" +
              "- " + failing.command + " \u2014 " +
              (failing.exitCode === null ? "no exit code" : "exit " + String(failing.exitCode)) +
              " \u00b7 " + failing.reason + "\n" + tailLines(failing.stdout + failing.stderr, 20),
          };
        }
      }

      // 3. Lazily create/reuse the session's gate VM for this worktree path and
      // rewrite the bash input to the equivalent `sbx exec` argv.
      const sandbox = await ensureGateSandbox(resolved.cwd);
      const rewritten = gateVmShellCommand(
        sandbox.name,
        resolved.cwd,
        gateVmExecEnv(resolved.cwd, { db: resolved.db }),
        appendGateDbOverride(resolved.argv, resolved.db),
      );
      (input as { command: string }).command = rewritten;
      // The host-side watchdog for the bash-dispatched gate: sbx exec has no
      // timeout flag, so bound the host command with the declared stall budget.
      // The bash tool's `timeout` parameter is in SECONDS (pi schema), so the
      // ms budget is converted up. A caller that already asked for MORE time
      // keeps its value.
      const stallMs = workerStallTimeoutMs(process.env, ctx.cwd);
      if (stallMs > 0) {
        const stallSeconds = Math.ceil(stallMs / 1000);
        const requested = (input as { timeout?: number }).timeout;
        (input as { timeout?: number }).timeout =
          typeof requested === "number" && requested > stallSeconds ? requested : stallSeconds;
      }
      void logGateDebug(
        ctx.cwd,
        "gate command re-dispatched to gate VM " + sandbox.name + " (worktree " + resolved.cwd +
          "): " + resolved.argv.join(" "),
      );
    } catch (error) {
      return {
        block: true,
        reason:
          "GATE VM UNAVAILABLE \u2014 the gate command was not run (no verdict): " +
          (error instanceof Error ? error.message : String(error)) +
          "\nThe gate set has no host fallback: a gate that cannot run in the gate VM FAILS.",
      };
    }
  });

  // Gate VM lifecycle: session-scoped reuse, all torn down at session end.
  // `session_shutdown` is the clean session-end hook (RELOAD/NEW/RESUME/FORK and
  // exit all emit it), so the VMs survive across turns and gate decisions and
  // die with the session. There is no local TTL; the host owns wall-clock.
  pi.on("session_shutdown", async () => {
    try {
      await teardownGateSandboxes();
    } catch (error) {
      console.error("[delegate] gate VM teardown failed:", error);
    }
  });

  // PROVE receipt writer (merge-back evidence). The extension records every
  // scoped prove run, because the merge-back gates (./mergegate.ts
  // checkProveCoverage, called by the jobs.ts approval relay and the
  // merge-path hook above) trust host receipts and
  // nothing else \u2014 and no worker ever writes one. Active in CHILD sessions
  // only (orchestrators): prove is the orchestrator's merge-back step, and a
  // main session has no job to prove for. HOST-ONLY resolution: the receipt
  // always lands in THIS session's own receipts dir. There is deliberately NO
  // leading-`cd` redirect \u2014 routing evidence into the worktree would let a
  // worker pre-forge a VERIFIED receipt inside its own mount.
  pi.on("tool_result", async (event, ctx) => {
    if (process.env.PI_DELEGATE_CHILD !== "1") return;
    if (isSandboxedChild()) return;
    if (event.toolName !== "bash") return;
    const input = event.input as { command?: unknown };
    if (typeof input.command !== "string" || input.command.trim().length === 0) return;
    const command = input.command;
    const ruleIds = proveRuleIdsIn(command);
    if (ruleIds.length === 0) return;

    const segments = splitTopLevelSegments(command)
      .map((segment) => segment.trim())
      .filter((segment) => segment.length > 0);
    if (segments.length === 0) return;

    // A leading `cd <worktree>` is still tolerated for COMMAND IDENTIFICATION
    // (the orchestrator's handoff form), but it never moves the receipt: proofs
    // are host artifacts.
    const cdTarget = leadingCdTarget(segments[0]);
    const rest = cdTarget !== null ? segments.slice(1) : segments;
    const receiptCwd = ctx.cwd;

    // The prove command is DECLARED (WORKFLOW.md `prove_cmd`), never
    // hardcoded: an unavailable policy means the gate set is undeclared, so no
    // run can be identified as prove \u2014 log it and write nothing (the
    // merge-back gate then refuses for the missing policy anyway).
    const loaded = loadWorkflowPolicy(receiptCwd);
    if (!loaded.ok) {
      void logGateDebug(receiptCwd, "prove receipt skipped (policy unavailable): " + command);
      return;
    }
    const proveTokens = splitCommandLine(loaded.policy.proveCmd);
    // The command may already be the REWRITTEN `sbx exec` line (the gate-VM
    // tool_call hook ran before this tool_result). Unwrap it: the in-VM argv is
    // what identifies a prove run, and `-w` names the worktree whose gate VM
    // executed it. A plain (unrewritten) command keeps the original matching.
    const unwrapped = parseGateVmShellCommand(command);
    const proves =
      unwrapped !== null
        ? isProveSegment(unwrapped.argv.join(" "), proveTokens)
        : rest.some((segment) => isProveSegment(segment, proveTokens));
    if (!proves) return;

    const exitCode = bashExitCodeOf(event);
    try {
      // The prove command ran in the gate VM (the bash tool_call hook rewrites
      // a declared gate command to `sbx exec`), so the receipt names the VM it
      // executed in. A missing VM entry simply means the receipt predates the
      // rewrite (or the command was identified after the fact) — no key.
      const proveCwd =
        unwrapped && unwrapped.worktree
          ? unwrapped.worktree
          : cdTarget !== null
            ? path.resolve(ctx.cwd, cdTarget)
            : ctx.cwd;
      await writeReceipt({
        cwd: receiptCwd,
        agent: process.env.PI_DELEGATE_ROLE ?? "worker",
        session: process.env.PI_SESSION_ID,
        // The FULL command line, so the receipt names the run a human/checker
        // reads back (including the `cd`); its exit code decides VERIFIED.
        commands: [{ command, exitCode, timedOut: false, durationMs: 0 }],
        ok: exitCode === 0,
        note: "prove rules=" + ruleIds.join(","),
        sandbox: gateSandboxFor(proveCwd)?.name,
      });
    } catch (error) {
      console.error("[delegate] prove receipt failed:", error);
    }
  });

  // Tool execute results cannot set runtime error state directly, so patch it
  // through the result hook. AnyToolDetails: the background start-failure
  // result also sets isError (BackgroundJobDetails.isError).
  pi.on("tool_result", (event) => {
    if (event.toolName !== "subagent") return;
    const details = event.details as AnyToolDetails | undefined;
    if (details?.isError) return { isError: true };
  });

  // Batch 1: POST-WRITE scoped gate. Sandboxed workers are inert in-VM and
  // verified by the HOST after exit (the gate VM executes), so this hook serves
  // only a HOST-PASSTHROUGH child — a role no profile defines today, since the
  // researcher is mountless; the seam stays for a future deliberate host role:
  // after a successful edit/write of a gate-relevant file (.go / .feature /
  // .yaml / docs/spec/**) the extension runs `make audit/static` + the scoped
  // spec check and appends a failure block to the tool result. Successes stay
  // silent (context stays small); a debounced skip still leaves a one-line note
  // so the worker knows a gate is due. The extension writes the receipt, never
  // the worker.
  pi.on("tool_result", async (event, ctx) => {
    if (!isWorkerChild()) return;
    if (event.toolName !== "edit" && event.toolName !== "write") return;
    if (event.isError) return;
    const input = event.input as { path?: unknown };
    if (typeof input.path !== "string" || input.path.length === 0) return;
    if (!isGateRelevantWrite(ctx.cwd, input.path)) return;

    const sessionKey = process.env.PI_SESSION_ID ?? "proc:" + String(process.pid);
    if (shouldRunPostWriteGate(sessionKey) !== "run") {
      void logGateDebug(ctx.cwd, "post-write gate skipped (debounce) after " + event.toolName + " " + input.path);
      return {
        content: appendToolResultNote(
          event.content,
          "\n[scoped gate] run skipped (debounced ~10s) — a gate run is due/running; results land after the next write or at settle.",
        ),
      };
    }

    markPostWriteGateStart(sessionKey);
    let run: Awaited<ReturnType<typeof runScopedGate>>;
    try {
      run = await runScopedGate({ cwd: ctx.cwd, scopeEnv: process.env.PI_DELEGATE_SPEC_SCOPE });
    } catch (error) {
      // Internal error ⇒ failure, never a silent pass (fail closed).
      console.error("[delegate] post-write gate crashed:", error);
      run = {
        ok: false,
        steps: [
          {
            command: "(scoped gate)",
            argv: [],
            ok: false,
            exitCode: null,
            timedOut: false,
            durationMs: 0,
            stdout: "",
            stderr: error instanceof Error ? error.message : String(error),
            spawnError: null,
            reason: "gate runner threw",
          },
        ],
      };
    } finally {
      markPostWriteGateEnd(sessionKey);
    }
    try {
      await writeReceipt({
        cwd: ctx.cwd,
        agent: process.env.PI_DELEGATE_ROLE ?? "worker",
        session: process.env.PI_SESSION_ID,
        commands: receiptCommandsOf(run),
        ok: run.ok,
        note: "post-write gate after " + event.toolName + " " + input.path,
        sandbox: run.steps.find((step) => step.gateSandbox)?.gateSandbox,
      });
    } catch (error) {
      console.error("[delegate] post-write receipt failed:", error);
    }
    if (run.ok) return;
    return {
      content: appendToolResultNote(
        event.content,
        "\n" +
          formatGateReport(run, {
            header: "\u2500\u2500 SCOPED GATE FAILED \u2500\u2500",
            footer:
              "Fix these before reporting done. The gate reruns after your next write; " +
              "make audit/static and spec check must pass before settle.",
            maxLines: 30,
          }),
      ),
    };
  });

  // Phase 5 two-tier: workers cannot recurse — skip the subagent tool
  // registration entirely in a worker child's process (PI_DELEGATE_ROLE=worker
  // is set on every foreground spawn), so a worker has neither the tool nor
  // the temptation, and its child context stays smaller. Main sessions and
  // child orchestrators keep it. job_status and the UI surfaces stay
  // registered (inert for workers: job_status is in no worker's tools list,
  // the rest is hasUI-gated).
  if (process.env.PI_DELEGATE_ROLE !== "worker") pi.registerTool({
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

  // Phase 3: pick up background job results on demand. No id → list all jobs;
  // id → per-job report (running/done/failed/aborted) from the .pi/jobs artifacts.
  // Phase 4: a specific FINISHED job additionally injects its result as a
  // delegate-job-result custom message (LLM context, no turn triggered) —
  // exactly once per job per session (surfacedJobIds).
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

  // Phase 7: parent-relayed approval. A confirm-handoff job idles in its pane
  // after emitting AWAITING_APPROVAL; this tool types the decision into that
  // pane from the MAIN session (tmux send-keys -l + Enter), so nobody has to
  // attach. Preconditions and refusal messages live in jobs.ts
  // (relayApprovalDecision); duplicates are never sent automatically — the
  // user decides, and a duplicate delivery is harmless but confusing.
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

  // ── UI commands + shortcut ────────────────────────────────────────────────
  // Registration happens at load time and is inert in -p/json mode: every
  // handler guards dialog/custom-component calls with ctx.hasUI (see the open*
  // helpers above) and the widget refresh is fire-and-forget.

  pi.registerCommand("jobs", {
    description: "Inspect background subagent jobs (list, or view one job's output)",
    // Completion providers receive only (prefix) — no ctx — so process.cwd()
    // is the only option here. pi runs with cwd = project root, identical to
    // the ctx.cwd every other handler in this extension uses.
    getArgumentCompletions: (prefix) => {
      const items: AutocompleteItem[] = listJobIdsSync(process.cwd()).map((id) => ({ value: id, label: id }));
      const filtered = items.filter((item) => item.value.startsWith(prefix));
      if (filtered.length === 0) return null;
      return filtered;
    },
    handler: async (args, ctx) => {
      const requested = (args ?? "").trim();
      if (requested) {
        await openJobViewer(ctx, requested);
        return;
      }
      await openJobsView(ctx);
    },
  });

  pi.registerCommand("agents", {
    description: "List discovered subagent agents and view their details",
    handler: async (_args, ctx) => {
      await openAgentsView(ctx);
    },
  });

  // ── Phase 4 renderers ──────────────────────────────────────────────────
  // "delegate-job" ENTRY cards (pi.appendEntry) are durable TUI-only content —
  // they NEVER reach the LLM. "delegate-job-result" MESSAGES (pi.sendMessage)
  // DO participate in LLM context; the renderer only restyles the transcript.
  pi.registerEntryRenderer<JobCardData>("delegate-job", (entry, options, theme) =>
    renderJobCardEntry(entry, options, theme),
  );
  pi.registerMessageRenderer<JobResultDetails>("delegate-job-result", (message, options, theme) =>
    renderJobResultMessage(message, options, theme),
  );

  // ctrl+\: byte 0x1C, which legacy terminals deliver directly and pi-tui's
  // parseKey maps to "ctrl+\\" (keys.js:1104). Unbound anywhere: pi core
  // defaults, user keybindings.json, and no extension use it. Rejected:
  // ctrl+y = tui.editor.yank (extension wins, yank breaks); ctrl+i/m/h =
  // legacy C0 collisions with tab/enter/backspace; ctrl+j = newline.
  pi.registerShortcut("ctrl+\\", {
    description: "Open the background jobs view (same as /jobs)",
    handler: async (ctx) => {
      await openJobsView(ctx);
    },
  });

  // ── Surfacing boundaries (./surfacing.ts) ─────────────────────────────
  // Round-start job injection, the settle auto-surface and the ambient footer
  // clear are registered by one call, in the order they had when they were
  // inline here: a settle clears the live counts, then surfaces finished jobs,
  // and only then reaches the worker-settle gate below.
  registerSurfacingHooks(pi);

  // Batch 1 + batch 2: WORKER-SETTLE gate. A worker child may not settle as
  // "done" while the scoped gate fails: the same two commands as the
  // post-write gate run again (no debounce), the extension writes a receipt,
  // and a bounded continue:true hands the worker turns to fix the failures.
  //
  // Batch 2 additions: (a) past the batch-1 immediate window the continuation
  // waits an exponential backoff (2s, 4s at the default base) and its reason
  // text is ESCALATING; after the last attempt the hard stop prints the full
  // per-attempt receipt trail. (b) A PASSING ladder is followed by
  // reconciliation-lite: the ladder is re-run and diffed against the newest
  // passing receipt (HEAD sha, docs/spec/rules catalog hash, exit codes). The
  // receipt for
  // THIS attempt is written first, so the checkpoint is the tree state the
  // green ladder just described — the drift it catches is a concurrent writer
  // (another worker committing mid-ladder), not a day-old receipt from a
  // sibling. That matters: `<cwd>/.pi/receipts/` is per REPO, not per worker,
  // and a mismatch against a stale day-long checkpoint would block a
  // legitimately green worker forever. A mismatch still counts as a failed
  // attempt and is reported in full.
  //
  // Loop safety: continue is conditional on NEW state — the attempt counter
  // (module state, reset by a PASSING run) strictly increases per failure, so
  // the attempt cap hands out the HARD STOP message as a TERMINAL turn
  // (continue:true once, bounded by the one-shot settleGateTerminal flag)
  // rather than an entries-only return. The flag is consumed by the very next
  // settle, which runs no gate and returns undefined — the proven-safe ending
  // (assistant message last ⇒ print mode flushes the worker's output).
  pi.on("agent_before_settle", async (event, ctx) => {
    if (!isWorkerChild()) return;
    // Terminal turn already handed out: end unconditionally, exactly once.
    if (settleGateTerminal) {
      settleGateTerminal = false;
      resetSettleGateAttempts();
      return undefined;
    }
    if (event.outcome !== "completed") return;

    const maxAttempts = settleGateMaxAttempts(ctx.cwd);
    const baseMs = settleBackoffBaseMs(ctx.cwd);
    const attempt = settleGateStateFor().attempts + 1;
    const agentName = process.env.PI_DELEGATE_ROLE ?? "worker";
    const scopeEnv = process.env.PI_DELEGATE_SPEC_SCOPE;

    let run = await runWorkerGate(ctx.cwd, scopeEnv);

    // Receipt FIRST (note names the attempt number for every attempt): on a
    // green ladder this receipt is the reconciliation checkpoint.
    const receiptPath = await writeSettleReceipt({
      cwd: ctx.cwd,
      agent: agentName,
      commands: receiptCommandsOf(run),
      ok: run.ok,
      note: "settle gate attempt " + String(attempt) + "/" + String(maxAttempts),
      sandbox: run.steps.find((step) => step.gateSandbox)?.gateSandbox,
    });

    let reconciliation: ReconciliationResult | undefined;
    if (run.ok) {
      // Reconciliation-lite: only worth running on a green ladder, and only
      // once per settle. A crash is a failure (fail closed), never a pass.
      try {
        reconciliation = await runReconciliation(ctx.cwd, {
          agent: agentName,
          session: process.env.PI_SESSION_ID,
          scopeEnv,
        });
      } catch (error) {
        console.error("[delegate] reconciliation crashed:", error);
        reconciliation = {
          ok: false,
          report:
            "── RECONCILIATION ──\nstatus: MISMATCH — reconciliation crashed: " +
            (error instanceof Error ? error.message : String(error)),
          steps: [],
          receipt: null,
        };
      }
      if (!reconciliation.ok) {
        run = reconciliationGateRun(reconciliation);
        if (reconciliation.steps.length > 0) {
          // The reconciliation ladder's own commands go into their own receipt
          // so a later trail can name exactly which run drifted.
          await writeSettleReceipt({
            cwd: ctx.cwd,
            agent: agentName,
            commands: receiptCommandsOf({ ok: reconciliation.ok, steps: reconciliation.steps }),
            ok: false,
            note: "reconciliation attempt " + String(attempt) + "/" + String(maxAttempts),
            sandbox: reconciliation.steps.find((step) => step.gateSandbox)?.gateSandbox,
          });
        }
      }
    }

    if (run.ok) {
      resetSettleGateAttempts();
      settleGateTerminal = false;
      return undefined;
    }

    const plan = planSettleGateAttempt(attempt, { maxAttempts, baseMs, cwd: ctx.cwd });
    bumpSettleGateAttempts();
    recordSettleGateAttempt({ run, receiptPath });
    // Wait BEFORE handing the continuation back: the backoff is the point, and
    // an entries-only early return would burn the turn instead.
    if (plan.waitMs > 0) await sleepMs(plan.waitMs);

    const escalation = formatSettleEscalation(plan, recentSettleFailures());
    const header =
      "\u2500\u2500 SETTLE GATE BLOCKED (attempt " + String(attempt) + "/" + String(maxAttempts) + ") \u2500\u2500";
    const failureFooter =
      "HARD STOP \u2014 TERMINAL TURN: " + String(maxAttempts) + " failed gate attempts. " +
      "This is your LAST turn: make NO further changes (no edits, no fixes), run no more gate commands, " +
      "and reply with your verbatim failure report \u2014 each failing command, its exit code, the failing " +
      "output lines above, and the receipt path. That report is what your parent receives.";
    const footer = plan.terminal
      ? escalation + "\n" + formatSettleTrail(settleGateStateFor()) + "\n" + failureFooter
      : escalation + "\nDo not report done; fix the gate failures.";
    const content =
      formatGateReport(run, { header, footer, maxLines: plan.terminal ? 20 : 30 }) +
      "\nreceipt: " + (receiptPath ?? "(not written)") +
      (reconciliation && !reconciliation.ok ? "\n" + reconciliation.report : "");
    const entries: SessionBoundaryDraft[] = [
      ...event.entries,
      { type: "custom_message", customType: "delegate-gate", content, display: true },
    ];
    // Hard stop: still continue ONCE (the terminal turn) so the run ends on an
    // assistant message instead of an entries-only return (see the
    // settleGateTerminal comment above).
    if (plan.terminal) settleGateTerminal = true;
    return { entries, continue: true };
  });
}

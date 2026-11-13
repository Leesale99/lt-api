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
 */

import { spawn } from "node:child_process";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import type { AgentToolResult, ThinkingLevel } from "@earendil-works/pi-agent-core";
import type { Message } from "@earendil-works/pi-ai";
import {
  CONFIG_DIR_NAME,
  DEFAULT_MAX_BYTES,
  DEFAULT_MAX_LINES,
  DynamicBorder,
  type ExtensionAPI,
  type ExtensionContext,
  formatReadLineRange,
  getAgentDir,
  getMarkdownTheme,
  type SessionBoundaryDraft,
  truncateHead,
  withFileMutationQueue,
} from "@earendil-works/pi-coding-agent";
import {
  type AutocompleteItem,
  Box,
  Container,
  Markdown,
  Spacer,
  type SelectItem,
  SelectList,
  Text,
  truncateToWidth,
} from "@earendil-works/pi-tui";
import { Type } from "typebox";
import { type AgentConfig, type AgentScope, discoverAgents } from "./agents.ts";
import {
  createChildStreamParser,
  emptyUsage,
  getFinalOutput,
  truncateParallelOutput,
} from "./output.ts";
import {
  type JobEntry,
  type JobStatus,
  type RunnerKind,
  formatElapsed,
  formatJobDetail,
  formatJobList,
  getJob,
  getJobResultText,
  jobsDirFor,
  listJobEntries,
  listJobIdsSync,
  listJobs,
  peekJobEntry,
  relayApprovalDecision,
  startBackgroundJob,
  taskPreview,
} from "./jobs.ts";

const MAX_PARALLEL_TASKS = 8;
const MAX_CONCURRENCY = 4;
const COLLAPSED_ITEM_COUNT = 10;
const TRUNCATION_NOTICE_RESERVE = 256;
/** Poll interval (ms) for background-job completion notifications. */
const JOB_POLL_MS = 10_000;
/** Max bytes of a job result injected into LLM context (pickup + settle surfacing). */
const JOB_RESULT_MAX_BYTES = 8192;
/** Appended to a cut result so the model knows where the full text lives. */
const JOB_RESULT_TRUNCATION_MARKER = "\n\n(truncated — full text via job_status)";
/** Headless settle waits at most this long for this session's running jobs. */
const SETTLE_WAIT_MS_DEFAULT = 90_000;
const SETTLE_WAIT_MS_MAX = 10 * 60_000;
const SETTLE_WAIT_POLL_MS = 1_000;
/** Result lines shown by the collapsed delegate-job-result card. */
const JOB_RESULT_PREVIEW_LINES = 12;
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

function formatTokens(count: number): string {
  if (count < 1000) return count.toString();
  if (count < 10000) return `${(count / 1000).toFixed(1)}k`;
  if (count < 1000000) return `${Math.round(count / 1000)}k`;
  return `${(count / 1000000).toFixed(1)}M`;
}

function formatUsageStats(
  usage: {
    input: number;
    output: number;
    cacheRead: number;
    cacheWrite: number;
    cost: number;
    contextTokens?: number;
    turns?: number;
  },
  model?: string,
): string {
  const parts: string[] = [];
  if (usage.turns) parts.push(`${usage.turns} turn${usage.turns > 1 ? "s" : ""}`);
  if (usage.input) parts.push(`↑${formatTokens(usage.input)}`);
  if (usage.output) parts.push(`↓${formatTokens(usage.output)}`);
  if (usage.cacheRead) parts.push(`R${formatTokens(usage.cacheRead)}`);
  if (usage.cacheWrite) parts.push(`W${formatTokens(usage.cacheWrite)}`);
  if (usage.cost) parts.push(`$${usage.cost.toFixed(4)}`);
  if (usage.contextTokens && usage.contextTokens > 0) {
    parts.push(`ctx:${formatTokens(usage.contextTokens)}`);
  }
  if (model) parts.push(model);
  return parts.join(" ");
}

function formatToolCall(
  toolName: string,
  args: Record<string, unknown>,
  themeFg: (color: any, text: string) => string,
): string {
  const shortenPath = (p: string) => {
    const home = os.homedir();
    return p.startsWith(home) ? `~${p.slice(home.length)}` : p;
  };

  switch (toolName) {
    case "bash": {
      const command = (args.command as string) || "...";
      const preview = command.length > 60 ? `${command.slice(0, 60)}...` : command;
      return themeFg("muted", "$ ") + themeFg("toolOutput", preview);
    }
    case "read": {
      const rawPath = (args.file_path || args.path || "...") as string;
      const filePath = shortenPath(rawPath);
      const lineRange = formatReadLineRange(args.offset, args.limit);
      let text = themeFg("accent", filePath);
      if (lineRange) {
        text += themeFg("warning", lineRange);
      }
      return themeFg("muted", "read ") + text;
    }
    case "write": {
      const rawPath = (args.file_path || args.path || "...") as string;
      const filePath = shortenPath(rawPath);
      const content = (args.content || "") as string;
      const lines = content.split("\n").length;
      let text = themeFg("muted", "write ") + themeFg("accent", filePath);
      if (lines > 1) text += themeFg("dim", ` (${lines} lines)`);
      return text;
    }
    case "edit": {
      const rawPath = (args.file_path || args.path || "...") as string;
      return themeFg("muted", "edit ") + themeFg("accent", shortenPath(rawPath));
    }
    case "ls": {
      const rawPath = (args.path || ".") as string;
      return themeFg("muted", "ls ") + themeFg("accent", shortenPath(rawPath));
    }
    case "find": {
      const pattern = (args.pattern || "*") as string;
      const rawPath = (args.path || ".") as string;
      return themeFg("muted", "find ") + themeFg("accent", pattern) + themeFg("dim", ` in ${shortenPath(rawPath)}`);
    }
    case "grep": {
      const pattern = (args.pattern || "") as string;
      const rawPath = (args.path || ".") as string;
      return (
        themeFg("muted", "grep ") +
        themeFg("accent", `/${pattern}/`) +
        themeFg("dim", ` in ${shortenPath(rawPath)}`)
      );
    }
    default: {
      const argsStr = JSON.stringify(args);
      const preview = argsStr.length > 50 ? `${argsStr.slice(0, 50)}...` : argsStr;
      return themeFg("accent", toolName) + themeFg("dim", ` ${preview}`);
    }
  }
}

type ChildStatus = "pending" | "running" | "completed" | "failed" | "aborted";
type UsageStats = ReturnType<typeof emptyUsage>;

interface SingleResult {
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
type AnyToolDetails = SubagentDetails | BackgroundJobDetails;

function isFailedResult(result: SingleResult): boolean {
  return result.status === "failed" || result.status === "aborted";
}

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
  add("Assistant output", getFinalOutput(result.messages));
  return evidence.join("\n\n") || "(no output)";
}

function getFailureSummary(result: SingleResult): string {
  return (
    result.errorMessage?.trim() ||
    (result.terminationSignal ? `Terminated by signal ${result.terminationSignal}` : "") ||
    result.stderr.trim() ||
    getFinalOutput(result.messages) ||
    "(no output)"
  );
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

type DisplayItem = { type: "text"; text: string } | { type: "toolCall"; name: string; args: Record<string, any> };

function getDisplayItems(messages: Message[]): DisplayItem[] {
  const items: DisplayItem[] = [];
  for (const msg of messages) {
    if (msg.role === "assistant") {
      for (const part of msg.content) {
        if (part.type === "text") items.push({ type: "text", text: part.text });
        else if (part.type === "toolCall") items.push({ type: "toolCall", name: part.name, args: part.arguments });
      }
    }
  }
  return items;
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

async function writePromptToTempFile(agentName: string, prompt: string): Promise<{ dir: string; filePath: string }> {
  const tmpDir = await fs.promises.mkdtemp(path.join(os.tmpdir(), "pi-subagent-"));
  const safeName = agentName.replace(/[^\w.-]+/g, "_");
  const filePath = path.join(tmpDir, `prompt-${safeName}.md`);
  await withFileMutationQueue(filePath, async () => {
    await fs.promises.writeFile(filePath, prompt, { encoding: "utf-8", mode: 0o600 });
  });
  return { dir: tmpDir, filePath };
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
 */
function buildTaskPrompt(agent: AgentConfig, task: string): string {
  const skills = agent.skills ?? [];
  if (skills.length === 0) return `Task: ${task}`;
  const [first, ...rest] = skills;
  let prompt = `/skill:${first} Task: ${task}`;
  if (rest.length > 0) {
    prompt +=
      `\n\nAlso required: load and follow the skills ${rest.map((s) => `"${s}"`).join(", ")}` +
      ` (each skill's SKILL.md path is advertised in your context) before doing the task.`;
  }
  return prompt;
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
    if (agent.systemPrompt.trim()) {
      const tmp = await writePromptToTempFile(agent.name, agent.systemPrompt);
      tmpPromptDir = tmp.dir;
      tmpPromptPath = tmp.filePath;
      args.push("--append-system-prompt", tmpPromptPath);
    }

    if (signal?.aborted) {
      currentResult.status = "aborted";
      currentResult.errorMessage = "Subagent was aborted before starting";
      return currentResult;
    }

    args.push(buildTaskPrompt(agent, task));
    emitUpdate();
    let wasAborted = false;
    const outcome = await new Promise<ProcessOutcome>((resolve) => {
      const invocation = getPiInvocation(args);
      let settled = false;
      let exited = false;
      let closed = false;
      let killTimer: ReturnType<typeof setTimeout> | undefined;
      let buffer = "";

      const processLine = (line: string) => {
        if (stream.processLine(line)) emitUpdate();
      };

      let proc: ReturnType<typeof spawn>;
      const finish = (result: ProcessOutcome) => {
        if (settled) return;
        settled = true;
        closed = true;
        if (killTimer) clearTimeout(killTimer);
        if (signal) signal.removeEventListener("abort", abortProcess);
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
      // PI_MODEL / PI_REASONING_LEVEL into tool subprocesses; scrub them so the
      // parent's session identity never leaks into a subagent's env.
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
        buffer += data.toString();
        const lines = buffer.split("\n");
        buffer = lines.pop() || "";
        for (const line of lines) processLine(line);
      });
      proc.stderr?.on("data", (data) => {
        currentResult.stderr += data.toString();
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

      if (signal) {
        if (signal.aborted) abortProcess();
        else signal.addEventListener("abort", abortProcess, { once: true });
      }
    });

    currentResult.exitCode = outcome.code;
    currentResult.terminationSignal = outcome.signal ?? undefined;
    if (!currentResult.model) currentResult.model = stream.model;
    currentResult.stopReason = stream.stopReason;
    currentResult.errorMessage ||= stream.errorMessage;

    if (wasAborted) {
      currentResult.status = "aborted";
      currentResult.errorMessage ||= "Subagent was aborted";
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
    return currentResult;
  } finally {
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
 * Orchestrator persona injected into MAIN sessions only (phase 2).
 * The extension sets PI_DELEGATE_CHILD=1 in every child spawn env; the env gate
 * here is what separates main from child. The decided text lives in
 * .pi/delegate/orchestrator.md (plain markdown body, no frontmatter) and is read
 * fresh on every before_agent_start — prompt edits apply without /reload. This
 * inline copy is the fallback for a missing/unreadable config file.
 */
const DEFAULT_ORCHESTRATOR_PROMPT = `
# Role
Senior backend engineer and mentor. The user is a junior developer preparing for job interviews — understanding beats shipping. Teach the concept and the why before the code; prefer hints and next steps over ready answers; keep it concise and senior-level.

# User-facing rules
- Do NOT write, edit, or create files unless the user explicitly asks to (e.g. "implement", "edit", "go ahead").
- Reading, searching, and explaining is always allowed.
- Default to explanation + minimal snippet / pseudo-code, not ready-to-apply diffs.

# Orchestrator rules
You are an orchestrator: you do not implement, search, or read the vault yourself. Route work via the subagent tool (foreground) or background jobs. Keep your context small: delegate recon to scout instead of reading files yourself.

## Routing policy (deterministic — first match wins)
0. Answerable from context or one trivial lookup → no delegation, or one foreground worker.
1. ≤1 file scope, ≤2 steps → ONE foreground worker.
2. ≥2 subtasks, each individually passing gate 1, independent → foreground tasks[] in parallel (same turn).
3. Background child orchestrator (agent \`orchestrator\`) ONLY if ALL hold:
   a. written plan exists (plan/phase file with ≥3 steps, or user explicitly frames multi-commit/multi-phase work)
   b. ≥3 file scopes (or ≥2 clearly disjoint work areas)
   c. tree helps: ≥2 sequential stages, at least one fanning out to ≥2 workers (collect→verify→fix loops count)
   d. no mid-flight human decision needed
Default: any gate-3 condition fails or is unknown → gate 2 (foreground parallel). When in doubt, foreground.
Before spawning background, state the verdict in your reply: routing: plan=<file or none/N steps> files=<n> tree=<yes/no> human=<yes/no> → <background|foreground>.
Agents: librarian → vault ops (Obsidian reads/writes); researcher → web research and library docs; go-worker → Go implementation tasks; scout → codebase recon; general → anything else.
Workers never delegate and never run in background — background is the orchestrator's privilege, and only this main session may start it (one level deep; a child orchestrator that tries is refused).

## Handoff modes for orchestrator tasks
A task delegated to the orchestrator agent must state its handoff mode in the task text: HANDOFF=auto or HANDOFF=confirm.
- HANDOFF=auto: the orchestrator implements via workers inside a dedicated git worktree (../lt-api-wt-<job-id> on branch delegate/<job-id>, created from HEAD), verifies, commits, merges back into the captured base branch, removes the worktree, and reports HANDOFF_COMPLETE with the merge summary.
- HANDOFF=confirm: after verification the orchestrator stops and emits an AWAITING_APPROVAL block (plan summary, branch, worktree path, files changed, test results) and ends its round without merging; the job surfaces to you as status awaiting_approval with that block as its result. Approve or reject from here with the job_approve tool: job_approve (id: the job id, message: "approved [note]") → the orchestrator merges back and reports HANDOFF_COMPLETE and the job advances to done; message "rejected: <reason>" → the orchestrator iterates (adjust or re-spawn workers) and emits a fresh AWAITING_APPROVAL block. Attaching to the pane still works: tmux attach -t pi-<job-id> and type the same message. Omitted HANDOFF means confirm (safe default).
Merge conflicts and blocked merges are reported as HANDOFF_BLOCKED with the conflict list; the worktree and branch stay in place for manual resolution, nothing is force-resolved, and implementation work never touches the parent checkout's working tree.
Session start: read the vault Home.md (~/Projects/vaults/lt-api/Home.md) via librarian. Its Now section declares the current task; load only what that task needs.
`;

/**
 * Read the orchestrator prompt fresh on every invocation so edits to
 * .pi/delegate/orchestrator.md apply without /reload. Resolved from the
 * session cwd (ctx.cwd — authoritative; identical to process.cwd() in
 * practice, pi runs with cwd = project root); a missing/unreadable file
 * falls back to the inline default so the extension never breaks.
 */
function loadOrchestratorPrompt(cwd: string): string {
  try {
    const filePath = path.resolve(cwd, CONFIG_DIR_NAME, "delegate", "orchestrator.md");
    return fs.readFileSync(filePath, "utf-8");
  } catch {
    return DEFAULT_ORCHESTRATOR_PROMPT;
  }
}

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

// ── Phase 4: job cards, result pickup, settle surfacing ─────────────────────

/** Extension load time: jobs finished before this must never resurface (previous sessions). */
const SESSION_START_MS = Date.now();
/** Job ids whose result was already injected as a delegate-job-result context message. */
const surfacedJobIds = new Set<string>();
/** Keys (id|status) of delegate-job cards already emitted — one card per transition. */
const emittedCardKeys = new Set<string>();
/** Set by the extension factory; card emission needs pi.appendEntry. */
let delegateApi: ExtensionAPI | undefined;

/** Payload persisted on delegate-job entry cards (also used as message details). */
interface JobCardData {
  id: string;
  agent: string;
  status: JobStatus;
  runner: RunnerKind;
  startedAt: string;
  finishedAt?: string;
}

interface JobResultDetails extends JobCardData {
  /** Result text as injected — already truncated, marker appended when cut. */
  text: string;
}

function cardDataOf(
  entry: Pick<JobEntry, "id" | "agent" | "status" | "runner" | "startedAt" | "finishedAt">,
): JobCardData {
  const data: JobCardData = {
    id: entry.id,
    agent: entry.agent,
    status: entry.status,
    runner: entry.runner,
    startedAt: entry.startedAt,
  };
  if (entry.finishedAt) data.finishedAt = entry.finishedAt;
  return data;
}

/**
 * Append a one-line delegate-job card. TUI-only (hasUI-gated; entries never
 * reach the LLM) and deduped per (id, status), so a job's start and its finish
 * each produce exactly one card.
 */
function emitJobCard(
  ctx: Pick<ExtensionContext, "hasUI">,
  entry: Pick<JobEntry, "id" | "agent" | "status" | "runner" | "startedAt" | "finishedAt">,
): void {
  if (!ctx.hasUI || !delegateApi) return;
  const key = entry.id + "|" + entry.status;
  if (emittedCardKeys.has(key)) return;
  emittedCardKeys.add(key);
  try {
    delegateApi.appendEntry("delegate-job", cardDataOf(entry));
  } catch {
    /* card is best effort */
  }
}

/** Byte-safe cut to ~8 KB (proportional shrink keeps multibyte sequences valid). */
function truncateJobResultText(text: string): { text: string; truncated: boolean } {
  if (Buffer.byteLength(text, "utf8") <= JOB_RESULT_MAX_BYTES) return { text, truncated: false };
  let cut = text;
  while (Buffer.byteLength(cut, "utf8") > JOB_RESULT_MAX_BYTES) {
    cut = cut.slice(0, Math.ceil(cut.length * 0.9));
  }
  return { text: cut, truncated: true };
}

/**
 * Build the delegate-job-result injection — shared by the job_status pickup and
 * the settle auto-surface: `content` is what the LLM reads, `details` feeds the
 * TUI message renderer (header + body). Phase 7: two phrasings — terminal
 * done/failed/aborted jobs read "finished with status ...", while
 * awaiting_approval jobs read "is awaiting_approval ... waiting for your
 * decision" with job_approve decision guidance.
 */
async function buildJobResultInjection(
  cwd: string,
  entry: JobEntry,
): Promise<{ content: string; details: JobResultDetails }> {
  const raw = await getJobResultText(cwd, entry.id);
  let base: string;
  if (raw === null) base = "(result text unavailable — job artifacts missing)";
  else if (raw.trim() === "") base = "(no assistant output)";
  else base = raw;
  const cut = truncateJobResultText(base);
  let text = cut.text;
  if (cut.truncated) text += JOB_RESULT_TRUNCATION_MARKER;
  const details: JobResultDetails = { ...cardDataOf(entry), text };
  let content: string;
  if (entry.status === "awaiting_approval") {
    // Phase 7: awaiting jobs surface with approval-oriented phrasing — the
    // parent must read "your decision is needed", not "finished".
    content =
      "Background job " + details.id + " (agent " + details.agent + ") is awaiting_approval: it ended its round with a confirm-handoff approval request and is waiting for your decision." +
      (cut.truncated ? " Approval request (truncated to ~8 KB)" : " Approval request") + ": " + text +
      "\nDecide with job_approve (id: \"" + details.id + "\", message: \"approved [note]\" or \"rejected: <reason>\").";
  } else {
    content = "Background job " + details.id + " (agent " + details.agent + ") finished with status " + details.status + ". Result";
    if (cut.truncated) content += " (truncated to ~8 KB)";
    content += ": " + text + "\nFull text: job_status id " + details.id + ".";
  }
  return { content, details };
}

/** A job is surfacable when terminal, finished after extension load, and not yet surfaced.
 * Awaiting_approval jobs MUST surface too — the parent needs to know a decision is
 * pending (with approval-oriented phrasing from buildJobResultInjection). */
function isSurfacableFinish(entry: JobEntry): boolean {
  if (entry.status === "running") return false;
  if (surfacedJobIds.has(entry.id)) return false;
  const finishedMs = entry.finishedAt ? Date.parse(entry.finishedAt) : NaN;
  return !Number.isNaN(finishedMs) && finishedMs > SESSION_START_MS;
}

interface SurfacableJobs {
  finished: JobEntry[];
  /** Running jobs this session started (after load) — candidates for the bounded wait. */
  runningHere: JobEntry[];
}

async function collectSurfacableJobs(ctx: ExtensionContext): Promise<SurfacableJobs> {
  const result: SurfacableJobs = { finished: [], runningHere: [] };
  for (const entry of await listJobEntries(ctx.cwd)) {
    if (entry.status === "running") {
      if (Date.parse(entry.startedAt) > SESSION_START_MS) result.runningHere.push(entry);
    } else if (isSurfacableFinish(entry)) {
      result.finished.push(entry);
    }
  }
  return result;
}

/** Headless-only bounded wait for this session's running jobs (env-overridable). */
function settleWaitMs(): number {
  const raw = Number(process.env.PI_DELEGATE_SETTLE_WAIT_MS);
  if (Number.isFinite(raw) && raw >= 0) return Math.min(raw, SETTLE_WAIT_MS_MAX);
  return SETTLE_WAIT_MS_DEFAULT;
}

/**
 * Settle-wait sleep. The timer MUST be referenced: in headless runs the event
 * loop can otherwise drain during the sleep (keep-alive sockets time out a few
 * seconds after agent_end) and the process exits cleanly mid-boundary — no
 * settle, no output, exit 0. Verified empirically; do not unref here.
 */
function sleepMs(ms: number): Promise<void> {
  return new Promise((resolve) => {
    setTimeout(resolve, ms);
  });
}

// ── UI surface (widget + /jobs + /agents + shortcut) ─────────────────────────

const JOB_WIDGET_KEY = "delegate-jobs";
const JOB_WIDGET_MAX_LINES = 6;
/** Lines of content shown at once by the text viewer (header/footer excluded). */
const VIEWER_ROWS = 16;

function jobStatusIcon(status: JobStatus): string {
  switch (status) {
    case "awaiting_approval":
      return "⏸";
    case "running":
      return "⏳";
    case "done":
      return "✓";
    case "failed":
      return "✗";
    default:
      return "◐";
  }
}

/** Elapsed for running jobs; finished-ago for the rest (finishedAt when known). */
function jobAge(entry: Pick<JobEntry, "status" | "startedAt" | "finishedAt">): string {
  if (entry.status === "running") return formatElapsed(entry.startedAt);
  return formatElapsed(entry.finishedAt ?? entry.startedAt) + " ago";
}

/**
 * Rebuild the delegate-jobs widget: one line per background job (max 6, then
 * "+N more"); empty registry clears the widget (content undefined). Best
 * effort — guarded by hasUI and swallowing errors so callers can fire it from
 * tool executes and timers without ever breaking a non-UI run.
 */
async function refreshJobWidget(ctx: ExtensionContext): Promise<void> {
  if (!ctx.hasUI) return;
  try {
    const entries = await listJobEntries(ctx.cwd);
    const lines: string[] = [];
    for (const entry of entries.slice(0, JOB_WIDGET_MAX_LINES)) {
      lines.push(jobStatusIcon(entry.status) + " " + entry.id + " · " + entry.agent + " · " + jobAge(entry));
    }
    if (entries.length > JOB_WIDGET_MAX_LINES) {
      lines.push("+ " + (entries.length - JOB_WIDGET_MAX_LINES) + " more");
    }
    ctx.ui.setWidget(JOB_WIDGET_KEY, lines.length > 0 ? lines : undefined, { placement: "aboveEditor" });
  } catch {
    /* widget refresh is best effort */
  }
}

/**
 * Scrollable text viewer as an inline custom component (replaces the editor
 * until esc/enter). Deliberately NOT the built-in ScrollView: ctx.ui.custom
 * components render through component.render(width) without the layout-node
 * pass, and ScrollView's viewport slicing only happens under the TUI's layout
 * frame — so the viewer windows the content lines itself with a scrollTop.
 */
async function openTextViewer(ctx: ExtensionContext, title: string, lines: string[]): Promise<void> {
  if (!ctx.hasUI) {
    ctx.ui.notify("The viewer needs an interactive session (TUI).", "warning");
    return;
  }
  await ctx.ui.custom<void>((tui, theme, keybindings, done) => {
    let scrollTop = 0;
    let cachedWidth: number | undefined;
    let cachedLines: string[] | undefined;

    const maxScrollTop = () => Math.max(0, lines.length - VIEWER_ROWS);
    const clampScroll = () => {
      scrollTop = Math.max(0, Math.min(maxScrollTop(), scrollTop));
    };
    const scroll = (delta: number) => {
      const next = Math.max(0, Math.min(maxScrollTop(), scrollTop + delta));
      if (next === scrollTop) return;
      scrollTop = next;
      cachedLines = undefined;
      tui.requestRender();
    };

    return {
      capturesViewportInput: true,
      render(width: number): string[] {
        if (cachedLines && cachedWidth === width) return cachedLines;
        cachedWidth = width;
        clampScroll();
        let footer = "↑↓ scroll · pageUp/pageDown page · esc close";
        if (lines.length > VIEWER_ROWS) {
          footer +=
            " · lines " + (scrollTop + 1) + "-" + Math.min(scrollTop + VIEWER_ROWS, lines.length) +
            " / " + lines.length;
        }
        const out: string[] = [
          truncateToWidth(theme.fg("accent", theme.bold(title)), width),
          truncateToWidth(theme.fg("muted", "──────────"), width),
        ];
        for (const line of lines.slice(scrollTop, scrollTop + VIEWER_ROWS)) {
          out.push(truncateToWidth(line, width));
        }
        out.push(truncateToWidth(theme.fg("dim", footer), width));
        cachedLines = out;
        return out;
      },
      handleInput(data: string): void {
        if (keybindings.matches(data, "tui.select.cancel") || keybindings.matches(data, "tui.select.confirm")) {
          done(undefined);
          return;
        }
        if (keybindings.matches(data, "tui.select.up") || keybindings.matches(data, "tui.altScreen.lineUp")) {
          scroll(-1);
        } else if (keybindings.matches(data, "tui.select.down") || keybindings.matches(data, "tui.altScreen.lineDown")) {
          scroll(1);
        } else if (keybindings.matches(data, "tui.altScreen.pageUp") || keybindings.matches(data, "tui.editor.pageUp")) {
          scroll(-(VIEWER_ROWS - 2));
        } else if (keybindings.matches(data, "tui.altScreen.pageDown") || keybindings.matches(data, "tui.editor.pageDown")) {
          scroll(VIEWER_ROWS - 2);
        } else if (keybindings.matches(data, "tui.altScreen.halfPageUp")) {
          scroll(-Math.floor(VIEWER_ROWS / 2));
        } else if (keybindings.matches(data, "tui.altScreen.halfPageDown")) {
          scroll(Math.floor(VIEWER_ROWS / 2));
        }
      },
      invalidate() {
        cachedWidth = undefined;
        cachedLines = undefined;
      },
    };
  });
}

/** Per-job result view: registry facts + the final assistant text (getJobResultText). */
async function openJobViewer(ctx: ExtensionContext, id: string): Promise<void> {
  const entry = await getJob(ctx.cwd, id);
  if (!entry) {
    const known = (await listJobEntries(ctx.cwd)).map((e) => e.id).join(", ") || "none";
    ctx.ui.notify("No job with id " + id + ". Known jobs: " + known, "error");
    return;
  }
  const resultText = await getJobResultText(ctx.cwd, id);
  const lines: string[] = [];
  lines.push("agent: " + entry.agent);
  lines.push("status: " + entry.status + " · started: " + entry.startedAt + " · " + jobAge(entry));
  lines.push("runner: " + entry.runner + (entry.tmuxSession ? " (tmux session: " + entry.tmuxSession + ")" : ""));
  lines.push("task: " + taskPreview(entry.task, 200));
  lines.push("");
  if (entry.status === "awaiting_approval") {
    lines.push("awaiting your approval — job_approve id=" + id + " message=\"approved [note]\" or \"rejected: <reason>\" (or type it in the pane)");
    lines.push("");
  }
  if (resultText === null) {
    lines.push("(no .jsonl artifact found for this job)");
  } else if (resultText.trim()) {
    for (const line of resultText.split("\n")) lines.push(line);
  } else if (entry.status === "running") {
    lines.push("(no output yet)");
  } else {
    lines.push("(no assistant output)");
    if (entry.status === "failed" || entry.status === "aborted") {
      lines.push("Use job_status id=" + id + " for exit code, stderr and last events.");
    }
  }
  await openTextViewer(ctx, "Job " + entry.id + " [" + entry.status + "]", lines);
}

/** SelectList over the registry (newest first); selection opens the job viewer. */
async function openJobsView(ctx: ExtensionContext): Promise<void> {
  if (!ctx.hasUI) {
    const entries = await listJobEntries(ctx.cwd);
    ctx.ui.notify(entries.length === 0 ? "No background jobs." : formatJobList(entries), "info");
    return;
  }
  const entries = await listJobEntries(ctx.cwd);
  if (entries.length === 0) {
    ctx.ui.notify("No background jobs (.pi/jobs/ has no registry entries).", "info");
    return;
  }
  const items: SelectItem[] = entries.map((entry) => ({
    value: entry.id,
    label: jobStatusIcon(entry.status) + " " + entry.id,
    description: entry.agent + " · " + entry.status + " · " + jobAge(entry) + " · " + taskPreview(entry.task, 40),
  }));
  const selected = await ctx.ui.custom<string | null>((tui, theme, _keybindings, done) => {
    const container = new Container();
    container.addChild(new DynamicBorder((str) => theme.fg("accent", str)));
    container.addChild(new Text(theme.fg("accent", theme.bold("Background jobs"))));
    const selectList = new SelectList(items, Math.min(items.length, 10), {
      selectedPrefix: (text) => theme.fg("accent", text),
      selectedText: (text) => theme.fg("accent", text),
      description: (text) => theme.fg("muted", text),
      scrollInfo: (text) => theme.fg("dim", text),
      noMatch: (text) => theme.fg("warning", text),
    });
    selectList.onSelect = (item) => done(item.value);
    selectList.onCancel = () => done(null);
    container.addChild(selectList);
    container.addChild(new Text(theme.fg("dim", "↑↓ navigate · enter view job · esc cancel")));
    container.addChild(new DynamicBorder((str) => theme.fg("accent", str)));
    return {
      capturesViewportInput: selectList.capturesViewportInput,
      render(width: number) {
        return container.render(width);
      },
      invalidate() {
        container.invalidate();
      },
      handleInput(data: string) {
        selectList.handleInput(data);
        tui.requestRender();
      },
    };
  });
  if (selected) await openJobViewer(ctx, selected);
}

/** Compact fact line for the /agents SelectList description column. */
function agentSummaryLine(agent: AgentConfig): string {
  const parts: string[] = [agent.source];
  if (agent.model) parts.push("model=" + agent.model);
  if (agent.thinking) parts.push("thinking=" + agent.thinking);
  parts.push((agent.tools?.length ?? 0) + " tools");
  if (agent.background) parts.push("background");
  return parts.join(" · ") + " — " + agent.description;
}

/** Agent detail view: frontmatter facts, tool/skill lists, full system prompt. */
async function openAgentDetails(ctx: ExtensionContext, agent: AgentConfig): Promise<void> {
  const lines: string[] = [];
  lines.push("source: " + agent.source);
  lines.push("file: " + agent.filePath);
  lines.push("model: " + (agent.model ?? "(inherited from dispatching session)"));
  lines.push("thinking: " + (agent.thinking ?? "(inherited unless model set)"));
  lines.push("tools: " + (agent.tools && agent.tools.length > 0 ? agent.tools.join(", ") : "(all default tools)"));
  lines.push("skills: " + (agent.skills && agent.skills.length > 0 ? agent.skills.join(", ") : "(none)"));
  lines.push("background: " + (agent.background ? "yes (single mode detaches into a job)" : "no (foreground by default)"));
  lines.push("description: " + agent.description);
  lines.push("");
  lines.push("─── system prompt ───");
  if (agent.systemPrompt.trim()) {
    for (const line of agent.systemPrompt.split("\n")) lines.push(line);
  } else {
    lines.push("(empty)");
  }
  await openTextViewer(ctx, "Agent " + agent.name, lines);
}

/** SelectList over discoverAgents(cwd, "both"); selection opens agent details. */
async function openAgentsView(ctx: ExtensionContext): Promise<void> {
  if (!ctx.hasUI) {
    const discovered = discoverAgents(ctx.cwd, "both").agents;
    const summary = discovered.map((a) => a.name + " (" + a.source + ")").join(", ") || "none";
    ctx.ui.notify("Discovered agents: " + summary, "info");
    return;
  }
  const agents = discoverAgents(ctx.cwd, "both").agents;
  if (agents.length === 0) {
    ctx.ui.notify("No agents found in " + CONFIG_DIR_NAME + "/agents or the user agent dir.", "warning");
    return;
  }
  const items: SelectItem[] = agents.map((agent) => ({
    value: agent.name,
    label: agent.name,
    description: agentSummaryLine(agent),
  }));
  const selected = await ctx.ui.custom<string | null>((tui, theme, _keybindings, done) => {
    const container = new Container();
    container.addChild(new DynamicBorder((str) => theme.fg("accent", str)));
    container.addChild(new Text(theme.fg("accent", theme.bold("Agents"))));
    const selectList = new SelectList(items, Math.min(items.length, 10), {
      selectedPrefix: (text) => theme.fg("accent", text),
      selectedText: (text) => theme.fg("accent", text),
      description: (text) => theme.fg("muted", text),
      scrollInfo: (text) => theme.fg("dim", text),
      noMatch: (text) => theme.fg("warning", text),
    });
    selectList.onSelect = (item) => done(item.value);
    selectList.onCancel = () => done(null);
    container.addChild(selectList);
    container.addChild(new Text(theme.fg("dim", "↑↓ navigate · enter view details · esc cancel")));
    container.addChild(new DynamicBorder((str) => theme.fg("accent", str)));
    return {
      capturesViewportInput: selectList.capturesViewportInput,
      render(width: number) {
        return container.render(width);
      },
      invalidate() {
        container.invalidate();
      },
      handleInput(data: string) {
        selectList.handleInput(data);
        tui.requestRender();
      },
    };
  });
  if (selected) {
    const chosen = agents.find((a) => a.name === selected);
    if (chosen) await openAgentDetails(ctx, chosen);
  }
}

/**
 * Routing reminder returned whenever the edit/write gate stops a call. The
 * non-UI block prefixes it (see tool_call below); the confirm-decline path
 * appends the dialog hint.
 */
const DELEGATE_REASON =
  "Orchestrator does not implement. Delegate this to a subagent (go-worker for Go, general for anything else) via the subagent tool";

export default function (pi: ExtensionAPI) {
  delegateApi = pi;

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
  // layer discourages it). Children (PI_DELEGATE_CHILD set) skip the gate
  // entirely so delegated work runs unattended.
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

  // Tool execute results cannot set runtime error state directly, so patch it
  // through the result hook. AnyToolDetails: the background start-failure
  // result also sets isError (BackgroundJobDetails.isError).
  pi.on("tool_result", (event) => {
    if (event.toolName !== "subagent") return;
    const details = event.details as AnyToolDetails | undefined;
    if (details?.isError) return { isError: true };
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
    description: [
      "Delegate tasks to specialized subagents with isolated context.",
      // Deterministic gate-table routing policy (enforced again at the
      // background decision point below — this text is what the model reads up front).
      "Routing (deterministic, first match wins): trivial or answerable from context → no delegation or one foreground worker; ≤1 file scope and ≤2 steps → ONE foreground worker; ≥2 independent subtasks each passing that narrow gate → FOREGROUND parallel tasks[] in one call; background child orchestrator (decomposes into foreground workers itself) ONLY if a written plan exists (plan/phase file with ≥3 steps, or explicit multi-commit/multi-phase framing), ≥3 file scopes (or ≥2 clearly disjoint areas), the tree helps (≥2 sequential stages, at least one fanning out to ≥2 workers), and no mid-flight human decision is needed — ALL must hold. Any gate-3 fail or unknown → foreground parallel; when in doubt, foreground. Before spawning background, state the verdict: routing: plan=<file or none/N steps> files=<n> tree=<yes/no> human=<yes/no> → <background|foreground>.",
      "Worker agents cannot delegate and cannot run in background.",
      "Agent discovery always runs in the invoking session's cwd; a per-call cwd only sets the worker's process cwd (e.g. a git worktree) — the resolved roster is identical for every cwd.",
      "Modes: single (agent + task), parallel (tasks array), chain (sequential with {previous} placeholder).",
      "Single mode: background=true forces the detached tmux-backed job (results via job_status), background=false forces foreground, omitted = the agent config's background frontmatter decides; rejected with tasks/chain.",
      "Handoff (single background mode): handoff:\"auto\"|\"confirm\" is forwarded into the orchestrator task prompt as HANDOFF=..., overriding a HANDOFF mention in the task text; omitted keeps the task-text convention (no mention = confirm). Rejected with tasks/chain and foreground runs.",
      `Default agent scope is "both": project-local agents (${CONFIG_DIR_NAME}/agents) override user agents (${path.join(getAgentDir(), "agents")}) with the same name.`,
      `Set agentScope: "user" to ignore project-local agents.`,
    ].join(" "),
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
          );
          results.push(result);

          const isError = isFailedResult(result);
          if (isError) {
            const errorMsg = getResultOutput(result);
            return {
              content: [
                {
                  type: "text",
                  text: truncateModelOutput(`Chain stopped at step ${i + 1} (${step.agent}): ${errorMsg}`),
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
              text: truncateModelOutput(getFinalOutput(results[results.length - 1].messages) || "(no output)"),
            },
          ],
          details: makeDetails("chain")(results),
        };
      }

      if (params.tasks && params.tasks.length > 0) {
        if (params.tasks.length > MAX_PARALLEL_TASKS)
          return {
            content: [
              {
                type: "text",
                text: `Too many parallel tasks (${params.tasks.length}). Max is ${MAX_PARALLEL_TASKS}.`,
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
          const result = await runSingleAgent(
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
          );
          allResults[index] = result;
          emitParallelUpdate();
          return result;
        });

        const successCount = results.filter((r) => !isFailedResult(r)).length;
        const summaries = results.map((r) => {
          const output = truncateParallelOutput(getResultOutput(r));
          const status =
            isFailedResult(r) && r.stopReason && r.stopReason !== "end"
              ? `${r.status} (${r.stopReason})`
              : r.status;
          return `### [${r.agent}] ${status}\n\n${output}`;
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
        const result = await runSingleAgent(
          dispatchDefaults,
          resolvedSingle,
          params.agent,
          params.task,
          undefined,
          signal,
          onUpdate,
          makeDetails("single"),
        );
        const isError = isFailedResult(result);
        if (isError) {
          const errorMsg = getResultOutput(result);
          return {
            content: [
              {
                type: "text",
                text: truncateModelOutput(`Agent ${result.stopReason || result.status}: ${errorMsg}`),
              },
            ],
            details: makeDetails("single")([result]),
          };
        }
        return {
          content: [
            {
              type: "text",
              text: truncateModelOutput(getFinalOutput(result.messages) || "(no output)"),
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

    renderCall(args, theme, _context) {
      const scope: AgentScope = args.agentScope ?? "both";
      if (args.chain && args.chain.length > 0) {
        let text =
          theme.fg("toolTitle", theme.bold("subagent ")) +
          theme.fg("accent", `chain (${args.chain.length} steps)`) +
          theme.fg("muted", ` [${scope}]`);
        for (let i = 0; i < Math.min(args.chain.length, 3); i++) {
          const step = args.chain[i];
          // Clean up {previous} placeholder for display
          const cleanTask = step.task.replace(/\{previous\}/g, "").trim();
          const preview = cleanTask.length > 40 ? `${cleanTask.slice(0, 40)}...` : cleanTask;
          text +=
            "\n  " +
            theme.fg("muted", `${i + 1}.`) +
            " " +
            theme.fg("accent", step.agent) +
            theme.fg("dim", ` ${preview}`);
        }
        if (args.chain.length > 3) text += `\n  ${theme.fg("muted", `... +${args.chain.length - 3} more`)}`;
        return new Text(text, 0, 0);
      }
      if (args.tasks && args.tasks.length > 0) {
        let text =
          theme.fg("toolTitle", theme.bold("subagent ")) +
          theme.fg("accent", `parallel (${args.tasks.length} tasks)`) +
          theme.fg("muted", ` [${scope}]`);
        for (const t of args.tasks.slice(0, 3)) {
          const preview = t.task.length > 40 ? `${t.task.slice(0, 40)}...` : t.task;
          text += `\n  ${theme.fg("accent", t.agent)}${theme.fg("dim", ` ${preview}`)}`;
        }
        if (args.tasks.length > 3) text += `\n  ${theme.fg("muted", `... +${args.tasks.length - 3} more`)}`;
        return new Text(text, 0, 0);
      }
      const agentName = args.agent || "...";
      const preview = args.task ? (args.task.length > 60 ? `${args.task.slice(0, 60)}...` : args.task) : "...";
      let text =
        theme.fg("toolTitle", theme.bold("subagent ")) +
        theme.fg("accent", agentName) +
        theme.fg("muted", ` [${scope}]`);
      if (args.background === true) text += theme.fg("warning", " [background]");
      else if (args.background === false) text += theme.fg("muted", " [foreground]");
      text += `\n  ${theme.fg("dim", preview)}`;
      return new Text(text, 0, 0);
    },

    renderResult(result, { expanded }, theme, _context) {
      const details = result.details as AnyToolDetails | undefined;
      // Background branch first: BackgroundJobDetails has no `results` array,
      // so the generic guard below would throw (`details.results.length`) and
      // pi would fall back to plain-text rendering — this branch never ran.
      if (details?.mode === "background") {
        const content = result.content[0];
        const body = content?.type === "text" ? content.text : "(no output)";
        const job = details.job;
        if (!job || typeof job.id !== "string") return new Text(body, 0, 0);
        // awaiting_approval + running → warning (waiting on work / on the user)
        const color = job.status === "done" ? "success" : job.status === "failed" || job.status === "aborted" ? "error" : job.status === "awaiting_approval" ? "warning" : "warning";
        let text = theme.fg(color, jobStatusIcon(job.status)) + " " + theme.fg("accent", job.id);
        text += theme.fg("muted", " · " + job.agent + " · ") + theme.fg(color, job.status);
        text += theme.fg("dim", " · " + jobAge(job));
        if (result.isError) text += "\n" + theme.fg("error", body);
        else text += "\n" + theme.fg("toolOutput", body);
        return new Text(text, 0, 0);
      }
      if (!details || details.results?.length === 0) {
        const text = result.content[0];
        return new Text(text?.type === "text" ? text.text : "(no output)", 0, 0);
      }

      const mdTheme = getMarkdownTheme();

      const renderDisplayItems = (items: DisplayItem[], limit?: number) => {
        const toShow = limit ? items.slice(-limit) : items;
        const skipped = limit && items.length > limit ? items.length - limit : 0;
        let text = "";
        if (skipped > 0) text += theme.fg("muted", `... ${skipped} earlier items\n`);
        for (const item of toShow) {
          if (item.type === "text") {
            const preview = expanded ? item.text : item.text.split("\n").slice(0, 3).join("\n");
            text += `${theme.fg("toolOutput", preview)}\n`;
          } else {
            text += `${theme.fg("muted", "→ ") + formatToolCall(item.name, item.args, theme.fg.bind(theme))}\n`;
          }
        }
        return text.trimEnd();
      };

      const resultIcon = (child: SingleResult): string => {
        if (child.status === "pending" || child.status === "running") {
          return theme.fg("warning", "⏳");
        }
        if (isFailedResult(child)) return theme.fg("error", "✗");
        return theme.fg("success", "✓");
      };

      if (details.mode === "single" && details.results.length === 1) {
        const r = details.results[0];
        const isError = isFailedResult(r);
        const isRunning = r.status === "pending" || r.status === "running";
        const icon = resultIcon(r);
        const displayItems = getDisplayItems(r.messages);
        const finalOutput = getFinalOutput(r.messages);

        if (expanded) {
          const container = new Container();
          let header = `${icon} ${theme.fg("toolTitle", theme.bold(r.agent))}${theme.fg("muted", ` (${r.agentSource})`)}`;
          if (isRunning) header += ` ${theme.fg("warning", `[${r.status}]`)}`;
          else if (isError) header += ` ${theme.fg("error", `[${r.stopReason ?? r.status}]`)}`;
          container.addChild(new Text(header, 0, 0));
          if (isError && r.errorMessage)
            container.addChild(new Text(theme.fg("error", `Error: ${r.errorMessage}`), 0, 0));
          if (r.terminationSignal)
            container.addChild(new Text(theme.fg("error", `Signal: ${r.terminationSignal}`), 0, 0));
          if (r.stderr.trim()) container.addChild(new Text(theme.fg("error", `stderr: ${r.stderr.trim()}`), 0, 0));
          container.addChild(new Spacer(1));
          container.addChild(new Text(theme.fg("muted", "─── Task ───"), 0, 0));
          container.addChild(new Text(theme.fg("dim", r.task), 0, 0));
          container.addChild(new Spacer(1));
          container.addChild(new Text(theme.fg("muted", "─── Output ───"), 0, 0));
          if (displayItems.length === 0 && !finalOutput) {
            container.addChild(new Text(theme.fg("muted", "(no output)"), 0, 0));
          } else {
            for (const item of displayItems) {
              if (item.type === "toolCall")
                container.addChild(
                  new Text(
                    theme.fg("muted", "→ ") + formatToolCall(item.name, item.args, theme.fg.bind(theme)),
                    0,
                    0,
                  ),
                );
            }
            if (finalOutput) {
              container.addChild(new Spacer(1));
              container.addChild(new Markdown(finalOutput.trim(), 0, 0, mdTheme));
            }
          }
          const usageStr = formatUsageStats(r.usage, r.model);
          if (usageStr) {
            container.addChild(new Spacer(1));
            container.addChild(new Text(theme.fg("dim", usageStr), 0, 0));
          }
          return container;
        }

        let text = `${icon} ${theme.fg("toolTitle", theme.bold(r.agent))}${theme.fg("muted", ` (${r.agentSource})`)}`;
        if (isRunning) text += ` ${theme.fg("warning", `[${r.status}]`)}`;
        else if (isError) text += ` ${theme.fg("error", `[${r.stopReason ?? r.status}]`)}`;
        if (isError && r.errorMessage) text += `\n${theme.fg("error", `Error: ${r.errorMessage}`)}`;
        else if (displayItems.length === 0) text += `\n${theme.fg("muted", "(no output)")}`;
        else {
          text += `\n${renderDisplayItems(displayItems, COLLAPSED_ITEM_COUNT)}`;
          if (displayItems.length > COLLAPSED_ITEM_COUNT) text += `\n${theme.fg("muted", "(Ctrl+O to expand)")}`;
        }
        const usageStr = formatUsageStats(r.usage, r.model);
        if (usageStr) text += `\n${theme.fg("dim", usageStr)}`;
        return new Text(text, 0, 0);
      }

      const aggregateUsage = (results: SingleResult[]): UsageStats => {
        const total: UsageStats = {
          input: 0,
          output: 0,
          cacheRead: 0,
          cacheWrite: 0,
          cost: 0,
          contextTokens: 0,
          turns: 0,
        };
        for (const result of results) {
          total.input += result.usage.input;
          total.output += result.usage.output;
          total.cacheRead += result.usage.cacheRead;
          total.cacheWrite += result.usage.cacheWrite;
          total.cost += result.usage.cost;
          total.turns += result.usage.turns;
        }
        return total;
      };

      if (details.mode === "chain") {
        const successCount = details.results.filter((result) => result.status === "completed").length;
        const hasActive = details.results.some(
          (result) => result.status === "pending" || result.status === "running",
        );
        let icon = theme.fg("error", "✗");
        if (hasActive) icon = theme.fg("warning", "⏳");
        else if (successCount === details.results.length) icon = theme.fg("success", "✓");

        if (expanded) {
          const container = new Container();
          container.addChild(
            new Text(
              icon +
              " " +
              theme.fg("toolTitle", theme.bold("chain ")) +
              theme.fg("accent", `${successCount}/${details.results.length} steps`),
              0,
              0,
            ),
          );

          for (const r of details.results) {
            const rIcon = resultIcon(r);
            const displayItems = getDisplayItems(r.messages);
            const finalOutput = getFinalOutput(r.messages);

            container.addChild(new Spacer(1));
            container.addChild(
              new Text(
                `${theme.fg("muted", `─── Step ${r.step}: `) + theme.fg("accent", r.agent)} ${rIcon}`,
                0,
                0,
              ),
            );
            container.addChild(new Text(theme.fg("muted", "Task: ") + theme.fg("dim", r.task), 0, 0));
            if (isFailedResult(r) && r.errorMessage)
              container.addChild(new Text(theme.fg("error", `Error: ${r.errorMessage}`), 0, 0));
            if (r.terminationSignal)
              container.addChild(new Text(theme.fg("error", `Signal: ${r.terminationSignal}`), 0, 0));
            if (r.stderr.trim())
              container.addChild(new Text(theme.fg("error", `stderr: ${r.stderr.trim()}`), 0, 0));

            // Show tool calls
            for (const item of displayItems) {
              if (item.type === "toolCall") {
                container.addChild(
                  new Text(
                    theme.fg("muted", "→ ") + formatToolCall(item.name, item.args, theme.fg.bind(theme)),
                    0,
                    0,
                  ),
                );
              }
            }

            // Show final output as markdown
            if (finalOutput) {
              container.addChild(new Spacer(1));
              container.addChild(new Markdown(finalOutput.trim(), 0, 0, mdTheme));
            }

            const stepUsage = formatUsageStats(r.usage, r.model);
            if (stepUsage) container.addChild(new Text(theme.fg("dim", stepUsage), 0, 0));
          }

          const usageStr = formatUsageStats(aggregateUsage(details.results));
          if (usageStr) {
            container.addChild(new Spacer(1));
            container.addChild(new Text(theme.fg("dim", `Total: ${usageStr}`), 0, 0));
          }
          return container;
        }

        // Collapsed view
        let text =
          icon +
          " " +
          theme.fg("toolTitle", theme.bold("chain ")) +
          theme.fg("accent", `${successCount}/${details.results.length} steps`);
        for (const r of details.results) {
          const rIcon = resultIcon(r);
          const displayItems = getDisplayItems(r.messages);
          text += `\n\n${theme.fg("muted", `─── Step ${r.step}: `)}${theme.fg("accent", r.agent)} ${rIcon}`;
          if (displayItems.length === 0) {
            const failed = isFailedResult(r);
            const summary = failed ? getFailureSummary(r) : "(no output)";
            text += `\n${theme.fg(failed ? "error" : "muted", summary)}`;
          } else {
            text += `\n${renderDisplayItems(displayItems, 5)}`;
          }
        }
        const usageStr = formatUsageStats(aggregateUsage(details.results));
        if (usageStr) text += `\n\n${theme.fg("dim", `Total: ${usageStr}`)}`;
        text += `\n${theme.fg("muted", "(Ctrl+O to expand)")}`;
        return new Text(text, 0, 0);
      }

      if (details.mode === "parallel") {
        const running = details.results.filter((result) => result.status === "running").length;
        const pending = details.results.filter((result) => result.status === "pending").length;
        const successCount = details.results.filter((result) => result.status === "completed").length;
        const failCount = details.results.filter(isFailedResult).length;
        const isRunning = running > 0 || pending > 0;
        let icon = theme.fg("success", "✓");
        if (isRunning) icon = theme.fg("warning", "⏳");
        else if (failCount > 0) icon = theme.fg("warning", "◐");
        const status = isRunning
          ? `${successCount + failCount}/${details.results.length} done, ${running} running, ${pending} pending`
          : `${successCount}/${details.results.length} tasks`;

        if (expanded && !isRunning) {
          const container = new Container();
          container.addChild(
            new Text(
              `${icon} ${theme.fg("toolTitle", theme.bold("parallel "))}${theme.fg("accent", status)}`,
              0,
              0,
            ),
          );

          for (const r of details.results) {
            const rIcon = resultIcon(r);
            const displayItems = getDisplayItems(r.messages);
            const finalOutput = getFinalOutput(r.messages);

            container.addChild(new Spacer(1));
            container.addChild(
              new Text(`${theme.fg("muted", "─── ") + theme.fg("accent", r.agent)} ${rIcon}`, 0, 0),
            );
            container.addChild(new Text(theme.fg("muted", "Task: ") + theme.fg("dim", r.task), 0, 0));
            if (isFailedResult(r) && r.errorMessage)
              container.addChild(new Text(theme.fg("error", `Error: ${r.errorMessage}`), 0, 0));
            if (r.terminationSignal)
              container.addChild(new Text(theme.fg("error", `Signal: ${r.terminationSignal}`), 0, 0));
            if (r.stderr.trim())
              container.addChild(new Text(theme.fg("error", `stderr: ${r.stderr.trim()}`), 0, 0));

            // Show tool calls
            for (const item of displayItems) {
              if (item.type === "toolCall") {
                container.addChild(
                  new Text(
                    theme.fg("muted", "→ ") + formatToolCall(item.name, item.args, theme.fg.bind(theme)),
                    0,
                    0,
                  ),
                );
              }
            }

            // Show final output as markdown
            if (finalOutput) {
              container.addChild(new Spacer(1));
              container.addChild(new Markdown(finalOutput.trim(), 0, 0, mdTheme));
            }

            const taskUsage = formatUsageStats(r.usage, r.model);
            if (taskUsage) container.addChild(new Text(theme.fg("dim", taskUsage), 0, 0));
          }

          const usageStr = formatUsageStats(aggregateUsage(details.results));
          if (usageStr) {
            container.addChild(new Spacer(1));
            container.addChild(new Text(theme.fg("dim", `Total: ${usageStr}`), 0, 0));
          }
          return container;
        }

        // Collapsed view (or still running)
        let text = `${icon} ${theme.fg("toolTitle", theme.bold("parallel "))}${theme.fg("accent", status)}`;
        for (const r of details.results) {
          const rIcon = resultIcon(r);
          const displayItems = getDisplayItems(r.messages);
          text += `\n\n${theme.fg("muted", "─── ")}${theme.fg("accent", r.agent)} ${rIcon}`;
          if (displayItems.length === 0) {
            const failed = isFailedResult(r);
            let summary = "(no output)";
            if (r.status === "pending") summary = "(pending...)";
            else if (r.status === "running") summary = "(running...)";
            else if (failed) summary = getFailureSummary(r);
            text += `\n${theme.fg(failed ? "error" : "muted", summary)}`;
          } else {
            text += `\n${renderDisplayItems(displayItems, 5)}`;
          }
        }
        if (!isRunning) {
          const usageStr = formatUsageStats(aggregateUsage(details.results));
          if (usageStr) text += `\n\n${theme.fg("dim", `Total: ${usageStr}`)}`;
        }
        if (!expanded) text += `\n${theme.fg("muted", "(Ctrl+O to expand)")}`;
        return new Text(text, 0, 0);
      }

      const text = result.content[0];
      return new Text(text?.type === "text" ? text.text : "(no output)", 0, 0);
    },
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
      "With id: for done jobs returns the final assistant output; for failed jobs exit code + stderr + last events; for running jobs current activity; for awaiting_approval jobs (confirm handoff) returns the orchestrator's approval request — decide via job_approve.",
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
        if (entry.status !== "running" && !process.env.PI_DELEGATE_CHILD && !surfacedJobIds.has(entry.id)) {
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
            surfacedJobIds.add(entry.id);
          } catch (error) {
            console.error(
              "[delegate] job_status result pickup failed for " + entry.id + "; id left unmarked:",
              error,
            );
            /* pickup is best effort; the tool report below still returns */
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
    renderCall(args, theme, _context) {
      const target = args.id ? theme.fg("accent", args.id) : theme.fg("muted", "(all jobs)");
      return new Text(theme.fg("toolTitle", theme.bold("job_status ")) + target, 0, 0);
    },
    renderResult(result, _extended, theme, _context) {
      const text = result.content[0];
      return new Text(text?.type === "text" ? text.text : "(no output)", 0, 0);
    },
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
      "The message is typed verbatim into the job's idle tmux pane (tmux send-keys -l + Enter), exactly as if the user typed it there. " +
      'message "approved [note]" → the orchestrator performs its merge-back and reports HANDOFF_COMPLETE (the job then advances to done); "rejected: <reason>" → it iterates and emits a fresh approval request. ' +
      "Refuses: unknown id, non-tmux job, dead tmux session, mid-round job, or a job whose last message carries no approval request. " +
      "After delivery, follow up with job_status on the same id to watch the continuation.",
    parameters: JobApproveParams,
    async execute(_toolCallId, params, _signal, _onUpdate, ctx) {
      void refreshJobWidget(ctx);
      const outcome = await relayApprovalDecision(ctx.cwd, params.id, params.message);
      if (!outcome.ok) {
        return { content: [{ type: "text", text: "Refused: " + (outcome.reason ?? "unknown reason") }], details: { delivered: false, id: params.id } };
      }
      const text =
        "Delivered to tmux session " + outcome.session + " (job " + params.id + "): \"" + params.message + "\"." +
        "\nThe orchestrator pane received your decision; follow up with job_status id \"" + params.id + "\" to watch it advance (approval → HANDOFF_COMPLETE, job done; rejection → a fresh approval request).";
      return { content: [{ type: "text", text }], details: { delivered: true, id: params.id, session: outcome.session } };
    },
    renderCall(args, theme, _context) {
      const preview = args.message ? String(args.message).slice(0, 40) : "...";
      return new Text(theme.fg("toolTitle", theme.bold("job_approve ")) + theme.fg("accent", args.id) + theme.fg("muted", " · " + preview), 0, 0);
    },
    renderResult(result, _expanded, theme, _context) {
      const text = result.content[0];
      return new Text(text?.type === "text" ? text.text : "(no output)", 0, 0);
    },
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

  // ── Phase 4 renderers ──────────────────────────────────────────────────
  // "delegate-job" ENTRY cards (pi.appendEntry) are durable TUI-only content —
  // they NEVER reach the LLM. "delegate-job-result" MESSAGES (pi.sendMessage)
  // DO participate in LLM context; the renderer only restyles the transcript.
  pi.registerEntryRenderer<JobCardData>("delegate-job", (entry, _options, theme) => {
    const data = entry.data;
    if (!data || typeof data.id !== "string") return new Text(theme.fg("muted", "bg job (no card data)"), 0, 0);
    // awaiting_approval + running → warning (waiting on work / on the user)
    const color = data.status === "done" ? "success" : data.status === "failed" || data.status === "aborted" ? "error" : data.status === "awaiting_approval" ? "warning" : "warning";
    let line = theme.fg(color, jobStatusIcon(data.status)) + " " + theme.fg("accent", data.id);
    line += theme.fg("muted", " · " + data.agent + " · ") + theme.fg(color, data.status);
    line += theme.fg("dim", " · " + jobAge(data));
    return new Text(line, 0, 0);
  });

  pi.registerMessageRenderer<JobResultDetails>("delegate-job-result", (message, options, theme) => {
    const details = message.details;
    const box = new Box(options.outputPad, 1, (text) => theme.bg("customMessageBg", text));
    if (!details || typeof details.id !== "string" || typeof details.text !== "string") {
      const body = typeof message.content === "string" ? message.content : "";
      box.addChild(new Text(theme.fg("muted", body), 0, 0));
      return box;
    }
    // awaiting_approval + running → warning (waiting on work / on the user)
    const color = details.status === "done" ? "success" : details.status === "failed" || details.status === "aborted" ? "error" : details.status === "awaiting_approval" ? "warning" : "warning";
    const header =
      theme.fg(color, jobStatusIcon(details.status)) + " " + theme.fg("accent", details.id) +
      theme.fg("muted", " · " + details.agent + " · ") + theme.fg(color, details.status) +
      theme.fg("dim", " · " + jobAge(details));
    box.addChild(new Text(header, 0, 0));
    const bodyLines = details.text.split("\n");
    if (options.expanded || bodyLines.length <= JOB_RESULT_PREVIEW_LINES) {
      box.addChild(new Text(theme.fg("toolOutput", details.text), 0, 0));
    } else {
      box.addChild(new Text(theme.fg("toolOutput", bodyLines.slice(0, JOB_RESULT_PREVIEW_LINES).join("\n")), 0, 0));
      box.addChild(
        new Text(theme.fg("dim", "... " + (bodyLines.length - JOB_RESULT_PREVIEW_LINES) + " more lines (ctrl+o to expand)"), 0, 0),
      );
    }
    return box;
  });

  // Phase 4: auto-surface finished background jobs at the settle boundary.
  // agent_before_settle is the only actionable boundary — it can append entries
  // and request exactly one more provider request via continue:true. Loop
  // safety (the endless-loop trap the docs warn about): continue is returned
  // ONLY when at least one NEW job was surfaced; surfacedJobIds marks every
  // surfaced job (after its draft is built and committed), so the next settle
  // returns undefined and the run ends. Awaiting_approval jobs surface through
  // the same path (isSurfacableFinish passes them) with the approval-oriented
  // phrasing from buildJobResultInjection — exactly once, surfacedJobIds guards it.
  pi.on("agent_before_settle", async (event, ctx) => {
    if (process.env.PI_DELEGATE_CHILD) return;
    if (event.outcome !== "completed") return;
    const surfaced = await collectSurfacableJobs(ctx);
    // Headless single-shot runs have no later boundary to surface at, so jobs
    // this session started get a bounded chance to finish here. Interactive
    // sessions never wait (later settles + the poller cover them; esc aborts).
    if (surfaced.finished.length === 0 && surfaced.runningHere.length > 0 && !ctx.hasUI) {
      const deadline = Date.now() + settleWaitMs();
      while (Date.now() < deadline) {
        await sleepMs(SETTLE_WAIT_POLL_MS);
        const stillRunning: JobEntry[] = [];
        for (const job of surfaced.runningHere) {
          const current = await getJob(ctx.cwd, job.id);
          if (!current) continue;
          if (current.status === "running") stillRunning.push(current);
          else if (isSurfacableFinish(current)) surfaced.finished.push(current);
        }
        surfaced.runningHere = stillRunning;
        void refreshJobWidget(ctx);
        if (surfaced.finished.length > 0 || surfaced.runningHere.length === 0) break;
      }
    }
    if (surfaced.finished.length === 0) return undefined;
    // Build ALL drafts before marking anything. Per-job try/catch: a job whose
    // payload build fails is marked surfaced ANYWAY (a permanently-broken
    // payload must not retry at every settle) but contributes no draft. No
    // blanket catch around the handler: unexpected errors (registry reads, the
    // wait loop) propagate — pi's runner catches handler errors and reports
    // them — because a silent `return undefined` here would both drop drafts
    // already built and hide the cause.
    const drafts: SessionBoundaryDraft[] = [...event.entries];
    const delivered: string[] = [];
    for (const job of surfaced.finished) {
      let injection: { content: string; details: JobResultDetails };
      try {
        injection = await buildJobResultInjection(ctx.cwd, job);
      } catch (error) {
        console.error(
          "[delegate] settle surfacing: payload build failed for job " + job.id + "; marked surfaced to avoid a retry loop:",
          error,
        );
        surfacedJobIds.add(job.id);
        continue;
      }
      delivered.push(job.id);
      const card = cardDataOf(job);
      const cardKey = card.id + "|" + card.status;
      if (!emittedCardKeys.has(cardKey)) {
        emittedCardKeys.add(cardKey);
        drafts.push({ type: "custom", customType: "delegate-job", data: card });
      }
      // Extension-authored user-role model message: reaches the LLM, skips
      // human-input hooks. display:true renders via the message renderer.
      drafts.push({
        type: "custom_message",
        customType: "delegate-job-result",
        content: injection.content,
        display: true,
        details: injection.details,
      });
    }
    // Mark only now: committing these drafts IS the delivery — returning them
    // with continue:true is the same step in which the boundary machinery
    // appends them, so there is no window where an id is marked while its
    // draft can still be lost.
    for (const id of delivered) surfacedJobIds.add(id);
    void refreshJobWidget(ctx);
    // Loop guard: continue only when at least one NEW job produced a draft.
    // Build-failed jobs are marked but deliver nothing, so a settle whose
    // jobs all failed to build returns undefined and the run ends.
    if (delivered.length === 0) return undefined;
    return { entries: drafts, continue: true };
  });
}

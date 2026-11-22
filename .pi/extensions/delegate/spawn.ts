/**
 * SPAWN layer — one child run, start to verified result.
 *
 * What lives here (moved verbatim out of index.ts by B3.4, behavior unchanged):
 *
 *   1. AGENT RESOLUTION — `resolveAgent`: discovery runs in the INVOKING
 *      session's cwd (never the per-call run cwd), and the returned record
 *      carries both the run cwd and the host root (receipts/claims resolve
 *      against the host root, never a worktree).
 *   2. THE INVOCATION — `getPiInvocation` + the `pi --mode json -p` argv
 *      builder, the sandbox child env assembly (`sandboxChildEnv`: a strict
 *      allowlist on purpose — the host process env never crosses into the VM),
 *      and the two transports: `sbx exec` into a per-task worker micro-VM (the
 *      only one any role uses today) and the retained HOST-PASSTHROUGH local
 *      spawn (`runLocalSpawn`) for a future deliberate host role.
 *   3. HOST VERIFICATION — `verifySandboxedWorker`: after the sandbox exits,
 *      the HOST runs the integrity check FIRST and refuses the gates on a dirty
 *      result; only then the authoritative ladder (in the GATE VM) and its
 *      receipt, in the HOST session's receipts dir.
 *   4. RESULT TEXT — `getResultOutput` / `truncateModelOutput` /
 *      `formatHostVerification` / `withHostVerification`: the model-facing text
 *      of one spawn result (the HOST VERIFICATION block is part of it). The
 *      CHAT rendering of the same result lives in ./surfacing.ts; the mode
 *      branching that drives spawns lives in ./dispatch.ts.
 *
 * Import direction: ./agents.ts, ./claims.ts, ./gates.ts, ./output.ts,
 * ./persona.ts, ./runtime.ts, ./sbx.ts and ./surfacing.ts (the live-run footer
 * counters, which the spawn itself counts in/out). Nothing imports dispatch or
 * the root from here, so the graph stays acyclic: index.ts → dispatch.ts →
 * spawn.ts.
 *
 * Loader gotcha (pi 1.5.2 extension parser): no `?:` ternaries with
 * double-quoted strings inside a template literal's ${} — this module sticks to
 * string concatenation throughout.
 */

import { spawn } from "node:child_process";
import * as fs from "node:fs";
import * as path from "node:path";
import type { AgentToolResult, ThinkingLevel } from "@earendil-works/pi-agent-core";
import type { Message } from "@earendil-works/pi-ai";
import {
  DEFAULT_MAX_BYTES,
  DEFAULT_MAX_LINES,
  type ExtensionContext,
  truncateHead,
} from "@earendil-works/pi-coding-agent";
import { type AgentConfig, type AgentScope, discoverAgents } from "./agents.ts";
import { jobIdForEnv, siblingContextFor } from "./claims.ts";
import {
  type GateRun,
  type IntegrityCheckResult,
  checkWorktreeIntegrity,
  formatGateReport,
  integrityRefusalReason,
  receiptCommandsOf,
  runScopedGate,
  safeHostReceipt,
} from "./gates.ts";
import { createChildStreamParser, emptyUsage, getFinalOutput } from "./output.ts";
import { buildTaskPrompt, writePromptToTempFile } from "./persona.ts";
import {
  type SandboxExecResult,
  type SandboxHandle,
  type SandboxMount,
  SANDBOX_ENV_FLAG,
  createWorkerSandbox,
  execInSandbox,
  profileForRole,
  teardownSandbox,
} from "./sbx.ts";
import { formatStallReason, startStallWatchdog, workerStallTimeoutMs } from "./runtime.ts";
import { beginLiveRun, endLiveRun, isFailedResult } from "./surfacing.ts";

/** Output kept for the model (head truncation, same budget as pi's own tools). */
const TRUNCATION_NOTICE_RESERVE = 256;

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
export interface HostVerification {
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
 * import — erased at runtime); this module produces it.
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

/** The result container a dispatch mode hands back (details of the tool result). */
export interface SubagentDetails {
  mode: "single" | "parallel" | "chain";
  results: SingleResult[];
  isError: boolean;
}

export interface DispatchDefaults {
  model?: string;
  thinkingLevel?: ThinkingLevel;
}

export type OnUpdateCallback = (partial: AgentToolResult<SubagentDetails>) => void;

interface ProcessOutcome {
  code: number | null;
  signal: NodeJS.Signals | null;
  spawnError?: Error;
}

export interface ResolvedAgent {
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

export function getResultOutput(result: SingleResult): string {
  return isFailedResult(result) ? getFailureEvidence(result) : getFinalOutput(result.messages) || "(no output)";
}

export function truncateModelOutput(output: string): string {
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

export function resolveAgent(defaultCwd: string, scope: AgentScope, name: string, cwd?: string): ResolvedAgent {
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
export function withHostVerification(text: string, ...results: SingleResult[]): string {
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
export function formatHostVerification(result: SingleResult): string {
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

export async function runSingleAgent(
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

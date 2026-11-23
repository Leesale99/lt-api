/**
 * SURFACING layer — the chat surface: job cards, result pickup/injection,
 * ambient status text, and every transcript renderer.
 *
 * What lives here (moved verbatim out of index.ts, behavior unchanged):
 *
 *   1. JOB CARDS + RESULT INJECTION — `cardDataOf`/`emitJobCard` (the durable
 *      `delegate-job` entry cards, TUI-only, never model-facing) and
 *      `buildJobResultInjection` (the ONE payload builder shared by the
 *      job_status pickup, the round-start surfacer and the settle surfacer;
 *      `content` is what the LLM reads, `details` feeds the card renderer).
 *   2. SURFACING LIFECYCLE — the exactly-once bookkeeping the root cannot own:
 *      `surfacedJobIds`, the parallel-call `pickupsInFlight` guard and the
 *      per-(id,status) `emittedCardKeys` dedupe. The tool layer (./dispatch.ts)
 *      reads/mutates them
 *      through the named accessors below, so "delivered once per session" has
 *      one home.
 *   3. AMBIENT FOOTER STATUS — `sbx 2 · host 1`: how many subagents are LIVE
 *      right now and on which plane (counted by runSingleAgent, cleared at the
 *      settle boundary).
 *   4. TRANSCRIPT RENDERERS — the `subagent`/`job_status`/`job_approve` result
 *      cards, the `delegate-job` entry card and the `delegate-job-result`
 *      message card, plus the formatting/truncation helpers they show the user
 *      (`formatUsageStats`, `formatToolCall`, the collapsed-item windows).
 *      the ROOT registers these; nothing here touches pi's tool schema.
 *   5. HOOKS — `registerSurfacingHooks(pi)` registers the two surfacing
 *      boundaries (round-start injection, settle auto-surface) and the ambient
 *      settle clear, in the same order they were registered when they lived in
 *      the root.
 *
 * Not here: the model-facing result text of a spawn (`getResultOutput`,
 * `truncateModelOutput`, the host-verification block) — that is dispatch, and
 * it stays with the spawn path. The pure badge helpers live in ./tui/badges.ts;
 * job fact formatting lives in ./jobs.ts.
 *
 * Import direction: ./jobs.ts, ./output.ts, ./runtime.ts, ./tui/*, and
 * TYPE-ONLY from ./spawn.ts and ./dispatch.ts (the result contracts those layers
 * own). Type-only imports are erased at runtime, so the graph stays acyclic:
 * index.ts → dispatch.ts → { spawn.ts, surfacing.ts } → tui/*.
 *
 * Loader gotcha (pi 1.5.2 extension parser): no `?:` ternaries with
 * double-quoted strings inside a template literal's ${} — the moved code keeps
 * to string concatenation.
 */
import * as os from "node:os";
import type { AgentToolResult } from "@earendil-works/pi-agent-core";
import type {
  ExtensionAPI,
  ExtensionContext,
  SessionBoundaryDraft,
} from "@earendil-works/pi-coding-agent";
import { formatReadLineRange, getMarkdownTheme } from "@earendil-works/pi-coding-agent";
import type { Message } from "@earendil-works/pi-ai";
import { Box, Container, Markdown, Spacer, Text } from "@earendil-works/pi-tui";
import {
  type JobEntry,
  type JobStatus,
  type RunnerKind,
  getJob,
  getJobResultText,
  listJobEntries,
} from "./jobs.ts";
import { getFinalOutput } from "./output.ts";
import { sleepMs } from "./runtime.ts";
import type { AgentScope } from "./agents.ts";
import { isHostPassthroughRole } from "./sbx.ts";
import { BACKGROUND_JOB_PLANE, planeBadge, planeInfoLineText, resultPlaneBadge } from "./tui/badges.ts";
import { jobAge, jobStatusIcon, refreshJobWidget } from "./tui/widgets.ts";
import type { SingleResult, UsageStats } from "./spawn.ts";
import type { AnyToolDetails } from "./dispatch.ts";

/** The slice of pi's theme the renderers use (identity-typed for smoke fakes). */
type Theme = {
  fg(color: any, text: string): string;
  bg(color: any, text: string): string;
  bold(text: string): string;
};

/** Collapsed result cards show the last N display items before "(Ctrl+O to expand)". */
const COLLAPSED_ITEM_COUNT = 10;
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

// ── Phase 4: job cards, result pickup, settle surfacing ─────────────────────

/** Extension load time: jobs finished before this must never resurface (previous sessions). */
const SESSION_START_MS = Date.now();
/** Job ids whose result was already injected as a delegate-job-result context message. */
const surfacedJobIds = new Set<string>();
/** Job ids whose pickup injection is currently being built (phase 8 fix): the
 * check-and-mark of the pickup path spans an await (buildJobResultInjection),
 * and pi may execute two tool calls of one assistant message IN PARALLEL —
 * observed live: two same-turn job_status calls both passed the
 * surfacedJobIds check and injected the result twice. The guard is taken
 * synchronously before the first await, so parallel callers see it; it is
 * released on failure so a retry stays possible (mark-AFTER-delivery intact:
 * a throw leaves the id unmarked). */
const pickupsInFlight = new Set<string>();
/** Keys (id|status) of delegate-job cards already emitted — one card per transition. */
const emittedCardKeys = new Set<string>();
/** Set by the extension factory; card emission needs pi.appendEntry. */
let surfacingApi: ExtensionAPI | undefined;

/** Wire the factory's pi handle in (replaces the root's former `delegateApi = pi`). */
export function setSurfacingApi(pi: ExtensionAPI): void {
  surfacingApi = pi;
}

/** Has this job's result already been injected (pickup, round start or settle)? */
export function hasSurfacedJob(id: string): boolean {
  return surfacedJobIds.has(id);
}

/** Mark a job's result as delivered (call ONLY once the injection is committed). */
export function markJobSurfaced(id: string): void {
  surfacedJobIds.add(id);
}

/** Is a pickup injection for this id being built right now (parallel-call guard)? */
export function isJobPickupInFlight(id: string): boolean {
  return pickupsInFlight.has(id);
}

/** Take the pickup guard; MUST be called synchronously before the first await. */
export function beginJobPickup(id: string): void {
  pickupsInFlight.add(id);
}

/** Release the pickup guard (a throw leaves the id unmarked but retryable). */
export function endJobPickup(id: string): void {
  pickupsInFlight.delete(id);
}

/** Payload persisted on delegate-job entry cards (also used as message details). */
export interface JobCardData {
  id: string;
  agent: string;
  status: JobStatus;
  runner: RunnerKind;
  startedAt: string;
  finishedAt?: string;
  /**
   * Execution plane of the job, as persisted on the card: background children
   * are HOST-side ORCHESTRATORS (a tmux pane on the host) — they are never a
   * sandbox id; the workers they spawn are the sandboxed ones.
   */
  plane?: string;
}

export interface JobResultDetails extends JobCardData {
  /** Result text as injected — already truncated, marker appended when cut. */
  text: string;
}

export function cardDataOf(
  entry: Pick<JobEntry, "id" | "agent" | "status" | "runner" | "startedAt" | "finishedAt">,
): JobCardData {
  const data: JobCardData = {
    id: entry.id,
    agent: entry.agent,
    status: entry.status,
    runner: entry.runner,
    startedAt: entry.startedAt,
    plane: BACKGROUND_JOB_PLANE,
  };
  if (entry.finishedAt) data.finishedAt = entry.finishedAt;
  return data;
}

/**
 * Append a one-line delegate-job card. TUI-only (hasUI-gated; entries never
 * reach the LLM) and deduped per (id, status), so a job's start and its finish
 * each produce exactly one card.
 */
export function emitJobCard(
  ctx: Pick<ExtensionContext, "hasUI">,
  entry: Pick<JobEntry, "id" | "agent" | "status" | "runner" | "startedAt" | "finishedAt">,
): void {
  if (!ctx.hasUI || !surfacingApi) return;
  const key = entry.id + "|" + entry.status;
  if (emittedCardKeys.has(key)) return;
  emittedCardKeys.add(key);
  try {
    surfacingApi.appendEntry("delegate-job", cardDataOf(entry));
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
export async function buildJobResultInjection(
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
 *
 * B3.4: the implementation moved to ./runtime.ts (the timing plane) because the
 * worker-settle gate's continuation backoff (./gates.ts) needs the same
 * referenced sleep, and the execution plane must not import the chat surface.
 */
export { sleepMs };

// ── Ambient execution-plane status (footer) ────────────────────────────────
// `sbx 2 · host 1`: how many subagents are LIVE right now and on which plane.
// Counted by runSingleAgent itself (one process per session, so module scope is
// exact), cleared at the settle boundary — no run outlives its turn.

const RUN_STATUS_KEY = "delegate";

const liveRuns = { sandbox: 0, host: 0 };
/** ctx of the call that owns the live runs; captured at run start (hasUI-gated). */
let liveRunCtx: Pick<ExtensionContext, "hasUI" | "ui"> | null = null;

/** Footer text for the given live counts; undefined ⇒ nothing to show (clears). */
export function formatRunStatus(sandbox: number, host: number): string | undefined {
  const parts: string[] = [];
  if (sandbox > 0) parts.push("sbx " + sandbox);
  if (host > 0) parts.push("host " + host);
  return parts.length > 0 ? parts.join(" \u00b7 ") : undefined;
}

function refreshRunStatus(): void {
  const ctx = liveRunCtx;
  if (!ctx || !ctx.hasUI) return;
  try {
    ctx.ui.setStatus(RUN_STATUS_KEY, formatRunStatus(liveRuns.sandbox, liveRuns.host));
  } catch {
    /* footer status is best effort */
  }
}

/** Count a starting subagent on its plane (paired with endLiveRun). */
export function beginLiveRun(role: string, ctx: Pick<ExtensionContext, "hasUI" | "ui">): void {
  liveRunCtx = ctx;
  if (isHostPassthroughRole(role)) liveRuns.host += 1;
  else liveRuns.sandbox += 1;
  refreshRunStatus();
}

export function endLiveRun(role: string): void {
  if (isHostPassthroughRole(role)) liveRuns.host = Math.max(0, liveRuns.host - 1);
  else liveRuns.sandbox = Math.max(0, liveRuns.sandbox - 1);
  refreshRunStatus();
}

/** Settle boundary: nothing this session spawned is still live, so clear. */
export function clearLiveRuns(ctx?: Pick<ExtensionContext, "hasUI" | "ui">): void {
  if (ctx) liveRunCtx = ctx;
  liveRuns.sandbox = 0;
  liveRuns.host = 0;
  refreshRunStatus();
}

/**
 * A child run that did not complete successfully. Shared with the spawn/dispatch
 * paths (./spawn.ts, ./dispatch.ts: tool-result error state, claim release,
 * chain stop), which
 * is why it lives HERE and is imported back: the renderers are the lower module.
 */
export function isFailedResult(result: SingleResult): boolean {
  return result.status === "failed" || result.status === "aborted";
}

// ── Result-card formatting helpers (transcript only, never model text) ──────

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

function getFailureSummary(result: SingleResult): string {
  return (
    result.errorMessage?.trim() ||
    (result.terminationSignal ? `Terminated by signal ${result.terminationSignal}` : "") ||
    result.stderr.trim() ||
    getFinalOutput(result.messages) ||
    "(no output)"
  );
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

// ── Result-card renderers (registered by the root) ──────────────────────────

export function renderSubagentCall(args: any, theme: Theme, _context: any) {
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
      // PRE-RUN plane badge: policy truth for this step's role, no id.
      const badge = planeBadge(step.agent);
      text +=
        "\n  " +
        theme.fg("muted", `${i + 1}.`) +
        " " +
        theme.fg("accent", step.agent) +
        theme.fg(badge.color, ` ${badge.label}`) +
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
      const badge = planeBadge(t.agent);
      text +=
        `\n  ${theme.fg("accent", t.agent)}` +
        theme.fg(badge.color, ` ${badge.label}`) +
        theme.fg("dim", ` ${preview}`);
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
  // PRE-RUN plane: a background single is a host-side orchestrator, a
  // foreground single runs on its role's plane (sandbox unless passthrough).
  const singleBadge = args.agent
    ? planeBadge(agentName, args.background === true)
    : undefined;
  if (singleBadge) text += theme.fg(singleBadge.color, ` ${singleBadge.label}`);
  text += `\n  ${theme.fg("dim", preview)}`;
  return new Text(text, 0, 0);
}

export function renderSubagentResult(
  result: AgentToolResult<AnyToolDetails>,
  { expanded }: { expanded: boolean },
  theme: Theme,
  _context: any,
) {
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
    text += theme.fg("dim", " · " + jobAge(job) + " · " + BACKGROUND_JOB_PLANE);
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
      const runBadge = resultPlaneBadge(r);
      if (runBadge) header += ` ${theme.fg(runBadge.color, runBadge.label)}`;
      container.addChild(new Text(header, 0, 0));
      const planeLine = planeInfoLineText(r);
      if (planeLine) container.addChild(new Text(theme.fg("dim", planeLine), 0, 0));
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
    const runBadge = resultPlaneBadge(r);
    if (runBadge) text += ` ${theme.fg(runBadge.color, runBadge.label)}`;
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
        const stepBadge = resultPlaneBadge(r);
        container.addChild(
          new Text(
            `${theme.fg("muted", `─── Step ${r.step}: `) + theme.fg("accent", r.agent)} ${rIcon}` +
              (stepBadge ? ` ${theme.fg(stepBadge.color, stepBadge.label)}` : ""),
            0,
            0,
          ),
        );
        const stepPlane = planeInfoLineText(r);
        if (stepPlane) container.addChild(new Text(theme.fg("dim", stepPlane), 0, 0));
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
      const stepBadge = resultPlaneBadge(r);
      if (stepBadge) text += ` ${theme.fg(stepBadge.color, stepBadge.label)}`;
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
        const taskBadge = resultPlaneBadge(r);
        container.addChild(
          new Text(
            `${theme.fg("muted", "─── ") + theme.fg("accent", r.agent)} ${rIcon}` +
              (taskBadge ? ` ${theme.fg(taskBadge.color, taskBadge.label)}` : ""),
            0,
            0,
          ),
        );
        const taskPlane = planeInfoLineText(r);
        if (taskPlane) container.addChild(new Text(theme.fg("dim", taskPlane), 0, 0));
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
      const taskBadge = resultPlaneBadge(r);
      if (taskBadge) text += ` ${theme.fg(taskBadge.color, taskBadge.label)}`;
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
}

export function renderJobStatusCall(args: any, theme: Theme, _context: any) {
  const target = args.id ? theme.fg("accent", args.id) : theme.fg("muted", "(all jobs)");
  return new Text(theme.fg("toolTitle", theme.bold("job_status ")) + target, 0, 0);
}

export function renderJobStatusResult(
  result: AgentToolResult<unknown>,
  _extended: any,
  theme: Theme,
  _context: any,
) {
  const text = result.content[0];
  return new Text(text?.type === "text" ? text.text : "(no output)", 0, 0);
}

export function renderJobApproveCall(args: any, theme: Theme, _context: any) {
  const preview = args.message ? String(args.message).slice(0, 40) : "...";
  return new Text(theme.fg("toolTitle", theme.bold("job_approve ")) + theme.fg("accent", args.id) + theme.fg("muted", " · " + preview), 0, 0);
}

export function renderJobApproveResult(
  result: AgentToolResult<unknown>,
  _expanded: any,
  theme: Theme,
  _context: any,
) {
  const text = result.content[0];
  return new Text(text?.type === "text" ? text.text : "(no output)", 0, 0);
}

/** Durable entry card for one (id, status) transition. */
export function renderJobCardEntry(entry: { data?: JobCardData }, _options: any, theme: Theme) {
  const data = entry.data;
  if (!data || typeof data.id !== "string") return new Text(theme.fg("muted", "bg job (no card data)"), 0, 0);
  // awaiting_approval + running → warning (waiting on work / on the user)
  const color = data.status === "done" ? "success" : data.status === "failed" || data.status === "aborted" ? "error" : data.status === "awaiting_approval" ? "warning" : "warning";
  let line = theme.fg(color, jobStatusIcon(data.status)) + " " + theme.fg("accent", data.id);
  line += theme.fg("muted", " · " + data.agent + " · ") + theme.fg(color, data.status);
  line += theme.fg("dim", " · " + jobAge(data) + " · " + (data.plane ?? BACKGROUND_JOB_PLANE));
  return new Text(line, 0, 0);
}

/**
 * Transcript card for an injected delegate-job-result message: header line +
 * body, collapsed to JOB_RESULT_PREVIEW_LINES until ctrl+o.
 */
export function renderJobResultMessage(
  message: { content: unknown; details?: JobResultDetails },
  options: { expanded: boolean; outputPad: number },
  theme: Theme,
) {
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
    theme.fg("dim", " · " + jobAge(details) + " · " + (details.plane ?? BACKGROUND_JOB_PLANE));
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
}

// ── Hook registration (same handlers, same order as the root had) ───────────

/**
 * Register the surfacing boundaries on the extension's pi handle:
 *
 *   - `before_agent_start` round-start injection (a job that finished while the
 *     parent idled is delivered in the same request that would otherwise miss it);
 *   - the ambient settle clear (`sbx N · host M` goes quiet when the turn ends);
 *   - `agent_before_settle` auto-surfacing (the only actionable boundary:
 *     entries + at most one continue:true, loop-guarded by surfacedJobIds).
 *
 * Order matters only within one event; the registration order below is the
 * order these handlers had when they lived inline in the root.
 */
export function registerSurfacingHooks(pi: ExtensionAPI): void {
  pi.on("before_agent_start", async (event, ctx) => {
    if (process.env.PI_DELEGATE_CHILD) return;
    if (event.messageType !== "user") return;
    const surfaced = await collectSurfacableJobs(ctx);
    for (const job of surfaced.finished) {
      try {
        const injection = await buildJobResultInjection(ctx.cwd, job);
        pi.sendMessage({
          customType: "delegate-job-result",
          content: injection.content,
          display: true,
          details: injection.details,
        });
        emitJobCard(ctx, job);
        surfacedJobIds.add(job.id);
      } catch (error) {
        // Same policy as the settle path: a permanently-broken payload must
        // not retry at every round start — mark surfaced, deliver nothing.
        console.error(
          "[delegate] round-start surfacing: payload build failed for job " + job.id + "; marked surfaced to avoid a retry loop:",
          error,
        );
        surfacedJobIds.add(job.id);
      }
    }
    if (surfaced.finished.length > 0) void refreshJobWidget(ctx);
  });

  // Ambient footer: the settle boundary ends every subagent this session
  // started, so the live-plane counter and its status line go quiet there.
  pi.on("agent_before_settle", (_event, ctx) => {
    if (process.env.PI_DELEGATE_CHILD) return;
    clearLiveRuns(ctx);
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

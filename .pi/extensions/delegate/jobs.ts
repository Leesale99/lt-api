/**
 * Background job runner (phase 3) — tmux-backed, with a detached-spawn fallback.
 *
 * Artifacts per job live in `.pi/jobs/` (gitignored):
 *   <id>.task    — full child prompt (file-based so arbitrary quoting is safe;
 *                  NEVER inline the task text into a shell string)
 *   <id>.jsonl   — headless-fallback child's `pi --mode json` stdout stream
 *                  (tmux jobs stream into the child's pi session file
 *                  `<ISO>_<session-id>.jsonl` in this dir instead)
 *   <id>.err     — child stderr
 *   <id>.exit    — exit code written by the runner script when the child ends
 *                  (tmux interactive jobs get one only when pi is quit)
 *   <id>.json    — registry entry (atomic write: temp + rename)
 *   <id>.sys.md  — agent system prompt for --append-system-prompt (kept as artifact)
 *   <id>.sh      — per-job runner script
 *
 * Job ids: `<agent>-<yyyymmdd-hhmmss>-<6 random base36 chars>`, agent part
 * sanitized. The random suffix adds sub-second entropy: the second-granularity
 * timestamp alone collided when two jobs launched in the same second (shared
 * id → shared `.task` tmp path → ENOENT on rename + the surviving job executed
 * the other's task). Ids are also tmux-session-safe (no dots/colons):
 * `pi-<id>` is passed as tmux session name AND --session-id; tmux creates
 * dotted names but then cannot address them (has-session/attach fail).
 * Background children run `--session-id pi-<id>` (resumable later), NOT
 * `--no-session`; foreground stays `--no-session`. All children also pass
 * `--session-dir <jobsDir>`, pinning their pi session file here so status and
 * result readers find it deterministically.
 *
 * tmux jobs run pi INTERACTIVELY in the pane (no --mode json/-p): attach to
 * `pi-<id>` to watch the TUI live or take the conversation over. Since stdout
 * is the screen, completion/results are read from the child's session file —
 * same event vocabulary as `--mode json`, `message` envelope instead of
 * `message_end`. The agent round is complete when the session's last message
 * is an assistant message with no pending tool calls; the tmux session then
 * stays alive (pi idles waiting for input) until the user quits it.
 *
 * Phase 7 — confirm-handoff wait as a first-class status: a phase-6
 * orchestrator ends an approval-request round with an `AWAITING_APPROVAL`
 * block and idles in its pane; the registry surfaces that wait as the
 * `awaiting_approval` status. It advances forward only — to done/failed via
 * the session file after the approval round, or via `<id>.exit` when the pane
 * eventually quits. New exports: APPROVAL_MARKER (the exact approval-round
 * token), sessionRoundState (round-complete + approval detection from the
 * session file), tmuxSessionAlive (now public) and relayApprovalDecision
 * (approval relay for the job_approve tool).
 *
 * If tmux is unavailable (or the new-session call fails), the runner script is
 * rewritten as the headless variant (`pi --mode json -p`, stdout redirected
 * into <id>.jsonl) and the job falls back to a detached child (`sh <runner>`,
 * setsid-style via Node's detached option), recording `runner: "fallback"` +
 * pid in the registry (no attach support).
 */

import { randomInt } from "node:crypto";
import { execFile, spawn } from "node:child_process";
import * as fs from "node:fs";
import * as path from "node:path";
import { promisify } from "node:util";
import { CONFIG_DIR_NAME } from "@earendil-works/pi-coding-agent";
import type { AgentConfig } from "./agents.ts";
import { getFinalOutput, parseChildJsonl, truncateParallelOutput } from "./output.ts";

const execFileP = promisify(execFile);

export type JobStatus = "running" | "awaiting_approval" | "done" | "failed" | "aborted";

/**
 * Exact marker a confirm-handoff (phase 6) orchestrator ends its approval-request
 * round with. Shared string between the orchestrator prompts and the registry
 * detection below — change both or neither.
 */
export const APPROVAL_MARKER = "AWAITING_APPROVAL";
/** Terminal handoff tokens; the orchestrator protocol puts them on the final line. */
const HANDOFF_TERMINAL_TOKENS = ["HANDOFF_COMPLETE", "HANDOFF_BLOCKED"] as const;

export type RunnerKind = "tmux" | "fallback";

export interface JobEntry {
  id: string;
  agent: string;
  task: string;
  startedAt: string;
  runner: RunnerKind;
  tmuxSession?: string;
  pid?: number;
  status: JobStatus;
  /** Set on every transition out of "running" or out of "awaiting_approval"; older entries lack it. */
  finishedAt?: string;
}

export function jobsDirFor(cwd: string): string {
  return path.join(cwd, CONFIG_DIR_NAME, "jobs");
}

/** POSIX single-quote escaping for embedding a path/text into a shell command. */
function shq(text: string): string {
  return "'" + text.replace(/'/g, "'\\''") + "'";
}

// tmux session-name safe: no dots/colons (tmux creates such session names but
// then cannot address them — has-session/attach/kill all fail to match).
function sanitizeIdPart(name: string): string {
  const cleaned = name.replace(/[^\w-]+/g, "_").slice(0, 40).replace(/^_+|_+$/g, "");
  return cleaned || "agent";
}

function timestamp(now: Date): string {
  const p = (n: number, width = 2) => String(n).padStart(width, "0");
  return (
    p(now.getFullYear(), 4) + p(now.getMonth() + 1) + p(now.getDate()) +
    "-" + p(now.getHours()) + p(now.getMinutes()) + p(now.getSeconds())
  );
}

async function writeAtomic(file: string, content: string): Promise<void> {
  const tmp = file + ".tmp";
  await fs.promises.writeFile(tmp, content, "utf8");
  await fs.promises.rename(tmp, file);
}

async function readEntry(dir: string, id: string): Promise<JobEntry | null> {
  try {
    const raw = await fs.promises.readFile(path.join(dir, id + ".json"), "utf8");
    return JSON.parse(raw) as JobEntry;
  } catch {
    return null;
  }
}

async function persistEntry(dir: string, entry: JobEntry): Promise<void> {
  await writeAtomic(path.join(dir, entry.id + ".json"), JSON.stringify(entry, null, 2) + "\n");
}

/** Length of the random base36 id suffix — sub-second entropy (36^6 ≈ 2·10⁹). */
const ID_RANDOM_LEN = 6;

const BASE36 = "0123456789abcdefghijklmnopqrstuvwxyz";

function randomBase36(len: number): string {
  let out = "";
  for (let i = 0; i < len; i++) out += BASE36[randomInt(36)];
  return out;
}

/**
 * Best-effort id generation. The registry (.json) pre-check is only a cheap
 * filter — it is written last and cannot arbitrate a same-second race; the
 * airtight guard is the exclusive `<id>.task` create in reserveJobId.
 * `drawSuffix` is injectable so collision paths can be smoke-tested.
 */
async function uniqueJobId(
  dir: string,
  agentName: string,
  drawSuffix: (len: number) => string = randomBase36,
): Promise<string> {
  const base = sanitizeIdPart(agentName) + "-" + timestamp(new Date());
  let id = base + "-" + drawSuffix(ID_RANDOM_LEN);
  while (fs.existsSync(path.join(dir, id + ".json"))) {
    id = base + "-" + drawSuffix(ID_RANDOM_LEN);
  }
  return id;
}

/** Node fs error code, or null for non-fs errors. */
function fsErrorCode(error: unknown): string | null {
  if (typeof error !== "object" || error === null) return null;
  const code = (error as { code?: unknown }).code;
  return typeof code === "string" ? code : null;
}

/**
 * Create `<id>.task` exclusively (O_EXCL) and write the prompt into it.
 * Returns true when this job owns the id; false when the file already exists —
 * i.e. another job grabbed the id. A tmp+rename cannot detect that case:
 * POSIX rename silently replaces an existing target (and a shared fixed-name
 * tmp vanishes under the second renamer with ENOENT), which is how two
 * same-second jobs used to corrupt each other's task files.
 */
async function reserveTaskArtifact(taskPath: string, prompt: string): Promise<boolean> {
  try {
    const handle = await fs.promises.open(taskPath, "wx", 0o644);
    try {
      await handle.writeFile(prompt, "utf8");
    } finally {
      await handle.close();
    }
    return true;
  } catch (error) {
    if (fsErrorCode(error) === "EEXIST") return false;
    throw error; // real setup error (permissions, missing directory, …)
  }
}

/**
 * Allocate a fresh job id and take exclusive ownership of it by creating its
 * `<id>.task` artifact (the reservation token). On a collision — another job
 * already owns the id — regenerate the id and retry once; a second collision
 * fails cleanly rather than launching a job that would execute another job's
 * task content. `drawSuffix` is injectable so collision paths can be
 * smoke-tested deterministically.
 */
export async function reserveJobId(
  dir: string,
  agentName: string,
  prompt: string,
  drawSuffix: (len: number) => string = randomBase36,
): Promise<string> {
  let id = await uniqueJobId(dir, agentName, drawSuffix);
  for (let attempt = 0; ; attempt++) {
    if (await reserveTaskArtifact(path.join(dir, id + ".task"), prompt)) return id;
    if (attempt > 0) {
      throw new Error(
        "background job id collision: '" + id + ".task' already exists again after regenerating the id; " +
        "refusing to launch a job that would execute another job's task content",
      );
    }
    id = await uniqueJobId(dir, agentName, drawSuffix);
  }
}

export async function tmuxSessionAlive(session: string): Promise<boolean> {
  try {
    await execFileP("tmux", ["has-session", "-t", session]);
    return true;
  } catch {
    return false;
  }
}

/**
 * The child's pi session file inside dir — pi names it
 * `<ISO-timestamp>_<session-id>.jsonl`, so match on the id suffix. Children
 * pass `--session-dir` pointed at the jobs dir to make this deterministic.
 */
function findSessionFile(dir: string, id: string): string | null {
  try {
    // Background children run `--session-id pi-<id>` (both runner variants), so
    // their session files are `<ts>_pi-<id>.jsonl` — the `pi-` prefix is part
    // of the session id and must be in the search suffix. (Latent bug found by
    // the first real tmux background job in phase 5: all earlier e2e completions
    // were fallback jobs, whose `<id>.jsonl` redirect bypasses this finder.)
    const hit = fs.readdirSync(dir).find((name) => name.endsWith("_pi-" + id + ".jsonl"));
    return hit ? path.join(dir, hit) : null;
  } catch {
    return null;
  }
}

export interface SessionRoundState {
  /** Round over: the last message is an assistant message with no pending tool calls. */
  complete: boolean;
  /** complete AND the last assistant text carries APPROVAL_MARKER (confirm-handoff approval request). */
  awaitingApproval: boolean;
}

/**
 * Phase-5-era round detection, extended (phase 7) to distinguish a plain done
 * round from one that ended with an approval request. Detection runs on the
 * LAST assistant message only — never scan the whole session file: the task
 * text (first user message) legitimately contains the marker too.
 *
 * Terminal handoff tokens win over the marker: per the orchestrator protocol
 * they sit on the final line, and a HANDOFF_COMPLETE/BLOCKED report may mention
 * the approval gate in passing without the job awaiting anything.
 */
export function sessionRoundState(dir: string, id: string): SessionRoundState {
  const sessionFile = findSessionFile(dir, id);
  if (!sessionFile) return { complete: false, awaitingApproval: false };
  let text: string;
  try {
    text = fs.readFileSync(sessionFile, "utf8");
  } catch {
    return { complete: false, awaitingApproval: false };
  }
  const messages = parseChildJsonl(text).messages;
  const last = messages[messages.length - 1];
  if (!last || last.role !== "assistant") return { complete: false, awaitingApproval: false };
  if (Array.isArray(last.content) && last.content.some((part) => part.type === "toolCall")) {
    return { complete: false, awaitingApproval: false };
  }
  const assistantText = Array.isArray(last.content)
    ? last.content
        .filter((part) => part.type === "text")
        .map((part) => (part as { text: string }).text)
        .join("\n")
    : "";
  if (!assistantText) return { complete: true, awaitingApproval: false };
  const nonEmpty = assistantText.split("\n").filter((line) => line.trim().length > 0);
  const lastLine = nonEmpty[nonEmpty.length - 1] ?? "";
  if (HANDOFF_TERMINAL_TOKENS.some((token) => lastLine.includes(token))) {
    return { complete: true, awaitingApproval: false };
  }
  return { complete: true, awaitingApproval: assistantText.includes(APPROVAL_MARKER) };
}

/** Read `<id>.exit` if present; null when missing/unparsable. */
async function readExitCode(dir: string, id: string): Promise<number | null> {
  try {
    const raw = (await fs.promises.readFile(path.join(dir, id + ".exit"), "utf8")).trim();
    const code = parseInt(raw, 10);
    return Number.isNaN(code) ? null : code;
  } catch {
    return null;
  }
}

export interface StartJobOptions {
  cwd: string;
  agent: AgentConfig;
  /** Original task text (for the registry preview). */
  task: string;
  /** Full child prompt (buildTaskPrompt output) written to `<id>.task`. */
  prompt: string;
  model?: string;
  thinking?: string;
}

/**
 * Launch a detached background job and register it. Returns the registry entry
 * (status "running"). tmux-backed jobs run pi's TUI interactively in the pane
 * (attach to pi-<id> to watch or take over); the headless fallback runs json
 * mode with redirected output. Throws on local setup errors (mkdir/write) and
 * on a persistent id collision (two ids in a row both had their `.task`
 * artifact already taken) — it never launches with another job's task content.
 */
export async function startBackgroundJob(opts: StartJobOptions): Promise<JobEntry> {
  const dir = jobsDirFor(opts.cwd);
  await fs.promises.mkdir(dir, { recursive: true });
  // Claim the id by exclusively creating `<id>.task` — the registry (.json) is
  // written last and cannot arbitrate a race; see reserveJobId.
  const id = await reserveJobId(dir, opts.agent.name, opts.prompt);
  const base = path.join(dir, id);
  const taskPath = base + ".task";
  const sysPath = base + ".sys.md";
  const jsonlPath = base + ".jsonl";
  const errPath = base + ".err";
  const exitPath = base + ".exit";
  const runnerPath = base + ".sh";
  const tmuxSession = "pi-" + id;

  const hasSystemPrompt = Boolean(opts.agent.systemPrompt && opts.agent.systemPrompt.trim());
  if (hasSystemPrompt) await writeAtomic(sysPath, opts.agent.systemPrompt);

  // Resolve the pi binary once so the detached runner does not depend on the
  // (possibly stale) PATH of an already-running tmux server.
  let piBin = "pi";
  try {
    const which = await execFileP("sh", ["-c", "command -v pi"]);
    const resolved = which.stdout.trim();
    if (resolved) piBin = resolved;
  } catch {
    /* fall back to the bare name; the runner shell resolves it */
  }

  // Build the runner script. The task text goes through the task file —
  // "$(cat task)" is the only way it enters a shell string, by design.
  // Two variants: interactive (tmux pane shows pi's TUI; stdout IS the screen)
  // and headless (json mode with redirected stdout for the no-tmux fallback).
  const buildScript = (interactive: boolean): string => {
    const lines: string[] = [];
    lines.push("#!/bin/sh");
    lines.push("# pi delegate background job " + id + (interactive ? " (interactive TUI in tmux pane)" : " (headless fallback)"));
    // Scrub pi's session env (the same five keys the foreground spawn deletes in
    // index.ts) so background children start clean: the runner inherits the full
    // parent env via tmux/detached spawn, including pi's bash-tool exports.
    lines.push("unset PI_SESSION_ID PI_SESSION_FILE PI_PROVIDER PI_MODEL PI_REASONING_LEVEL");
    lines.push("cd " + shq(opts.cwd) + " || exit 1");
    // --session-dir pins the child's pi session file into the jobs dir so
    // refreshStatus/readJsonl can find it deterministically (both variants).
    // Phase 5: PI_DELEGATE_ROLE=orchestrator — background is the child
    // orchestrator's privilege, so every background child runs in the
    // orchestrator role (both the tmux and the headless-fallback variant;
    // the assignment prefix also overrides any inherited role value).
    let cmd = "PI_DELEGATE_CHILD=1 PI_DELEGATE_ROLE=orchestrator " + piBin + " --session-dir " + shq(dir) + " --session-id " + shq(tmuxSession);
    if (!interactive) cmd += " --mode json -p";
    if (opts.model) cmd += " --model " + shq(opts.model);
    if (opts.thinking) cmd += " --thinking " + shq(opts.thinking);
    if (opts.agent.tools && opts.agent.tools.length > 0) {
      cmd += " --tools " + shq(opts.agent.tools.join(","));
    }
    if (hasSystemPrompt) cmd += " --append-system-prompt " + shq(sysPath);
    // Double-quoted "$(cat ...)": no word splitting / globbing — the prompt reaches
    // the child as ONE argv element, byte-identical to what was written to disk.
    cmd += " \"$(cat " + shq(taskPath) + ")\"";
    if (interactive) {
      // stdout stays on the tmux pane (the TUI draws there); only stderr —
      // warnings — goes to the artifact.
      cmd += " 2> " + shq(errPath);
    } else {
      cmd += " > " + shq(jsonlPath) + " 2> " + shq(errPath);
    }
    lines.push(cmd);
    lines.push("code=$?");
    lines.push('echo "$code" > ' + shq(exitPath));
    return lines.join("\n") + "\n";
  };

  await fs.promises.writeFile(runnerPath, buildScript(true), "utf8");
  await fs.promises.chmod(runnerPath, 0o755);

  let entry: JobEntry;
  try {
    await execFileP("tmux", ["new-session", "-d", "-s", tmuxSession, "sh " + shq(runnerPath)]);
    entry = { id, agent: opts.agent.name, task: opts.task, startedAt: new Date().toISOString(), runner: "tmux", tmuxSession, status: "running" };
  } catch {
    // Headless fallback: no TTY to draw the TUI on. Rewrite the runner as the
    // json-mode variant (stdout streams events into <id>.jsonl) and detach.
    // Safe because a failed new-session means the interactive script never ran.
    await fs.promises.writeFile(runnerPath, buildScript(false), "utf8");
    const child = spawn("sh", [runnerPath], { cwd: opts.cwd, detached: true, stdio: "ignore" });
    child.unref();
    entry = { id, agent: opts.agent.name, task: opts.task, startedAt: new Date().toISOString(), runner: "fallback", pid: child.pid, status: "running" };
  }
  await persistEntry(dir, entry);
  return entry;
}

/**
 * Re-read the entry and advance its status from on-disk evidence:
 * exit file → done/failed; child session file round-complete → done, or
 * awaiting_approval when the last assistant text carries the approval marker
 * (phase 7). Awaiting entries advance forward only (never back to running /
 * done flips); pre-phase-7 registry entries never carry the new status and
 * keep reading correctly. Persisted (atomically) whenever the status changes.
 */
async function refreshStatus(dir: string, entry: JobEntry): Promise<JobEntry> {
  if (entry.status === "aborted") {
    // A false "aborted" (transient runner probe failure while the child was
    // still exiting) must not be sticky: if an .exit file has appeared since,
    // reclassify by its code. Genuine aborts (runner killed, machine restart)
    // never produce an .exit file, so they stay aborted.
    const lateExitCode = await readExitCode(dir, entry.id);
    if (lateExitCode === null) return entry;
    entry.status = lateExitCode === 0 ? "done" : "failed";
    entry.finishedAt ??= new Date().toISOString();
    await persistEntry(dir, entry);
    return entry;
  }
  if (entry.status === "awaiting_approval") {
    // Forward-only advance (phase 7). After the relayed or typed approval the
    // orchestrator runs its merge-back round: once the session's last message
    // no longer carries the marker, the job is done. An .exit file still wins
    // when the child eventually quits — a killed pane yields a nonzero code →
    // failed; a clean quit after an approved handoff lands as done.
    const exitCode = await readExitCode(dir, entry.id);
    if (exitCode !== null) {
      entry.status = exitCode === 0 ? "done" : "failed";
      entry.finishedAt = new Date().toISOString();
      await persistEntry(dir, entry);
      return entry;
    }
    const state = sessionRoundState(dir, entry.id);
    if (state.complete && !state.awaitingApproval) {
      entry.status = "done";
      entry.finishedAt = new Date().toISOString();
      await persistEntry(dir, entry);
    }
    return entry;
  }
  if (entry.status !== "running") return entry;

  const exitCode = await readExitCode(dir, entry.id);
  if (exitCode !== null) {
    entry.status = exitCode === 0 ? "done" : "failed";
    entry.finishedAt = new Date().toISOString();
    await persistEntry(dir, entry);
    return entry;
  }

  // Interactive (tmux) jobs write no .exit until pi is quit; completion is
  // detected from the child's session file instead (see sessionRoundState).
  // For headless jobs this mostly short-circuits the alive probe by a few ms —
  // harmless either way.
  const round = sessionRoundState(dir, entry.id);
  if (round.complete) {
    // Phase 7: a confirm-handoff orchestrator ends its round with an
    // AWAITING_APPROVAL block — a first-class waiting status, not plain done.
    entry.status = round.awaitingApproval ? "awaiting_approval" : "done";
    entry.finishedAt = new Date().toISOString();
    await persistEntry(dir, entry);
    return entry;
  }

  let alive = false;
  if (entry.runner === "tmux" && entry.tmuxSession) {
    alive = await tmuxSessionAlive(entry.tmuxSession);
  } else if (entry.pid) {
    try {
      process.kill(entry.pid, 0);
      alive = true;
    } catch {
      alive = false;
    }
  }
  if (!alive) {
    entry.status = "aborted";
    entry.finishedAt = new Date().toISOString();
    await persistEntry(dir, entry);
  }
  return entry;
}

export async function getJob(cwd: string, id: string): Promise<JobEntry | null> {
  const dir = jobsDirFor(cwd);
  const entry = await readEntry(dir, id);
  if (!entry) return null;
  return await refreshStatus(dir, entry);
}

/**
 * Registry entry WITHOUT a status refresh — lets callers detect the
 * running→terminal transition that a subsequent getJob()/listJobs() advances
 * (e.g. job_status emitting a finish card only when it advanced the status).
 */
export async function peekJobEntry(cwd: string, id: string): Promise<JobEntry | null> {
  return readEntry(jobsDirFor(cwd), id);
}

/**
 * Job ids, newest first, read synchronously — for command-argument completion
 * (getArgumentCompletions has no ctx and must not await tmux probes).
 */
export function listJobIdsSync(cwd: string): string[] {
  const dir = jobsDirFor(cwd);
  let names: string[];
  try {
    names = fs.readdirSync(dir);
  } catch {
    return [];
  }
  const entries: JobEntry[] = [];
  for (const name of names) {
    if (!name.endsWith(".json")) continue;
    try {
      entries.push(JSON.parse(fs.readFileSync(path.join(dir, name), "utf8")) as JobEntry);
    } catch {
      /* unreadable entry — skip */
    }
  }
  entries.sort((a, b) => (a.startedAt < b.startedAt ? 1 : a.startedAt > b.startedAt ? -1 : a.id > b.id ? -1 : 1));
  return entries.map((entry) => entry.id);
}

/**
 * Shared helper for the UI surfaces (jobs widget, /jobs): every registry entry,
 * newest first. listJobs itself stays oldest-first for job_status's list.
 */
export async function listJobEntries(cwd: string): Promise<JobEntry[]> {
  const entries = await listJobs(cwd);
  entries.reverse();
  return entries;
}

export async function listJobs(cwd: string): Promise<JobEntry[]> {
  const dir = jobsDirFor(cwd);
  let names: string[];
  try {
    names = await fs.promises.readdir(dir);
  } catch {
    return [];
  }
  const entries: JobEntry[] = [];
  for (const name of names) {
    if (!name.endsWith(".json")) continue;
    const id = name.slice(0, -5);
    const entry = await readEntry(dir, id);
    if (entry) entries.push(await refreshStatus(dir, entry));
  }
  entries.sort((a, b) => (a.startedAt < b.startedAt ? -1 : a.startedAt > b.startedAt ? 1 : a.id < b.id ? -1 : 1));
  return entries;
}

/** Short human elapsed time since an ISO startedAt. */
export function formatElapsed(startedAt: string, now = new Date()): string {
  const startMs = Date.parse(startedAt);
  if (Number.isNaN(startMs)) return "?";
  let seconds = Math.max(0, Math.round((now.getTime() - startMs) / 1000));
  const hours = Math.floor(seconds / 3600);
  seconds -= hours * 3600;
  const minutes = Math.floor(seconds / 60);
  seconds -= minutes * 60;
  if (hours > 0) return hours + "h " + minutes + "m";
  if (minutes > 0) return minutes + "m " + seconds + "s";
  return seconds + "s";
}

/** Single-line task preview (newlines collapsed). */
export function taskPreview(task: string, max: number): string {
  const flat = task.replace(/\s+/g, " ").trim();
  if (flat.length <= max) return flat;
  return flat.slice(0, max) + "...";
}

const TAIL_CAP = 4 * 1024;

function tailOf(text: string, max: number): string {
  const trimmed = text.trimEnd();
  if (trimmed.length <= max) return trimmed;
  return "[...] " + trimmed.slice(-max);
}

/**
 * Read a job's event stream: the `<id>.jsonl` artifact (headless jobs), or —
 * when that is missing, i.e. tmux interactive jobs whose stdout is the pane —
 * the child's pi session file. Null when neither exists/is readable.
 */
function readJsonl(dir: string, id: string): string | null {
  try {
    return fs.readFileSync(path.join(dir, id + ".jsonl"), "utf8");
  } catch {
    const sessionFile = findSessionFile(dir, id);
    if (!sessionFile) return null;
    try {
      return fs.readFileSync(sessionFile, "utf8");
    } catch {
      return null;
    }
  }
}

/** One line per job for the list view (id omitted → job_status lists all). */
export function formatJobList(entries: JobEntry[]): string {
  const lines: string[] = [];
  lines.push("Background jobs (" + entries.length + ") — artifacts in .pi/jobs/:");
  for (const entry of entries) {
    lines.push(
      "- " + entry.id + " [" + entry.status + "] agent=" + entry.agent +
      " started=" + entry.startedAt + " elapsed=" + formatElapsed(entry.startedAt) +
      " task: " + taskPreview(entry.task, 80),
    );
  }
  lines.push("Use job_status with id = the job id for the full output.");
  return lines.join("\n");
}

export interface ApprovalRelayOutcome {
  ok: boolean;
  /** Refusal reason when ok is false. */
  reason?: string;
  /** tmux session the message was delivered to when ok is true. */
  session?: string;
}

/**
 * Phase 7: relay an approval decision into a confirm-handoff job's idle tmux
 * pane so the parent session can approve without attaching. The message is
 * forwarded VERBATIM (typed literally via tmux send-keys -l, then Enter) —
 * exactly what a user typing in the pane does. Delivery is always
 * user-initiated; never resend automatically (a duplicate is harmless to pi
 * but confusing). Every refusal is a clean, actionable message.
 */
export async function relayApprovalDecision(cwd: string, id: string, message: string): Promise<ApprovalRelayOutcome> {
  const dir = jobsDirFor(cwd);
  const entry = await getJob(cwd, id);
  if (!entry) {
    const known = listJobIdsSync(cwd).join(", ") || "none";
    return { ok: false, reason: "No job with id " + id + ". Known jobs: " + known };
  }
  if (entry.runner !== "tmux" || !entry.tmuxSession) {
    return { ok: false, reason: "Job " + id + " has no tmux pane (runner: " + entry.runner + ") — approval relay needs an interactive pane." };
  }
  if (!(await tmuxSessionAlive(entry.tmuxSession))) {
    return { ok: false, reason: "tmux session " + entry.tmuxSession + " is not alive — the orchestrator process is gone; check job_status for artifacts." };
  }
  const state = sessionRoundState(dir, entry.id);
  if (!state.complete) {
    return { ok: false, reason: "Job " + id + " is mid-round (its pi session has an unfinished turn) — wait for the round to end, then retry (job_status shows the tail)." };
  }
  if (!state.awaitingApproval) {
    return { ok: false, reason: "Job " + id + " is not awaiting approval — its last message carries no " + APPROVAL_MARKER + " marker (nothing to approve; job_status id " + id + " shows the tail)." };
  }
  if (!message.trim()) {
    return { ok: false, reason: "Empty approval message — pass \"approved [note]\" or \"rejected: <reason>\"." };
  }
  if (/\r|\n/.test(message)) {
    return { ok: false, reason: "Approval message must be a single line — a newline would submit the pane's editor early and split the decision from its note." };
  }
  await execFileP("tmux", ["send-keys", "-t", entry.tmuxSession, "-l", message]);
  await execFileP("tmux", ["send-keys", "-t", entry.tmuxSession, "Enter"]);
  return { ok: true, session: entry.tmuxSession };
}

/**
 * Final assistant text from a job's `<id>.jsonl` artifact — the same parsing
 * job_status uses (parseChildJsonl + getFinalOutput), capped like job_status's
 * done branch. Returns null when the job id is unknown or the jsonl is
 * missing; empty string when the job produced no assistant text.
 */
export async function getJobResultText(cwd: string, id: string): Promise<string | null> {
  const entry = await getJob(cwd, id);
  if (!entry) return null;
  const jsonlText = readJsonl(jobsDirFor(cwd), id);
  if (jsonlText === null) return null;
  return truncateParallelOutput(getFinalOutput(parseChildJsonl(jsonlText).messages) || "");
}

/**
 * Detailed report for one job. Finished jobs get their `<id>.jsonl` parsed via
 * the shared module; the final assistant text is capped at ~50 KB. Failures
 * surface exit code + stderr tail + last events; awaiting_approval (phase 7)
 * shows the captured approval block plus the job_approve relay hint.
 */
export async function formatJobDetail(entry: JobEntry, dir: string): Promise<string> {
  const lines: string[] = [];
  lines.push("Job " + entry.id + " [" + entry.status + "]");
  lines.push("agent: " + entry.agent);
  lines.push("started: " + entry.startedAt + "  elapsed: " + formatElapsed(entry.startedAt));
  lines.push("runner: " + entry.runner + (entry.tmuxSession ? " (tmux session: " + entry.tmuxSession + ")" : ""));
  lines.push("task: " + taskPreview(entry.task, 200));
  const jsonlText = readJsonl(dir, entry.id);
  const parsed = jsonlText === null ? null : parseChildJsonl(jsonlText);

  if (entry.status === "running") {
    lines.push("--- recent activity ---");
    const soFar = parsed ? getFinalOutput(parsed.messages) : "";
    lines.push(soFar ? tailOf(soFar, TAIL_CAP) : "(no output yet)");
  } else if (entry.status === "awaiting_approval") {
    lines.push("--- awaiting approval ---");
    const block = parsed ? truncateParallelOutput(getFinalOutput(parsed.messages) || "(no approval block captured)") : "(no approval block captured)";
    lines.push(block);
    lines.push(
      "Approve or reject via the job_approve tool (id: \"" + entry.id + "\", message: \"approved [note]\" or \"rejected: <reason>\")" +
      (entry.tmuxSession ? ", or type the same message in the pane (tmux attach -t " + entry.tmuxSession + ")" : "") + ".",
    );
  } else if (entry.status === "done") {
    lines.push("--- final output ---");
    const finalText = parsed ? truncateParallelOutput(getFinalOutput(parsed.messages) || "(no output)") : "(no output)";
    lines.push(finalText);
  } else if (entry.status === "failed") {
    const exitCode = await readExitCode(dir, entry.id);
    lines.push("exit code: " + (exitCode === null ? "(missing .exit file)" : String(exitCode)));
    let stderrText = "";
    try {
      stderrText = fs.readFileSync(path.join(dir, entry.id + ".err"), "utf8");
    } catch {
      /* no stderr captured */
    }
    lines.push("stderr (tail): " + (stderrText.trim() ? tailOf(stderrText, TAIL_CAP) : "(empty)"));
    lines.push("--- last events ---");
    const lastAssistant = parsed ? getFinalOutput(parsed.messages) : "";
    lines.push(lastAssistant ? tailOf(lastAssistant, TAIL_CAP) : "(no assistant output)");
  } else {
    // aborted: runner died or the machine restarted before the child exited.
    lines.push("The runner died before the child exited (no .exit file). Partial output:");
    const soFar = parsed ? getFinalOutput(parsed.messages) : "";
    lines.push(soFar ? tailOf(soFar, TAIL_CAP) : "(no output captured)");
  }
  return lines.join("\n");
}

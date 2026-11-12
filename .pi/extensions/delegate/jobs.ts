/**
 * Background job runner (phase 3) — tmux-backed, with a detached-spawn fallback.
 *
 * Artifacts per job live in `.pi/jobs/` (gitignored):
 *   <id>.task    — full child prompt (file-based so arbitrary quoting is safe;
 *                  NEVER inline the task text into a shell string)
 *   <id>.jsonl   — child's `pi --mode json` stdout event stream
 *   <id>.err     — child stderr
 *   <id>.exit    — exit code written by the runner script when the child ends
 *   <id>.json    — registry entry (atomic write: temp + rename)
 *   <id>.sys.md  — agent system prompt for --append-system-prompt (kept as artifact)
 *   <id>.sh      — per-job runner script
 *
 * Job ids: `<agent>-<yyyymmdd-hhmmss>` with the agent part sanitized.
 * Background children run `--session-id pi-<id>` (resumable later), NOT
 * `--no-session`; foreground stays `--no-session`.
 *
 * If tmux is unavailable (or the new-session call fails), the job falls back to
 * a detached child (`sh <runner>`, setsid-style via Node's detached option) and
 * records `runner: "fallback"` + pid in the registry (no attach support).
 */

import { execFile, spawn } from "node:child_process";
import * as fs from "node:fs";
import * as path from "node:path";
import { promisify } from "node:util";
import { CONFIG_DIR_NAME } from "@earendil-works/pi-coding-agent";
import type { AgentConfig } from "./agents.ts";
import { getFinalOutput, parseChildJsonl, truncateParallelOutput } from "./output.ts";

const execFileP = promisify(execFile);

export type JobStatus = "running" | "done" | "failed" | "aborted";
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
  /** Set when the runner observes the transition out of "running"; older entries lack it. */
  finishedAt?: string;
}

export function jobsDirFor(cwd: string): string {
  return path.join(cwd, CONFIG_DIR_NAME, "jobs");
}

/** POSIX single-quote escaping for embedding a path/text into a shell command. */
function shq(text: string): string {
  return "'" + text.replace(/'/g, "'\\''") + "'";
}

function sanitizeIdPart(name: string): string {
  const cleaned = name.replace(/[^\w.-]+/g, "_").slice(0, 40).replace(/^_+|_+$/g, "");
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

async function uniqueJobId(dir: string, agentName: string): Promise<string> {
  const base = sanitizeIdPart(agentName) + "-" + timestamp(new Date());
  let id = base;
  for (let n = 2; fs.existsSync(path.join(dir, id + ".json")); n++) {
    id = base + "-" + n;
  }
  return id;
}

async function tmuxSessionAlive(session: string): Promise<boolean> {
  try {
    await execFileP("tmux", ["has-session", "-t", session]);
    return true;
  } catch {
    return false;
  }
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
 * (status "running"). Throws only on local setup errors (mkdir/write).
 */
export async function startBackgroundJob(opts: StartJobOptions): Promise<JobEntry> {
  const dir = jobsDirFor(opts.cwd);
  await fs.promises.mkdir(dir, { recursive: true });
  const id = await uniqueJobId(dir, opts.agent.name);
  const base = path.join(dir, id);
  const taskPath = base + ".task";
  const sysPath = base + ".sys.md";
  const jsonlPath = base + ".jsonl";
  const errPath = base + ".err";
  const exitPath = base + ".exit";
  const runnerPath = base + ".sh";
  const tmuxSession = "pi-" + id;

  await writeAtomic(taskPath, opts.prompt);
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
  const lines: string[] = [];
  lines.push("#!/bin/sh");
  lines.push("# pi delegate background job " + id);
  // Scrub pi's session env (the same five keys the foreground spawn deletes in
  // index.ts) so background children start clean: the runner inherits the full
  // parent env via tmux/detached spawn, including pi's bash-tool exports.
  lines.push("unset PI_SESSION_ID PI_SESSION_FILE PI_PROVIDER PI_MODEL PI_REASONING_LEVEL");
  lines.push("cd " + shq(opts.cwd) + " || exit 1");
  let cmd = "PI_DELEGATE_CHILD=1 " + piBin + " --mode json -p --session-id " + shq(tmuxSession);
  if (opts.model) cmd += " --model " + shq(opts.model);
  if (opts.thinking) cmd += " --thinking " + shq(opts.thinking);
  if (opts.agent.tools && opts.agent.tools.length > 0) {
    cmd += " --tools " + shq(opts.agent.tools.join(","));
  }
  if (hasSystemPrompt) cmd += " --append-system-prompt " + shq(sysPath);
  // Double-quoted "$(cat ...)": no word splitting / globbing — the prompt reaches
  // the child as ONE argv element, byte-identical to what was written to disk.
  cmd += " \"$(cat " + shq(taskPath) + ")\"";
  cmd += " > " + shq(jsonlPath) + " 2> " + shq(errPath);
  lines.push(cmd);
  lines.push('code=$?');
  lines.push('echo "$code" > ' + shq(exitPath));

  await fs.promises.writeFile(runnerPath, lines.join("\n") + "\n", "utf8");
  await fs.promises.chmod(runnerPath, 0o755);

  let entry: JobEntry;
  try {
    await execFileP("tmux", ["new-session", "-d", "-s", tmuxSession, "sh " + shq(runnerPath)]);
    entry = { id, agent: opts.agent.name, task: opts.task, startedAt: new Date().toISOString(), runner: "tmux", tmuxSession, status: "running" };
  } catch {
    const child = spawn("sh", [runnerPath], { cwd: opts.cwd, detached: true, stdio: "ignore" });
    child.unref();
    entry = { id, agent: opts.agent.name, task: opts.task, startedAt: new Date().toISOString(), runner: "fallback", pid: child.pid, status: "running" };
  }
  await persistEntry(dir, entry);
  return entry;
}

/**
 * Re-read the entry and advance its status from on-disk evidence:
 * exit file → done/failed; runner gone without an exit file → aborted.
 * Persisted (atomically) whenever the status changes.
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
  if (entry.status !== "running") return entry;

  const exitCode = await readExitCode(dir, entry.id);
  if (exitCode !== null) {
    entry.status = exitCode === 0 ? "done" : "failed";
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

/** Read a job's jsonl stream; returns null when the file is missing/unreadable. */
function readJsonl(dir: string, id: string): string | null {
  try {
    return fs.readFileSync(path.join(dir, id + ".jsonl"), "utf8");
  } catch {
    return null;
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
 * surface exit code + stderr tail + last events.
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

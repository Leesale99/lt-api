/**
 * Worker gates (batch 1): fail-closed gate runner, receipts, gate-file
 * protection and content-bound approval helpers.
 *
 * Design rules, in order of authority:
 *
 * 1. FAIL CLOSED. A gate is ok ONLY when a command actually ran and exited 0
 *    inside its timeout. Missing binary (ENOENT), non-runnable file, spawn
 *    throw, timeout, signal death or any internal error is a FAILURE with a
 *    human-readable reason — never "assume fine".
 * 2. NEVER WEAKER THAN FULL. A scoped spec check that fails for ANY reason
 *    (including "unknown flag" because the scoped flags have not landed yet)
 *    falls back to the full `go run ./cmd/spec check --strict`. A failed full
 *    run is a failed gate.
 * 3. The EXTENSION writes receipts; workers never do. Receipts are appended to
 *    `<cwd>/<receipts_dir>/<ISO-date>.jsonl` (default `.pi/receipts`,
 *    gitignored). Receipt metadata is fail-open (a missing rules.yaml or a
 *    broken git just records an empty field) — ONLY the receipt degrades,
 *    never the gate verdict.
 * 4. Gate-file protection is a DENY list with a single file-based escape
 *    hatch: the policy's `allowlist_path` (default
 *    `.pi/delegate/gate-allowlist.json`, a JSON array of repo-root-relative
 *    globs). There are deliberately NO env-var bypasses. A missing,
 *    unreadable or invalid allowlist behaves exactly like an empty one (deny).
 *    The allowlist file itself is always denied, even if its own globs match
 *    it, so no worker can self-authorize — and so is WORKFLOW.md itself.
 * 5. THE POLICY IS DECLARED, NOT HARDCODED (batch 3). Gate commands, the
 *    protected/editable path sets, the allowlist path, the receipt/claims
 *    dirs, the settle limits, the stall timeout and the concurrency cap come
 *    from the repo-root WORKFLOW.md front matter (policy.ts). A missing or
 *    invalid policy is a REFUSAL: no command runs and every in-repo write is
 *    denied until the file exists and parses.
 *
 * Task-packet convention (SPEC-SCOPE):
 *   The orchestrator includes a line in the worker task text —
 *     SPEC-SCOPE: module=game rules=R-GAME-001,R-GAME-002
 *   (module and/or rules, both optional but at least one required for the line
 *   to count). `parseSpecScope` extracts it and index.ts exports the normalized
 *   form as PI_DELEGATE_SPEC_SCOPE in the child spawn env; children spawned
 *   without a scope line get NO env var and gate on the FULL spec check.
 *
 * Loader gotcha (pi 1.5.2 extension parser): avoid `?:` ternaries containing
 * double-quoted strings inside a template literal's ${} — this module sticks to
 * string concatenation, so nothing here can trip that parser.
 */

import { spawn, type ChildProcess } from "node:child_process";
import { createHash } from "node:crypto";
import * as fs from "node:fs";
import * as path from "node:path";
import {
  DEFAULT_ALLOWLIST_PATH,
  DEFAULT_CLAIMS_DIR,
  DEFAULT_PI_EDITABLE_PATHS,
  DEFAULT_PROTECTED_PATHS,
  DEFAULT_RECEIPTS_DIR,
  type WorkflowPolicy,
  WORKFLOW_FILE_REL_PATH,
  loadWorkflowPolicy,
  workflowPolicyRefusal,
} from "./policy.ts";

// ── Fail-closed command runner ───────────────────────────────────────────────

/** Default hard timeout for `make` gates (audit/static is the slow one). */
export const DEFAULT_TIMEOUT_MS = 300_000;
/** Hard timeout for one scoped `go run ./cmd/spec check` invocation. */
export const SPEC_CHECK_TIMEOUT_MS = 180_000;
/** Hard timeout for a `git rev-parse` metadata probe. */
export const GIT_TIMEOUT_MS = 10_000;
/** Output kept per stream (tail) — gates report the last lines only. */
const OUTPUT_CAP = 64 * 1024;

export interface GateOptions {
  /** Working directory for the command (defaults to process.cwd()). */
  cwd?: string;
  /** Hard timeout in ms; the process is SIGKILLed when it expires. */
  timeoutMs?: number;
}

export interface GateResult {
  /** true ONLY for exit code 0, inside the timeout, with no spawn error. */
  ok: boolean;
  /** Exit code when the process exited; null on timeout / spawn error. */
  exitCode: number | null;
  stdout: string;
  stderr: string;
  timedOut: boolean;
  /** Spawn failure message (ENOENT, EACCES, non-runnable, …) or null. */
  spawnError: string | null;
  durationMs: number;
  /** One-line human reason when ok is false. */
  reason: string;
}

/**
 * Run one command with a hard timeout. Never throws: every failure mode is
 * folded into a GateResult with ok:false and a reason.
 */
export async function runGate(command: string, args: string[], options: GateOptions = {}): Promise<GateResult> {
  const timeoutMs = options.timeoutMs ?? DEFAULT_TIMEOUT_MS;
  const startedAt = Date.now();
  return await new Promise<GateResult>((resolve) => {
    let settled = false;
    let stdout = "";
    let stderr = "";
    let timedOut = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let child: ChildProcess | undefined;

    const finish = (patch: {
      exitCode?: number | null;
      spawnError?: string | null;
      ok?: boolean;
      reason?: string;
    }) => {
      if (settled) return;
      settled = true;
      if (timer) clearTimeout(timer);
      // Detach listeners so a late data chunk cannot mutate a settled result.
      child?.stdout?.removeAllListeners();
      child?.stderr?.removeAllListeners();
      child?.removeAllListeners();
      const exitCode = patch.exitCode === undefined ? null : patch.exitCode;
      const spawnError = patch.spawnError === undefined ? null : patch.spawnError;
      const ok = patch.ok === true;
      let reason = patch.reason ?? "";
      if (!reason) {
        if (spawnError) reason = "spawn failed: " + spawnError;
        else if (timedOut) reason = "timed out after " + String(timeoutMs) + "ms";
        else if (!ok) reason = "exited with code " + String(exitCode);
      }
      resolve({
        ok,
        exitCode,
        stdout,
        stderr,
        timedOut,
        spawnError,
        durationMs: Date.now() - startedAt,
        reason,
      });
    };

    try {
      child = spawn(command, args, {
        cwd: options.cwd,
        shell: false,
        stdio: ["ignore", "pipe", "pipe"],
        env: process.env,
      });
    } catch (error) {
      finish({ spawnError: errorMessage(error), reason: "spawn threw: " + errorMessage(error) });
      return;
    }

    timer = setTimeout(() => {
      timedOut = true;
      try {
        child?.kill("SIGKILL");
      } catch {
        /* already gone */
      }
      // Do not wait for a close event that may never come (a wedged child that
      // ignores SIGKILL is rare but the gate must still terminate).
      finish({ exitCode: null, reason: "timed out after " + String(timeoutMs) + "ms" });
    }, timeoutMs);

    if (!child.stdout || !child.stderr) {
      finish({ spawnError: "no stdio pipes", reason: "spawn produced no stdio pipes" });
      return;
    }

    child.stdout.on("data", (data: Buffer | string) => {
      stdout += data.toString();
      if (stdout.length > OUTPUT_CAP) stdout = stdout.slice(-OUTPUT_CAP);
    });
    child.stderr.on("data", (data: Buffer | string) => {
      stderr += data.toString();
      if (stderr.length > OUTPUT_CAP) stderr = stderr.slice(-OUTPUT_CAP);
    });
    child.on("error", (error) => {
      finish({ spawnError: errorMessage(error), reason: "spawn failed: " + errorMessage(error) });
    });
    child.on("close", (code, signal) => {
      if (timedOut) return finish({ exitCode: null, reason: "timed out after " + String(timeoutMs) + "ms" });
      if (signal) {
        return finish({
          exitCode: code,
          reason: "killed by signal " + signal,
        });
      }
      if (code === 0) return finish({ exitCode: 0, ok: true });
      finish({ exitCode: code, reason: "exited with code " + String(code) });
    });
  });
}

function errorMessage(error: unknown): string {
  if (error instanceof Error) return error.message;
  return String(error);
}

// ── Gate steps + scoped gate ────────────────────────────────────────────────

export interface GateStep {
  /** Human command line ("make audit/static"). */
  command: string;
  /** argv actually executed. */
  argv: string[];
  ok: boolean;
  exitCode: number | null;
  timedOut: boolean;
  durationMs: number;
  stdout: string;
  stderr: string;
  spawnError: string | null;
  reason: string;
  /** Why this step exists (fallback marker, scope line, …). */
  note?: string;
}

export interface GateRun {
  ok: boolean;
  steps: GateStep[];
}

export interface SpecScope {
  module?: string;
  rules: string[];
}

function stepOf(result: GateResult, argv: string[], note?: string): GateStep {
  return {
    command: argv.join(" "),
    argv,
    ok: result.ok,
    exitCode: result.exitCode,
    timedOut: result.timedOut,
    durationMs: result.durationMs,
    stdout: result.stdout,
    stderr: result.stderr,
    spawnError: result.spawnError,
    reason: result.reason,
    note,
  };
}

async function runStep(argv: string[], cwd: string, timeoutMs: number, note?: string): Promise<GateStep> {
  const [command, ...args] = argv;
  const result = await runGate(command, args, { cwd, timeoutMs });
  return stepOf(result, argv, note);
}

// ── SPEC-SCOPE parsing (task text → env → scoped check) ─────────────────────

/**
 * Parse a SPEC-SCOPE body ("module=game rules=R-A-1,R-B-2") into a scope.
 * Returns null when neither module nor rules is present — a bare "SPEC-SCOPE:"
 * line must not silence the full check.
 */
function parseScopeBody(body: string): SpecScope | null {
  const moduleMatch = /module=([A-Za-z0-9_./-]+)/.exec(body);
  const rulesMatch = /rules=([A-Za-z0-9_.,/-]+)/.exec(body);
  const module = moduleMatch ? moduleMatch[1].replace(/[.\/]+$/, "") : undefined;
  const rules = rulesMatch
    ? rulesMatch[1]
        .split(",")
        .map((rule) => rule.trim())
        .filter((rule) => /^[A-Za-z0-9_-]+$/.test(rule))
    : [];
  if (!module && rules.length === 0) return null;
  return { module, rules };
}

/**
 * Extract the scope from a task packet. Only lines whose trimmed form starts
 * with `SPEC-SCOPE:` count; the first such line with usable content wins.
 */
export function parseSpecScope(text: string): SpecScope | null {
  for (const rawLine of text.split("\n")) {
    const line = rawLine.trim();
    if (!line.toUpperCase().startsWith("SPEC-SCOPE:")) continue;
    const scope = parseScopeBody(line.slice("SPEC-SCOPE:".length));
    if (scope) return scope;
  }
  return null;
}

/**
 * Parse the PI_DELEGATE_SPEC_SCOPE env value (already normalized by index.ts).
 * Lenient: a hand-set value in either "module=… rules=…" or bare "rules=…"
 * form is accepted; unusable values yield null (→ full check).
 */
export function parseSpecScopeEnv(value: string | undefined): SpecScope | null {
  if (!value || !value.trim()) return null;
  return parseScopeBody(value);
}

/** Normalized single-line form exported into PI_DELEGATE_SPEC_SCOPE. */
export function formatSpecScope(scope: SpecScope): string {
  const parts: string[] = [];
  if (scope.module) parts.push("module=" + scope.module);
  if (scope.rules.length > 0) parts.push("rules=" + scope.rules.join(","));
  return parts.join(" ");
}

/**
 * Split a declared policy command string ("go run ./cmd/spec check --strict")
 * into argv: whitespace-separated, single/double quotes group a token, a
 * backslash escapes inside double quotes. No shell interpretation anywhere.
 */
export function splitCommandLine(command: string): string[] {
  const tokens: string[] = [];
  let current = "";
  let started = false;
  let quote: string | null = null;
  for (let index = 0; index < command.length; index++) {
    const char = command[index];
    if (quote) {
      if (char === quote) {
        quote = null;
        continue;
      }
      if (char === "\\" && quote === '"' && index + 1 < command.length) {
        current += command[++index];
        continue;
      }
      current += char;
      continue;
    }
    if (char === '"' || char === "'") {
      quote = char;
      started = true;
      continue;
    }
    if (/\s/.test(char)) {
      if (started) {
        tokens.push(current);
        current = "";
        started = false;
      }
      continue;
    }
    current += char;
    started = true;
  }
  if (started) tokens.push(current);
  return tokens;
}

/**
 * The fail-closed GateRun for an unavailable WORKFLOW.md policy: no command
 * ran, one step carries the reason, and the report names the missing policy.
 */
export function policyRefusalGateRun(reason: string): GateRun {
  return {
    ok: false,
    steps: [
      {
        command: "(workflow policy)",
        argv: [],
        ok: false,
        exitCode: null,
        timedOut: false,
        durationMs: 0,
        stdout: "",
        stderr: reason,
        spawnError: null,
        reason: workflowPolicyRefusal(reason),
        note: "WORKFLOW.md front matter",
      },
    ],
  };
}

/**
 * Run the worker gate: the declared `audit_static_cmd` plus the scoped spec
 * check (`scoped_check_cmd` from WORKFLOW.md, scope flags appended).
 *
 * Spec check resolution (never weaker than full):
 *   - no scope      → `go run ./cmd/spec check --strict`
 *   - module scoped → `--strict --module <m>` (one run per module)
 *   - rules scoped  → `--strict --rule <id>` (one run per rule)
 *   - ANY scoped invocation fails (real violation, unknown flag, timeout,
 *     missing binary) → the full `go run ./cmd/spec check --strict` decides.
 *     The failing scoped step stays in `steps` for the receipt/report.
 */
export async function runScopedGate(opts: {
  cwd: string;
  scopeEnv?: string;
  makeTimeoutMs?: number;
  specTimeoutMs?: number;
}): Promise<GateRun> {
  // Batch 3: the ladder's commands come from WORKFLOW.md. An unavailable
  // policy means the gate set is undeclared, so NOTHING runs and the gate is a
  // loud failure — never an unpoliced "pass".
  const loaded = loadWorkflowPolicy(opts.cwd);
  if (!loaded.ok) return policyRefusalGateRun(loaded.reason);

  const steps: GateStep[] = [];
  const makeStep = await runStep(
    splitCommandLine(loaded.policy.auditStaticCmd),
    opts.cwd,
    opts.makeTimeoutMs ?? DEFAULT_TIMEOUT_MS,
  );
  steps.push(makeStep);

  const scope = parseSpecScopeEnv(opts.scopeEnv);
  const specTimeout = opts.specTimeoutMs ?? SPEC_CHECK_TIMEOUT_MS;
  const base = splitCommandLine(loaded.policy.scopedCheckCmd);
  const scopedArgv: string[][] = [];
  if (scope?.module) scopedArgv.push([...base, "--module", scope.module]);
  for (const rule of scope?.rules ?? []) scopedArgv.push([...base, "--rule", rule]);

  if (scopedArgv.length === 0) {
    steps.push(await runStep(base, opts.cwd, specTimeout, "full (no SPEC-SCOPE)"));
    return { ok: steps.every((step) => step.ok), steps };
  }

  let scopedFailed = false;
  for (const argv of scopedArgv) {
    const step = await runStep(argv, opts.cwd, specTimeout, "scoped");
    steps.push(step);
    if (!step.ok) scopedFailed = true;
  }
  if (scopedFailed) {
    // Fail-closed fallback: a scoped failure (including "unknown flag") must
    // never be reported as a pass; the full check decides.
    steps.push(await runStep(base, opts.cwd, specTimeout, "full fallback (scoped check failed)"));
    const full = steps[steps.length - 1];
    return { ok: makeStep.ok && full.ok, steps };
  }
  return { ok: steps.every((step) => step.ok), steps };
}

/** Last N lines of a step's combined output (stdout first, then stderr). */
export function tailLines(text: string, maxLines: number): string {
  const lines = text.replace(/\s+$/, "").split("\n");
  if (lines.length <= maxLines) return lines.join("\n");
  return lines.slice(-maxLines).join("\n");
}

function stepOutput(step: GateStep, maxLines: number): string {
  const combined = step.stdout + (step.stderr ? (step.stdout.endsWith("\n") ? "" : "\n") + step.stderr : "");
  const tail = tailLines(combined, maxLines);
  return tail.trim() ? tail : "(no output)";
}

export interface GateReportOptions {
  /** "── SCOPED GATE FAILED ──" vs "── SETTLE GATE BLOCKED … ──". */
  header: string;
  /** Footer instruction line. */
  footer: string;
  /** Output lines per command. */
  maxLines?: number;
  /** Steps to report; defaults to every step that failed. */
  steps?: GateStep[];
}

/**
 * Delimited, self-contained failure block appended to a tool result or sent as
 * a custom message. Lists every failing command with its exit code/reason and
 * the last ~30 lines of output.
 */
export function formatGateReport(run: GateRun, opts: GateReportOptions): string {
  const maxLines = opts.maxLines ?? 30;
  const failed = (opts.steps ?? run.steps).filter((step) => !step.ok);
  const lines: string[] = [opts.header];
  for (const step of failed) {
    const code = step.exitCode === null ? "no exit code" : "exit " + String(step.exitCode);
    const noteSuffix = step.note ? " [" + step.note + "]" : "";
    lines.push("- " + step.command + " — " + code + " · " + step.reason + noteSuffix);
    lines.push(stepOutput(step, maxLines));
  }
  if (failed.length === 0) lines.push("- (no failing step recorded — internal error)");
  lines.push(opts.footer);
  return lines.join("\n");
}

// ── Receipts ────────────────────────────────────────────────────────────────

export interface ReceiptCommand {
  command: string;
  exitCode: number | null;
  timedOut: boolean;
  durationMs: number;
}

export interface ReceiptInput {
  cwd: string;
  /** Agent name when known, else PI_DELEGATE_ROLE. */
  agent: string;
  /** PI_SESSION_ID when the process has one (worker children scrub it). */
  session?: string;
  commands: ReceiptCommand[];
  /** Extra context (gate kind, pass/fail, debounce skip). */
  note?: string;
  ok?: boolean;
}

/**
 * `<cwd>/.pi/receipts` (or the declared `receipts_dir`). Batch 3: the policy
 * field wins when present; the default keeps today's path.
 */
export function receiptsDirFor(cwd: string): string {
  const loaded = loadWorkflowPolicy(cwd);
  const rel = loaded.ok && loaded.declared.has("receipts_dir") ? loaded.policy.receiptsDir : DEFAULT_RECEIPTS_DIR;
  return path.join(cwd, rel);
}

/**
 * Append one JSON line to `<cwd>/.pi/receipts/<ISO-date>.jsonl`. Returns the
 * file path, or null when even the receipt could not be written (the caller
 * logs; a receipt failure never changes a gate verdict).
 *
 * Metadata is fail-open: rulesHash is "" when docs/spec/rules.yaml is missing,
 * headSha is "" when git fails. The gate commands themselves stay fail-closed.
 */
export async function writeReceipt(input: ReceiptInput): Promise<string | null> {
  const dir = receiptsDirFor(input.cwd);
  const day = new Date().toISOString().slice(0, 10);
  const file = path.join(dir, day + ".jsonl");
  const record = {
    timestamp: new Date().toISOString(),
    agent: input.agent,
    session: input.session ?? "",
    commands: input.commands,
    rulesHash: await hashSpecRules(input.cwd),
    headSha: await headShaOf(input.cwd),
    ok: input.ok === true,
    note: input.note ?? "",
  };
  try {
    await fs.promises.mkdir(dir, { recursive: true });
    await fs.promises.appendFile(file, JSON.stringify(record) + "\n", "utf8");
    return file;
  } catch (error) {
    console.error("[delegate] receipt write failed (" + file + "):", error);
    return null;
  }
}

/** sha256 of docs/spec/rules.yaml; "" when unreadable (fail-open, receipt only). */
export async function hashSpecRules(cwd: string): Promise<string> {
  try {
    const raw = await fs.promises.readFile(path.join(cwd, "docs", "spec", "rules.yaml"));
    return createHash("sha256").update(raw).digest("hex");
  } catch {
    return "";
  }
}

/** `git rev-parse HEAD` in cwd; "" on any failure (fail-open, receipt only). */
export async function headShaOf(cwd: string): Promise<string> {
  const result = await runGate("git", ["rev-parse", "HEAD"], { cwd, timeoutMs: GIT_TIMEOUT_MS });
  if (!result.ok) return "";
  const sha = result.stdout.trim();
  return /^[0-9a-f]{40}$/i.test(sha) ? sha.toLowerCase() : "";
}

/** Receipt command rows from gate steps (one per executed command). */
export function receiptCommandsOf(run: GateRun): ReceiptCommand[] {
  return run.steps.map((step) => ({
    command: step.command,
    exitCode: step.exitCode,
    timedOut: step.timedOut,
    durationMs: step.durationMs,
  }));
}

/** Best-effort debug log for gate decisions (debounce skips, fallbacks). */
export async function logGateDebug(cwd: string, line: string): Promise<void> {
  try {
    const dir = receiptsDirFor(cwd);
    await fs.promises.mkdir(dir, { recursive: true });
    await fs.promises.appendFile(
      path.join(dir, "gates-debug.log"),
      new Date().toISOString() + " " + line + "\n",
      "utf8",
    );
  } catch {
    /* debug logging is advisory only */
  }
}

// ── Debounce (post-write gate) ──────────────────────────────────────────────

/** Minimum gap between post-write gate runs for one session. */
export const POST_WRITE_GATE_WINDOW_MS = 10_000;

interface DebounceRecord {
  lastRunAt: number;
  inFlight: boolean;
}

const debounceBySession = new Map<string, DebounceRecord>();

export type DebounceDecision = "run" | "skip-inflight" | "skip-window";

/**
 * Decide whether a post-write gate run is due for this session. A burst of
 * small edits must not queue one gate per write: after a run, further writes
 * inside POST_WRITE_GATE_WINDOW_MS are skipped (and while a run is in flight,
 * always skipped). Module state; per session key (caller passes PI_SESSION_ID
 * or a process-scoped fallback).
 */
export function shouldRunPostWriteGate(sessionKey: string, now: number = Date.now()): DebounceDecision {
  const record = debounceBySession.get(sessionKey);
  if (record?.inFlight) return "skip-inflight";
  if (record && now - record.lastRunAt < POST_WRITE_GATE_WINDOW_MS) return "skip-window";
  return "run";
}

/** Mark a gate run as started (clears the window, sets in-flight). */
export function markPostWriteGateStart(sessionKey: string, now: number = Date.now()): void {
  debounceBySession.set(sessionKey, { lastRunAt: now, inFlight: true });
}

/** Mark a gate run as finished; the debounce window starts at the START. */
export function markPostWriteGateEnd(sessionKey: string): void {
  const record = debounceBySession.get(sessionKey);
  if (record) record.inFlight = false;
  else debounceBySession.set(sessionKey, { lastRunAt: Date.now(), inFlight: false });
}

// ── Worker-settle gate attempts + backoff ───────────────────────────────────

/**
 * Batch 2: attempts 1..SETTLE_GATE_IMMEDIATE_ATTEMPTS behave EXACTLY like batch
 * 1 (immediate block + continue). Every later attempt waits an exponential
 * backoff before its continuation (`base * 2^(n-4)`: 2s, 4s at the default
 * base) and the LAST attempt is the hard stop. Backoff exists because a worker
 * retrying a slow `make audit/static` ladder in a tight loop burns turns on a
 * transient failure (another worker mid-write, a stale lock); the wait lets
 * the tree settle.
 */
/**
 * Batch 3: the settle knobs are declared in WORKFLOW.md. Resolution order per
 * knob — a field PRESENT and well-typed in the policy wins; otherwise the
 * batch-2 env override applies; otherwise the code default. `cwd` is the
 * project root (process.cwd() by default; callers pass ctx.cwd).
 */
export const SETTLE_GATE_MAX_ATTEMPTS = 5;
/** First backoff wait (ms); doubled per attempt past the immediate window. */
export const SETTLE_BACKOFF_BASE_MS = 2_000;
/** Attempts that retry with no wait (batch-1 semantics). */
export const SETTLE_GATE_IMMEDIATE_ATTEMPTS = 3;
/** Doublings cap, so a hand-set huge max cannot make the gate wait for hours. */
export const SETTLE_BACKOFF_MAX_DOUBLINGS = 5;

/** Immediate (no-wait) attempts: policy field → default. */
export function settleImmediateAttempts(cwd: string = process.cwd()): number {
  const loaded = loadWorkflowPolicy(cwd);
  if (loaded.ok && loaded.declared.has("settle_immediate_attempts")) {
    return loaded.policy.settleImmediateAttempts;
  }
  return SETTLE_GATE_IMMEDIATE_ATTEMPTS;
}

/** Backoff doublings cap: policy field → default. */
export function settleBackoffMaxDoublings(cwd: string = process.cwd()): number {
  const loaded = loadWorkflowPolicy(cwd);
  if (loaded.ok && loaded.declared.has("settle_backoff_max_doublings")) {
    return loaded.policy.settleBackoffMaxDoublings;
  }
  return SETTLE_BACKOFF_MAX_DOUBLINGS;
}

/** Total attempts: policy field → env override → default (never below immediate). */
export function settleGateMaxAttempts(cwd: string = process.cwd()): number {
  const immediate = settleImmediateAttempts(cwd);
  const loaded = loadWorkflowPolicy(cwd);
  if (loaded.ok && loaded.declared.has("settle_max_attempts")) {
    return Math.max(loaded.policy.settleMaxAttempts, immediate);
  }
  const raw = Number(process.env.PI_DELEGATE_SETTLE_MAX_ATTEMPTS);
  if (!Number.isFinite(raw) || raw < immediate) return SETTLE_GATE_MAX_ATTEMPTS;
  return Math.floor(raw);
}

/** Backoff base (ms): policy field → env override → default; 0 keeps no wait. */
export function settleBackoffBaseMs(cwd: string = process.cwd()): number {
  const loaded = loadWorkflowPolicy(cwd);
  if (loaded.ok && loaded.declared.has("settle_backoff_base_ms")) {
    return loaded.policy.settleBackoffBaseMs;
  }
  const raw = Number(process.env.PI_DELEGATE_BACKOFF_BASE_MS);
  if (!Number.isFinite(raw) || raw < 0) return SETTLE_BACKOFF_BASE_MS;
  return Math.floor(raw);
}

/** One failed settle-gate attempt, as it lands in the terminal receipt trail. */
export interface SettleAttemptRecord {
  attempt: number;
  /** Epoch ms of the failed run. */
  at: number;
  /** Every command of that run, joined with ', '. */
  command: string;
  commands: ReceiptCommand[];
  receiptPath: string | null;
  /** First failing step's reason — the 'previous failures' tail. */
  reason: string;
}

/**
 * Settle-gate state for this session (module state, session lifetime): a state
 * record instead of the batch-1 counter, so backoff can tell 'which attempt'
 * from 'how long ago' and the hard stop can print the whole trail. Reset by a
 * PASSING settle gate.
 */
export interface SettleGateState {
  attempts: number;
  lastAttemptAt: number;
  history: SettleAttemptRecord[];
}

let settleGateState: SettleGateState = { attempts: 0, lastAttemptAt: 0, history: [] };

/** Live state record (read-only by convention; mutate through the helpers). */
export function settleGateStateFor(): SettleGateState {
  return settleGateState;
}

export function settleGateAttemptsFor(): number {
  return settleGateState.attempts;
}

export function bumpSettleGateAttempts(now: number = Date.now()): number {
  settleGateState.attempts += 1;
  settleGateState.lastAttemptAt = now;
  return settleGateState.attempts;
}

/** Append the failed attempt to the trail (call AFTER bump). */
export function recordSettleGateAttempt(
  input: { run: GateRun; receiptPath: string | null; at?: number },
  now: number = Date.now(),
): SettleAttemptRecord {
  const failing = input.run.steps.find((step) => !step.ok);
  const record: SettleAttemptRecord = {
    attempt: settleGateState.attempts,
    at: input.at ?? now,
    command: input.run.steps.map((step) => step.command).join(", ") || "(no command)",
    commands: receiptCommandsOf(input.run),
    receiptPath: input.receiptPath,
    reason: failing ? failing.reason : "(no failing step recorded — internal error)",
  };
  settleGateState.history.push(record);
  return record;
}

export function resetSettleGateAttempts(): void {
  settleGateState = { attempts: 0, lastAttemptAt: 0, history: [] };
}

/** Backoff wait (ms) before the continuation of `attempt` (0 = immediate). */
export function settleBackoffMsFor(
  attempt: number,
  baseMs: number = settleBackoffBaseMs(),
  cwd: string = process.cwd(),
): number {
  const immediate = settleImmediateAttempts(cwd);
  if (attempt <= immediate) return 0;
  const doublings = Math.min(attempt - immediate - 1, settleBackoffMaxDoublings(cwd));
  return Math.max(0, baseMs) * 2 ** doublings;
}

export interface SettleAttemptPlan {
  attempt: number;
  maxAttempts: number;
  /** Wait before handing the continuation back (0 = immediate retry). */
  waitMs: number;
  /** true ⇒ this attempt's continuation is the TERMINAL (hard-stop) turn. */
  terminal: boolean;
  kind: "immediate" | "backoff" | "terminal";
}

/**
 * Pure decision for one failing attempt: how long to wait, and whether this
 * attempt is the hard stop. Attempts past the cap (only reachable when the
 * state was hand-driven) stay terminal with a capped wait.
 */
export function planSettleGateAttempt(
  attempt: number,
  opts: { maxAttempts?: number; baseMs?: number; cwd?: string } = {},
): SettleAttemptPlan {
  const cwd = opts.cwd ?? process.cwd();
  const maxAttempts = opts.maxAttempts ?? settleGateMaxAttempts(cwd);
  const waitMs = settleBackoffMsFor(attempt, opts.baseMs ?? settleBackoffBaseMs(cwd), cwd);
  const terminal = attempt >= maxAttempts;
  const kind: SettleAttemptPlan["kind"] = terminal ? "terminal" : waitMs > 0 ? "backoff" : "immediate";
  return { attempt, maxAttempts, waitMs, terminal, kind };
}

/** Human wait string: '2s', '500ms', 'none'. */
export function describeWaitMs(ms: number): string {
  if (!Number.isFinite(ms) || ms <= 0) return "none";
  if (ms < 1000) return String(Math.round(ms)) + "ms";
  const seconds = ms / 1000;
  return (Number.isInteger(seconds) ? String(seconds) : String(Math.round(seconds * 10) / 10)) + "s";
}

/** The 'previous failures' tail (oldest first, capped). */
export function recentSettleFailures(limit: number = 3): string[] {
  return settleGateState.history
    .slice(-limit)
    .map((record) => "attempt " + String(record.attempt) + ": " + record.command + " \u2192 " + record.reason);
}

/**
 * ESCALATING reason line for a continuing attempt, e.g.
 * `attempt 4/5 — retrying after 2s backoff; previous failures: attempt 1: …`.
 */
export function formatSettleEscalation(plan: SettleAttemptPlan, failures: string[] = recentSettleFailures()): string {
  const tail = failures.length > 0 ? failures.join(" | ") : "(none recorded)";
  if (plan.kind === "immediate") {
    return (
      "attempt " + String(plan.attempt) + "/" + String(plan.maxAttempts) +
      " \u2014 immediate retry; previous failures: " + tail
    );
  }
  const wait = describeWaitMs(plan.waitMs);
  const suffix = plan.terminal ? ", then HARD STOP" : "";
  return (
    "attempt " + String(plan.attempt) + "/" + String(plan.maxAttempts) +
    " \u2014 retrying after " + wait + " backoff" + suffix +
    "; previous failures: " + tail
  );
}

/** Full per-attempt receipt trail for the terminal report. */
export function formatSettleTrail(state: SettleGateState = settleGateState): string {
  if (state.history.length === 0) return "Settle gate trail: (no failed attempt recorded)";
  const lines: string[] = ["Settle gate trail (every failed attempt, oldest first):"];
  for (const record of state.history) {
    const codes = record.commands
      .map((entry) => entry.command + " \u2192 " + (entry.exitCode === null ? "no exit code" : "exit " + String(entry.exitCode)))
      .join("; ");
    lines.push(
      "- attempt " + String(record.attempt) + " \u00b7 " + (codes || record.command) +
      " \u00b7 receipt: " + (record.receiptPath ?? "(not written)") +
      " \u00b7 reason: " + record.reason,
    );
  }
  return lines.join("\n");
}

// ── Path classification ─────────────────────────────────────────────────────

function toPosix(value: string): string {
  return value.split(path.sep).join("/");
}

/** Repo-root-relative posix path, or null when the path escapes the repo. */
export function repoRelativePath(repoRoot: string, inputPath: string): string | null {
  const absolute = path.resolve(repoRoot, inputPath);
  const relative = toPosix(path.relative(repoRoot, absolute));
  if (!relative || relative === ".") return null;
  if (relative === ".." || relative.startsWith("../") || path.isAbsolute(relative)) return null;
  return relative;
}

/**
 * Tiny glob matcher for allowlist patterns and gate rules: `**` spans path
 * segments (including none), `*` and `?` stay inside one segment. Everything
 * else is literal (regex metacharacters escaped). Patterns and paths are
 * repo-root-relative posix strings.
 */
export function matchGlob(pattern: string, relPath: string): boolean {
  const normalized = toPosix(pattern).replace(/^\.\//, "").replace(/\/+$/, "");
  let source = "";
  for (let index = 0; index < normalized.length; index++) {
    const char = normalized[index];
    if (char === "*") {
      const isDouble = normalized[index + 1] === "*";
      if (isDouble) {
        const afterDouble = normalized[index + 2];
        if (afterDouble === "/") {
          source += "(?:.*/)?";
          index += 2;
        } else {
          source += ".*";
          index += 1;
        }
      } else {
        source += "[^/]*";
      }
      continue;
    }
    if (char === "?") {
      source += "[^/]";
      continue;
    }
    source += char.replace(/[.+^${}()|[\]\\]/g, "\\$&");
  }
  let regex: RegExp;
  try {
    regex = new RegExp("^" + source + "$");
  } catch {
    return false;
  }
  return regex.test(toPosix(relPath));
}

/**
 * Repo-root-relative path of the single gate-file escape hatch. Batch 3: the
 * effective path comes from the WORKFLOW.md policy (`allowlist_path`); this
 * constant is the default, kept for the callers/tests that never load policy.
 */
export const GATE_ALLOWLIST_REL_PATH = DEFAULT_ALLOWLIST_PATH;

/** Default deny set / editable set (the policy file's defaults when omitted). */
export const DEFAULT_GATE_PROTECTED_PATHS = DEFAULT_PROTECTED_PATHS;
export const DEFAULT_GATE_EDITABLE_PATHS = DEFAULT_PI_EDITABLE_PATHS;

export interface GatePathDecision {
  /** true ⇒ block the edit/write. */
  blocked: boolean;
  /** Repo-root-relative path when it resolved inside the repo. */
  rel: string | null;
  /** Why a non-blocked path was allowed. */
  allowedBy?: "not-gate-file" | "editable-pi-prose" | "allowlist" | "outside-repo";
  /** Blocking reason (names the protected files). */
  reason?: string;
}

/**
 * Read the escape-hatch allowlist. Missing / unreadable / non-array / non-
 * string entries ⇒ empty list (fail closed: gate files stay denied).
 */
export function readGateAllowlist(repoRoot: string, relPath: string = GATE_ALLOWLIST_REL_PATH): string[] {
  const file = path.join(repoRoot, relPath);
  let raw: string;
  try {
    raw = fs.readFileSync(file, "utf8");
  } catch {
    return [];
  }
  try {
    const parsed: unknown = JSON.parse(raw);
    if (!Array.isArray(parsed)) {
      console.error("[delegate] " + relPath + ": expected a JSON array of globs; ignoring (gate files stay denied)");
      return [];
    }
    return parsed.filter((entry): entry is string => typeof entry === "string" && entry.trim().length > 0);
  } catch (error) {
    console.error("[delegate] " + relPath + ": invalid JSON; ignoring (gate files stay denied):", error);
    return [];
  }
}

/**
 * Gate-file protection for child-session edits/writes: DENY by default for
 * every path in the policy's `protected_paths` — except the policy's
 * `pi_editable_paths` prose and any path matched by the allowlist escape
 * hatch. An unavailable policy denies every in-repo write loudly: a worker
 * must not edit under an undeclared rule set.
 */
export function evaluateGateFileWrite(repoRoot: string, inputPath: string): GatePathDecision {
  const rel = repoRelativePath(repoRoot, inputPath);
  if (!rel) return { blocked: false, rel: null, allowedBy: "outside-repo" };

  const loaded = loadWorkflowPolicy(repoRoot);
  if (!loaded.ok) {
    return { blocked: true, rel, reason: workflowPolicyRefusal(loaded.reason) };
  }
  const policy = loaded.policy;

  // Always denied, even when a protected-path or allowlist glob matches it: a
  // worker must not be able to authorize itself (the file is the ONLY
  // mechanism). This invariant is code, not policy.
  if (rel === policy.allowlistPath) {
    return { blocked: true, rel, reason: gateFileReason(rel, policy) };
  }
  // Same invariant for the policy itself (batch 3): a child that can rewrite
  // WORKFLOW.md can rewrite its own gates. Never grantable, not even through
  // the allowlist — that needs a human edit outside a delegated session.
  if (rel === WORKFLOW_FILE_REL_PATH) {
    return { blocked: true, rel, reason: workflowPolicySelfDeny(rel) };
  }

  const isGateFile = policy.protectedPaths.some((pattern) => matchGlob(pattern, rel));
  if (!isGateFile) return { blocked: false, rel, allowedBy: "not-gate-file" };

  if (policy.piEditablePaths.some((pattern) => matchGlob(pattern, rel))) {
    return { blocked: false, rel, allowedBy: "editable-pi-prose" };
  }

  const allowlist = readGateAllowlist(repoRoot, policy.allowlistPath);
  if (allowlist.some((pattern) => matchGlob(pattern, rel))) {
    return { blocked: false, rel, allowedBy: "allowlist" };
  }

  return { blocked: true, rel, reason: gateFileReason(rel, policy) };
}

function workflowPolicySelfDeny(rel: string): string {
  return (
    "The workflow policy file is protected from delegated edits: " + rel + ". " +
    "It declares the gates, caps and protected paths that bind this session, so a worker must not " +
    "rewrite it (not grantable through the allowlist either). Report the needed policy change to the " +
    "orchestrator/user instead."
  );
}

function gateFileReason(rel: string, policy?: WorkflowPolicy): string {
  const protectedList = (policy ? policy.protectedPaths : DEFAULT_GATE_PROTECTED_PATHS).join(", ");
  const editableList = (policy ? policy.piEditablePaths : DEFAULT_GATE_EDITABLE_PATHS).join(", ");
  const allowlist = policy ? policy.allowlistPath : GATE_ALLOWLIST_REL_PATH;
  return (
    "Gate file is protected from delegated edits: " + rel + ". " +
    "Protected by WORKFLOW.md protected_paths: " + protectedList + ". " +
    "Worker-editable exception (pi_editable_paths): " + editableList + ". " +
    "Only a matching glob in " + allowlist + " (repo-root-relative) allows this edit; " +
    "no env-var bypass exists. Report the needed change to the orchestrator instead."
  );
}

// ── Post-write gate relevance ───────────────────────────────────────────────

/**
 * Files whose successful write triggers the scoped post-write gate: Go,
 * Gherkin and YAML sources plus anything under docs/spec/.
 */
export function isGateRelevantWrite(repoRoot: string, inputPath: string): boolean {
  const rel = repoRelativePath(repoRoot, inputPath);
  if (!rel) return false;
  if (rel.startsWith("docs/spec/")) return true;
  return rel.endsWith(".go") || rel.endsWith(".feature") || rel.endsWith(".yaml");
}

// ── Reconciliation-lite ─────────────────────────────────────────────────────

/** Shared receipt shape (what writeReceipt appends). */
export interface ReceiptRecord {
  timestamp: string;
  agent: string;
  session: string;
  commands: ReceiptCommand[];
  rulesHash: string;
  headSha: string;
  ok: boolean;
  note: string;
}

/** `<cwd>/.pi/receipts/<YYYY-MM-DD>.jsonl` — the day's receipt log. */
export function receiptsFileFor(cwd: string, day: string = new Date().toISOString().slice(0, 10)): string {
  return path.join(receiptsDirFor(cwd), day + ".jsonl");
}

/**
 * Every readable receipt of `day` (default: today), oldest first. Malformed
 * lines are skipped: the receipt log is telemetry and must never throw.
 */
export async function readDayReceipts(cwd: string, day?: string): Promise<ReceiptRecord[]> {
  const file = receiptsFileFor(cwd, day);
  let raw: string;
  try {
    raw = await fs.promises.readFile(file, "utf8");
  } catch {
    return [];
  }
  const records: ReceiptRecord[] = [];
  for (const line of raw.split("\n")) {
    if (!line.trim()) continue;
    try {
      const parsed = JSON.parse(line) as Partial<ReceiptRecord>;
      if (!parsed || typeof parsed !== "object") continue;
      records.push({
        timestamp: typeof parsed.timestamp === "string" ? parsed.timestamp : "",
        agent: typeof parsed.agent === "string" ? parsed.agent : "",
        session: typeof parsed.session === "string" ? parsed.session : "",
        commands: Array.isArray(parsed.commands) ? (parsed.commands as ReceiptCommand[]) : [],
        rulesHash: typeof parsed.rulesHash === "string" ? parsed.rulesHash : "",
        headSha: typeof parsed.headSha === "string" ? parsed.headSha : "",
        ok: parsed.ok === true,
        note: typeof parsed.note === "string" ? parsed.note : "",
      });
    } catch {
      /* unreadable receipt line — skip */
    }
  }
  return records;
}

/**
 * The newest PASSING receipt of the day, i.e. the checkpoint to reconcile
 * against. A failing receipt is not a checkpoint (comparing against one would
 * make every later green run "mismatch" until it, too, was failing).
 */
export async function latestPassingReceipt(cwd: string, day?: string): Promise<ReceiptRecord | null> {
  const records = await readDayReceipts(cwd, day);
  for (let index = records.length - 1; index >= 0; index--) {
    if (records[index].ok) return records[index];
  }
  return null;
}

export interface ReconciliationCurrent {
  headSha: string;
  rulesHash: string;
  commands: ReceiptCommand[];
}

/**
 * Field-by-field diff of a recorded receipt against the current tree + a fresh
 * ladder run. Returns one human line per discrepancy (empty array = match).
 */
export function diffReceiptAgainst(record: ReceiptRecord | null, current: ReconciliationCurrent): string[] {
  if (!record) return [];
  const discrepancies: string[] = [];
  const recordedSha = record.headSha || "(not recorded)";
  const currentSha = current.headSha || "(not recorded)";
  if (record.headSha !== current.headSha) {
    discrepancies.push("headSha: receipt " + recordedSha + ", now " + currentSha);
  }
  const recordedHash = record.rulesHash || "(not recorded)";
  const currentHash = current.rulesHash || "(not recorded)";
  if (record.rulesHash !== current.rulesHash) {
    discrepancies.push("rulesHash: receipt " + recordedHash + ", now " + currentHash);
  }
  for (const recorded of record.commands) {
    const fresh = current.commands.find((entry) => entry.command === recorded.command);
    if (!fresh) {
      discrepancies.push(recorded.command + ": exit " + formatExit(recorded.exitCode) + " recorded, not run now");
      continue;
    }
    if (fresh.exitCode !== recorded.exitCode) {
      discrepancies.push(recorded.command + ": exit " + formatExit(recorded.exitCode) + " recorded, exit " + formatExit(fresh.exitCode) + " now");
    }
  }
  return discrepancies;
}

function formatExit(code: number | null): string {
  return code === null ? "(none)" : String(code);
}

export interface ReconciliationResult {
  ok: boolean;
  report: string;
  /** Steps of the fresh ladder (for a receipt/trail; empty when it never ran). */
  steps: GateStep[];
  /** The checkpoint receipt compared against (null when none exists yet). */
  receipt: ReceiptRecord | null;
}

/**
 * Reconciliation-lite: re-run the cheap gate ladder (`make audit/static` +
 * the scoped spec check via runScopedGate) and diff the fresh result against
 * the newest passing receipt of the day — HEAD sha, rules.yaml hash, and every
 * recorded command's exit code. Any difference is a mismatch, and a failing
 * fresh ladder is always a mismatch (fail closed on the ladder, fail open on
 * the receipt metadata).
 */
export async function runReconciliation(
  cwd: string,
  opts: {
    agent: string;
    session?: string;
    scopeEnv?: string;
    makeTimeoutMs?: number;
    specTimeoutMs?: number;
    /** Test seam: which receipt day to read (default: today). */
    day?: string;
    /** Test seam: skip the fresh ladder and use this run instead. */
    freshRun?: GateRun;
  },
): Promise<ReconciliationResult> {
  let fresh: GateRun;
  try {
    fresh =
      opts.freshRun ??
      (await runScopedGate({
        cwd,
        scopeEnv: opts.scopeEnv,
        makeTimeoutMs: opts.makeTimeoutMs,
        specTimeoutMs: opts.specTimeoutMs,
      }));
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    fresh = {
      ok: false,
      steps: [
        {
          command: "(reconciliation)",
          argv: [],
          ok: false,
          exitCode: null,
          timedOut: false,
          durationMs: 0,
          stdout: "",
          stderr: message,
          spawnError: null,
          reason: "reconciliation ladder threw",
        },
      ],
    };
  }

  const current: ReconciliationCurrent = {
    headSha: await headShaOf(cwd),
    rulesHash: await hashSpecRules(cwd),
    commands: receiptCommandsOf(fresh),
  };
  const recorded = await latestPassingReceipt(cwd, opts.day);
  const discrepancies = diffReceiptAgainst(recorded, current);
  const gateFailures = fresh.steps.filter((step) => !step.ok);

  const lines: string[] = ["\u2500\u2500 RECONCILIATION \u2500\u2500"];
  lines.push("agent: " + opts.agent + (opts.session ? " \u00b7 session: " + opts.session : ""));
  lines.push("receipt: " + (recorded ? receiptsFileFor(cwd, opts.day) + " (" + recorded.timestamp + ")" : "(no passing receipt today)"));
  for (const step of gateFailures) {
    lines.push(
      "- fresh ladder FAILED: " + step.command + " \u00b7 " +
        (step.exitCode === null ? "no exit code" : "exit " + String(step.exitCode)) + " \u00b7 " + step.reason,
    );
  }
  for (const discrepancy of discrepancies) lines.push("- MISMATCH " + discrepancy);
  const ok = fresh.ok && discrepancies.length === 0;
  lines.push(
    ok
      ? "status: ok \u2014 the ladder matches the recorded checkpoint."
      : "status: MISMATCH \u2014 do not settle on the recorded receipt; re-check the named fields above.",
  );
  return { ok, report: lines.join("\n"), steps: fresh.steps, receipt: recorded };
}

/**
 * Turn a failed reconciliation into a GateRun so the settle-gate report /
 * receipt / hard-stop trail path is shared instead of forked.
 */
export function reconciliationGateRun(result: ReconciliationResult): GateRun {
  if (result.ok) return { ok: true, steps: [] };
  const step: GateStep = {
    command: "(reconciliation)",
    argv: [],
    ok: false,
    exitCode: null,
    timedOut: false,
    durationMs: 0,
    stdout: "",
    stderr: result.report,
    spawnError: null,
    reason: "reconciliation mismatch",
    note: "fresh ladder vs recorded receipt",
  };
  return { ok: false, steps: result.steps.length > 0 ? [...result.steps.filter((entry) => !entry.ok), step] : [step] };
}

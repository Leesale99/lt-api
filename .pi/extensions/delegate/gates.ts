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
 *    `<cwd>/.pi/receipts/<ISO-date>.jsonl` (gitignored). Receipt metadata is
 *    fail-open (a missing rules.yaml or a broken git just records an empty
 *    field) — ONLY the receipt degrades, never the gate verdict.
 * 4. Gate-file protection is a DENY list with a single file-based escape
 *    hatch: `.pi/delegate/gate-allowlist.json` (JSON array of repo-root-
 *    relative globs). There are deliberately NO env-var bypasses. A missing,
 *    unreadable or invalid allowlist behaves exactly like an empty one (deny).
 *    The allowlist file itself is always denied, even if its own globs match
 *    it, so no worker can self-authorize.
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
 * Run the worker gate: `make audit/static` plus the scoped spec check.
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
  const steps: GateStep[] = [];
  const makeStep = await runStep(
    ["make", "audit/static"],
    opts.cwd,
    opts.makeTimeoutMs ?? DEFAULT_TIMEOUT_MS,
  );
  steps.push(makeStep);

  const scope = parseSpecScopeEnv(opts.scopeEnv);
  const specTimeout = opts.specTimeoutMs ?? SPEC_CHECK_TIMEOUT_MS;
  const base = ["go", "run", "./cmd/spec", "check", "--strict"];
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

export function receiptsDirFor(cwd: string): string {
  return path.join(cwd, ".pi", "receipts");
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

// ── Worker-settle gate attempts ─────────────────────────────────────────────

/**
 * Settle-gate attempts for this session (module state, session lifetime).
 * Reset by a PASSING settle gate; incremented by a failing one. 3 failures ⇒
 * hard stop (no more continue:true).
 */
export const SETTLE_GATE_MAX_ATTEMPTS = 3;
let settleGateAttempts = 0;

export function settleGateAttemptsFor(): number {
  return settleGateAttempts;
}

export function bumpSettleGateAttempts(): number {
  settleGateAttempts += 1;
  return settleGateAttempts;
}

export function resetSettleGateAttempts(): void {
  settleGateAttempts = 0;
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

/** Repo-root-relative path of the single gate-file escape hatch. */
export const GATE_ALLOWLIST_REL_PATH = ".pi/delegate/gate-allowlist.json";

/** Files a delegated worker may still edit: prose the orchestrator rewrites. */
const PI_EDITABLE_PATTERNS = [".pi/agents/**", ".pi/prompts/**", ".pi/delegate/**", ".pi/plans/**"];

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
export function readGateAllowlist(repoRoot: string): string[] {
  const file = path.join(repoRoot, GATE_ALLOWLIST_REL_PATH);
  let raw: string;
  try {
    raw = fs.readFileSync(file, "utf8");
  } catch {
    return [];
  }
  try {
    const parsed: unknown = JSON.parse(raw);
    if (!Array.isArray(parsed)) {
      console.error("[delegate] " + GATE_ALLOWLIST_REL_PATH + ": expected a JSON array of globs; ignoring (gate files stay denied)");
      return [];
    }
    return parsed.filter((entry): entry is string => typeof entry === "string" && entry.trim().length > 0);
  } catch (error) {
    console.error("[delegate] " + GATE_ALLOWLIST_REL_PATH + ": invalid JSON; ignoring (gate files stay denied):", error);
    return [];
  }
}

/**
 * Gate-file protection for child-session edits/writes: DENY by default for
 * docs/spec/rules.schema.md, cmd/spec/**, Makefile and .pi/** — except
 * .pi/agents|prompts|delegate|plans prose and any path matched by the
 * .pi/delegate/gate-allowlist.json escape hatch.
 */
export function evaluateGateFileWrite(repoRoot: string, inputPath: string): GatePathDecision {
  const rel = repoRelativePath(repoRoot, inputPath);
  if (!rel) return { blocked: false, rel: null, allowedBy: "outside-repo" };

  // Always denied, even when the allowlist matches it: a worker must not be
  // able to authorize itself (the file is the ONLY mechanism).
  if (rel === GATE_ALLOWLIST_REL_PATH) {
    return { blocked: true, rel, reason: gateFileReason(rel) };
  }

  const isGateFile =
    rel === "Makefile" || rel === "cmd/spec" || matchGlob("cmd/spec/**", rel) || matchGlob("docs/spec/**", rel) || matchGlob(".pi/**", rel);
  if (!isGateFile) return { blocked: false, rel, allowedBy: "not-gate-file" };

  if (PI_EDITABLE_PATTERNS.some((pattern) => matchGlob(pattern, rel))) {
    return { blocked: false, rel, allowedBy: "editable-pi-prose" };
  }

  const allowlist = readGateAllowlist(repoRoot);
  if (allowlist.some((pattern) => matchGlob(pattern, rel))) {
    return { blocked: false, rel, allowedBy: "allowlist" };
  }

  return { blocked: true, rel, reason: gateFileReason(rel) };
}

function gateFileReason(rel: string): string {
  return (
    "Gate file is protected from delegated edits: " + rel + ". " +
    "Protected: docs/spec/rules.schema.md, docs/spec/**, cmd/spec/**, Makefile, .pi/extensions/**, " +
    ".pi/delegate/gate-allowlist.json and every other .pi/ path that is not agents|prompts|delegate|plans prose. " +
    "Only a matching glob in .pi/delegate/gate-allowlist.json (repo-root-relative) allows this edit; " +
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

/**
 * Worker gates: fail-closed gate runner, receipts, the host-side integrity
 * check and content-bound approval helpers.
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
 *    `<host-root>/<receipts_dir>/<ISO-date>.jsonl` (default `.pi/receipts`,
 *    gitignored) where the host root is the HOST SESSION's own repo root —
 *    never a `cd` target named inside a command. A worker cannot write here:
 *    the receipts dir is gitignored and absent from a fresh worktree, and the
 *    extension instance inside a sandbox is inert (`isSandboxedChild`).
 *    Receipt metadata is fail-open (a missing docs/spec/rules catalog or a
 *    broken git just records an empty field) — ONLY the receipt degrades,
 *    never the verdict.
 * 4. THE WRITE BOUNDARY IS THE HYPERVISOR (sandbox era). A worker runs inside
 *    a per-task micro-VM whose only host surface is its mount (./sbx.ts
 *    chooses the mount per role): what it may write is decided by what is
 *    mounted, never by an in-process classifier. The net is
 *    `checkWorktreeIntegrity` — a host-side detector at the merge boundary, run
 *    BEFORE any gate command: the gate files ARE the definition of green, so a
 *    worker that touched them has invalidated its own verdict and the host
 *    refuses to run gates at all.
 * 5. THE POLICY IS DECLARED, NOT HARDCODED (batch 3). Gate commands, the
 *    receipt/claims dirs, the settle limits, the stall timeout and the
 *    concurrency cap come from the repo-root WORKFLOW.md front matter
 *    (policy.ts). A missing or invalid policy is a REFUSAL: no command runs
 *    until the file exists and parses.
 * 6. PROVE IS EVIDENCE, NOT A PROMISE (merge-back). A job whose SPEC-SCOPE
 *    (recorded at spawn in the claims log) names rules needs a VERIFIED prove
 *    receipt for EVERY named rule before the merge executes. The scope read and
 *    the whole prove-receipt policy (coverage check, `--rule` matching, the
 *    prove-receipt note test) live in ./mergegate.ts, which the merge execution
 *    path in index.ts and the approval relay in jobs.ts BOTH call — one
 *    resolver, one answer, so a relay approval and a merge block can never
 *    disagree about a job's scope: receipts written by the host extension on a
 *    scoped `make prove args='--rule <id>'` run. The scoped gate
 *    (`runScopedGate`) checks the spec statically; only prove says the behavior
 *    was actually exercised.
 * 7. IN-VM GATES ARE ADVISORY. `runInVmAdvisoryGate` is the only gate ladder a
 *    sandboxed worker may run for itself: the declared scoped spec check plus a
 *    direct `gofmt -l` over the changed Go files. It never runs the Makefile
 *    target (`make audit/static`'s gofmt step enumerates `git ls-files`, and the
 *    mount carries no usable `.git`) and it never writes a receipt.
 * 8. AUTHORITATIVE GATES RUN IN A GATE VM (gate-VM era). The declared gate
 *    commands still come from WORKFLOW.md verbatim, but the host does not
 *    spawn them: it spawns `sbx exec` into a second, locked-down VM created
 *    from `pi-gate:v1` (see ./sbx.ts `createGateSandbox`). The host keeps two
 *    jobs only — the pre-gate integrity check (`git`, a trusted host binary
 *    reading paths, never worktree code) and writing receipts. The gate VM's
 *    mount is the worktree (rw: gates legitimately regenerate artifacts) plus,
 *    for a linked worktree, the main repo root (ro) so git resolves in-VM.
 *
 * Task packets (SPEC-SCOPE / CLAIMS-PATHS) are parsed by ./protocol.ts, the one
 * owner of that grammar; this module only CONSUMES the normalized scope env
 * (`parseSpecScopeEnv`) when it builds a scoped check.
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
  DEFAULT_GATE_FILE_PATHS,
  DEFAULT_RECEIPTS_DIR,
  loadWorkflowPolicy,
  workflowPolicyRefusal,
} from "./policy.ts";
import { parseSpecScopeEnv } from "./protocol.ts";
import { workerStallTimeoutMs } from "./runtime.ts";
import {
  ensureGateSandbox,
  execInSandbox,
  execSandboxArgs,
  type SandboxExecResult,
} from "./sbx.ts";

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
  /** Name of the gate VM that executed the step (absent for a policy refusal). */
  gateSandbox?: string;
}

export interface GateRun {
  ok: boolean;
  steps: GateStep[];
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

// ── Gate VM: the authoritative execution plane ──────────────────────────────
//
// Every authoritative gate command (the declared `audit_static_cmd`,
// `scoped_check_cmd`, `audit_cmd`, `prove_cmd`) is handed VERBATIM to a
// locked-down gate VM: `sbx exec --no-input -w <worktree> <gate-vm> [-e …]
// <argv…>`. The host never spawns `make`/`go` for a gate again — it only runs
// `sbx`, the trusted CLI. `runGate` (a host spawn) therefore remains only for
// the two things that MUST stay host-side: the integrity check's `git` probe
// and the in-VM ADVISORY ladder (which already runs inside the worker VM).
//
// `sbx exec` has no timeout flag, so the host keeps the watchdog: the executor
// below passes the caller's timeout budget to the adapter, which SIGTERMs and
// then SIGKILLs the exec. A timed-out exec is a failed gate, never a hang.

/** Database the DB-backed gates use inside the gate VM (the repo's test DB). */
export const GATE_DB_NAME = "lt_api_test";
/** Role the throwaway in-VM DSN authenticates as (SUPERUSER in the image). */
export const GATE_DB_USER = "lt_test";
/** Throwaway in-VM password; plaintext on purpose — it protects nothing real. */
export const GATE_DB_PASSWORD = "test";
/**
 * How the DSN reaches the VM-local server. `127.0.0.1` (TCP) rather than the
 * image's unix socket: the suites connect over TCP anyway, and the pi-gate
 * image configures `listen_addresses = '127.0.0.1'` with `auth-host=trust`.
 */
export const GATE_DB_HOST = "127.0.0.1";
/**
 * 5433 on purpose: `internal/testdb.DefaultTestDSN` and the (gitignored)
 * `.envrc` both pin the test database to localhost:5433, and the Makefile
 * `-include .envrc` re-exports it — overriding any env-passed DSN. The gate
 * image's Postgres therefore LISTENS on 5433, so the repo's documented DSN is
 * literally true inside the gate VM.
 */
export const GATE_DB_PORT = 5433;
/**
 * The pi-gate image bakes an agent-owned cluster (PGDATA=/home/agent/pgdata)
 * because `sbx template save` snapshots the filesystem, not /var/lib/docker. It
 * starts it with the image's `pg-up` helper (Debian's `pg_ctlcluster`/
 * `service postgresql start` require root and the VM runs as `agent`), and its
 * unix socket lives here (image ENV `PGHOST`).
 */
export const GATE_DB_SOCKET_DIR = "/home/agent/pgsock";
/**
 * The DSN a DB-backed gate receives, mirroring the shape the Makefile and
 * internal/testdb expect (`<dsn-db>_<suite>` databases are created by the
 * suites themselves). It is delivered as a make COMMAND-LINE variable
 * (`make audit LT_API_TEST_DSN=…`), NOT via `-e`: the Makefile's
 * `-include .envrc` re-exports the repo DSN, and a makefile assignment beats
 * the process environment — only a command-line variable beats it, and fresh
 * worktrees (where the gitignored `.envrc` does not exist) still need one.
 */
export const GATE_DB_DSN =
  "postgres://" + GATE_DB_USER + ":" + GATE_DB_PASSWORD + "@" + GATE_DB_HOST + ":" +
  String(GATE_DB_PORT) + "/" + GATE_DB_NAME + "?sslmode=disable";
/** `pg_isready` attempts before the DB gate pre-sequence gives up. */
export const GATE_PG_READY_ATTEMPTS = 30;
/** Wait between `pg_isready` attempts (ms). */
export const GATE_PG_READY_INTERVAL_MS = 500;
/** Timeout for one DB pre-sequence command (start / drop / create). */
export const GATE_DB_STEP_TIMEOUT_MS = 60_000;

/**
 * Env for an `sbx exec` gate.
 *
 * `GIT_OPTIONAL_LOCKS=0` stops `git status` from taking the index lock in a
 * shared mount. The `safe.directory` config is load-bearing: the repo-root
 * mount is owned by another UID once it is read-only in the VM, and git refuses
 * to operate on a repo it considers foreign (dubious ownership) without it.
 * `GIT_CONFIG_COUNT`/`GIT_CONFIG_KEY_n` is the env-only way to set it, so no
 * in-VM git config has to be written.
 *
 * A DB-backed gate gets `LT_API_TEST_DSN` as a make command-line variable
 * (see GATE_DB_DSN) — never via this env, which `-include .envrc` would
 * override. Static gates get nothing DB-related at all.
 */
export function gateVmExecEnv(worktreePath: string, opts: { db?: boolean } = {}): Record<string, string> {
  const env: Record<string, string> = {
    GIT_OPTIONAL_LOCKS: "0",
    GIT_CONFIG_COUNT: "1",
    GIT_CONFIG_KEY_0: "safe.directory",
    GIT_CONFIG_VALUE_0: "*",
  };
  void opts;
  return env;
}

/** One command of the DB pre-sequence (argv + its exec env). */
export interface GateDbPrestep {
  argv: string[];
  env: Record<string, string>;
}

/**
 * The DB gate pre-sequence, in order: start Postgres → wait for readiness →
 * drop the test DB → create it. The gate command itself runs AFTER this.
 *
 * `pg-up` is the pi-gate image's own helper (baked cluster + `pg_isready`
 * health check); Debian's `service postgresql start`/`pg_ctlcluster` are NOT
 * used because the VM runs as `agent` and the image deliberately keeps no
 * root-owned distro cluster. Readiness is re-checked explicitly so a failed
 * start cannot fall through to the gate. The role (`lt_test`, SUPERUSER) and
 * the cluster are baked into the image; only the throwaway database is reset
 * per gate run.
 */
export function gateDbPresteps(): GateDbPrestep[] {
  const clientEnv = { PGPASSWORD: GATE_DB_PASSWORD };
  const endpoint = ["-h", GATE_DB_SOCKET_DIR, "-p", String(GATE_DB_PORT), "-U", GATE_DB_USER];
  return [
    { argv: ["pg-up"], env: {} },
    {
      argv: ["pg_isready", ...endpoint, "-d", GATE_DB_NAME],
      env: {},
    },
    { argv: ["dropdb", "--if-exists", ...endpoint, GATE_DB_NAME], env: clientEnv },
    { argv: ["createdb", ...endpoint, "-O", GATE_DB_USER, GATE_DB_NAME], env: clientEnv },
  ];
}

function sleepGap(ms: number): Promise<void> {
  return new Promise<void>((resolve) => setTimeout(resolve, ms));
}

/**
 * One `sbx exec` in the gate VM, mapped into the fail-closed GateResult shape.
 * A 125/127/timeout/spawn error is infrastructure (no verdict) and still a
 * FAILED gate: the caller never sees a silent pass.
 */
async function execGateVmCommand(
  sandbox: string,
  argv: readonly string[],
  opts: { cwd: string; timeoutMs: number; env: Record<string, string> },
): Promise<{ result: GateResult; sandbox: string }> {
  const startedAt = Date.now();
  let raw: SandboxExecResult;
  try {
    raw = await execInSandbox(sandbox, argv, {
      worktreePath: opts.cwd,
      env: opts.env,
      timeoutMs: opts.timeoutMs,
    });
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    return {
      sandbox,
      result: {
        ok: false,
        exitCode: null,
        stdout: "",
        stderr: message,
        timedOut: false,
        spawnError: message,
        durationMs: Date.now() - startedAt,
        reason: "gate VM exec threw: " + message,
      },
    };
  }

  let reason = "";
  if (!raw.ok) {
    if (raw.timedOut) reason = "timed out after " + String(opts.timeoutMs) + "ms";
    else if (raw.classification.infraFailure) reason = raw.classification.reason;
    else if (raw.exitCode === null) reason = "no exit code";
    else reason = "exited with code " + String(raw.exitCode);
  }
  return {
    sandbox,
    result: {
      ok: raw.ok,
      exitCode: raw.exitCode,
      stdout: raw.stdout,
      stderr: raw.stderr,
      timedOut: raw.timedOut,
      spawnError: raw.spawnError,
      durationMs: raw.durationMs,
      reason,
    },
  };
}

/**
 * Run the DB gate pre-sequence in the gate VM. Returns ok:true when Postgres is
 * ready and the test DB was recreated; ok:false carries a failed GateStep so
 * the gate report names exactly which step stopped the run. The pre-sequence is
 * NOT part of the receipt's command list on success (it produces no verdict);
 * a failure IS reported as a step.
 */
export async function prepareGateDbVm(
  worktreePath: string,
  opts: { timeoutMs?: number; readyAttempts?: number; intervalMs?: number } = {},
): Promise<{ ok: boolean; step: GateStep | null; sandbox: string }> {
  const sandbox = await ensureGateSandbox(worktreePath);
  const timeoutMs = opts.timeoutMs ?? GATE_DB_STEP_TIMEOUT_MS;
  const attempts = Math.max(1, opts.readyAttempts ?? GATE_PG_READY_ATTEMPTS);
  const intervalMs = Math.max(0, opts.intervalMs ?? GATE_PG_READY_INTERVAL_MS);
  const [start, ready, drop, create] = gateDbPresteps();

  const fail = (argv: string[], failure: GateResult): { ok: false; step: GateStep; sandbox: string } => ({
    ok: false,
    sandbox,
    step: stepOf(failure, argv, "gate VM db pre-sequence"),
  });

  const started = await execGateVmCommand(sandbox.name, start.argv, {
    cwd: worktreePath,
    timeoutMs,
    env: { ...gateVmExecEnv(worktreePath), ...start.env },
  });
  if (!started.result.ok) return fail(start.argv, started.result);

  let readyRun = await execGateVmCommand(sandbox.name, ready.argv, {
    cwd: worktreePath,
    timeoutMs,
    env: { ...gateVmExecEnv(worktreePath), ...ready.env },
  });
  for (let attempt = 1; !readyRun.result.ok && attempt < attempts; attempt++) {
    if (intervalMs > 0) await sleepGap(intervalMs);
    readyRun = await execGateVmCommand(sandbox.name, ready.argv, {
      cwd: worktreePath,
      timeoutMs,
      env: { ...gateVmExecEnv(worktreePath), ...ready.env },
    });
  }
  if (!readyRun.result.ok) return fail(ready.argv, readyRun.result);

  for (const step of [drop, create]) {
    const run = await execGateVmCommand(sandbox.name, step.argv, {
      cwd: worktreePath,
      timeoutMs,
      env: { ...gateVmExecEnv(worktreePath), ...step.env },
    });
    if (!run.result.ok) return fail(step.argv, run.result);
  }
  return { ok: true, step: null, sandbox };
}

/**
 * Run one gate command argv in the gate VM (lazily creating/reusing the VM for
 * this worktree path). DB-backed gates run the Postgres pre-sequence first.
 * This is the ONLY execution path for an authoritative gate command; the host
 * spawns `sbx`, not the command.
 */
export async function runStepInGateVm(
  argv: string[],
  cwd: string,
  timeoutMs: number,
  note?: string,
  opts: { db?: boolean } = {},
): Promise<GateStep> {
  if (opts.db) {
    const prep = await prepareGateDbVm(cwd);
    if (!prep.ok && prep.step) return prep.step;
  }
  const sandbox = await ensureGateSandbox(cwd);
  const run = await execGateVmCommand(sandbox.name, argv, {
    cwd,
    timeoutMs,
    env: gateVmExecEnv(cwd, { db: opts.db === true }),
  });
  const step = stepOf(run.result, argv, note);
  step.gateSandbox = sandbox.name;
  return step;
}

/**
 * Run one DECLARED gate command string (WORKFLOW.md value, verbatim argv) in
 * the gate VM. `db` selects the Postgres pre-sequence; `extraArgs` appends
 * tokens (`make prove` + `args=--rule R-X`).
 */
export async function runDeclaredGateStep(
  command: string,
  opts: { cwd: string; db?: boolean; timeoutMs?: number; extraArgs?: string[]; note?: string },
): Promise<GateStep> {
  const argv = [...splitCommandLine(command), ...(opts.extraArgs ?? [])];
  // The DB DSN rides the make command line — see appendGateDbOverride.
  const withDb = appendGateDbOverride(argv, opts.db === true);
  return await runStepInGateVm(
    withDb,
    opts.cwd,
    opts.timeoutMs ?? DEFAULT_TIMEOUT_MS,
    opts.note,
    { db: opts.db === true },
  );
}

/**
 * The full-audit gate (`audit_cmd`, DB-backed). Exported so any call site that
 * needs the merge-boundary audit gets the gate-VM plane and the DB sequence
 * without re-implementing either.
 */
export async function runAuditGate(opts: { cwd: string; timeoutMs?: number }): Promise<GateRun> {
  const loaded = loadWorkflowPolicy(opts.cwd);
  if (!loaded.ok) return policyRefusalGateRun(loaded.reason);
  const step = await runDeclaredGateStep(loaded.policy.auditCmd, {
    cwd: opts.cwd,
    db: true,
    // `make audit` runs the whole tree; the host watchdog is the declared
    // stall budget (worker_stall_timeout_ms) unless the caller tightens it.
    timeoutMs: opts.timeoutMs ?? workerStallTimeoutMs(process.env, opts.cwd),
    note: "full audit (DB-backed, gate VM)",
  });
  return { ok: step.ok, steps: [step] };
}

/**
 * The prove gate (`prove_cmd`, DB-backed). `args` is the declared `args=…`
 * tail the Makefile forwards (`--rule <id>` / `--base …`); omitted ⇒ `--all`
 * is the Makefile's own default.
 */
export async function runProveGate(opts: {
  cwd: string;
  args?: string;
  timeoutMs?: number;
}): Promise<GateRun> {
  const loaded = loadWorkflowPolicy(opts.cwd);
  if (!loaded.ok) return policyRefusalGateRun(loaded.reason);
  const extraArgs = opts.args && opts.args.trim() ? ["args=" + opts.args.trim()] : [];
  const step = await runDeclaredGateStep(loaded.policy.proveCmd, {
    cwd: opts.cwd,
    db: true,
    timeoutMs: opts.timeoutMs ?? workerStallTimeoutMs(process.env, opts.cwd),
    extraArgs,
    note: "prove (DB-backed, gate VM)",
  });
  return { ok: step.ok, steps: [step] };
}

/** Quote one token for a POSIX shell command line (bash tool re-dispatch). */
export function shellQuote(token: string): string {
  if (/^[A-Za-z0-9_@%+=:,./-]+$/.test(token)) return token;
  return "'" + token.replace(/'/g, "'\\''") + "'";
}

/**
 * The DB DSN as a make COMMAND-LINE variable, for both delivery paths
 * (`runDeclaredGateStep` and the bash-rewrite in index.ts). See GATE_DB_DSN
 * for why command-line and not env: the Makefile's `-include .envrc` re-exports
 * the repo DSN and a makefile assignment beats the process environment.
 */
export function appendGateDbOverride(argv: readonly string[], db: boolean): string[] {
  if (!db) return [...argv];
  if (!(argv[0] === "make" || argv[0] === "/usr/bin/make")) return [...argv];
  return [...argv, "LT_API_TEST_DSN=" + GATE_DB_DSN];
}

/**
 * The host shell line that runs one gate argv in the gate VM. Used when a host
 * session types a declared gate command into the bash tool: the tool_call hook
 * replaces that line with this `sbx exec` line, so the host executes the trusted
 * CLI only and the gate command itself runs in the VM. Every token is quoted,
 * so the in-VM argv is the declared argv verbatim — no shell interpretation.
 */
export function gateVmShellCommand(
  sandbox: string,
  worktreePath: string,
  env: Record<string, string>,
  argv: readonly string[],
): string {
  const sbxArgv = ["sbx", ...execSandboxArgs(sandbox, worktreePath, env, argv)];
  return sbxArgv.map(shellQuote).join(" ");
}

export interface ParsedGateVmShellCommand {
  sandbox: string;
  /** `-w` value: the worktree the gate ran against ("" when omitted). */
  worktree: string;
  env: Record<string, string>;
  /** The in-VM argv, verbatim. */
  argv: string[];
}

/**
 * Parse a rewritten `sbx exec --no-input -w <wt> [-e K=V]… <sandbox> <argv…>`
 * line back into its parts. Read-only helper for post-exec receipt writing: a
 * tool_result sees the REWRITTEN bash command, so the prove receipt path needs
 * the in-VM argv and the worktree to identify the run and its gate VM. Returns
 * null for anything that is not the gate-VM shape.
 */
export function parseGateVmShellCommand(command: string): ParsedGateVmShellCommand | null {
  const words = splitCommandLine(command);
  if (words[0] !== "sbx" || words[1] !== "exec") return null;
  let index = 2;
  let worktree = "";
  const env: Record<string, string> = {};
  while (index < words.length) {
    const token = words[index];
    if (token === "--no-input") {
      index += 1;
      continue;
    }
    if (token === "-w" && index + 1 < words.length) {
      worktree = words[index + 1];
      index += 2;
      continue;
    }
    if (token === "-e" && index + 1 < words.length) {
      const pair = words[index + 1];
      const split = pair.indexOf("=");
      if (split > 0) env[pair.slice(0, split)] = pair.slice(split + 1);
      index += 2;
      continue;
    }
    break;
  }
  const sandbox = words[index];
  if (!sandbox || sandbox.startsWith("-")) return null;
  return { sandbox, worktree, env, argv: words.slice(index + 1) };
}

/** A host-shell command recognised as a declared gate run. */
export interface ResolvedDeclaredGate {
  kind: "audit_static" | "audit" | "prove" | "scoped_check";
  /** true ⇒ DB-backed: run the Postgres pre-sequence and export the DSN. */
  db: boolean;
  /** Absolute worktree the gate must run against (a leading `cd` wins). */
  cwd: string;
  /** The declared command argv, verbatim (never re-quoted through a shell). */
  argv: string[];
  /** Original gate segment text (for reports/debug). */
  segment: string;
}

function startsWithTokens(words: string[], base: string[]): boolean {
  if (base.length === 0 || words.length < base.length) return false;
  for (let index = 0; index < base.length; index++) {
    if (words[index] !== base[index]) return false;
  }
  return true;
}

/**
 * Recognise a host-shell command that IS a declared gate command, so it can be
 * re-dispatched into the gate VM instead of executing in the host shell.
 *
 * Deliberately strict: exactly one gate segment, optionally preceded by one
 * `cd <dir>`. Anything else (pipes, redirects, extra commands) is NOT
 * rewritten — a compound command is not the declared gate invocation, and
 * guessing at intent is how a "gate" runs half on the host. `audit`/
 * `audit_static` must match the declared argv exactly; `prove` may carry the
 * Makefile `args=…` tail; `scoped_check` may carry `--module`/`--rule` flags.
 */
export function resolveDeclaredGateCommand(
  command: string,
  sessionCwd: string,
): ResolvedDeclaredGate | null {
  const loaded = loadWorkflowPolicy(sessionCwd);
  if (!loaded.ok) return null;
  const segments = splitTopLevelSegments(command)
    .map((segment) => segment.trim())
    .filter((segment) => segment.length > 0);
  if (segments.length === 0) return null;

  let cwd = sessionCwd;
  let gateSegment = segments[0];
  if (segments.length === 2) {
    const cdWords = splitCommandLine(segments[0]);
    if (cdWords.length === 2 && cdWords[0] === "cd") {
      cwd = path.resolve(sessionCwd, cdWords[1]);
      gateSegment = segments[1];
    } else {
      return null;
    }
  } else if (segments.length !== 1) {
    return null;
  }

  const words = splitCommandLine(gateSegment);
  const policy = loaded.policy;
  const exact = (declared: string): boolean => {
    const base = splitCommandLine(declared);
    return base.length > 0 && words.length === base.length && startsWithTokens(words, base);
  };
  if (exact(policy.auditCmd)) {
    return { kind: "audit", db: true, cwd, argv: words, segment: gateSegment };
  }
  if (exact(policy.auditStaticCmd)) {
    return { kind: "audit_static", db: false, cwd, argv: words, segment: gateSegment };
  }
  const proveBase = splitCommandLine(policy.proveCmd);
  if (startsWithTokens(words, proveBase)) {
    const extras = words.slice(proveBase.length);
    if (extras.every((token) => token.startsWith("args="))) {
      return { kind: "prove", db: true, cwd, argv: words, segment: gateSegment };
    }
  }
  const scopedBase = splitCommandLine(policy.scopedCheckCmd);
  if (startsWithTokens(words, scopedBase)) {
    const extras = words.slice(scopedBase.length);
    // `--module game` / `--rule R-1` are flag + value pairs: the first extra
    // must be a long flag, the rest ride along. A shell metacharacter or a
    // second command word is not part of a scoped check.
    if (extras.length === 0 || extras[0].startsWith("--")) {
      return { kind: "scoped_check", db: false, cwd, argv: words, segment: gateSegment };
    }
  }
  return null;
}

// ── Scoped-check argv (normalized scope env → scoped check) ─────────────────

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
 * EXECUTION PLANE: both commands run in the session's gate VM (see
 * `runStepInGateVm`), not on the host. The argv still comes from WORKFLOW.md
 * verbatim; only the executor changed.
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
  const makeStep = await runStepInGateVm(
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
    steps.push(await runStepInGateVm(base, opts.cwd, specTimeout, "full (no SPEC-SCOPE)"));
    return { ok: steps.every((step) => step.ok), steps };
  }

  let scopedFailed = false;
  for (const argv of scopedArgv) {
    const step = await runStepInGateVm(argv, opts.cwd, specTimeout, "scoped");
    steps.push(step);
    if (!step.ok) scopedFailed = true;
  }
  if (scopedFailed) {
    // Fail-closed fallback: a scoped failure (including "unknown flag") must
    // never be reported as a pass; the full check decides.
    steps.push(await runStepInGateVm(base, opts.cwd, specTimeout, "full fallback (scoped check failed)"));
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
  /** Gate VM that executed the commands, when they ran in one (additive). */
  sandbox?: string;
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
 * Metadata is fail-open: catalogHash is "" when the docs/spec/rules catalog is
 * missing, headSha is "" when git fails. The gate commands themselves stay
 * fail-closed.
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
    // Catalog schema v2 (per-rule-file catalog): `schema_version` lets a reader
    // tell a v1 receipt (rules.yaml hash) from a v2 one and supersede it.
    schema_version: RECEIPT_SCHEMA_VERSION,
    catalogHash: await catalogHash(input.cwd),
    headSha: await headShaOf(input.cwd),
    ok: input.ok === true,
    note: input.note ?? "",
    // Additive and optional: the gate VM's name when the commands ran there.
    // Every existing field keeps its meaning; a receipt without this key is
    // still a complete receipt (the host plane is the default).
    ...(input.sandbox ? { sandbox: input.sandbox } : {}),
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

/** Current receipt schema: 2 = per-rule-file catalog manifest hash. */
export const RECEIPT_SCHEMA_VERSION = 2;

/**
 * MANIFEST sha256 over the rule catalog `docs/spec/rules/**`; "" when the
 * directory is missing/unreadable (fail-open, receipt only — never stricter
 * than the old single-file hash it replaces).
 *
 * Recipe (deterministic across platforms and filesystems):
 *   1. Walk `docs/spec/rules` recursively from `cwd`. Skip dot-entries only
 *      (a path segment whose name starts with "."); every other file counts,
 *      including `README.md` and non-`.md` files.
 *   2. For every file, form the pair (relpath, content) where relpath is
 *      relative to `cwd` and slash-normalized (`path.sep` → "/"). File bytes
 *      are used verbatim (no newline translation, no re-encoding).
 *   3. Sort the pairs by relpath (plain code-unit order).
 *   4. Feed ONE running sha256 with, per entry in that order, the UTF-8 bytes
 *      of  relpath + "\0" + content + "\0"  and return the lowercase hex
 *      digest. The NUL separators make the framing unambiguous.
 *
 * Any walk/read error yields "": a partly-read catalog must not masquerade as
 * a valid checkpoint hash.
 */
export async function catalogHash(cwd: string): Promise<string> {
  const pairs: Array<{ rel: string; content: Buffer }> = [];
  const walk = async (dir: string): Promise<void> => {
    const dirents = await fs.promises.readdir(dir, { withFileTypes: true });
    for (const dirent of dirents) {
      if (dirent.name.startsWith(".")) continue;
      const abs = path.join(dir, dirent.name);
      if (dirent.isDirectory()) {
        await walk(abs);
        continue;
      }
      pairs.push({
        rel: path.relative(cwd, abs).split(path.sep).join("/"),
        content: await fs.promises.readFile(abs),
      });
    }
  };
  try {
    await walk(path.join(cwd, "docs", "spec", "rules"));
  } catch {
    return "";
  }
  pairs.sort((a, b) => (a.rel < b.rel ? -1 : a.rel > b.rel ? 1 : 0));
  const hash = createHash("sha256");
  for (const pair of pairs) {
    hash.update(pair.rel, "utf8");
    hash.update("\0", "utf8");
    hash.update(pair.content);
    hash.update("\0", "utf8");
  }
  return hash.digest("hex");
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

/**
 * Repo-root-relative posix path, or null when the path escapes the repo.
 */
export function repoRelativePath(repoRoot: string, inputPath: string): string | null {
  const absolute = path.resolve(repoRoot, inputPath);
  const relative = toPosix(path.relative(repoRoot, absolute));
  if (!relative || relative === ".") return null;
  if (relative === ".." || relative.startsWith("../") || path.isAbsolute(relative)) return null;
  return relative;
}

/**
 * Tiny glob matcher: `**` spans path segments (including none), `*` and `?`
 * stay inside one segment. Everything else is literal (regex metacharacters
 * escaped). Patterns and paths are repo-root-relative posix strings.
 *
 * Used by the claims log for owned-path overlap detection.
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

// ── Host-side integrity check (the merge-boundary detector) ──

/**
 * The paths the integrity check covers: the `gate_file_paths` DECLARED in the
 * worktree's WORKFLOW.md front matter, or `DEFAULT_GATE_FILE_PATHS` when the
 * field is absent. An unreadable policy also yields the defaults — the gate
 * path refuses on the policy itself in that case, and the check must keep
 * covering the canonical gate files rather than trust a broken policy file.
 */
export function gateFilePathsFor(worktreePath: string): string[] {
  const loaded = loadWorkflowPolicy(worktreePath);
  if (!loaded.ok) return [...DEFAULT_GATE_FILE_PATHS];
  return [...loaded.policy.gateFilePaths];
}

/** Exit code `git diff --exit-code` uses for "there is a diff". */
export const GIT_DIFF_DIRTY_EXIT = 1;

export interface IntegrityCheckResult {
  /** true ⇒ HEAD's gate files are exactly what is on disk (worktree + index). */
  clean: boolean;
  /** true ⇒ gates MUST NOT run (dirty, or unverifiable). */
  refused: boolean;
  /** One human line per command (status + what it means). */
  report: string;
  /** The executed checks, so a receipt can record them verbatim. */
  steps: GateStep[];
  /** Paths covered by the check (the declared `gate_file_paths`). */
  paths: string[];
}

/**
 * HOST-SIDE integrity check, run in the worktree BEFORE any gate command.
 *
 *   git diff --exit-code HEAD -- <gate_file_paths…>
 *   git diff --cached --exit-code HEAD -- <gate_file_paths…>
 *
 * The paths are the ones the policy DECLARES (`gate_file_paths` in WORKFLOW.md),
 * with the four canonical gate files (Makefile, cmd/spec, .github, WORKFLOW.md)
 * as the default when the field is absent: a worker that changes any of them has
 * voided its own verdict, because the host must never execute a Makefile,
 * cmd/spec binary, CI workflow or WORKFLOW.md that differs from HEAD.
 *
 * FAIL CLOSED: exit 1 = dirty (refuse the gates and route to human review).
 * Anything else that is not a clean exit 0 — a missing git, a broken HEAD, a
 * timeout — is "cannot verify", which is also a refusal: the point of the check
 * is that the host never runs an unverified gate file.
 */
export async function checkWorktreeIntegrity(
  worktreePath: string,
  opts: { timeoutMs?: number } = {},
): Promise<IntegrityCheckResult> {
  const paths = gateFilePathsFor(worktreePath);
  const timeoutMs = opts.timeoutMs ?? GIT_TIMEOUT_MS;
  const commands: string[][] = [
    ["diff", "--exit-code", "HEAD", "--", ...paths],
    ["diff", "--cached", "--exit-code", "HEAD", "--", ...paths],
  ];
  const steps: GateStep[] = [];
  const lines: string[] = ["\u2500\u2500 INTEGRITY CHECK (host, pre-gate) \u2500\u2500"];
  lines.push("paths: " + paths.join(" "));
  let dirty = false;
  let unverifiable = false;

  for (const args of commands) {
    const result = await runGate("git", args, { cwd: worktreePath, timeoutMs });
    const argv = ["git", ...args];
    const step = stepOf(result, argv, result.exitCode === 0 ? "clean" : "dirty/unverifiable");
    steps.push(step);
    if (result.ok) {
      lines.push("- " + argv.join(" ") + " \u2014 clean");
      continue;
    }
    if (result.exitCode === GIT_DIFF_DIRTY_EXIT && !result.timedOut && !result.spawnError) {
      dirty = true;
      lines.push("- " + argv.join(" ") + " \u2014 DIRTY (gate files differ from HEAD)");
      continue;
    }
    unverifiable = true;
    lines.push(
      "- " + argv.join(" ") + " \u2014 CANNOT VERIFY (" + result.reason + ")",
    );
  }

  const clean = !dirty && !unverifiable;
  if (clean) {
    lines.push("status: clean \u2014 gate files match HEAD; authoritative gates may run.");
  } else {
    lines.push(
      "status: REFUSED \u2014 " +
        (dirty
          ? "the worktree/index gate files differ from HEAD, so running them would grade the worker with the worker's own definition of green"
          : "the integrity of the gate files could not be verified") +
        ". No gate command was run; this batch routes to human review (HANDOFF_BLOCKED).",
    );
  }
  return { clean, refused: !clean, report: lines.join("\n"), steps, paths };
}

/**
 * The loud, routing reason a dirty integrity check produces. Kept as a function
 * so the refusal text cannot drift between the host gate path and a receipt.
 */
export function integrityRefusalReason(result: IntegrityCheckResult): string {
  const detail = result.clean
    ? ""
    : tailLines(
      result.steps.filter((step) => !step.ok).map((step) => step.command + ": " + step.reason).join("\n"),
      8,
    );
  return (
    "INTEGRITY CHECK FAILED \u2014 gates REFUSED, batch routes to human review (HANDOFF_BLOCKED). " +
    "The worktree's " + result.paths.join(", ") +
    " do not match HEAD; the gate files are the definition of green, so a batch that touched them " +
    "cannot be graded by them. No gate command was run and no verdict was produced. " + detail
  );
}

// ── Merge-path PROVE enforcement helpers ────────────────────────────────────

export interface MergeBackCommand {
  /** The shell segment the merge was found in (trimmed, verbatim). */
  segment: string;
  /** The merge target token (e.g. `delegate/<job-id>` or a base branch). */
  target: string;
  /** `-C <path>` argument when the command carried one, else null. */
  worktree: string | null;
  /** true for `--abort` / `--continue` / `--quit` (never blocked). */
  controlOnly: boolean;
}

/** Quote-aware split of a bash command on `&&`, `||`, `;`, `|` and newlines. */
export function splitTopLevelSegments(command: string): string[] {
  const segments: string[] = [];
  let current = "";
  let quote: string | null = null;
  for (let index = 0; index < command.length; index++) {
    const char = command[index];
    if (quote) {
      current += char;
      if (char === "\\" && quote === '"' && index + 1 < command.length) {
        current += command[++index];
        continue;
      }
      if (char === quote) quote = null;
      continue;
    }
    if (char === '"' || char === "'") {
      quote = char;
      current += char;
      continue;
    }
    if (char === "\\" && index + 1 < command.length) {
      current += char + command[++index];
      continue;
    }
    const two = command.slice(index, index + 2);
    if (two === "&&" || two === "||") {
      segments.push(current);
      current = "";
      index++;
      continue;
    }
    if (char === ";" || char === "|" || char === "\n") {
      segments.push(current);
      current = "";
      continue;
    }
    current += char;
  }
  segments.push(current);
  return segments.map((segment) => segment.trim()).filter((segment) => segment.length > 0);
}

/**
 * Detect merge-back executions in a bash command: `git merge …` in any of its
 * spellings, including the `git -C <worktree> merge` form the orchestrator
 * protocol uses. Detection is on the EXECUTION PATH, not on the approval path,
 * so a typed-in-pane `approved` cannot bypass the PROVE precondition.
 *
 * `--abort` / `--continue` / `--quit` are reported as `controlOnly` (they end or
 * resume a merge, they do not merge) and are never blocked. A bare `git merge`
 * with no target token is not a merge either.
 */
export function detectMergeBackCommands(command: string): MergeBackCommand[] {
  const found: MergeBackCommand[] = [];
  for (const segment of splitTopLevelSegments(command)) {
    const words = splitCommandLine(segment);
    let index = 0;
    // Tolerate a leading `cd <dir>` (the orchestrator's handoff form).
    if (words[index] === "cd" && words[index + 1] !== undefined) index += 2;
    if (words[index] !== "git") continue;
    index += 1;
    let worktree: string | null = null;
    if (words[index] === "-C" && words[index + 1] !== undefined) {
      worktree = words[index + 1];
      index += 2;
    }
    if (words[index] !== "merge") continue;
    index += 1;
    const rest = words.slice(index);
    const control = rest.find((word) => word === "--abort" || word === "--continue" || word === "--quit");
    if (control) {
      found.push({ segment, target: control, worktree, controlOnly: true });
      continue;
    }
    const target = rest.find((word) => !word.startsWith("-"));
    if (!target) continue;
    found.push({ segment, target, worktree, controlOnly: false });
  }
  return found;
}

// ── In-VM advisory ladder (sandboxed workers only) ──────────────────────────

export interface AdvisoryGateOptions {
  cwd: string;
  scopeEnv?: string;
  /** Paths the worker just wrote (used for the direct `gofmt -l` check). */
  changedPaths?: string[];
  specTimeoutMs?: number;
}

/**
 * The IN-VM advisory ladder. Deliberately narrow:
 *
 *   - the declared scoped spec check (`go run ./cmd/spec check --strict`, plus
 *     `--module`/`--rule`), which needs no `.git` and therefore runs fine in a
 *     mount where `.git` is unresolvable;
 *   - a DIRECT `gofmt -l` over the changed Go files — never `make audit/static`,
 *     whose gofmt step enumerates `git ls-files` and fails in the VM for an
 *     environment reason rather than a code reason.
 *
 * Nothing here writes a receipt or produces a verdict: the host re-runs the
 * authoritative gates after the sandbox exits, and its verdict decides. A
 * missing policy in the VM is reported as an advisory note, not a failure loop.
 */
export async function runInVmAdvisoryGate(opts: AdvisoryGateOptions): Promise<GateRun> {
  const loaded = loadWorkflowPolicy(opts.cwd);
  if (!loaded.ok) {
    return policyRefusalGateRun(loaded.reason);
  }
  const steps: GateStep[] = [];
  const specTimeout = opts.specTimeoutMs ?? SPEC_CHECK_TIMEOUT_MS;
  const base = splitCommandLine(loaded.policy.scopedCheckCmd);
  const scope = parseSpecScopeEnv(opts.scopeEnv);
  const scopedArgv: string[][] = [];
  if (scope?.module) scopedArgv.push([...base, "--module", scope.module]);
  for (const rule of scope?.rules ?? []) scopedArgv.push([...base, "--rule", rule]);
  if (scopedArgv.length === 0) {
    steps.push(await runStep(base, opts.cwd, specTimeout, "advisory full check"));
  } else {
    for (const argv of scopedArgv) steps.push(await runStep(argv, opts.cwd, specTimeout, "advisory scoped check"));
  }

  const goFiles = (opts.changedPaths ?? [])
    .filter((entry) => typeof entry === "string" && entry.endsWith(".go"))
    .map((entry) => entry.trim())
    .filter((entry) => entry.length > 0);
  if (goFiles.length > 0) {
    const argv = ["gofmt", "-l", ...goFiles];
    const result = await runGate("gofmt", ["-l", ...goFiles], { cwd: opts.cwd, timeoutMs: 60_000 });
    const unformatted = result.stdout.trim();
    const step = stepOf(result, argv, "advisory gofmt -l (no git needed)");
    if (result.ok && unformatted !== "") {
      step.ok = false;
      step.reason = "needs gofmt: " + unformatted.split("\n").join(", ");
    }
    steps.push(step);
  }

  return { ok: steps.every((step) => step.ok), steps };
}

// ── Reconciliation-lite ─────────────────────────────────────────────────────

/** Shared receipt shape (what writeReceipt appends). */
export interface ReceiptRecord {
  timestamp: string;
  agent: string;
  session: string;
  commands: ReceiptCommand[];
  /** Receipt schema; 2 = catalogHash is the docs/spec/rules manifest hash. */
  schema_version: number;
  catalogHash: string;
  headSha: string;
  ok: boolean;
  note: string;
  /** Gate VM name when the commands ran there (optional, additive). */
  sandbox?: string;
}

/** `<cwd>/.pi/receipts/<YYYY-MM-DD>.jsonl` — the day's receipt log. */
export function receiptsFileFor(cwd: string, day: string = new Date().toISOString().slice(0, 10)): string {
  return path.join(receiptsDirFor(cwd), day + ".jsonl");
}

/**
 * Parse one day's JSONL text into records, oldest first. Malformed lines are
 * skipped: the receipt log is telemetry and must never throw. Shared by the
 * per-day reader here and ./mergegate.ts's all-days prove reader, so both stay
 * lenient in exactly the same way (exported for that second reader only).
 */
export function parseReceiptLines(raw: string): ReceiptRecord[] {
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
        // Absent = v1 (the deleted docs/spec/rules.yaml hash); keep it as 0 so
        // diffReceiptAgainst can supersede it instead of comparing hashes.
        schema_version: typeof parsed.schema_version === "number" ? parsed.schema_version : 0,
        catalogHash: typeof parsed.catalogHash === "string" ? parsed.catalogHash : "",
        headSha: typeof parsed.headSha === "string" ? parsed.headSha : "",
        ok: parsed.ok === true,
        note: typeof parsed.note === "string" ? parsed.note : "",
        ...(typeof (parsed as { sandbox?: unknown }).sandbox === "string"
          ? { sandbox: (parsed as { sandbox: string }).sandbox }
          : {}),
      });
    } catch {
      /* unreadable receipt line — skip */
    }
  }
  return records;
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
  return parseReceiptLines(raw);
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
  catalogHash: string;
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
  // Catalog schema v2 (one authored file per rule under docs/spec/rules/): a v2
  // receipt's catalogHash is the manifest hash of that tree, so it compares
  // directly. A v1 receipt (no schema_version, or < 2) hashed the DELETED
  // docs/spec/rules.yaml — comparing it would be a permanent false mismatch, so
  // it is SUPERSEDED: headSha and exit codes still count, the hash is reported
  // once as not compared. That one-time note is accepted; do NOT loosen the v2
  // comparison to silence it.
  if (!(typeof record.schema_version === "number" && record.schema_version >= 2)) {
    discrepancies.push("catalogHash: receipt predates catalog schema v2 \u2014 superseded, not compared");
  } else {
    const recordedHash = record.catalogHash || "(not recorded)";
    const currentHash = current.catalogHash || "(not recorded)";
    if (record.catalogHash !== current.catalogHash) {
      discrepancies.push("catalogHash: receipt " + recordedHash + ", now " + currentHash);
    }
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
 * the newest passing receipt of the day — HEAD sha, the docs/spec/rules
 * catalog manifest hash, and every recorded command's exit code. Any difference
 * is a mismatch, and a failing
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
    catalogHash: await catalogHash(cwd),
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

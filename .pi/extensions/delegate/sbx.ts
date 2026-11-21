/**
 * Docker Sandboxes (`sbx`) adapter — the ONLY module in this extension that
 * invokes the `sbx` CLI.
 *
 * Why a dedicated adapter: worker isolation is now a hypervisor property, not a
 * path classifier inside the agent. Every delegated worker runs fully inside a
 * per-task Docker Sandbox micro-VM; the host (main session + orchestrator
 * sessions) is the trusted verifier plane and the only place a verdict is
 * produced. This module owns the whole VM lifecycle (create → network policy →
 * exec → stop → rm) so the rest of the extension never learns a CLI shape.
 *
 * VERSION-PIN QUARANTINE. `sbx` shipped breaking CLI changes in 0.48, 0.53 and
 * 0.54; the version this module was written against is `SBX_CLI_VERSION` and
 * nailing it here (rather than sprinkling argv strings through index.ts) is the
 * whole point of the indirection. The surface is deliberately thin and shaped
 * like a processes/files interface (`SbxProcessOps`) so a future `sbx` SDK can
 * replace `defaultProcessOps` without touching call sites.
 *
 * ONE INJECTION POINT. Every CLI invocation goes through `runSbx`, which
 * delegates to the active `SbxProcessOps`. Tests stub `setSbxProcessOps` and
 * assert on argv without a daemon, a VM, or a subnet.
 *
 * FAIL CLOSED. A create/exec that cannot run is an INFRA-FAILURE result — never
 * a silent fallback to a host-side spawn, which would hand a worker host reach
 * behind the profile's back.
 *
 * Loader gotcha (pi 1.5.2 extension parser): no `?:` ternaries with
 * double-quoted strings inside a template literal's `${}`; this module sticks
 * to concatenation throughout.
 */

import { spawn } from "node:child_process";
import * as fs from "node:fs";
import * as path from "node:path";

// ── Version pin + binary ────────────────────────────────────────────────────

/** The `sbx` CLI version this adapter's argv shapes were written against. */
export const SBX_CLI_VERSION = "0.54.0";
/** CLI binary name (PATH-resolved; tests never spawn it). */
export const SBX_BINARY = "sbx";

// ── Sandbox image / sizing constants ────────────────────────────────────────

/**
 * Saved sandbox template the worker VMs are created from. Produced by a
 * parallel kit/template step; it may not exist yet on a fresh host, which is
 * exactly why the create path has the `SANDBOX_KIT_FALLBACK_PATH` fallback and
 * records which one produced the VM (`createdFrom`).
 */
export const SANDBOX_TEMPLATE_NAME = "pi-worker";
/**
 * Fallback create-from-kit reference, relative to the launching repo root
 * (resolved by `createWorkerSandbox`). Used only when the saved template is
 * unavailable. An absent kit path is a loud infra-failure, never a silent
 * agent-image fallback.
 */
export const SANDBOX_KIT_FALLBACK_PATH = ".pi/sandbox/pi-worker-kit";
/** CPU allocation per VM. `sbx create` defaults to ALL host CPUs — never default. */
export const SANDBOX_DEFAULT_CPUS = 4;
/** Memory per VM (binary units). `sbx create` defaults to 50% of host RAM. */
export const SANDBOX_DEFAULT_MEMORY = "8g";
/** Agent positional used for every VM: the image comes from the template/kit. */
export const SANDBOX_CREATE_AGENT = "shell";

/** Egress allowlist for roles with a read-write worktree mount (default-deny). */
export const SANDBOX_WORKER_EGRESS_HOSTS: readonly string[] = [
  "opencode.ai:443",
  "proxy.golang.org:443",
  "sum.golang.org:443",
  "storage.googleapis.com:443",
];

/** The librarian's read-write vault. Not a git repo: no integrity check covers it. */
export const SANDBOX_VAULT_PATH = "/Users/aleksrdvn/Projects/vaults/lt-api";

/**
 * Env-var NAMES whose values the `sbx` proxy injects through the custom-secret
 * sentinel mechanism (stored host-side with `sbx secret set-custom`). The
 * adapter never forwards the real value: a compose `-e OPENCODE_GO_API_KEY`
 * with a real key would put the credential in the VM and in the host process
 * table. Documented here so the profile table can name its secret surface.
 */
export const SANDBOX_SECRET_SENTINEL_ENVS: readonly string[] = ["OPENCODE_GO_API_KEY"];

/** Exit code: `sbx` could not run the command (missing sandbox, not runnable). */
export const SBX_EXIT_COULD_NOT_RUN = 125;
/** Exit code: the command itself was not found inside the sandbox. */
export const SBX_EXIT_MISSING_EXECUTABLE = 127;

/** Default timeouts for the lifecycle verbs (ms). `exec` has none of its own. */
export const SBX_CREATE_TIMEOUT_MS = 300_000;
export const SBX_POLICY_TIMEOUT_MS = 60_000;
export const SBX_STOP_TIMEOUT_MS = 60_000;
export const SBX_RM_TIMEOUT_MS = 60_000;

// ── Processes/files interface (the future-SDK seam) ─────────────────────────

export interface SbxProcessOptions {
  cwd?: string;
  /** Hard timeout in ms; 0/undefined = no adapter-level timeout. */
  timeoutMs?: number;
  /** Abort signal: SIGTERM, then SIGKILL after `killGraceMs`. */
  signal?: AbortSignal;
  /** Grace period between SIGTERM and SIGKILL on abort/timeout. */
  killGraceMs?: number;
  /** Streaming hooks (the exec stdout is a JSONL event stream). */
  onStdout?: (chunk: string) => void;
  onStderr?: (chunk: string) => void;
}

export interface SbxProcessResult {
  /** argv[0..] actually handed to the CLI (without the binary). */
  args: string[];
  /** true ONLY for exit code 0. */
  ok: boolean;
  exitCode: number | null;
  stdout: string;
  stderr: string;
  timedOut: boolean;
  /** Spawn failure message (ENOENT, EACCES, …) or null. */
  spawnError: string | null;
  durationMs: number;
  /** The run was interrupted by its abort signal. */
  aborted: boolean;
}

/** The thin processes/files interface. A future `sbx` SDK implements this. */
export interface SbxProcessOps {
  run(args: string[], opts?: SbxProcessOptions): Promise<SbxProcessResult>;
  /** Does this host path exist? (kit-fallback probing; tests stub it). */
  exists(target: string): boolean;
}

function outputCap(text: string): string {
  return text.length > 64 * 1024 ? text.slice(-64 * 1024) : text;
}

const defaultProcessOps: SbxProcessOps = {
  run(args, opts = {}) {
    const startedAt = Date.now();
    return new Promise<SbxProcessResult>((resolve) => {
      let settled = false;
      let stdout = "";
      let stderr = "";
      let timedOut = false;
      let aborted = false;
      let killTimer: ReturnType<typeof setTimeout> | undefined;
      let timer: ReturnType<typeof setTimeout> | undefined;
      let child: ReturnType<typeof spawn> | undefined;

      const finish = (patch: {
        exitCode?: number | null;
        spawnError?: string | null;
        ok?: boolean;
      }): void => {
        if (settled) return;
        settled = true;
        if (timer) clearTimeout(timer);
        if (killTimer) clearTimeout(killTimer);
        child?.stdout?.removeAllListeners();
        child?.stderr?.removeAllListeners();
        child?.removeAllListeners();
        if (opts.signal) opts.signal.removeEventListener("abort", onAbort);
        resolve({
          args,
          ok: patch.ok === true,
          exitCode: patch.exitCode === undefined ? null : patch.exitCode,
          stdout,
          stderr,
          timedOut,
          spawnError: patch.spawnError === undefined ? null : patch.spawnError,
          durationMs: Date.now() - startedAt,
          aborted,
        });
      };

      const terminate = (): void => {
        if (!child || child.exitCode !== null || child.signalCode !== null) return;
        try {
          child.kill("SIGTERM");
        } catch {
          /* already gone */
        }
        killTimer = setTimeout(() => {
          try {
            child?.kill("SIGKILL");
          } catch {
            /* already gone */
          }
        }, opts.killGraceMs && opts.killGraceMs > 0 ? opts.killGraceMs : 5_000);
      };

      function onAbort(): void {
        aborted = true;
        terminate();
      }

      try {
        child = spawn(SBX_BINARY, args, {
          cwd: opts.cwd,
          shell: false,
          stdio: ["ignore", "pipe", "pipe"],
          env: process.env,
        });
      } catch (error) {
        finish({
          spawnError: error instanceof Error ? error.message : String(error),
          ok: false,
        });
        return;
      }

      if (opts.timeoutMs && opts.timeoutMs > 0) {
        timer = setTimeout(() => {
          timedOut = true;
          terminate();
          // Do not wait for a close event that may never come.
          finish({ exitCode: null, ok: false });
        }, opts.timeoutMs);
      }

      child.stdout?.on("data", (data: Buffer | string) => {
        const text = data.toString();
        stdout = outputCap(stdout + text);
        opts.onStdout?.(text);
      });
      child.stderr?.on("data", (data: Buffer | string) => {
        const text = data.toString();
        stderr = outputCap(stderr + text);
        opts.onStderr?.(text);
      });
      child.on("error", (error) => {
        finish({
          spawnError: error instanceof Error ? error.message : String(error),
          ok: false,
        });
      });
      child.on("close", (code) => {
        if (timedOut) return finish({ exitCode: null, ok: false });
        finish({ exitCode: code, ok: code === 0 });
      });

      if (opts.signal) {
        if (opts.signal.aborted) onAbort();
        else opts.signal.addEventListener("abort", onAbort, { once: true });
      }
    });
  },
  exists(target) {
    try {
      return fs.existsSync(target);
    } catch {
      return false;
    }
  },
};

let activeOps: SbxProcessOps = defaultProcessOps;

/** Install a stubbed processes/files implementation (tests); null restores the default. */
export function setSbxProcessOps(ops: SbxProcessOps | null): void {
  activeOps = ops ?? defaultProcessOps;
}

/** The active processes/files implementation. */
export function getSbxProcessOps(): SbxProcessOps {
  return activeOps;
}

/**
 * THE injection point: every `sbx` invocation in this extension goes through
 * here, which forwards to the active `SbxProcessOps`.
 */
export async function runSbx(args: string[], opts: SbxProcessOptions = {}): Promise<SbxProcessResult> {
  return await activeOps.run(args, opts);
}

// ── Exit-code mapping (exported for tests) ──────────────────────────────────

export type SbxExecOutcome = "ok" | "child-failed" | "infra-failure";

export interface SbxExitClassification {
  outcome: SbxExecOutcome;
  /** true for 125/127/spawn-error/timeout: the CLI could not run the command. */
  infraFailure: boolean;
  /** One-line, operator-facing reason (empty for ok). */
  reason: string;
}

/**
 * Map an `sbx exec` exit status. This is the load-bearing distinction of the
 * whole worker model:
 *
 *   - 0                    → the child command succeeded
 *   - 125                  → `sbx` could not run it (sandbox missing / stopped /
 *                            not runnable) — an INFRASTRUCTURE failure, not a
 *                            verdict about the worker's code
 *   - 127                  → the executable was not found inside the sandbox —
 *                            also infrastructure (the VM, not the tree, is wrong)
 *   - anything else        → the child command's OWN status (a real failure)
 *   - spawn error/timeout/abort → infrastructure
 *
 * Callers must never fold `infraFailure` into "the worker failed its gates":
 * the remediation is a host/VM fix, and retrying the same command is fine.
 */
export function classifySbxExit(
  exitCode: number | null,
  opts: { timedOut?: boolean; aborted?: boolean; spawnError?: string | null } = {},
): SbxExitClassification {
  if (opts.spawnError) {
    return {
      outcome: "infra-failure",
      infraFailure: true,
      reason: "sbx CLI could not be started: " + opts.spawnError,
    };
  }
  if (opts.timedOut) {
    return { outcome: "infra-failure", infraFailure: true, reason: "sbx timed out before the command finished" };
  }
  if (opts.aborted) {
    return { outcome: "infra-failure", infraFailure: true, reason: "sbx was aborted by the host" };
  }
  if (exitCode === 0) return { outcome: "ok", infraFailure: false, reason: "" };
  if (exitCode === SBX_EXIT_COULD_NOT_RUN) {
    return {
      outcome: "infra-failure",
      infraFailure: true,
      reason:
        "sbx could not run the command in the sandbox (exit " + String(SBX_EXIT_COULD_NOT_RUN) +
        "): sandbox missing or not runnable",
    };
  }
  if (exitCode === SBX_EXIT_MISSING_EXECUTABLE) {
    return {
      outcome: "infra-failure",
      infraFailure: true,
      reason:
        "the sandbox is missing the executable (exit " + String(SBX_EXIT_MISSING_EXECUTABLE) +
        "): the VM image, not the worktree, is wrong",
    };
  }
  return {
    outcome: "child-failed",
    infraFailure: false,
    reason: "the in-sandbox command exited with code " + String(exitCode),
  };
}

// ── Per-role profiles ───────────────────────────────────────────────────────

/** A mount source: the task worktree, or the (non-git) vault. */
export type SandboxMountKind = "worktree" | "vault";

export interface SandboxMount {
  kind: SandboxMountKind;
  mode: "rw" | "ro";
}

export interface SandboxProfile {
  /** Agent/role name this profile applies to. */
  role: string;
  /** true ⇒ spawn on the host (no VM). Documented residual, not a policy escape. */
  hostPassthrough: boolean;
  /**
   * Mounts in `sbx create` positional order. The FIRST entry is the primary
   * workspace; the rest are additional workspaces (`:ro` for read-only).
   */
  mounts: SandboxMount[];
  /** Hosts allowed through the default-deny egress proxy (empty ⇒ no rules added). */
  networkHosts: string[];
  /** Secret sentinel env NAMES the proxy injects (never values, never `-e`). */
  secretSentinelEnvs: string[];
  cpus: number;
  memory: string;
}

/**
 * The profile table IS the policy (WORKFLOW.md "Sandbox profiles as code").
 * Everything not listed here does not exist for that role: no mount, no
 * egress, no secret.
 */
export const SANDBOX_PROFILES: Record<string, SandboxProfile> = {
  worker: {
    role: "worker",
    hostPassthrough: false,
    mounts: [{ kind: "worktree", mode: "rw" }],
    networkHosts: [...SANDBOX_WORKER_EGRESS_HOSTS],
    secretSentinelEnvs: [...SANDBOX_SECRET_SENTINEL_ENVS],
    cpus: SANDBOX_DEFAULT_CPUS,
    memory: SANDBOX_DEFAULT_MEMORY,
  },
  general: {
    role: "general",
    hostPassthrough: false,
    mounts: [{ kind: "worktree", mode: "rw" }],
    networkHosts: [...SANDBOX_WORKER_EGRESS_HOSTS],
    secretSentinelEnvs: [...SANDBOX_SECRET_SENTINEL_ENVS],
    cpus: SANDBOX_DEFAULT_CPUS,
    memory: SANDBOX_DEFAULT_MEMORY,
  },
  scout: {
    role: "scout",
    hostPassthrough: false,
    mounts: [{ kind: "worktree", mode: "ro" }],
    networkHosts: [],
    secretSentinelEnvs: [],
    cpus: SANDBOX_DEFAULT_CPUS,
    memory: SANDBOX_DEFAULT_MEMORY,
  },
  librarian: {
    role: "librarian",
    hostPassthrough: false,
    // Vault RW first (the job), worktree read-only second (the reference).
    mounts: [
      { kind: "vault", mode: "rw" },
      { kind: "worktree", mode: "ro" },
    ],
    networkHosts: [],
    secretSentinelEnvs: [],
    cpus: SANDBOX_DEFAULT_CPUS,
    memory: SANDBOX_DEFAULT_MEMORY,
  },
  researcher: {
    // NOT sandboxed this batch: the profile is deferred and documented as the
    // one un-sandboxed role. `hostPassthrough` is a marker, not an escape hatch
    // for other roles — see profileForRole.
    role: "researcher",
    hostPassthrough: true,
    mounts: [],
    networkHosts: [],
    secretSentinelEnvs: [],
    cpus: SANDBOX_DEFAULT_CPUS,
    memory: SANDBOX_DEFAULT_MEMORY,
  },
};

/**
 * Profile for a role. Unknown roles get the `worker` profile (the narrow
 * read-write-worktree profile) — never a host passthrough: a new agent name
 * must not silently acquire host reach.
 */
export function profileForRole(role: string): SandboxProfile {
  const named = SANDBOX_PROFILES[role];
  if (named) return named;
  return { ...SANDBOX_PROFILES.worker, role };
}

/** true ⇒ this role spawns on the host (only `researcher` today). */
export function isHostPassthroughRole(role: string): boolean {
  return profileForRole(role).hostPassthrough === true;
}

// ── In-VM inert-mode predicate ──────────────────────────────────────────────

/** Env flag the host exports into every sandbox: the in-VM extension is inert. */
export const SANDBOX_ENV_FLAG = "PI_DELEGATE_SANDBOX";

/**
 * Is THIS process running inside a worker sandbox? When true the extension
 * registers nothing that enforces or records — no deny hooks, no scanner, no
 * receipts, no claims, no subagent tool, no job surfaces — and only appends
 * clearly-marked ADVISORY gate notes. The host re-runs the authoritative gates
 * after the worker exits; that verdict, not an in-VM one, decides.
 */
export function isSandboxedChild(env: NodeJS.ProcessEnv = process.env): boolean {
  return env[SANDBOX_ENV_FLAG] === "1";
}

// ── Sandbox names ───────────────────────────────────────────────────────────

const BASE36 = "0123456789abcdefghijklmnopqrstuvwxyz";

/** Short unique per-task sandbox name (`pi-<role>-<time>-<rand>`), sbx-legal. */
export function generatedSandboxName(
  role: string,
  now: number = Date.now(),
  nonce: () => number = Math.random,
): string {
  const safeRole = role
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 16) || "worker";
  let stamp = "";
  let value = Math.floor(now / 1000);
  while (stamp.length < 6) {
    stamp = BASE36[value % 36] + stamp;
    value = Math.floor(value / 36);
  }
  let suffix = "";
  for (let index = 0; index < 4; index++) {
    suffix += BASE36[Math.floor(nonce() * 36) % 36];
  }
  // sbx: 2–63 chars, [a-z0-9.-], each dot part starts/ends alphanumeric.
  return ("pi-" + safeRole + "-" + stamp + "-" + suffix).slice(0, 63);
}

// ── Lifecycle ───────────────────────────────────────────────────────────────

export interface CreateSandboxOptions {
  /** Role name from the agent frontmatter (`go-worker`, `scout`, …). */
  role: string;
  /** Absolute host path of the worktree to mount at the same path in the VM. */
  worktreePath: string;
  /** Repo root, for resolving the fallback kit path. */
  cwd?: string;
  /** Override the generated name (tests / retries). */
  name?: string;
  template?: string;
  kitPath?: string;
  /** Override the librarian vault path (tests / relocation). */
  vaultPath?: string;
  cpus?: number;
  memory?: string;
}

export interface SandboxHandle {
  name: string;
  role: string;
  profile: SandboxProfile;
  worktreePath: string;
  /** Which reference produced the VM. */
  createdFrom: "template" | "kit";
  /** Egress rules actually added (empty ⇒ none were added). */
  networkHosts: string[];
}

/** `PATH` or `PATH:ro` — sbx's workspace argument form. */
export function workspaceArg(hostPath: string, mode: "rw" | "ro"): string {
  return mode === "ro" ? hostPath + ":ro" : hostPath;
}

/** The workspace positionals for a profile, in declaration order. */
export function workspaceArgsFor(
  profile: SandboxProfile,
  worktreePath: string,
  vaultPath: string = SANDBOX_VAULT_PATH,
): string[] {
  return profile.mounts.map((mount) =>
    workspaceArg(mount.kind === "vault" ? vaultPath : worktreePath, mount.mode),
  );
}

/** `sbx create` argv for one profile (exported so tests can pin the shape). */
export function createSandboxArgs(opts: {
  name: string;
  profile: SandboxProfile;
  worktreePath: string;
  reference: "template" | "kit";
  template?: string;
  kitPath?: string;
  vaultPath?: string;
}): string[] {
  const args = ["create", "--name", opts.name, "--no-progress"];
  args.push("--cpus", String(opts.profile.cpus), "--memory", opts.profile.memory);
  if (opts.reference === "template") {
    args.push("-t", opts.template ?? SANDBOX_TEMPLATE_NAME);
  } else if (opts.kitPath) {
    args.push("--kit", opts.kitPath);
  }
  args.push(
    SANDBOX_CREATE_AGENT,
    ...workspaceArgsFor(opts.profile, opts.worktreePath, opts.vaultPath),
  );
  return args;
}

/** `sbx policy allow network` argv for one sandbox (exported for tests). */
export function policyAllowArgs(sandbox: string, hosts: readonly string[]): string[] {
  return ["policy", "allow", "network", "--sandbox", sandbox, hosts.join(",")];
}

/**
 * Create one worker sandbox from the saved template, falling back to the local
 * kit when the template is not on this host yet (the template may be created by
 * a parallel step). Applies the profile's egress allow rules per sandbox.
 *
 * Throws on failure (with the failing command + reason) — the caller turns that
 * into an infra-failure result. It NEVER retries on the host.
 */
export async function createWorkerSandbox(opts: CreateSandboxOptions): Promise<SandboxHandle> {
  const base = profileForRole(opts.role);
  const profile: SandboxProfile = { ...base };
  if (opts.cpus && opts.cpus > 0) profile.cpus = opts.cpus;
  if (opts.memory && opts.memory.trim() !== "") profile.memory = opts.memory.trim();
  const name = opts.name ?? generatedSandboxName(opts.role);
  const template = opts.template ?? SANDBOX_TEMPLATE_NAME;
  const kitPath = path.resolve(opts.cwd ?? process.cwd(), opts.kitPath ?? SANDBOX_KIT_FALLBACK_PATH);

  const create = async (reference: "template" | "kit"): Promise<SbxProcessResult> =>
    await runSbx(
      createSandboxArgs({ name, profile, worktreePath: opts.worktreePath, reference, template, kitPath, vaultPath: opts.vaultPath }),
      { timeoutMs: SBX_CREATE_TIMEOUT_MS },
    );

  let created = await create("template");
  let used: "template" | "kit" = "template";
  if (!created.ok) {
    // Template missing (or not loadable): try the local kit once. An absent kit
    // is a loud failure — there is no "just use the base agent image" path,
    // because that image would not know how to run pi at all.
    if (!getSbxProcessOps().exists(kitPath)) {
      throw new Error(
        "sbx create from template '" + template + "' failed (" + describeFailure(created) +
        ") and the fallback kit is missing at " + kitPath,
      );
    }
    created = await create("kit");
    used = "kit";
    if (!created.ok) {
      throw new Error(
        "sbx create failed from both the '" + template + "' template and the kit at " + kitPath +
        ": " + describeFailure(created),
      );
    }
  }

  if (profile.networkHosts.length > 0) {
    const allowed = await runSbx(policyAllowArgs(name, profile.networkHosts), {
      timeoutMs: SBX_POLICY_TIMEOUT_MS,
    });
    if (!allowed.ok) {
      // A VM without its declared egress is useless but not dangerous; a VM
      // with un-declared egress would be. Tear it down and fail loudly.
      await rmSandbox(name);
      throw new Error(
        "sbx policy allow network failed for sandbox " + name + ": " + describeFailure(allowed),
      );
    }
  }

  return {
    name,
    role: opts.role,
    profile,
    worktreePath: opts.worktreePath,
    createdFrom: used,
    networkHosts: [...profile.networkHosts],
  };
}

function describeFailure(result: SbxProcessResult): string {
  const parts: string[] = [];
  if (result.spawnError) parts.push("spawn error: " + result.spawnError);
  if (result.timedOut) parts.push("timed out");
  if (result.exitCode !== null) parts.push("exit " + String(result.exitCode));
  const tail = (result.stderr || result.stdout).trim().split("\n").slice(-4).join(" / ");
  if (tail) parts.push(tail);
  return parts.join("; ") || "no output";
}

export interface ExecSandboxOptions {
  /** `-w`: working directory inside the VM (the mounted worktree). */
  worktreePath: string;
  /** `-e K=V` pairs. Never a real secret value — see SANDBOX_SECRET_SENTINEL_ENVS. */
  env?: Record<string, string>;
  timeoutMs?: number;
  signal?: AbortSignal;
  onStdout?: (chunk: string) => void;
  onStderr?: (chunk: string) => void;
}

export interface SandboxExecResult extends SbxProcessResult {
  sandbox: string;
  /** The child argv run inside the VM (without the `exec … SANDBOX` prefix). */
  argv: string[];
  classification: SbxExitClassification;
}

/** `sbx exec` argv (exported so tests can pin the shape). */
export function execSandboxArgs(
  sandbox: string,
  worktreePath: string,
  env: Record<string, string>,
  argv: readonly string[],
): string[] {
  const args = ["exec", "--no-input", "-w", worktreePath];
  for (const [key, value] of Object.entries(env)) args.push("-e", key + "=" + value);
  args.push(sandbox, ...argv);
  return args;
}

/**
 * Foreground `sbx exec`. There is NO `--format` flag on exec: stdout/stderr are
 * captured through the normal child-process pipes (and streamed to the caller's
 * hooks when given). The exit status is mapped by `classifySbxExit`.
 */
export async function execInSandbox(
  sandbox: string,
  argv: readonly string[],
  opts: ExecSandboxOptions,
): Promise<SandboxExecResult> {
  const env = opts.env ?? {};
  const result = await runSbx(execSandboxArgs(sandbox, opts.worktreePath, env, argv), {
    timeoutMs: opts.timeoutMs,
    signal: opts.signal,
    onStdout: opts.onStdout,
    onStderr: opts.onStderr,
  });
  return {
    ...result,
    sandbox,
    argv: [...argv],
    classification: classifySbxExit(result.exitCode, {
      timedOut: result.timedOut,
      aborted: result.aborted,
      spawnError: result.spawnError,
    }),
  };
}

/** `sbx stop <name>` — best effort at the call site, never a throw. */
export async function stopSandbox(name: string): Promise<SbxProcessResult> {
  return await runSbx(["stop", name], { timeoutMs: SBX_STOP_TIMEOUT_MS });
}

/** `sbx rm --force <name>` — removes the VM and its sandbox-scoped secrets. */
export async function rmSandbox(name: string): Promise<SbxProcessResult> {
  return await runSbx(["rm", "--force", name], { timeoutMs: SBX_RM_TIMEOUT_MS });
}

export interface TeardownResult {
  name: string;
  stopped: boolean;
  removed: boolean;
  /** One line per failed verb (empty ⇒ clean teardown). */
  errors: string[];
}

/**
 * THE teardown path: `sbx stop` then `sbx rm --force`, both best-effort, never
 * throwing. The stall watchdog / abort / normal-exit paths all end here, so no
 * sandbox can outlive its worker (there is no local TTL — the host owns
 * wall-clock).
 */
export async function teardownSandbox(name: string): Promise<TeardownResult> {
  const errors: string[] = [];
  let stopped = false;
  let removed = false;
  try {
    const stop = await stopSandbox(name);
    stopped = stop.ok;
    if (!stop.ok) errors.push("sbx stop: " + describeFailure(stop));
  } catch (error) {
    errors.push("sbx stop threw: " + (error instanceof Error ? error.message : String(error)));
  }
  try {
    const rm = await rmSandbox(name);
    removed = rm.ok;
    if (!rm.ok) errors.push("sbx rm: " + describeFailure(rm));
  } catch (error) {
    errors.push("sbx rm threw: " + (error instanceof Error ? error.message : String(error)));
  }
  return { name, stopped, removed, errors };
}

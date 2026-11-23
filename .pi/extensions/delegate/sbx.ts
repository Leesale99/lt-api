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
 * TWO VM SHAPES. `createWorkerSandbox` builds a per-task worker VM from the
 * `pi-worker` profile table. `createGateSandbox` builds the GATE VM the
 * authoritative gates execute in: `pi-gate:v1`, one read-write worktree mount
 * (plus the main repo root `:ro` when the worktree is linked, so its gitdir
 * pointer resolves in-VM), Go-proxy-only egress, no secret sentinel, no model.
 * The gate VM registry is session-scoped and keyed by worktree path; all of
 * them are torn down at session end.
 *
 * THE IMAGE'S DATABASE IS IMAGE KNOWLEDGE (B3.4). The gate VM's Postgres — its
 * port, its baked cluster and socket dir, the throwaway role/DSN the DB-backed
 * gates receive, and the start/wait/drop/create pre-sequence — lives HERE, next
 * to the template it describes. ./gates.ts is only the RUNNER: it hands argv to
 * `sbx exec` through `execGateVmCommand` and reuses `prepareGateDbVm`, so the
 * sandbox module never depends on the gate runner at runtime (the step/report
 * types below are TYPE-ONLY imports, erased by type stripping).
 *
 * FAIL CLOSED. A create/exec that cannot run is an INFRA-FAILURE result — never
 * a silent fallback to a host-side spawn, which would hand a worker host reach
 * behind the profile's back.
 *
 * Loader gotcha (pi 1.5.2 extension parser): no `?:` ternaries with
 * double-quoted strings inside a template literal's `${}`; this module sticks
 * to concatenation throughout.
 */

import { execFileSync, spawn } from "node:child_process";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
// TYPE-ONLY (erased at runtime): the gate runner's step/result shapes. The
// runtime dependency points the other way — ./gates.ts imports this module.
import type { GateResult, GateStep } from "./gates.ts";

// ── Version pin + binary ────────────────────────────────────────────────────

/** The `sbx` CLI version this adapter's argv shapes were written against. */
export const SBX_CLI_VERSION = "0.54.0";
/** CLI binary name (PATH-resolved; tests never spawn it). */
export const SBX_BINARY = "sbx";

// ── Sandbox image / sizing constants ────────────────────────────────────────

/**
 * Saved sandbox template the GATE VMs are created from.
 *
 * THIS IS NOT THE WORKER IMAGE. A gate VM is a second, locked-down VM that runs
 * the authoritative gate commands (WORKFLOW.md `audit_static_cmd`,
 * `scoped_check_cmd`, `audit_cmd`, `prove_cmd`) against a mount of the
 * worktree. It has no model, no secret sentinel, and egress for the Go module
 * proxy only, so a poisoned worktree cannot turn a gate run into host reach.
 * The template is built by the parallel `pi-gate` kit step; there is NO kit
 * fallback here — a missing template is a loud infra failure, because falling
 * back to the worker image would silently widen the gate VM's surface.
 */
export const GATE_SANDBOX_TEMPLATE_NAME = "docker.io/library/pi-gate:v1";
/** Role tag for a gate VM (`pi-gate-<stamp>`), never an agent name. */
export const GATE_SANDBOX_ROLE = "gate";
/** Gate VM vCPUs: `make audit` runs the whole tree, so it gets the worker's shape. */
export const GATE_SANDBOX_CPUS = 4;
/** Gate VM memory (binary units). Explicit for the same reason as every role. */
export const GATE_SANDBOX_MEMORY = "8g";
/**
 * The gate VM's ENTIRE egress allowlist (default-deny otherwise): the Go module
 * proxy fallback only, for a worktree whose module cache is not warm yet. No
 * model endpoint and no secret sentinel — a gate VM never calls a model and
 * never carries a credential.
 */
export const GATE_SANDBOX_EGRESS_HOSTS: readonly string[] = [
  "proxy.golang.org:443",
  "sum.golang.org:443",
  "storage.googleapis.com:443",
];

/**
 * Saved sandbox template the worker VMs are created from. Produced by a
 * parallel kit/template step; it may not exist yet on a fresh host, which is
 * exactly why the create path has the `SANDBOX_KIT_FALLBACK_PATH` fallback and
 * records which one produced the VM (`createdFrom`).
 *
 * QUALIFIED ON PURPOSE: `sbx template ls` prints saved snapshots as
 * REPOSITORY + TAG, and `-t` resolves against the local image store by that
 * fully-qualified name. A bare `pi-worker` is NOT cached under the short name
 * with sbx 0.54.0: sbx falls back to its default pull policy and fails the
 * create with `403 Forbidden: pull failed for image "pi-worker"` (the S1 e2e
 * dogfood hit exactly that). Keep the REPOSITORY:TAG form; `docker.io/library`
 * is where the saved `pi-worker` snapshot lives.
 */
export const SANDBOX_TEMPLATE_NAME = "docker.io/library/pi-worker:v1";
/**
 * Fallback create-from-kit reference, relative to the launching repo root
 * (resolved by `createWorkerSandbox`). Used only when the saved template is
 * unavailable. An absent kit path is a loud infra-failure, never a silent
 * agent-image fallback.
 *
 * The KIT DIRECTORY is the artifact. It is a v3 workload kit (`pi-worker.yaml`
 * plus the stem-paired `pi-worker.dockerfile`), so the image is built from the
 * checked-in recipe: there is nothing to pack, and no `.zip` is produced any
 * more (`sbx kit pack` was the v2 flow). `dist/` is gitignored and written by
 * the same `.pi/sandboxes/build.sh` run that saves the `pi-worker:v1` template,
 * so on the fresh host this fallback exists for, the checked-in directory is
 * the only thing guaranteed to be present.
 *
 * PASSED POSITIONALLY, NOT WITH `--kit`. `sbx create AGENT|SANDBOX_KIT
 * [PATH...]` takes the kit in the agent slot (`sbx create ../path/to/my-agent/`
 * is the documented local-kit form), while `--kit` is documented as a MIXIN
 * flag (`sbx create claude --kit ./my-mixin/`) — a v3 workload in the `--kit`
 * slot is the wrong kind of kit. Local refs also have to be explicit paths; the
 * path handed to `sbx` here is resolved to an absolute one.
 */
export const SANDBOX_KIT_FALLBACK_PATH = ".pi/sandboxes/kit";
/** CPU allocation per VM. `sbx create` defaults to ALL host CPUs — never default. */
export const SANDBOX_DEFAULT_CPUS = 4;
/** Memory per VM (binary units). `sbx create` defaults to 50% of host RAM. */
export const SANDBOX_DEFAULT_MEMORY = "8g";
/**
 * CPU allocation for the read-only / mountless roles (scout, librarian,
 * researcher). Those roles never run a build or a gate in the VM: they are
 * model-call-bound (read, write prose, fetch pages), so half the worker's CPUs
 * is the right shape. `sbx create --cpus 0` means ALL host CPUs, so every role
 * states an explicit number — the table never relies on an sbx default.
 */
export const SANDBOX_LIGHT_CPUS = 2;
/** Memory for the read-only / mountless roles. Explicit for the same reason. */
export const SANDBOX_LIGHT_MEMORY = "4g";
/**
 * Agent positional for a TEMPLATE create: the image comes from the template, so
 * the slot only names a built-in agent. On the kit fallback this token is
 * replaced by the kit directory itself (`sbx create AGENT|SANDBOX_KIT
 * [PATH...]`), which is why `createSandboxArgs` pushes exactly one of the two.
 */
export const SANDBOX_CREATE_AGENT = "shell";

/** Egress allowlist for roles with a read-write worktree mount (default-deny). */
export const SANDBOX_WORKER_EGRESS_HOSTS: readonly string[] = [
  "opencode.ai:443",
  "proxy.golang.org:443",
  "sum.golang.org:443",
  "storage.googleapis.com:443",
];

/**
 * The model provider, and nothing else.
 *
 * "No network" cannot mean literally zero egress for a role that runs pi: the
 * agent's own model call goes through the same default-deny proxy as everything
 * else, so a profile with an empty allowlist creates a VM that dies on its first
 * turn with `403 Approval required for opencode.ai:443` (the S1 e2e dogfood hit
 * exactly that with the librarian). The sentinel-delivered key is useless
 * without this one rule. A role whose policy is "no network" therefore gets the
 * provider only — no module proxies, no general web, no task-relevant egress.
 */
export const SANDBOX_MODEL_EGRESS_HOSTS: readonly string[] = ["opencode.ai:443"];

/**
 * The host codex-search relay: the researcher's ONLY web-search path.
 *
 * `web_search` (`pi-web-access`) is configured by the baked
 * `~/.pi/agent/web-search.json` to POST to
 * `http://host.docker.internal:8787/responses`. The sbx proxy rewrites
 * `host.docker.internal` to the host's `localhost`, so the per-sandbox allow
 * rule is `localhost:8787` — nothing else needs to be reachable. The relay
 * (`.pi/sandboxes/relay/codex-search-relay.mjs`) runs on the host, holds the
 * host's `openai-codex` OAuth credential, and forwards to the codex backend.
 *
 * WHY THIS REPLACED `api.openai.com:443`: that path spent a pay-as-you-go
 * OpenAI API key that had to be injected into the VM as a custom secret. Now no
 * OpenAI credential enters any VM, and `chatgpt.com` never enters a VM either
 * (the relay is the only thing that talks to it). A sandbox without this rule,
 * or a relay that is not running, fails the web-search tool closed.
 */
export const SANDBOX_RELAY_EGRESS = "localhost:8787";

/**
 * The researcher's egress: the model provider (it has to think), the HOST codex
 * search relay, and the docs-lookup backend. The role is MOUNTLESS, so its only
 * work surface is the network, and the whole surface is these four rules:
 *
 *   opencode.ai:443      the model call (same provider as every other role)
 *   *.opencode.ai:443    provider subdomains (the proxy matches the wildcard)
 *   localhost:8787       the host codex-search relay — see SANDBOX_RELAY_EGRESS
 *   context7.com:443     the docs-lookup provider — the baked extension package
 *                        `@upstash/context7-pi` serves `resolve-library-id` and
 *                        `query-docs` from context7.com, authenticating with
 *                        `CONTEXT7_API_KEY`
 *
 * Anything else a `fetch_content` call reaches for is denied by the default-deny
 * proxy. That is the profile, not an accident: a research task that needs some
 * other host is a blocked report, not a reason to widen the list.
 */
export const SANDBOX_RESEARCHER_EGRESS_HOSTS: readonly string[] = [
  "opencode.ai:443",
  "*.opencode.ai:443",
  SANDBOX_RELAY_EGRESS,
  "context7.com:443",
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

/**
 * The researcher's docs-lookup credential — and now its ONLY VM sentinel.
 *
 * `@upstash/context7-pi` (baked into the image as a pi extension package, see
 * `kit/pi-worker.dockerfile`) reads `CONTEXT7_API_KEY` for its `context7.com`
 * requests, and `resolve-library-id`/`query-docs` work only when that name is
 * injected. Same sentinel mechanism, and a GLOBAL custom secret targeting
 * `context7.com` already exists on the host — so this one needs no per-host
 * setup. The VM only ever holds the `sbx-cs-` placeholder.
 *
 * RETIRED: the web-search sentinel (`SANDBOX_OPENAI_SECRET_SENTINEL_ENV` =
 * `OPENAI_API_KEY`, backed by `sbx secret set-custom --host api.openai.com`).
 * Web search now goes through the host relay (`SANDBOX_RELAY_EGRESS`), which
 * holds the codex credential HOST-side and substitutes it upstream, so there is
 * no key for the VM to carry. The constant was deleted rather than repurposed
 * so nothing can silently start injecting that retrograde env var again.
 */
export const SANDBOX_CONTEXT7_SECRET_SENTINEL_ENV = "CONTEXT7_API_KEY";

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

/**
 * A mount source: the task worktree, the (non-git) vault, or a disposable
 * scratch directory the adapter creates on the host (see
 * `createScratchWorkspace`).
 */
export type SandboxMountKind = "worktree" | "vault" | "scratch";

/**
 * Host temp-dir prefix for a profile's disposable primary workspace. `sbx`
 * rejects a read-only primary workspace ("primary workspace must be
 * read/write"), so a role that must not write the repo (scout) is created with
 * a throwaway rw dir first and the worktree mounted read-only second.
 */
export const SANDBOX_SCRATCH_PREFIX = "pi-scratch-";

export interface SandboxMount {
  kind: SandboxMountKind;
  mode: "rw" | "ro";
}

/**
 * The GATE profile: one read-write worktree mount (gates legitimately write
 * generated artifacts such as docs/spec/business-rules.md), plus — for a LINKED
 * worktree — the main repo root as a read-only EXTRA workspace so the
 * worktree's `.git` pointer (`.git` is a file naming the main repo's gitdir)
 * resolves inside the VM. Absolute paths are preserved, which is what makes the
 * pointer work. Egress and secrets are the narrowest in the table.
 */
export const GATE_SANDBOX_PROFILE: SandboxProfile = {
  role: GATE_SANDBOX_ROLE,
  hostPassthrough: false,
  // The extra repo-root :ro workspace is NOT in this table: it is conditional
  // on the host-side linked-worktree probe and is passed to `create` as an
  // extra workspace (see createGateSandbox), so the static profile is honest
  // about the mounts every gate VM has.
  mounts: [{ kind: "worktree", mode: "rw" }],
  networkHosts: [...GATE_SANDBOX_EGRESS_HOSTS],
  secretSentinelEnvs: [],
  denyNetworkHosts: [],
  cpus: GATE_SANDBOX_CPUS,
  memory: GATE_SANDBOX_MEMORY,
};

export interface SandboxProfile {
  /** Agent/role name this profile applies to. */
  role: string;
  /**
   * true ⇒ spawn on the host (no VM). A mechanism, not a policy escape: NO role
   * uses it today (researcher used to, and is now a mountless sandbox). Kept so
   * a future role can be declared host-side deliberately, and so `false` here
   * is an explicit statement rather than an absence.
   */
  hostPassthrough: boolean;
  /**
   * Mounts in `sbx create` positional order. The FIRST entry is the primary
   * workspace and sbx requires it to be read/write ("primary workspace must be
   * read/write" — it rejects a `:ro` primary outright); the rest are additional
   * workspaces (`:ro` for read-only). A role that must not write the repo
   * therefore declares `scratch` (rw) first and `worktree` (`:ro`) second.
   */
  mounts: SandboxMount[];
  /** Hosts allowed through the default-deny egress proxy (empty ⇒ no rules added). */
  networkHosts: string[];
  /** Secret sentinel env NAMES the proxy injects (never values, never `-e`). */
  secretSentinelEnvs: string[];
  /**
   * Per-sandbox network DENY rules passed at create time as repeated
   * `--deny-network <host>` (sbx 0.54.0: `--deny-network strings`, repeatable).
   * They are applied by `sbx create` itself, i.e. BEFORE the per-sandbox allow
   * policy below runs, which closes the post-create egress window for anything
   * a role must never reach. Deny beats allow (a local deny can only narrow).
   *
   * Default is EMPTY for every role: this is the mechanism, and no role has a
   * justified deny yet. Omit or list [] rather than inventing one.
   */
  denyNetworkHosts?: string[];
  /** vCPUs for the VM. Always explicit — `--cpus 0` means all host CPUs. */
  cpus: number;
  /** Memory (binary units). Always explicit — sbx defaults to 50% of host RAM. */
  memory: string;
}

/**
 * The profile table IS the policy: this file is the declaration of the sandbox
 * profiles that WORKFLOW.md points at, and `smoke-sbx.mjs` asserts them.
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
    // sbx needs a writable primary workspace, and scout must have zero write
    // access to the repo. The primary is therefore a disposable host temp dir
    // (`createScratchWorkspace`) and the task worktree is the read-only SECOND
    // workspace: reads work, every write lands in a directory that dies with
    // the VM.
    mounts: [
      { kind: "scratch", mode: "rw" },
      { kind: "worktree", mode: "ro" },
    ],
    // Model provider only: the agent has to be able to think. No task egress.
    networkHosts: [...SANDBOX_MODEL_EGRESS_HOSTS],
    secretSentinelEnvs: [],
    denyNetworkHosts: [],
    cpus: SANDBOX_LIGHT_CPUS,
    memory: SANDBOX_LIGHT_MEMORY,
  },
  librarian: {
    role: "librarian",
    hostPassthrough: false,
    // Vault RW first (the job), worktree read-only second (the reference).
    mounts: [
      { kind: "vault", mode: "rw" },
      { kind: "worktree", mode: "ro" },
    ],
    // Model provider only — the vault is the work surface, not the network.
    networkHosts: [...SANDBOX_MODEL_EGRESS_HOSTS],
    secretSentinelEnvs: [],
    denyNetworkHosts: [],
    cpus: SANDBOX_LIGHT_CPUS,
    memory: SANDBOX_LIGHT_MEMORY,
  },
  researcher: {
    // SANDBOXED, MOUNTLESS. There is no worktree in the VM at all: the role's
    // whole product is prose about the web, so a repo mount would be reach it
    // does not need. `mounts: []` emits NO workspace positionals on `sbx
    // create` (verified supported: the workspace list is `[PATH...]`, and a
    // create with none leaves the VM with its default (unmounted) root). Egress
    // is the model provider plus the two backends its TOOLSET needs — the host
    // codex-search relay for web search (`pi-web-access`, see
    // SANDBOX_RELAY_EGRESS) and context7.com for docs lookup
    // (`@upstash/context7-pi`), both baked into the image as extension
    // packages — see SANDBOX_RESEARCHER_EGRESS_HOSTS. Context7's key rides the
    // custom-secret sentinel mechanism like the model key; web search has NO key
    // in the VM at all (the relay holds it host-side).
    role: "researcher",
    hostPassthrough: false,
    mounts: [],
    networkHosts: [...SANDBOX_RESEARCHER_EGRESS_HOSTS],
    secretSentinelEnvs: [SANDBOX_CONTEXT7_SECRET_SENTINEL_ENV],
    denyNetworkHosts: [],
    cpus: SANDBOX_LIGHT_CPUS,
    memory: SANDBOX_LIGHT_MEMORY,
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

/**
 * true ⇒ this role spawns on the host. NO role is passthrough today (researcher
 * became a mountless sandbox); the predicate is retained because the spawn seam
 * branches on it and an unknown role must never be one (it gets `worker`).
 */
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

/**
 * Is THIS process a spawned WORKER child (foreground spawns only — background
 * children are orchestrators)? The spawn writes both flags (see ./spawn.ts), so
 * the worker-only surfaces (settle gate, post-write gate) can be registered
 * without re-deriving the role.
 */
export function isWorkerChild(env: NodeJS.ProcessEnv = process.env): boolean {
  return env.PI_DELEGATE_CHILD === "1" && env.PI_DELEGATE_ROLE === "worker";
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
  /** Role name from the agent frontmatter (`worker`, `scout`, …). */
  role: string;
  /**
   * Absolute host path of the worktree to mount at the same path in the VM.
   * IGNORED by a mountless profile (researcher), which emits no workspace
   * positionals at all rather than mounting a path it has no business seeing.
   */
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
  /**
   * Disposable rw primary workspace (only for a `scratch`-mount profile: scout).
   * Host-managed and removed by `teardownSandbox`. `undefined` otherwise.
   */
  scratchPath?: string;
  /** Which reference produced the VM. */
  createdFrom: "template" | "kit";
  /**
   * Extra workspaces beyond the profile mounts, in create order. A gate VM on a
   * linked worktree carries the main repo root here (`:ro`) so git resolves.
   * `undefined` for every worker/scout/librarian/researcher VM.
   */
  extraWorkspaces?: string[];
  /** Egress rules actually added (empty ⇒ none were added). */
  networkHosts: string[];
  /** Deny rules passed at create time (empty ⇒ none). */
  denyNetworkHosts: string[];
  /**
   * false ⇒ the VM has NO workspace mount (the mountless researcher). `exec`
   * then uses `SANDBOX_MOUNTLESS_WORKDIR` instead of the host worktree path,
   * which does not exist inside a VM with no mounts.
   */
  hasWorkspace: boolean;
}

/** `PATH` or `PATH:ro` — sbx's workspace argument form. */
export function workspaceArg(hostPath: string, mode: "rw" | "ro"): string {
  return mode === "ro" ? hostPath + ":ro" : hostPath;
}

/**
 * Create the disposable read-write primary workspace a `scratch`-mount profile
 * needs. sbx mounts a workspace by host path and refuses a read-only primary, so
 * the adapter has to hand it an existing, empty, host-owned directory: a
 * `mkdtemp` dir under the host temp root, labelled with the sandbox name so an
 * operator can correlate it. Never inside the repo — that would be the write
 * access the role is denied.
 */
export function createScratchWorkspace(label: string): string {
  const safeLabel = label.toLowerCase().replace(/[^a-z0-9.-]+/g, "-").slice(0, 48) || "sandbox";
  return fs.mkdtempSync(path.join(os.tmpdir(), SANDBOX_SCRATCH_PREFIX + safeLabel + "-"));
}

/**
 * The workspace positionals for a profile, in declaration order.
 *
 * A profile with `mounts: []` (the mountless researcher) yields NO positionals
 * at all: `sbx create AGENT|SANDBOX_KIT [PATH...]` accepts zero workspaces, and
 * a mountless role must not be handed a path it would then be able to see.
 *
 * `scratchPath` is required for (and only used by) a `scratch` mount; a profile
 * that declares one and gets no path is a programming error, not something to
 * paper over with a fallback that could point at the repo.
 *
 * Every mount path is RESOLVED (`path.resolve`) before it is emitted, and two
 * mounts that resolve to the same host path collapse into ONE workspace
 * positional: the FIRST declaration wins, so the profile's mount order stays
 * authoritative (for the librarian the vault rw is primary and a worktree that
 * happens to BE the vault is dropped instead of being re-declared `:ro`). This
 * is what keeps `sbx create` from failing with "conflicting read-only settings
 * for workspace" when the spawn cwd and a profile mount are the same directory.
 * Nesting (a worktree INSIDE the vault) is a different failure and is refused
 * outright by `createWorkerSandbox`.
 */
export function workspaceArgsFor(
  profile: SandboxProfile,
  worktreePath: string,
  vaultPath: string = SANDBOX_VAULT_PATH,
  scratchPath?: string,
): string[] {
  const args: string[] = [];
  const emitted = new Set<string>();
  for (const mount of profile.mounts) {
    let hostPath: string;
    if (mount.kind === "vault") {
      hostPath = path.resolve(vaultPath);
    } else if (mount.kind === "scratch") {
      if (!scratchPath) {
        throw new Error(
          "profile '" + profile.role + "' declares a scratch workspace but no scratch path was provided",
        );
      }
      hostPath = path.resolve(scratchPath);
    } else {
      hostPath = path.resolve(worktreePath);
    }
    if (emitted.has(hostPath)) continue;
    emitted.add(hostPath);
    args.push(workspaceArg(hostPath, mount.mode));
  }
  return args;
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
  scratchPath?: string;
  /** Extra workspace positionals appended after the profile mounts. */
  extraWorkspaces?: { path: string; mode: "rw" | "ro" }[];
}): string[] {
  // FAIL CLOSED: `--no-input` is a GLOBAL sbx flag (0.54.0 `sbx create --help`,
  // Global Flags) and the host process is never interactive. Without it, an
  // unmatched egress request — or any other prompt, e.g. a secret replacement or
  // a permission confirmation — would HANG this spawn instead of failing, and a
  // hung create is a worker that never starts and never gets a verdict.
  const args = ["create", "--no-input", "--name", opts.name, "--no-progress"];
  args.push("--cpus", String(opts.profile.cpus), "--memory", opts.profile.memory);
  // Create-time DENY rules come before any allow rule can exist for this
  // sandbox: they are part of the create call, while the profile's allow list is
  // a separate `sbx policy allow` afterwards. Nothing is denied today (every
  // profile declares []); the plumbing is what closes the post-create window for
  // a future role.
  for (const host of profileDenyHosts(opts.profile)) args.push("--deny-network", host);
  if (opts.reference === "template") {
    args.push("-t", opts.template ?? SANDBOX_TEMPLATE_NAME);
  }
  // The first positional is the agent slot: a built-in agent name for a
  // template create, or the sandbox-kit reference for a kit create
  // (`sbx create AGENT|SANDBOX_KIT [PATH...]`). The kit path is NOT a `--kit`
  // value — that flag is mixin-only, so a v3 workload passed there would be the
  // wrong kind of kit. Everything after the first positional is a workspace
  // path, in profile order — and a mountless profile contributes none.
  const agentSlot =
    opts.reference === "kit" && opts.kitPath ? opts.kitPath : SANDBOX_CREATE_AGENT;
  args.push(
    agentSlot,
    ...workspaceArgsFor(opts.profile, opts.worktreePath, opts.vaultPath, opts.scratchPath),
    ...(opts.extraWorkspaces ?? []).map((extra) => workspaceArg(extra.path, extra.mode)),
  );
  return args;
}

/** A profile's create-time deny hosts (absent ⇒ none, never an sbx default). */
export function profileDenyHosts(profile: SandboxProfile): string[] {
  return [...(profile.denyNetworkHosts ?? [])];
}

/** `sbx policy allow network` argv for one sandbox (exported for tests). */
export function policyAllowArgs(sandbox: string, hosts: readonly string[]): string[] {
  return ["policy", "allow", "network", "--sandbox", sandbox, hosts.join(",")];
}

/**
 * true ⇒ `target` IS `root` or lies beneath it. A separator-respecting prefix
 * test (`path.relative`), not `String.startsWith`: `/vault-evil` is NOT inside
 * `/vault`.
 */
function isSameOrInside(target: string, root: string): boolean {
  const relative = path.relative(root, target);
  return relative === "" || (!relative.startsWith("..") && !path.isAbsolute(relative));
}

/**
 * FAIL CLOSED before any `sbx` invocation: a spawn cwd that is (or lies inside)
 * a directory the sandbox mounts for another purpose would mount that directory
 * with the WORKTREE's mode.
 *
 *  - cwd === vault (a librarian session started in the vault): the same path
 *    would arrive twice with contradictory modes, and `sbx create` refuses it
 *    with "conflicting read-only settings for workspace" — a spawn that cannot
 *    even start, reported as an opaque CLI failure.
 *  - cwd INSIDE the vault from a worker/general spawn: the vault would be a
 *    read-write WORKSPACE of that VM, silently. That is the real hole —
 *    "only the librarian touches the vault" must be enforced here, not left to
 *    convention (the vault is not a git checkout, exactly like a worktree, so
 *    nothing downstream would notice).
 *
 * A refusal is cheap (no VM, no scratch dir) and the message says what to do.
 */
function refuseWorkspaceOverlap(
  worktreePath: string,
  protectedMounts: { label: string; root: string; hint: string }[],
): void {
  const worktree = path.resolve(worktreePath);
  for (const protectedMount of protectedMounts) {
    const root = path.resolve(protectedMount.root);
    if (!isSameOrInside(worktree, root)) continue;
    throw new Error(
      "spawn refused: cwd " + worktree +
      (worktree === root ? " is" : " lies inside") +
      " the sandbox's " + protectedMount.label + " mount " + root +
      "; " + protectedMount.hint + " — spawn with the repo root as cwd",
    );
  }
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
  // The vault is protected for EVERY role, not just the role that mounts it: a
  // worker/general spawn with a cwd inside the vault would otherwise mount the
  // vault read-write as its worktree. A `scratch` mount is protected too (it is
  // the disposable rw PRIMARY, so a worktree inside it must never be a second
  // workspace). Both checks run before anything is created.
  refuseWorkspaceOverlap(opts.worktreePath, [
    {
      label: "vault",
      root: opts.vaultPath ?? SANDBOX_VAULT_PATH,
      hint: profile.mounts.some((mount) => mount.kind === "vault")
        ? "the profile already mounts it"
        : "only a librarian sandbox may mount the vault",
    },
  ]);
  // sbx requires a read/write PRIMARY workspace. A production role mounts the
  // worktree rw and needs nothing else; a scratch-mount role (scout) must not
  // write the repo, so it gets a throwaway host dir as the primary and the
  // worktree as `:ro` second. Created before `sbx create` because sbx mounts an
  // existing host directory.
  const scratchPath = profile.mounts.some((mount) => mount.kind === "scratch")
    ? createScratchWorkspace(name)
    : undefined;
  // Disposable by definition: a failed create must not leave a temp dir behind,
  // and a failed cleanup must not mask the real create/policy error.
  const discardScratch = (): void => {
    if (!scratchPath) return;
    try {
      fs.rmSync(scratchPath, { recursive: true, force: true });
    } catch {
      /* host temp dir; the OS cleans it eventually */
    }
  };
  // Same fail-closed check against the scratch PRIMARY, now that its path
  // exists: a worktree inside the disposable dir would be mounted twice (rw
  // primary + worktree). Refusing must not leak the temp dir.
  if (scratchPath) {
    try {
      refuseWorkspaceOverlap(opts.worktreePath, [
        { label: "scratch", root: scratchPath, hint: "the profile already mounts it as the primary workspace" },
      ]);
    } catch (error) {
      discardScratch();
      throw error;
    }
  }

  const create = async (reference: "template" | "kit"): Promise<SbxProcessResult> =>
    await runSbx(
      createSandboxArgs({
        name,
        profile,
        worktreePath: opts.worktreePath,
        reference,
        template,
        kitPath,
        vaultPath: opts.vaultPath,
        scratchPath,
      }),
      { timeoutMs: SBX_CREATE_TIMEOUT_MS },
    );

  let created = await create("template");
  let used: "template" | "kit" = "template";
  if (!created.ok) {
    // Template missing (or not loadable): try the local kit once. An absent kit
    // is a loud failure — there is no "just use the base agent image" path,
    // because that image would not know how to run pi at all.
    if (!getSbxProcessOps().exists(kitPath)) {
      discardScratch();
      throw new Error(
        "sbx create from template '" + template + "' failed (" + describeFailure(created) +
        ") and the fallback kit is missing at " + kitPath,
      );
    }
    created = await create("kit");
    used = "kit";
    if (!created.ok) {
      discardScratch();
      throw new Error(
        "sbx create failed from both the '" + template + "' template and the kit at " + kitPath +
        ": " + describeFailure(created),
      );
    }
  }

  // Contract with `exec`: a profile with no mounts produced a VM with no
  // workspace, so `-w <host path>` would point at a directory that does not
  // exist inside it (exec substitutes SANDBOX_MOUNTLESS_WORKDIR). Recorded from
  // the profile actually used (not re-derived from the role) and dropped again
  // by teardown.
  sandboxHasWorkspace.set(name, profile.mounts.length > 0);

  if (profile.networkHosts.length > 0) {
    const allowed = await runSbx(policyAllowArgs(name, profile.networkHosts), {
      timeoutMs: SBX_POLICY_TIMEOUT_MS,
    });
    if (!allowed.ok) {
      // A VM without its declared egress is useless but not dangerous; a VM
      // with un-declared egress would be. Tear it down and fail loudly.
      await rmSandbox(name);
      sandboxHasWorkspace.delete(name);
      discardScratch();
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
    scratchPath,
    createdFrom: used,
    networkHosts: [...profile.networkHosts],
    denyNetworkHosts: profileDenyHosts(profile),
    hasWorkspace: profile.mounts.length > 0,
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
  /**
   * `-w`: working directory inside the VM (the mounted worktree). For a
   * MOUNTLESS sandbox this host path does not exist in the VM, so
   * `sandboxWorkdir` substitutes `SANDBOX_MOUNTLESS_WORKDIR` (`/home/agent`, the
   * one directory the image guarantees) — the host path is never passed.
   */
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

/**
 * The working directory for a MOUNTLESS sandbox's `exec`.
 *
 * `-w <host worktree>` would point at a directory that does not exist in a VM
 * with no mounts, and omitting `-w` would defer to a "primary workspace" a
 * mountless sandbox does not have. The agent's home is the one absolute path the
 * image guarantees (`PI_AGENT_HOME=/home/agent/.pi/agent`, owned by the agent),
 * so exec gets an explicit, existing directory and pi writes any incidental
 * state under its own home instead of failing on a missing cwd.
 */
export const SANDBOX_MOUNTLESS_WORKDIR = "/home/agent";

/**
 * `sbx exec` argv (exported so tests can pin the shape).
 *
 * `worktreePath === ""` OMITS `-w` entirely (a caller with no directory at all);
 * a mountless sandbox gets `SANDBOX_MOUNTLESS_WORKDIR` from `sandboxWorkdir`
 * below, never the host worktree path, which cannot exist inside the VM.
 */
export function execSandboxArgs(
  sandbox: string,
  worktreePath: string,
  env: Record<string, string>,
  argv: readonly string[],
): string[] {
  const args = ["exec", "--no-input"];
  if (worktreePath !== "") args.push("-w", worktreePath);
  for (const [key, value] of Object.entries(env)) args.push("-e", key + "=" + value);
  args.push(sandbox, ...argv);
  return args;
}

/**
 * Sandbox name → does its VM have a workspace mount?
 *
 * Proxy-creation state, not policy: `createWorkerSandbox` is the only creator
 * and it records the answer from the profile it actually used, so `exec` can
 * decide whether `-w` is meaningful without re-deriving the role's profile (and
 * without a caller having to thread it through). Unknown names (an exec on a
 * sandbox this process did not create) keep the `-w` they asked for.
 */
const sandboxHasWorkspace = new Map<string, boolean>();

/** The `-w` value for an exec: the mounted worktree, or the agent home when mountless. */
export function sandboxWorkdir(sandbox: string, worktreePath: string): string {
  return sandboxHasWorkspace.get(sandbox) === false ? SANDBOX_MOUNTLESS_WORKDIR : worktreePath;
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
  const result = await runSbx(
    execSandboxArgs(sandbox, sandboxWorkdir(sandbox, opts.worktreePath), env, argv),
    {
      timeoutMs: opts.timeoutMs,
      signal: opts.signal,
      onStdout: opts.onStdout,
      onStderr: opts.onStderr,
    },
  );
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
 * wall-clock). A scratch-mount profile's primary workspace is a host temp dir
 * this adapter created, so it dies here too.
 */
export async function teardownSandbox(name: string, scratchPath?: string): Promise<TeardownResult> {
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
  if (scratchPath) {
    try {
      fs.rmSync(scratchPath, { recursive: true, force: true });
    } catch (error) {
      errors.push("scratch cleanup: " + (error instanceof Error ? error.message : String(error)));
    }
  }
  sandboxHasWorkspace.delete(name);
  return { name, stopped, removed, errors };
}

// ── Gate VM: mount plan ─────────────────────────────────────────────────────

/** Timeout for the host-side `git rev-parse --git-common-dir` probe (ms). */
export const GATE_GIT_PROBE_TIMEOUT_MS = 10_000;

/** Result of the host-side git-common-dir probe. */
export interface GateGitProbe {
  ok: boolean;
  /** Raw `--git-common-dir` output (absolute after resolution); "" on failure. */
  commonDir: string;
  /** Human reason when ok is false. */
  reason: string;
}

/**
 * HOST-SIDE probe: `git -C <worktree> rev-parse --git-common-dir`.
 *
 * This RUNS ON THE HOST and reads only git metadata; it is the pre-gate trust
 * anchor (the host integrity check) that decides the gate VM's mount set. The
 * worktree's gitdir pointer is then resolved IN THE VM from the preserved
 * absolute paths. Failure is not fatal here: an unprobeable worktree gets the
 * single worktree mount (see gateMountPlanFromCommonDir).
 */
export function gitCommonDirOf(worktreePath: string): GateGitProbe {
  try {
    const output = execFileSync("git", ["-C", worktreePath, "rev-parse", "--git-common-dir"], {
      encoding: "utf8",
      timeout: GATE_GIT_PROBE_TIMEOUT_MS,
      stdio: ["ignore", "pipe", "pipe"],
    }).trim();
    if (!output) return { ok: false, commonDir: "", reason: "git rev-parse returned no common dir" };
    return { ok: true, commonDir: output, reason: "" };
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    return { ok: false, commonDir: "", reason: "git rev-parse --git-common-dir failed: " + message };
  }
}

export interface GateMountPlan {
  /** Absolute worktree path: the rw primary workspace. */
  worktreePath: string;
  /**
   * Absolute main repo root mounted `:ro` as an EXTRA workspace, or null when
   * the worktree is not linked (its own `.git` directory is the common dir).
   */
  repoRoot: string | null;
  /** true ⇒ the common git dir lives elsewhere (a `git worktree add` worktree). */
  linkedWorktree: boolean;
}

/**
 * Pure decision behind the gate VM mount set.
 *
 * A linked worktree's `.git` is a FILE containing `gitdir: <main>/.git/worktrees/<id>`.
 * A VM mounting only the worktree cannot resolve that pointer (the target is
 * outside the mount), so git commands fail inside the gate VM. Mounting the
 * MAIN REPO ROOT at its absolute path, read-only, makes the pointer resolve
 * because the paths are preserved — a live probe confirmed git works in-VM on a
 * repo-root mount. The extra mount is `:ro`: the gate VM may write the worktree
 * (generated artifacts) but has no reason to write the main repo.
 *
 * An unprobeable worktree (commonDir empty/not ok) is treated as NOT linked:
 * the single worktree mount is what we can prove, never a guessed extra mount.
 */
export function gateMountPlanFromCommonDir(worktreePath: string, commonDir: string): GateMountPlan {
  const absolute = path.resolve(worktreePath);
  const probed = commonDir.trim();
  if (probed === "") return { worktreePath: absolute, repoRoot: null, linkedWorktree: false };
  const resolvedCommon = path.resolve(absolute, probed);
  const localGitDir = path.join(absolute, ".git");
  if (resolvedCommon === localGitDir) {
    return { worktreePath: absolute, repoRoot: null, linkedWorktree: false };
  }
  // The common dir is `<main repo>/.git` (or a bare-ish variant): its parent is
  // the repo root to mount. Never point the extra mount at the worktree itself.
  const repoRoot = path.dirname(resolvedCommon);
  if (repoRoot === absolute) return { worktreePath: absolute, repoRoot: null, linkedWorktree: false };
  return { worktreePath: absolute, repoRoot, linkedWorktree: true };
}

/** Resolve the mount plan with the real git probe (or an injected one). */
export function resolveGateMountPlan(
  worktreePath: string,
  probe: (target: string) => GateGitProbe = gitCommonDirOf,
): GateMountPlan {
  const result = probe(worktreePath);
  if (!result.ok) return gateMountPlanFromCommonDir(worktreePath, "");
  return gateMountPlanFromCommonDir(worktreePath, result.commonDir);
}

// ── Gate VM: create ─────────────────────────────────────────────────────────

export interface CreateGateSandboxOptions {
  /** Override the generated `pi-gate-<stamp>` name (tests / retries). */
  name?: string;
  template?: string;
  /** Test seam: inject the host-side git-common-dir probe. */
  probe?: (target: string) => GateGitProbe;
  /** Test seam: inject the whole mount plan (skips the probe). */
  mountPlan?: GateMountPlan;
}

/**
 * Create the session's gate VM for one worktree path.
 *
 * NO KIT FALLBACK: the worker-image fallback exists because the worker image
 * may not be saved yet; the gate VM's whole point is its narrow surface, so a
 * missing `pi-gate:v1` template is a loud infra failure, never a downgrade to
 * the worker image. Egress is the declared Go-proxy set applied per sandbox;
 * every other host is denied by the proxy.
 */
export async function createGateSandbox(
  worktreePath: string,
  opts: CreateGateSandboxOptions = {},
): Promise<SandboxHandle> {
  const plan = opts.mountPlan ?? resolveGateMountPlan(worktreePath, opts.probe);
  const name = opts.name ?? generatedSandboxName(GATE_SANDBOX_ROLE);
  const template = opts.template ?? GATE_SANDBOX_TEMPLATE_NAME;
  const profile: SandboxProfile = { ...GATE_SANDBOX_PROFILE };
  const extraWorkspaces = plan.repoRoot ? [{ path: plan.repoRoot, mode: "ro" as const }] : [];

  const created = await runSbx(
    createSandboxArgs({
      name,
      profile,
      worktreePath: plan.worktreePath,
      reference: "template",
      template,
      extraWorkspaces,
    }),
    { timeoutMs: SBX_CREATE_TIMEOUT_MS },
  );
  if (!created.ok) {
    throw new Error(
      "sbx create for the gate VM from template '" + template + "' failed (" +
      describeFailure(created) + "); there is no fallback image for a gate VM",
    );
  }

  sandboxHasWorkspace.set(name, true);

  if (profile.networkHosts.length > 0) {
    const allowed = await runSbx(policyAllowArgs(name, profile.networkHosts), {
      timeoutMs: SBX_POLICY_TIMEOUT_MS,
    });
    if (!allowed.ok) {
      await rmSandbox(name);
      sandboxHasWorkspace.delete(name);
      throw new Error(
        "sbx policy allow network failed for gate sandbox " + name + ": " + describeFailure(allowed),
      );
    }
  }

  return {
    name,
    role: GATE_SANDBOX_ROLE,
    profile,
    worktreePath: plan.worktreePath,
    createdFrom: "template",
    ...(extraWorkspaces.length > 0 ? { extraWorkspaces: extraWorkspaces.map((extra) => extra.path) } : {}),
    networkHosts: [...profile.networkHosts],
    denyNetworkHosts: profileDenyHosts(profile),
    hasWorkspace: true,
  };
}

// ── Gate VM: session-scoped registry ────────────────────────────────────────

/**
 * Session-scoped reuse, KEYED BY WORKTREE PATH. The worktree mount is live, so
 * every exec on the same path sees the current dirty tree — one VM is enough
 * for a whole session's gate decisions. A background job's fresh worktree path
 * is a different key and therefore a DIFFERENT VM (never a VM mounted at the
 * previous path); all of them are torn down together at session end.
 */
const gateSandboxes = new Map<string, SandboxHandle>();
const gateSandboxCreates = new Map<string, Promise<SandboxHandle>>();

/** Registry key for a worktree path (absolute, so `./wt` and `/abs/wt` match). */
export function gateSandboxKey(worktreePath: string): string {
  return path.resolve(worktreePath);
}

/** The live gate VM for a worktree path, if one was created this session. */
export function gateSandboxFor(worktreePath: string): SandboxHandle | undefined {
  return gateSandboxes.get(gateSandboxKey(worktreePath));
}

/** Every live gate VM, in creation order. */
export function listGateSandboxes(): SandboxHandle[] {
  return [...gateSandboxes.values()];
}

/**
 * Lazily create (once per worktree path) and return the session's gate VM.
 * Concurrent gate decisions for the same path share one in-flight create, so
 * two gates cannot race two VMs onto the same mount.
 */
export async function ensureGateSandbox(
  worktreePath: string,
  opts: CreateGateSandboxOptions = {},
): Promise<SandboxHandle> {
  const key = gateSandboxKey(worktreePath);
  const existing = gateSandboxes.get(key);
  if (existing) return existing;
  const inFlight = gateSandboxCreates.get(key);
  if (inFlight) return await inFlight;
  const pending = createGateSandbox(worktreePath, opts)
    .then((handle) => {
      gateSandboxes.set(key, handle);
      return handle;
    })
    .finally(() => {
      gateSandboxCreates.delete(key);
    });
  gateSandboxCreates.set(key, pending);
  return await pending;
}

/**
 * Tear down EVERY gate VM this session created (session_shutdown). Best
 * effort, never throws: a leaked VM is a host cleanup problem, not a reason to
 * fail the session teardown.
 */
export async function teardownGateSandboxes(): Promise<TeardownResult[]> {
  const handles = [...gateSandboxes.values()];
  gateSandboxes.clear();
  gateSandboxCreates.clear();
  const results: TeardownResult[] = [];
  for (const handle of handles) {
    results.push(await teardownSandbox(handle.name, handle.scratchPath));
  }
  return results;
}

/** Test seam: forget the registry WITHOUT running sbx (the stub already did). */
export function resetGateSandboxRegistryForTests(): void {
  gateSandboxes.clear();
  gateSandboxCreates.clear();
}

// ── Gate VM: the in-VM database (image knowledge) ───────────────────────────
//
// Moved here from ./gates.ts (B3.4): what the `pi-gate:v1` image bakes is
// image knowledge, so it lives with the template constant it describes. The
// gate RUNNER keeps only the argv it hands to `sbx exec`.

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
 * (./gates.ts `appendGateDbOverride` appends that command-line variable.)
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

// ── Gate VM: exec env + the fail-closed exec adapter ────────────────────────

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
  void worktreePath;
  void opts;
  return env;
}

/**
 * One `sbx exec` in the gate VM, mapped into the fail-closed GateResult shape.
 * A 125/127/timeout/spawn error is infrastructure (no verdict) and still a
 * FAILED gate: the caller never sees a silent pass.
 */
export async function execGateVmCommand(
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

// ── Gate VM: DB-backed gate pre-sequence ────────────────────────────────────

function sleepGap(ms: number): Promise<void> {
  return new Promise<void>((resolve) => setTimeout(resolve, ms));
}

/**
 * The gate-step shape for a FAILED pre-sequence command. Mirrors ./gates.ts
 * `stepOf` (the one step shape a gate report renders) WITHOUT importing the
 * gate runner: this module stays the leaf of the import graph.
 */
function dbStepOf(result: GateResult, argv: string[], note: string): GateStep {
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
): Promise<{ ok: boolean; step: GateStep | null; sandbox: SandboxHandle }> {
  const sandbox = await ensureGateSandbox(worktreePath);
  const timeoutMs = opts.timeoutMs ?? GATE_DB_STEP_TIMEOUT_MS;
  const attempts = Math.max(1, opts.readyAttempts ?? GATE_PG_READY_ATTEMPTS);
  const intervalMs = Math.max(0, opts.intervalMs ?? GATE_PG_READY_INTERVAL_MS);
  const [start, ready, drop, create] = gateDbPresteps();

  const fail = (argv: string[], failure: GateResult): { ok: false; step: GateStep; sandbox: SandboxHandle } => ({
    ok: false,
    sandbox,
    step: dbStepOf(failure, argv, "gate VM db pre-sequence"),
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

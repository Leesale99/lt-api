/**
 * Worker-turn runtime guards (batch 2): a stall timeout for foreground worker
 * spawns.
 *
 * Why: a foreground worker can wedge — a provider call that never returns, a
 * tool subprocess waiting on a lock, a child that stopped emitting JSONL. The
 * parent then waits forever with no tool result. The guard is pure activity
 * based: every parsed event of the child's JSONL stream refreshes
 * `lastActivityMs`, and a periodic (1s) check aborts the child through the
 * existing SIGTERM→SIGKILL path once `now - lastActivityMs > timeout`.
 *
 * The timer is deliberately REFERENCED (never `unref()`): an unref'd interval
 * would let node's event loop drain while the child is still running, which
 * silently ends a headless run before the abort can happen.
 *
 * Everything here is importable and clock-injectable so the smoke test can
 * drive the decision logic without real time.
 */

import { loadWorkflowPolicy } from "./policy.ts";

/** Default stall timeout: 10 minutes of child silence. */
export const WORKER_STALL_TIMEOUT_MS = 600_000;
/** Watchdog tick. */
export const STALL_TICK_MS = 1_000;

/**
 * Stall timeout in ms. Batch 3 resolution order: the WORKFLOW.md field
 * `worker_stall_timeout_ms` when present and well-typed → the env override
 * `PI_DELEGATE_WORKER_STALL_TIMEOUT_MS` → the default. `0` disables the guard;
 * invalid/negative falls through (the same clamp style as pollCooldownMs).
 */
export function workerStallTimeoutMs(
  env: NodeJS.ProcessEnv = process.env,
  cwd: string = process.cwd(),
): number {
  const loaded = loadWorkflowPolicy(cwd);
  if (loaded.ok && loaded.declared.has("worker_stall_timeout_ms")) {
    return loaded.policy.workerStallTimeoutMs;
  }
  const raw = env.PI_DELEGATE_WORKER_STALL_TIMEOUT_MS;
  if (raw === undefined || raw.trim() === "") return WORKER_STALL_TIMEOUT_MS;
  const value = Number(raw);
  if (!Number.isFinite(value) || value < 0) return WORKER_STALL_TIMEOUT_MS;
  return Math.floor(value);
}

export interface StallDecision {
  stalled: boolean;
  /** Silence duration at decision time. */
  idleMs: number;
}

/**
 * Pure decision: stalled when the silence strictly exceeds the timeout.
 * Returns null when the guard is disabled (`timeoutMs <= 0`) or the clock
 * inputs are unusable — never a false abort on bad input.
 */
export function stallDecision(lastActivityMs: number, nowMs: number, timeoutMs: number): StallDecision | null {
  if (!Number.isFinite(timeoutMs) || timeoutMs <= 0) return null;
  if (!Number.isFinite(lastActivityMs) || !Number.isFinite(nowMs)) return null;
  const idleMs = Math.max(0, nowMs - lastActivityMs);
  return { stalled: idleMs > timeoutMs, idleMs };
}

/** "10min" / "90s" / "2.4s" — for the operator-facing stall reason. */
export function formatIdleMs(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return "unknown";
  if (ms >= 60_000) {
    const minutes = ms / 60_000;
    return (Number.isInteger(minutes) ? String(minutes) : String(Math.round(minutes * 10) / 10)) + "min";
  }
  if (ms >= 1000) {
    const seconds = ms / 1000;
    return (Number.isInteger(seconds) ? String(seconds) : String(Math.round(seconds * 10) / 10)) + "s";
  }
  return String(Math.round(ms)) + "ms";
}

/** `stalled: no child activity for 10min (timeout 600s)`. */
export function formatStallReason(idleMs: number, timeoutMs: number): string {
  return (
    "stalled: no child activity for " + formatIdleMs(idleMs) +
    " (timeout " + String(Math.round(timeoutMs / 1000)) + "s)"
  );
}

export interface StallWatchdog {
  /** Stop ticking (idempotent; also called by the runner's finish path). */
  stop(): void;
  /** Run one check now (test seam); true when the stall fired. */
  check(nowMs?: number): boolean;
}

/**
 * Start the referenced 1s watchdog. `onStall` fires at most once; the caller
 * aborts the child there. A disabled timeout (0) returns an inert handle, so
 * call sites need no branch.
 */
export function startStallWatchdog(opts: {
  timeoutMs: number;
  getLastActivityMs: () => number;
  onStall: (decision: StallDecision) => void;
  tickMs?: number;
  now?: () => number;
}): StallWatchdog {
  const now = opts.now ?? Date.now;
  let fired = false;
  let stopped = false;

  const check = (nowMs?: number): boolean => {
    // Reports whether THIS call fired (a repeat call after the first is false).
    if (fired || stopped) return false;
    const decision = stallDecision(opts.getLastActivityMs(), nowMs ?? now(), opts.timeoutMs);
    if (!decision || !decision.stalled) return false;
    fired = true;
    try {
      opts.onStall(decision);
    } catch (error) {
      console.error("[delegate] stall watchdog handler failed:", error);
    }
    return true;
  };

  const stop = () => {
    if (stopped) return;
    stopped = true;
    if (timer) clearInterval(timer);
  };

  let timer: ReturnType<typeof setInterval> | undefined;
  if (Number.isFinite(opts.timeoutMs) && opts.timeoutMs > 0) {
    // Referenced on purpose: see the module header (event-loop drain).
    timer = setInterval(() => {
      check();
    }, opts.tickMs && opts.tickMs > 0 ? opts.tickMs : STALL_TICK_MS);
  }

  return { stop, check };
}

/**
 * Referenced sleep (ms). Moved here from ./surfacing.ts (B3.4): both the
 * surfacing settle-wait and the worker-settle gate's continuation backoff need
 * the SAME referenced timer, and this module is the timing plane that both may
 * import.
 *
 * The timer MUST be referenced: in headless runs the event loop can otherwise
 * drain during the sleep (keep-alive sockets time out a few seconds after
 * agent_end) and the process exits cleanly mid-boundary — no settle, no output,
 * exit 0. Verified empirically; do not unref here.
 */
export function sleepMs(ms: number): Promise<void> {
  return new Promise((resolve) => {
    setTimeout(resolve, ms);
  });
}

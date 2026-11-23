/**
 * TUI layer, part 1 of 3 — EXECUTION-PLANE BADGES (pure helpers).
 *
 * TUI-only surfaces (never model-facing): the PRE-RUN badge is policy truth
 * from the role's profile and never invents a sandbox id; the POST-RUN badge +
 * info line read the instance truth persisted in `details`. Nothing here draws:
 * the renderers in ../surfacing.ts call these and `theme.fg(...)` the result,
 * which is why every seam is directly importable (smoke-ui.mjs pins them).
 *
 * Import direction: this module imports ./sbx.ts (profiles, passthrough roles)
 * and type-only from ../spawn.ts (the result contracts the spawn layer owns).
 * The type-only import is erased at runtime, so the graph stays acyclic:
 * index.ts → dispatch.ts → spawn.ts → surfacing.ts → tui/*.
 *
 * Loader gotcha (pi 1.5.2 extension parser): no `?:` ternaries with
 * double-quoted strings inside a template literal's ${} — the moved code below
 * already keeps to string concatenation.
 */
import type { SandboxMount } from "../sbx.ts";
import { isHostPassthroughRole, profileForRole } from "../sbx.ts";
import type { SandboxDetails, SingleResult } from "../spawn.ts";

/** The plane a run occupies. Policy for pre-run, instance truth for post-run. */
export type ExecutionPlane = "host" | "sandbox";

export interface PlaneBadge {
  plane: ExecutionPlane;
  /** Badge text without color, e.g. `[host]`, `[sbx wt:rw]`, `[sbx <name>]`. */
  label: string;
  /** Theme color token: host = warning, sandbox = success. */
  color: "warning" | "success";
}

/**
 * The plane label of a background child: a HOST-side orchestrator (tmux pane /
 * detached child), never a sandbox id. Its own worker spawns are sandboxed.
 */
export const BACKGROUND_JOB_PLANE = "orchestrator (host)";

/** Compact mount hint for the PRE-RUN badge: `wt:rw`, `wt:ro`, `vault`, `—`. */
export function compactMountSummary(mounts: readonly SandboxMount[]): string {
  if (mounts.some((mount) => mount.kind === "vault")) return "vault";
  const worktree = mounts.filter((mount) => mount.kind === "worktree");
  if (worktree.length === 0) return "\u2014";
  return "wt:" + worktree.map((mount) => mount.mode).join("+");
}

/** Full mount list for the EXPANDED line: `worktree:rw`, `vault:rw+worktree:ro`, `none`. */
export function formatMountsLong(mounts: readonly SandboxMount[]): string {
  if (mounts.length === 0) return "none";
  return mounts.map((mount) => mount.kind + ":" + mount.mode).join("+");
}

/** First few egress hosts plus a `+N` remainder; `none` when the VM had no egress. */
export function summarizeEgress(hosts: readonly string[], max = 3): string {
  if (hosts.length === 0) return "none";
  const head = hosts.slice(0, max).join(",");
  return hosts.length > max ? head + " +" + (hosts.length - max) : head;
}

/**
 * PRE-RUN plane from the ROLE's profile (policy truth). `background` wins: a
 * background single runs as a host-side ORCHESTRATOR (tmux pane) whatever the
 * target agent's profile says — it spawns its own sandboxed workers and is
 * never itself inside a VM.
 */
export function planeBadge(role: string, background = false): PlaneBadge {
  if (background || isHostPassthroughRole(role)) {
    return { plane: "host", label: "[host]", color: "warning" };
  }
  return {
    plane: "sandbox",
    label: "[sbx " + compactMountSummary(profileForRole(role).mounts) + "]",
    color: "success",
  };
}

/** POST-RUN header badge from the result's instance truth (undefined ⇒ nothing ran). */
export function resultPlaneBadge(result: SingleResult): PlaneBadge | undefined {
  if (result.sandbox) {
    return { plane: "sandbox", label: "[sbx " + result.sandbox.name + "]", color: "success" };
  }
  // A sandboxed role whose create failed has no name: say `[sbx]`, not a fake id.
  if (result.plane === "sandbox") return { plane: "sandbox", label: "[sbx]", color: "success" };
  if (result.plane === "host") return { plane: "host", label: "[host]", color: "warning" };
  return undefined;
}

/** EXPANDED one-liner: `sbx: …` for a VM run, `host: <cwd>` for a passthrough run. */
export function planeInfoLineText(result: SingleResult): string | undefined {
  if (result.sandbox) return formatSandboxInfoLine(result.sandbox);
  if (result.plane === "host" && result.hostCwd) return formatHostPlaneLine(result.hostCwd);
  return undefined;
}

export function formatSandboxInfoLine(sandbox: SandboxDetails): string {
  return (
    "sbx: " + sandbox.name +
    " \u00b7 role=" + sandbox.role +
    " \u00b7 from=" + sandbox.createdFrom +
    " \u00b7 mounts=" + formatMountsLong(sandbox.mounts) +
    " \u00b7 egress=" + summarizeEgress(sandbox.networkHosts) +
    " \u00b7 teardown=" + (sandbox.teardown ?? "pending")
  );
}

export function formatHostPlaneLine(cwd: string): string {
  return "host: " + cwd;
}

/**
 * TUI layer, part 2 of 3 — WIDGETS (the code that draws the jobs widget and
 * the shared status glyphs/ages every job surface reuses).
 *
 * `refreshJobWidget` is the only writer of the `delegate-jobs` widget; it is
 * fire-and-forget by design (hasUI-gated + error-swallowing) so tool executes
 * and timers can call it without ever breaking a non-UI run. `jobStatusIcon` /
 * `jobAge` are exported because the card renderers in ../surfacing.ts use the
 * same glyph set — one copy, or a card and its widget would disagree.
 *
 * Import direction: ../jobs.ts (registry reads) + ./badges.ts (plane label).
 * Nothing here imports ../surfacing.ts, so the graph stays acyclic.
 */
import type { ExtensionContext } from "@earendil-works/pi-coding-agent";
import { type JobEntry, type JobStatus, formatElapsed, listJobEntries } from "../jobs.ts";
import { BACKGROUND_JOB_PLANE } from "./badges.ts";

export const JOB_WIDGET_KEY = "delegate-jobs";
export const JOB_WIDGET_MAX_LINES = 6;

export function jobStatusIcon(status: JobStatus): string {
  switch (status) {
    case "awaiting_approval":
      return "⏸";
    case "running":
      return "⏳";
    case "done":
      return "✓";
    case "failed":
      return "✗";
    default:
      return "◐";
  }
}

/** Elapsed for running jobs; finished-ago for the rest (finishedAt when known). */
export function jobAge(entry: Pick<JobEntry, "status" | "startedAt" | "finishedAt">): string {
  if (entry.status === "running") return formatElapsed(entry.startedAt);
  return formatElapsed(entry.finishedAt ?? entry.startedAt) + " ago";
}

/**
 * Rebuild the delegate-jobs widget: one line per background job (max 6, then
 * "+N more"); empty registry clears the widget (content undefined). Best
 * effort — guarded by hasUI and swallowing errors so callers can fire it from
 * tool executes and timers without ever breaking a non-UI run.
 */
export async function refreshJobWidget(ctx: ExtensionContext): Promise<void> {
  if (!ctx.hasUI) return;
  try {
    const entries = await listJobEntries(ctx.cwd);
    const lines: string[] = [];
    for (const entry of entries.slice(0, JOB_WIDGET_MAX_LINES)) {
      lines.push(
        jobStatusIcon(entry.status) + " " + entry.id + " · " + entry.agent +
          " · " + jobAge(entry) + " · " + BACKGROUND_JOB_PLANE,
      );
    }
    if (entries.length > JOB_WIDGET_MAX_LINES) {
      lines.push("+ " + (entries.length - JOB_WIDGET_MAX_LINES) + " more");
    }
    ctx.ui.setWidget(JOB_WIDGET_KEY, lines.length > 0 ? lines : undefined, { placement: "aboveEditor" });
  } catch {
    /* widget refresh is best effort */
  }
}

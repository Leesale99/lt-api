/**
 * TUI layer — the live UI of the delegate extension, split by what it draws.
 *
 *   - ./badges.ts  — EXECUTION-PLANE badges and info lines. Pure helpers (no
 *                    ctx.ui): the pre-run POLICY badge from the role's profile,
 *                    the post-run INSTANCE badge/`sbx:` line from `details`,
 *                    mount + egress compaction, and `BACKGROUND_JOB_PLANE`. They
 *                    are the seams smoke-ui.mjs pins.
 *   - ./widgets.ts — the `delegate-jobs` widget plus the status glyph/age every
 *                    job surface reuses (`jobStatusIcon`, `jobAge`).
 *   - ./viewers.ts — the `ctx.ui.custom` screens: the scrollable text viewer,
 *                    /jobs, /agents (and the plain-text fallbacks without UI).
 *
 * This barrel is the layer's public face: the root imports the widget refresher
 * and the viewers it registers, and ../surfacing.ts imports the badge/widget
 * helpers its renderers draw with. Splitting by file (rather than one big
 * `tui.ts`) keeps the pure badge seams importable without pulling in the
 * SelectList/viewer machinery.
 *
 * Import direction: tui/* → ./jobs.ts / ./agents.ts / ./sbx.ts / pi-tui, and
 * TYPE-ONLY to ../spawn.ts (the result contracts). Nothing in tui/ imports
 * ../surfacing.ts, so the graph stays acyclic: index.ts → { surfacing.ts,
 * tui/* }.
 *
 * Loader gotcha (pi 1.5.2 extension parser): no `?:` ternaries with
 * double-quoted strings inside a template literal's ${} — the moved code keeps
 * to string concatenation.
 */
export {
  BACKGROUND_JOB_PLANE,
  compactMountSummary,
  formatHostPlaneLine,
  formatMountsLong,
  formatSandboxInfoLine,
  planeBadge,
  planeInfoLineText,
  resultPlaneBadge,
  summarizeEgress,
  type ExecutionPlane,
  type PlaneBadge,
} from "./badges.ts";
export { JOB_WIDGET_KEY, JOB_WIDGET_MAX_LINES, jobAge, jobStatusIcon, refreshJobWidget } from "./widgets.ts";
export {
  VIEWER_ROWS,
  agentSummaryLine,
  openAgentDetails,
  openAgentsView,
  openJobViewer,
  openJobsView,
  openTextViewer,
} from "./viewers.ts";

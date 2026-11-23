/**
 * TUI layer, part 3 of 3 — VIEWERS (the `ctx.ui.custom` screens behind /jobs,
 * /agents and the ctrl+\ shortcut, plus the plain-text fallbacks used when
 * there is no UI).
 *
 * Every entry point is inert without a UI: `openTextViewer` notifies instead of
 * opening, and the list views degrade to a `ctx.ui.notify(...)` summary, so the
 * same handlers serve -p/json mode and an interactive session. Registration
 * lives in ../index.ts (pi.registerCommand/registerShortcut), which is why this
 * module exports the openers instead of registering them.
 *
 * Import direction: ./widgets.ts (status glyph + age), ../jobs.ts (registry
 * reads), ../agents.ts (discovery). Nothing here imports ../surfacing.ts or
 * ../index.ts, so the graph stays acyclic.
 */
import { CONFIG_DIR_NAME, DynamicBorder, type ExtensionContext } from "@earendil-works/pi-coding-agent";
import { type SelectItem, Container, SelectList, Text, truncateToWidth } from "@earendil-works/pi-tui";
import { type AgentConfig, discoverAgents } from "../agents.ts";
import {
  formatJobList,
  getJob,
  getJobResultText,
  listJobEntries,
  taskPreview,
} from "../jobs.ts";
import { jobAge, jobStatusIcon } from "./widgets.ts";

/** Lines of content shown at once by the text viewer (header/footer excluded). */
export const VIEWER_ROWS = 16;

/**
 * Scrollable text viewer as an inline custom component (replaces the editor
 * until esc/enter). Deliberately NOT the built-in ScrollView: ctx.ui.custom
 * components render through component.render(width) without the layout-node
 * pass, and ScrollView's viewport slicing only happens under the TUI's layout
 * frame — so the viewer windows the content lines itself with a scrollTop.
 */
export async function openTextViewer(ctx: ExtensionContext, title: string, lines: string[]): Promise<void> {
  if (!ctx.hasUI) {
    ctx.ui.notify("The viewer needs an interactive session (TUI).", "warning");
    return;
  }
  await ctx.ui.custom<void>((tui, theme, keybindings, done) => {
    let scrollTop = 0;
    let cachedWidth: number | undefined;
    let cachedLines: string[] | undefined;

    const maxScrollTop = () => Math.max(0, lines.length - VIEWER_ROWS);
    const clampScroll = () => {
      scrollTop = Math.max(0, Math.min(maxScrollTop(), scrollTop));
    };
    const scroll = (delta: number) => {
      const next = Math.max(0, Math.min(maxScrollTop(), scrollTop + delta));
      if (next === scrollTop) return;
      scrollTop = next;
      cachedLines = undefined;
      tui.requestRender();
    };

    return {
      capturesViewportInput: true,
      render(width: number): string[] {
        if (cachedLines && cachedWidth === width) return cachedLines;
        cachedWidth = width;
        clampScroll();
        let footer = "↑↓ scroll · pageUp/pageDown page · esc close";
        if (lines.length > VIEWER_ROWS) {
          footer +=
            " · lines " + (scrollTop + 1) + "-" + Math.min(scrollTop + VIEWER_ROWS, lines.length) +
            " / " + lines.length;
        }
        const out: string[] = [
          truncateToWidth(theme.fg("accent", theme.bold(title)), width),
          truncateToWidth(theme.fg("muted", "──────────"), width),
        ];
        for (const line of lines.slice(scrollTop, scrollTop + VIEWER_ROWS)) {
          out.push(truncateToWidth(line, width));
        }
        out.push(truncateToWidth(theme.fg("dim", footer), width));
        cachedLines = out;
        return out;
      },
      handleInput(data: string): void {
        if (keybindings.matches(data, "tui.select.cancel") || keybindings.matches(data, "tui.select.confirm")) {
          done(undefined);
          return;
        }
        if (keybindings.matches(data, "tui.select.up") || keybindings.matches(data, "tui.altScreen.lineUp")) {
          scroll(-1);
        } else if (keybindings.matches(data, "tui.select.down") || keybindings.matches(data, "tui.altScreen.lineDown")) {
          scroll(1);
        } else if (keybindings.matches(data, "tui.altScreen.pageUp") || keybindings.matches(data, "tui.editor.pageUp")) {
          scroll(-(VIEWER_ROWS - 2));
        } else if (keybindings.matches(data, "tui.altScreen.pageDown") || keybindings.matches(data, "tui.editor.pageDown")) {
          scroll(VIEWER_ROWS - 2);
        } else if (keybindings.matches(data, "tui.altScreen.halfPageUp")) {
          scroll(-Math.floor(VIEWER_ROWS / 2));
        } else if (keybindings.matches(data, "tui.altScreen.halfPageDown")) {
          scroll(Math.floor(VIEWER_ROWS / 2));
        }
      },
      invalidate() {
        cachedWidth = undefined;
        cachedLines = undefined;
      },
    };
  });
}

/** Per-job result view: registry facts + the final assistant text (getJobResultText). */
export async function openJobViewer(ctx: ExtensionContext, id: string): Promise<void> {
  const entry = await getJob(ctx.cwd, id);
  if (!entry) {
    const known = (await listJobEntries(ctx.cwd)).map((e) => e.id).join(", ") || "none";
    ctx.ui.notify("No job with id " + id + ". Known jobs: " + known, "error");
    return;
  }
  const resultText = await getJobResultText(ctx.cwd, id);
  const lines: string[] = [];
  lines.push("agent: " + entry.agent);
  lines.push("status: " + entry.status + " · started: " + entry.startedAt + " · " + jobAge(entry));
  lines.push("runner: " + entry.runner + (entry.tmuxSession ? " (tmux session: " + entry.tmuxSession + ")" : ""));
  lines.push("task: " + taskPreview(entry.task, 200));
  lines.push("");
  if (entry.status === "awaiting_approval") {
    lines.push("awaiting your approval — job_approve id=" + id + " message=\"approved [note]\" or \"rejected: <reason>\" (or type it in the pane)");
    lines.push("");
  }
  if (resultText === null) {
    lines.push("(no .jsonl artifact found for this job)");
  } else if (resultText.trim()) {
    for (const line of resultText.split("\n")) lines.push(line);
  } else if (entry.status === "running") {
    lines.push("(no output yet)");
  } else {
    lines.push("(no assistant output)");
    if (entry.status === "failed" || entry.status === "aborted") {
      lines.push("Use job_status id=" + id + " for exit code, stderr and last events.");
    }
  }
  await openTextViewer(ctx, "Job " + entry.id + " [" + entry.status + "]", lines);
}

/** SelectList over the registry (newest first); selection opens the job viewer. */
export async function openJobsView(ctx: ExtensionContext): Promise<void> {
  if (!ctx.hasUI) {
    const entries = await listJobEntries(ctx.cwd);
    ctx.ui.notify(entries.length === 0 ? "No background jobs." : formatJobList(entries), "info");
    return;
  }
  const entries = await listJobEntries(ctx.cwd);
  if (entries.length === 0) {
    ctx.ui.notify("No background jobs (.pi/jobs/ has no registry entries).", "info");
    return;
  }
  const items: SelectItem[] = entries.map((entry) => ({
    value: entry.id,
    label: jobStatusIcon(entry.status) + " " + entry.id,
    description: entry.agent + " · " + entry.status + " · " + jobAge(entry) + " · " + taskPreview(entry.task, 40),
  }));
  const selected = await ctx.ui.custom<string | null>((tui, theme, _keybindings, done) => {
    const container = new Container();
    container.addChild(new DynamicBorder((str: string) => theme.fg("accent", str)));
    container.addChild(new Text(theme.fg("accent", theme.bold("Background jobs"))));
    const selectList = new SelectList(items, Math.min(items.length, 10), {
      selectedPrefix: (text) => theme.fg("accent", text),
      selectedText: (text) => theme.fg("accent", text),
      description: (text) => theme.fg("muted", text),
      scrollInfo: (text) => theme.fg("dim", text),
      noMatch: (text) => theme.fg("warning", text),
    });
    selectList.onSelect = (item) => done(item.value);
    selectList.onCancel = () => done(null);
    container.addChild(selectList);
    container.addChild(new Text(theme.fg("dim", "↑↓ navigate · enter view job · esc cancel")));
    container.addChild(new DynamicBorder((str: string) => theme.fg("accent", str)));
    return {
      capturesViewportInput: selectList.capturesViewportInput,
      render(width: number) {
        return container.render(width);
      },
      invalidate() {
        container.invalidate();
      },
      handleInput(data: string) {
        selectList.handleInput(data);
        tui.requestRender();
      },
    };
  });
  if (selected) await openJobViewer(ctx, selected);
}

/** Compact fact line for the /agents SelectList description column. */
export function agentSummaryLine(agent: AgentConfig): string {
  const parts: string[] = [agent.source];
  if (agent.model) parts.push("model=" + agent.model);
  if (agent.thinking) parts.push("thinking=" + agent.thinking);
  parts.push((agent.tools?.length ?? 0) + " tools");
  if (agent.background) parts.push("background");
  return parts.join(" · ") + " — " + agent.description;
}

/** Agent detail view: frontmatter facts, tool/skill lists, full system prompt. */
export async function openAgentDetails(ctx: ExtensionContext, agent: AgentConfig): Promise<void> {
  const lines: string[] = [];
  lines.push("source: " + agent.source);
  lines.push("file: " + agent.filePath);
  lines.push("model: " + (agent.model ?? "(inherited from dispatching session)"));
  lines.push("thinking: " + (agent.thinking ?? "(inherited unless model set)"));
  lines.push("tools: " + (agent.tools && agent.tools.length > 0 ? agent.tools.join(", ") : "(all default tools)"));
  lines.push("skills: " + (agent.skills && agent.skills.length > 0 ? agent.skills.join(", ") : "(none)"));
  lines.push("background: " + (agent.background ? "yes (single mode detaches into a job)" : "no (foreground by default)"));
  lines.push("description: " + agent.description);
  lines.push("");
  lines.push("─── system prompt ───");
  if (agent.systemPrompt.trim()) {
    for (const line of agent.systemPrompt.split("\n")) lines.push(line);
  } else {
    lines.push("(empty)");
  }
  await openTextViewer(ctx, "Agent " + agent.name, lines);
}

/** SelectList over discoverAgents(cwd, "both"); selection opens agent details. */
export async function openAgentsView(ctx: ExtensionContext): Promise<void> {
  if (!ctx.hasUI) {
    const discovered = discoverAgents(ctx.cwd, "both").agents;
    const summary = discovered.map((a) => a.name + " (" + a.source + ")").join(", ") || "none";
    ctx.ui.notify("Discovered agents: " + summary, "info");
    return;
  }
  const agents = discoverAgents(ctx.cwd, "both").agents;
  if (agents.length === 0) {
    ctx.ui.notify("No agents found in " + CONFIG_DIR_NAME + "/agents or the user agent dir.", "warning");
    return;
  }
  const items: SelectItem[] = agents.map((agent) => ({
    value: agent.name,
    label: agent.name,
    description: agentSummaryLine(agent),
  }));
  const selected = await ctx.ui.custom<string | null>((tui, theme, _keybindings, done) => {
    const container = new Container();
    container.addChild(new DynamicBorder((str: string) => theme.fg("accent", str)));
    container.addChild(new Text(theme.fg("accent", theme.bold("Agents"))));
    const selectList = new SelectList(items, Math.min(items.length, 10), {
      selectedPrefix: (text) => theme.fg("accent", text),
      selectedText: (text) => theme.fg("accent", text),
      description: (text) => theme.fg("muted", text),
      scrollInfo: (text) => theme.fg("dim", text),
      noMatch: (text) => theme.fg("warning", text),
    });
    selectList.onSelect = (item) => done(item.value);
    selectList.onCancel = () => done(null);
    container.addChild(selectList);
    container.addChild(new Text(theme.fg("dim", "↑↓ navigate · enter view details · esc cancel")));
    container.addChild(new DynamicBorder((str: string) => theme.fg("accent", str)));
    return {
      capturesViewportInput: selectList.capturesViewportInput,
      render(width: number) {
        return container.render(width);
      },
      invalidate() {
        container.invalidate();
      },
      handleInput(data: string) {
        selectList.handleInput(data);
        tui.requestRender();
      },
    };
  });
  if (selected) {
    const chosen = agents.find((a) => a.name === selected);
    if (chosen) await openAgentDetails(ctx, chosen);
  }
}

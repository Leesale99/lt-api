/**
 * Subagent Tool - Delegate tasks to specialized agents
 *
 * Spawns a separate `pi` process for each subagent invocation,
 * giving it an isolated context window.
 *
 * Supports three modes:
 *   - Single: { agent: "name", task: "..." } — foreground, or detached in a
 *     tmux-backed background job; a per-call `background` param overrides the
 *     agent frontmatter (true = force background, false = force foreground)
 *   - Parallel: { tasks: [{ agent: "name", task: "..." }, ...] }
 *   - Chain: { chain: [{ agent: "name", task: "... {previous} ..." }, ...] }
 *
 * Uses JSON mode to capture structured output from subagents.
 *
 * Phase 3 adds the `job_status` tool for picking up background job results
 * (runner + registry live in ./jobs.ts; shared child-output parsing in
 * ./output.ts).
 *
 * Phase 4 adds the session-level surfaces: one-line `delegate-job` entry cards
 * (TUI-only, never reach the LLM), `delegate-job-result` context messages
 * injected on explicit job_status pickup of a finished job, and auto-surfacing
 * of finished jobs at the agent_before_settle boundary (continue:true only when
 * something new was surfaced — loop-guarded).
 *
 * Phase 7 adds the `job_approve` tool (parent-relayed approval into a
 * confirm-handoff job's idle pane) and the first-class `awaiting_approval`
 * registry status (refreshStatus in jobs.ts detects the AWAITING_APPROVAL
 * marker; the UI surfaces and the settle injection phrase it as
 * waiting-for-approval).
 *
 * Batch 1 (worker gates + content-bound approvals, all logic in ./gates.ts):
 *   - POST-WRITE gate: a HOST-PASSTHROUGH worker child that successfully
 *     edits/writes a .go, .feature, .yaml or docs/spec/ file gets
 *     `make audit/static` + the scoped spec check appended to its tool result on
 *     failure (debounced ~10s). Sandboxed workers never run this: their
 *     instance is inert and the HOST runs the authoritative ladder after the
 *     sandbox exits.
 *   - WORKER-SETTLE gate: the same two commands at settle, up to 3 attempts
 *     (continue:true is bounded by the attempt counter), then a hard stop.
 *   - Receipts: the EXTENSION appends one JSON line per gate run to
 *     `<host-root>/<receipts_dir>/<iso-date>.jsonl` (commands, exit codes,
 *     rules hash, HEAD). Host-only: an in-sandbox instance writes none.
 *   - PROVE receipts: a bash run of the DECLARED prove command (WORKFLOW.md
 *     `prove_cmd`) carrying `--rule <id>` gets its own receipt, recorded in the
 *     HOST SESSION'S OWN receipts dir (a leading `cd <worktree>` no longer
 *     redirects it — a worker could pre-forge a VERIFIED receipt there).
 *   - TASK PACKETS: the task text's `SPEC-SCOPE` / `CLAIMS-PATHS` lines are
 *     parsed ONCE at the subagent boundary (./protocol.ts, the grammar owner);
 *     a malformed packet refuses the spawn before any claim/sandbox/child
 *     exists. A valid scope is recorded in the claims log at spawn and its
 *     normalized form is exported as PI_DELEGATE_SPEC_SCOPE into the sandbox's
 *     `-e` env (no line = no env var = full spec check).
 *   - job_approve binds the relayed decision to the `HEAD SHA:` recorded in the
 *     job's approval block and refuses when the worktree HEAD moved; for a job
 *     whose recorded scope names rules it also refuses while any scoped rule
 *     lacks a VERIFIED prove receipt (./mergegate.ts `checkProveCoverage`, fed
 *     by the single job-scope resolver there — the same call the merge-path
 *     block makes).
 *
 * Sandbox era:
 *   - Every foreground worker runs inside a per-task Docker Sandbox micro-VM
 *     (./sbx.ts owns the whole CLI): the worktree is mounted per the role's
 *     profile (`researcher` is mountless), the pi invocation is exec'd
 *     foreground, and the stall watchdog owns teardown (`sbx stop`, then
 *     `rm --force`).
 *   - The HOST session is the verifier plane: after a sandboxed exec exits it
 *     runs the integrity check FIRST (`checkWorktreeIntegrity`), refuses the
 *     gates on a dirty result, and otherwise runs the authoritative ladder and
 *     writes the receipts.
 *   - The extension ALSO loads inside the VM and is INERT there
 *     (`isSandboxedChild`): it registers no enforcing hook, no receipts, no
 *     claims, no subagent tool and no job surfaces. It only appends
 *     clearly-marked ADVISORY notes from `runInVmAdvisoryGate`.
 *   - The mount is the boundary, and the integrity check is the net at the
 *     merge boundary; nothing classifies paths or inspects commands.
 *   - MERGE-PATH PROVE: in an orchestrator host session a bash hook blocks a
 *     merge command when the job's recorded SPEC-SCOPE names rules without
 *     VERIFIED host-side prove receipts — independent of how the approval
 *     arrived.
 *
 * Gate-VM era:
 *   - The AUTHORITATIVE gate commands (declared in WORKFLOW.md) no longer run
 *     on the host. They run in a second, locked-down `pi-gate:v1` VM
 *     (./sbx.ts `createGateSandbox`): the host spawns `sbx exec` and nothing
 *     else. A gate command typed into a host bash session is recognised and
 *     REWRITTEN to the equivalent `sbx exec` line (after the host integrity
 *     check and, for `make audit`/`make prove`, the Postgres pre-sequence); the
 *     extension-driven ladder/settle/reconciliation paths call the gate VM
 *     directly. The host keeps only the integrity check (`git`) and receipts.
 *   - Gate VMs are session-scoped and keyed by worktree path (a background
 *     job's fresh worktree gets its own VM); all are torn down at
 *     `session_shutdown`.
 *
 * Module layout (B3.4 — this file is the extension ROOT and nothing else: the
 * `default` entry, the env switches, and the ORDER in which the modules below
 * register their hooks/tools/commands. Any logic found here beyond that is a
 * layering bug):
 *   - ./dispatch.ts  the `subagent` tool (schema, mode branching, the ONE
 *                    task-packet parse, claims, refusals) plus `job_status`
 *                    and `job_approve`. Registered here, implemented there.
 *   - ./spawn.ts     one child run: agent resolution, the `sbx exec`/local
 *                    invocation, the sandbox child env, host verification
 *                    after exit, and the model-facing result text.
 *   - ./persona.ts   prompt assembly — the main-session persona
 *                    (`.pi/agents/main.md`), the child prompt
 *                    (`buildTaskPrompt`, `writePromptToTempFile`) and the
 *                    model-facing `subagent` tool description. NO protocol
 *                    grammar lives there: ./protocol.ts owns it, WORKFLOW.md §
 *                    Task packets documents it.
 *   - ./surfacing.ts the chat surface — job cards, the ONE result-injection
 *                    builder, the exactly-once surfacing bookkeeping, the
 *                    ambient live-plane footer, and every transcript renderer.
 *   - ./tui/         the live UI — badges.ts (pure execution-plane helpers),
 *                    widgets.ts (the jobs widget), viewers.ts (/jobs, /agents,
 *                    the scrollable viewer). See ./tui/index.ts.
 *   - ./gates.ts     the gate runner + receipts + integrity + settle state, and
 *                    the hooks that enforce them (in-VM advisory, gate-VM
 *                    re-dispatch, post-write gate, worker-settle gate).
 *   - ./sbx.ts       the sandbox CLI adapter: worker/gate VM lifecycle, the
 *                    gate VM image + its baked database, the DB pre-sequence.
 *   - ./mergegate.ts THE job-scope resolver + THE prove-receipt policy, and the
 *                    two merge-path hooks that use them (merge block, prove
 *                    receipt writer).
 *   - ./protocol.ts  the task-packet grammar (one parse, at the spawn boundary).
 *   - ./jobs.ts, ./claims.ts, ./policy.ts, ./runtime.ts, ./output.ts,
 *     ./agents.ts, ./contracts.ts — the existing planes.
 *
 * Loader gotcha (pi 1.5.2 extension parser), applies to every module here and
 * to the ones above: no `?:` ternaries with double-quoted strings inside a
 * template literal's ${} — stick to string concatenation.
 */

import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import type { AutocompleteItem } from "@earendil-works/pi-tui";
import {
  registerJobApproveTool,
  registerJobStatusTool,
  registerSubagentTool,
  type AnyToolDetails,
} from "./dispatch.ts";
import {
  registerGateVmDispatchHook,
  registerInVmAdvisory,
  registerPostWriteGateHook,
  registerWorkerSettleHook,
} from "./gates.ts";
import { listJobIdsSync } from "./jobs.ts";
import { registerMergePathProveHook, registerProveReceiptHook } from "./mergegate.ts";
import { loadOrchestratorPrompt } from "./persona.ts";
import { isSandboxedChild, teardownGateSandboxes } from "./sbx.ts";
import {
  type JobCardData,
  type JobResultDetails,
  registerSurfacingHooks,
  renderJobCardEntry,
  renderJobResultMessage,
  setSurfacingApi,
} from "./surfacing.ts";
import { openAgentsView, openJobsView, openJobViewer } from "./tui/index.ts";

/**
 * Routing reminder returned whenever the edit/write gate stops a call. The
 * non-UI block prefixes it (see tool_call below); the confirm-decline path
 * appends the dialog hint.
 */
const DELEGATE_REASON =
  "Orchestrator does not implement. Delegate this to a subagent (worker for Go, general for anything else) via the subagent tool";

export default function (pi: ExtensionAPI) {
  // Sandboxed (in-VM) instance: inert. Register the advisory notes and stop —
  // nothing below may run inside a worker VM (no enforcement, no records, no
  // teardown, no tool surfaces).
  if (isSandboxedChild()) {
    registerInVmAdvisory(pi);
    return;
  }

  setSurfacingApi(pi);

  // Phase 2: env-gated orchestrator injection. Main sessions get persona/rules/routing
  // appended to the system prompt; children (PI_DELEGATE_CHILD set) get nothing.
  pi.on("before_agent_start", (event, ctx) => {
    if (process.env.PI_DELEGATE_CHILD) return;
    // Opt-in delegate-only mode: PI_DELEGATE_ONLY set in the main session leaves subagent as its only callable tool.
    if (process.env.PI_DELEGATE_ONLY) pi.setActiveTools(["subagent"]);
    // Host-tool seal: the host plane — this interactive session — reads
    // untrusted text, so capability minimization leaves it read-only plus the
    // extension's own verbs: delegate, poll a job, relay an approval.
    // WORKFLOW.md documents the policy, including why the background
    // orchestrator keeps bash for now (the gate-VM dispatch and merge-path
    // prove seams are hooks on the bash tool) and is sealed by its card's
    // `--tools` list instead: a job carries PI_DELEGATE_CHILD and returned
    // above.
    // PI_DELEGATE_UNSEALED is the human escape hatch for a session that needs
    // the full toolset back.
    // This call is the only mechanism that reaches extension- and
    // package-registered tools: `defaultTools` in the project
    // `.pi/settings.json` replaces the inherited builtin list, and a plain name
    // list there neither registers nor narrows what an extension or an
    // installed package registered. PI_DELEGATE_ONLY above is the stricter
    // list, so its call is left standing when both apply.
    if (!process.env.PI_DELEGATE_UNSEALED && !process.env.PI_DELEGATE_ONLY) {
      pi.setActiveTools(["read", "subagent", "job_status", "job_approve"]);
    }
    return { systemPrompt: event.systemPrompt + loadOrchestratorPrompt(ctx.cwd) };
  });

  // Phase 2: hard-layer gate. edit/write in a MAIN session must not run
  // unimpeded: UI present → confirm dialog, declining blocks with a routing
  // reminder, confirming allows harness/plan work. Non-UI main sessions (-p)
  // get a HARD block instead — nobody can approve a dialog there, and without
  // this the orchestrator could silently implement. bash is not gated (prompt
  // layer discourages it). Children (PI_DELEGATE_CHILD set) skip THIS gate so
  // delegated work runs unattended; a sandboxed worker has no write barrier in
  // the VM by design (the mount is the boundary), and the host integrity check
  // at the merge boundary is the net.
  pi.on("tool_call", async (event, ctx) => {
    if (process.env.PI_DELEGATE_CHILD) return;
    if (event.toolName !== "edit" && event.toolName !== "write") return;
    if (!ctx.hasUI) {
      return {
        block: true,
        reason: "Non-UI main session: edit/write is blocked; delegate to a subagent. " + DELEGATE_REASON + ".",
      };
    }
    const ok = await ctx.ui.confirm("Delegate instead?", "The orchestrator routes implementation work to subagents. Proceed with the " + event.toolName + " anyway?");
    if (!ok) {
      return {
        block: true,
        reason: DELEGATE_REASON + ", or confirm the edit in the dialog if this is harness/plan work.",
      };
    }
  });

  // MERGE-PATH PROVE enforcement (./mergegate.ts owns the resolver + policy).
  // Closes the typed-in-pane bypass: the precondition lives on the MERGE
  // EXECUTION PATH, not on the approval path.
  registerMergePathProveHook(pi);

  // GATE-VM EXECUTION (host shell → gate VM): a declared gate command typed
  // into the bash tool is integrity-checked, DB-pre-sequenced and rewritten to
  // the equivalent `sbx exec` line. ./gates.ts owns the body.
  registerGateVmDispatchHook(pi);

  // Gate VM lifecycle: session-scoped reuse, all torn down at session end.
  // `session_shutdown` is the clean session-end hook (RELOAD/NEW/RESUME/FORK and
  // exit all emit it), so the VMs survive across turns and gate decisions and
  // die with the session. There is no local TTL; the host owns wall-clock.
  pi.on("session_shutdown", async () => {
    try {
      await teardownGateSandboxes();
    } catch (error) {
      console.error("[delegate] gate VM teardown failed:", error);
    }
  });

  // PROVE receipt writer (merge-back evidence): the extension records every
  // scoped prove run in the HOST session's receipts dir; no worker ever writes
  // one. ./mergegate.ts owns the body.
  registerProveReceiptHook(pi);

  // Tool execute results cannot set runtime error state directly, so patch it
  // through the result hook. AnyToolDetails: the background start-failure
  // result also sets isError (BackgroundJobDetails.isError).
  pi.on("tool_result", (event) => {
    if (event.toolName !== "subagent") return;
    const details = event.details as AnyToolDetails | undefined;
    if (details?.isError) return { isError: true };
  });

  // Batch 1: POST-WRITE scoped gate (host-passthrough worker children only;
  // the body lives in ./gates.ts).
  registerPostWriteGateHook(pi);

  // Phase 5 two-tier: workers cannot recurse — skip the subagent tool
  // registration entirely in a worker child's process (PI_DELEGATE_ROLE=worker
  // is set on every foreground spawn), so a worker has neither the tool nor
  // the temptation, and its child context stays smaller. Main sessions and
  // child orchestrators keep it. job_status and the UI surfaces stay
  // registered (inert for workers: job_status is in no worker's tools list,
  // the rest is hasUI-gated).
  if (process.env.PI_DELEGATE_ROLE !== "worker") registerSubagentTool(pi);

  // Phase 3: pick up background job results on demand (./dispatch.ts body).
  registerJobStatusTool(pi);

  // Phase 7: parent-relayed approval into a confirm-handoff job's pane.
  registerJobApproveTool(pi);

  // ── UI commands + shortcut ────────────────────────────────────────────────
  // Registration happens at load time and is inert in -p/json mode: every
  // handler guards dialog/custom-component calls with ctx.hasUI (see the open*
  // helpers) and the widget refresh is fire-and-forget.

  pi.registerCommand("jobs", {
    description: "Inspect background subagent jobs (list, or view one job's output)",
    // Completion providers receive only (prefix) — no ctx — so process.cwd()
    // is the only option here. pi runs with cwd = project root, identical to
    // the ctx.cwd every other handler in this extension uses.
    getArgumentCompletions: (prefix) => {
      const items: AutocompleteItem[] = listJobIdsSync(process.cwd()).map((id) => ({ value: id, label: id }));
      const filtered = items.filter((item) => item.value.startsWith(prefix));
      if (filtered.length === 0) return null;
      return filtered;
    },
    handler: async (args, ctx) => {
      const requested = (args ?? "").trim();
      if (requested) {
        await openJobViewer(ctx, requested);
        return;
      }
      await openJobsView(ctx);
    },
  });

  pi.registerCommand("agents", {
    description: "List discovered subagent agents and view their details",
    handler: async (_args, ctx) => {
      await openAgentsView(ctx);
    },
  });

  // ── Phase 4 renderers ──────────────────────────────────────────────────
  // "delegate-job" ENTRY cards (pi.appendEntry) are durable TUI-only content —
  // they NEVER reach the LLM. "delegate-job-result" MESSAGES (pi.sendMessage)
  // DO participate in LLM context; the renderer only restyles the transcript.
  pi.registerEntryRenderer<JobCardData>("delegate-job", (entry, options, theme) =>
    renderJobCardEntry(entry, options, theme),
  );
  pi.registerMessageRenderer<JobResultDetails>("delegate-job-result", (message, options, theme) =>
    renderJobResultMessage(message, options, theme),
  );

  // ctrl+\: byte 0x1C, which legacy terminals deliver directly and pi-tui's
  // parseKey maps to "ctrl+\\" (keys.js:1104). Unbound anywhere: pi core
  // defaults, user keybindings.json, and no extension use it. Rejected:
  // ctrl+y = tui.editor.yank (extension wins, yank breaks); ctrl+i/m/h =
  // legacy C0 collisions with tab/enter/backspace; ctrl+j = newline.
  pi.registerShortcut("ctrl+\\", {
    description: "Open the background jobs view (same as /jobs)",
    handler: async (ctx) => {
      await openJobsView(ctx);
    },
  });

  // ── Surfacing boundaries (./surfacing.ts) ─────────────────────────────
  // Round-start job injection, the settle auto-surface and the ambient footer
  // clear are registered by one call, in the order they had when they were
  // inline here: a settle clears the live counts, then surfaces finished jobs,
  // and only then reaches the worker-settle gate below.
  registerSurfacingHooks(pi);

  // Batch 1 + batch 2: WORKER-SETTLE gate — the scoped ladder at settle, the
  // receipt, the bounded continue:true with its exponential backoff, the
  // reconciliation-lite diff against the newest passing receipt, and the
  // one-shot TERMINAL turn. ./gates.ts owns the body and the settle state.
  registerWorkerSettleHook(pi);
}

/**
 * Merge-back gate: THE job-scope resolver and THE prove-receipt policy.
 *
 * Why this module exists (defect 2): "which rules does this job own?" used to
 * have TWO answers. The approval relay in ./jobs.ts parsed the job's task text,
 * the merge-path block read the claims log — so a job whose claim
 * named rule ids but whose task text carried no `SPEC-SCOPE` line could be
 * relay-approved and then merge-blocked on the very same merge-back. One
 * resolver, one answer: both consumers call `resolveJobRuleIds`, so
 * "relay-approved but merge-blocked on the same scope" is structurally
 * impossible.
 *
 * Scope precedence (documented here once, implemented once):
 *
 *   1. CLAIMS FIRST. The job's spawn claims in the claims log (./claims.ts,
 *      `<claims_dir>/<job-id>/claims.jsonl`) record the `ruleIds` of every
 *      worker spawn under that job. The union of those ids, in log order,
 *      deduped, IS the job's scope. Rule ids are read from `claim` records
 *      (`ruleIds`, `module`); `completed` records are ignored (they carry the
 *      same ids as their claim and would change nothing but order).
 *   2. TASK-TEXT FALLBACK. Only when the claims carry NO rule id at all does
 *      the resolver read the job's task text from the jobs registry
 *      (`<cwd>/.pi/jobs/<job-id>.json`, `task`) and parse its `SPEC-SCOPE`
 *      line with ./protocol.ts's lenient `parseSpecScope`. The registry is read
 *      HERE (never handed in by a caller), so every caller sees the same text.
 *   3. NO SCOPE ⇒ NO RULES. Neither source names a rule id ⇒ `[]` ⇒ the
 *      merge-back needs no prove receipts (a non-rule-change batch merges
 *      freely). This is the fail-open-to-EMPTY direction on purpose: the
 *      scope read is telemetry-shaped, while the prove verdict built on top of
 *      it stays fail-closed (`checkProveCoverage`).
 *
 * The prove-receipt policy also lives here — one copy of everything that reads
 * a receipt as merge-back evidence: `proveRuleIdsIn` (which rule ids a prove
 * run's argv covered), `isProveSegment` (is this command word sequence the
 * declared prove command?), the ONE `--rule <id>` matcher (built from
 * `RULE_FLAG_SOURCE`, so the scanner and the per-rule matcher can never drift),
 * the prove-receipt note test, the all-days receipt reader and
 * `checkProveCoverage`. VERIFIED is the only verdict that counts: a receipt
 * covers a rule iff it is `ok === true`, noted `prove …`, with a command that
 * mentions `--rule <id>` on a token boundary AND exited 0, at the reviewed
 * HEAD when one is given.
 *
 * Receipts are HOST-ONLY: `receiptsRoot` is the calling host session's own repo
 * root, never a worker's worktree (a mount could otherwise carry a pre-forged
 * VERIFIED receipt).
 *
 * Import direction: this module imports ./claims.ts, ./gates.ts, ./policy.ts
 * and ./protocol.ts, and NOT ./jobs.ts — jobs.ts imports this module (the relay
 * is one of the two consumers), and the jobs registry is read directly so the
 * module stays importable from a pi-free process (smoke-gates.mjs imports it).
 * The registry path shape is `jobs.ts` `jobsDirFor`'s (`<cwd>/.pi/jobs`), pinned
 * by a smoke assertion.
 *
 * Loader gotcha (pi 1.5.2 extension parser): avoid `?:` ternaries containing
 * double-quoted strings inside a template literal's ${} — this module sticks to
 * string concatenation, so nothing here can trip that parser.
 */

import * as fs from "node:fs";
import * as path from "node:path";
import type { ExtensionAPI, ToolResultEvent } from "@earendil-works/pi-coding-agent";
import { DEFAULT_CLAIMS_DIR, loadWorkflowPolicy, workflowPolicyRefusal } from "./policy.ts";
import {
  type ReceiptRecord,
  detectMergeBackCommands,
  logGateDebug,
  parseGateVmShellCommand,
  parseReceiptLines,
  splitCommandLine,
  splitTopLevelSegments,
  writeReceipt,
} from "./gates.ts";
import { type ClaimsRecord, jobIdForEnv, readClaims } from "./claims.ts";
import { parseSpecScope } from "./protocol.ts";
import { gateSandboxFor, isSandboxedChild } from "./sbx.ts";

// ── The job-scope resolver (ONE answer) ─────────────────────────────────────

/** A job reference: its registry id, or any entry that carries one. */
export type JobScopeRef = string | { id: string };

/** Where a resolved scope came from (diagnostics only — never a decision). */
export type JobScopeSource = "claim" | "task" | "none";

/** The scope ONE job owns: the rule ids the merge-back must prove. */
export interface JobScope {
  ruleIds: string[];
  module?: string;
  source: JobScopeSource;
}

/**
 * `<cwd>/.pi/jobs/<id>.json` — the jobs registry entry the runner writes (see
 * ./jobs.ts `jobsDirFor`; `.pi/jobs` is also the policy loader's default claims
 * dir, co-located by design). smoke-gates.mjs writes this exact shape and reads
 * it back through the resolver, so the two stay pinned together.
 */
function registryEntryPath(cwd: string, jobId: string): string {
  return path.join(cwd, DEFAULT_CLAIMS_DIR, jobId + ".json");
}

/**
 * The job id of a reference, or "" when it can never name a registry entry.
 * A path separator or a dot-dot would escape the registry dir, so it resolves
 * to "no scope" instead of probing the filesystem.
 */
function jobIdOf(job: JobScopeRef): string {
  const raw = typeof job === "string" ? job : job && typeof job.id === "string" ? job.id : "";
  const id = raw.trim();
  if (!id || id === "." || id === ".." || /[\\/]/.test(id)) return "";
  return id;
}

/** Rule ids trimmed, non-empty, deduped — the shape every consumer compares. */
function normalizeRules(values: readonly string[]): string[] {
  const rules: string[] = [];
  for (const value of values) {
    if (typeof value !== "string") continue;
    const rule = value.trim();
    if (rule.length === 0 || rules.includes(rule)) continue;
    rules.push(rule);
  }
  return rules;
}

/**
 * The union of the job's SPAWN CLAIM rule ids (claims log, log order, deduped)
 * plus the first module any claim names. A readable-but-empty log, a missing
 * log and a claims read error all yield "no claim scope" — the caller then
 * falls back to the registry task text (today's fail-open telemetry behavior).
 */
async function claimScope(cwd: string, jobId: string): Promise<{ ruleIds: string[]; module?: string }> {
  const ruleIds: string[] = [];
  let module: string | undefined;
  let records: ClaimsRecord[] = [];
  try {
    records = await readClaims(cwd, jobId);
  } catch (error) {
    console.error("[delegate] claims read failed for the job-scope resolver:", error);
    records = [];
  }
  for (const record of records) {
    if (record.type !== "claim") continue;
    for (const rule of normalizeRules(record.ruleIds)) {
      if (!ruleIds.includes(rule)) ruleIds.push(rule);
    }
    if (!module && record.module) module = record.module;
  }
  return { ruleIds, module };
}

/**
 * The job's task text from the jobs registry, or "" when the entry is absent or
 * unreadable. Read here (not by a caller) so both consumers see one text.
 */
async function registryTaskText(cwd: string, jobId: string): Promise<string> {
  let raw: string;
  try {
    raw = await fs.promises.readFile(registryEntryPath(cwd, jobId), "utf8");
  } catch {
    return "";
  }
  try {
    const parsed: unknown = JSON.parse(raw);
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) return "";
    const task = (parsed as { task?: unknown }).task;
    return typeof task === "string" ? task : "";
  } catch {
    return "";
  }
}

/**
 * THE job-scope resolver: claims first, registry task text second, nothing
 * third. Both merge-back consumers (./jobs.ts `relayApprovalDecision` and the
 * merge-path prove hook in this module) call this — there is no other
 * derivation of "which rules does this job own".
 */
export async function resolveJobScope(cwd: string, job: JobScopeRef): Promise<JobScope> {
  const jobId = jobIdOf(job);
  if (!jobId) return { ruleIds: [], source: "none" };

  const claimed = await claimScope(cwd, jobId);
  if (claimed.ruleIds.length > 0) {
    return { ruleIds: claimed.ruleIds, module: claimed.module, source: "claim" };
  }

  const scope = parseSpecScope(await registryTaskText(cwd, jobId));
  const rules = scope ? normalizeRules(scope.rules) : [];
  if (rules.length === 0) return { ruleIds: [], module: scope?.module, source: "none" };
  return { ruleIds: rules, module: scope?.module, source: "task" };
}

/** The rule ids the job owns — the one-answer projection both consumers use. */
export async function resolveJobRuleIds(cwd: string, job: JobScopeRef): Promise<string[]> {
  const scope = await resolveJobScope(cwd, job);
  return scope.ruleIds;
}

// ── The prove-receipt policy (ONE copy) ─────────────────────────────────────

/**
 * The single `--rule` matcher source: `--rule <id>` or `--rule=<id>`. Both the
 * argv scanner below and the per-rule receipt matcher are built from it, so the
 * two can never disagree about what "this receipt covers that rule" means.
 */
const RULE_FLAG_SOURCE = "--rule(?:=|\\s+)";
/** Rule ids are `[A-Za-z0-9_-]` (protocol.ts `RULE_ID`), used as a token class. */
const RULE_ID_TOKEN = "[A-Za-z0-9_-]";

/**
 * `--rule <id>` / `--rule=<id>` occurrences in a command line (deduped, order
 * kept). The exit code decides VERIFIED; this only says which rules the run
 * named.
 */
export function proveRuleIdsIn(command: string): string[] {
  const ids: string[] = [];
  const pattern = new RegExp(RULE_FLAG_SOURCE + "(" + RULE_ID_TOKEN + "+)", "g");
  let match: RegExpExecArray | null;
  while ((match = pattern.exec(command)) !== null) {
    if (!ids.includes(match[1])) ids.push(match[1]);
  }
  return ids;
}

/** Escape one rule id for the `--rule` matcher (ids are [A-Za-z0-9_-]). */
function escapeRegExp(text: string): string {
  return text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

/**
 * The `--rule <id>` / `--rule=<id>` matcher for ONE rule, built from the same
 * source as `proveRuleIdsIn`. The trailing token boundary is load-bearing: a
 * receipt for R-GAME-001 must never count as covering R-GAME-0011 (nor the
 * reverse) — a substring match would silently widen what a green scoped run is
 * allowed to prove.
 */
export function ruleFlagPattern(rule: string): RegExp {
  return new RegExp(RULE_FLAG_SOURCE + escapeRegExp(rule) + "(?!" + RULE_ID_TOKEN + ")");
}

/**
 * Is this receipt a PROVE receipt? Only extension-written prove receipts are
 * prove evidence: the writer notes them `prove rules=<id,...>`. Anything else
 * may share a command line (a settle gate whose scope env ran `--rule`) and
 * must not launder into coverage.
 */
export function isProveReceipt(record: ReceiptRecord): boolean {
  return /^prove\b/i.test(record.note.trim());
}

/**
 * Does this command segment's word sequence begin with the declared prove
 * tokens (`make prove`)? Word-exact: `make proven` and `echo make prove` are
 * not prove runs, so a receipt cannot be minted by mentioning the command.
 */
export function isProveSegment(segment: string, proveTokens: string[]): boolean {
  if (proveTokens.length === 0) return false;
  const words = splitCommandLine(segment);
  if (words.length < proveTokens.length) return false;
  for (let index = 0; index < proveTokens.length; index++) {
    if (words[index] !== proveTokens[index]) return false;
  }
  return true;
}

/** Did any command of this receipt mention `--rule <rule>`? */
function receiptMentionsRule(record: ReceiptRecord, pattern: RegExp): boolean {
  return record.commands.some(
    (entry) => typeof entry?.command === "string" && pattern.test(entry.command),
  );
}

/** Did a command mentioning this rule actually exit 0? */
function receiptVerifiesRule(record: ReceiptRecord, pattern: RegExp): boolean {
  return record.commands.some(
    (entry) => entry?.exitCode === 0 && typeof entry.command === "string" && pattern.test(entry.command),
  );
}

/**
 * Every readable `<dir>/*.jsonl` record, all days, oldest file name first.
 * Jobs cross midnight and a prove run can land in yesterday's file, so the
 * reader takes the whole (small) receipts dir rather than today's log only.
 * Malformed lines are skipped exactly like gates.ts `readDayReceipts`; a
 * missing or unreadable dir is "no receipts", never a throw — the receipt log
 * is telemetry. Fail-closed decisions are made on top of it, in
 * `checkProveCoverage`.
 */
async function readAllReceipts(dir: string): Promise<{ records: ReceiptRecord[]; files: number }> {
  let names: string[];
  try {
    names = await fs.promises.readdir(dir);
  } catch {
    return { records: [], files: 0 };
  }
  const dayFiles = names.filter((name) => name.endsWith(".jsonl")).sort();
  const records: ReceiptRecord[] = [];
  for (const name of dayFiles) {
    try {
      records.push(...parseReceiptLines(await fs.promises.readFile(path.join(dir, name), "utf8")));
    } catch {
      /* unreadable day file — skip (telemetry only) */
    }
  }
  return { records, files: dayFiles.length };
}

/**
 * The merge-back PROVE verdict. `missing` is what the caller prints: every rule
 * id that has no VERIFIED prove receipt behind it, verbatim.
 */
export interface ProveCoverageResult {
  ok: boolean;
  covered: string[];
  missing: string[];
  report: string;
}

/**
 * Merge-back PROVE coverage: does the HOST receipts root hold a VERIFIED prove
 * receipt for EVERY scoped rule?
 *
 * `receiptsRoot` is the HOST SESSION's own repo root (its cwd) — receipts are
 * host-only artifacts and are never read out of a worker's worktree, where a
 * worker could pre-forge one. The optional `opts.worktreePath` is reported for
 * context only; it does not change where evidence is read from.
 *
 * A rule R is covered iff one receipt record carries ALL of:
 *   - `ok === true`,
 *   - a note starting with "prove" (the extension-written prove receipt; an
 *     ordinary green gate receipt is not prove evidence),
 *   - a commands entry with `exitCode === 0` whose command mentions
 *     `--rule <R>` or `--rule=<R>` on a token boundary,
 *   - and, when `expectedHeadSha` is given, `headSha` equal to it.
 *
 * FAIL CLOSED: an unavailable WORKFLOW.md policy is `ok:false` (no receipts
 * dir, no declared prove command ⇒ no evidence can be trusted), and every
 * missing rule is named verbatim in `report` with the reason it does not
 * count (no receipt at all / non-VERIFIED exit / stale headSha).
 */
export async function checkProveCoverage(
  receiptsRoot: string,
  rules: string[],
  opts: { expectedHeadSha?: string; worktreePath?: string } = {},
): Promise<ProveCoverageResult> {
  const scoped = rules
    .filter((rule): rule is string => typeof rule === "string")
    .map((rule) => rule.trim())
    .filter((rule) => rule.length > 0);
  if (scoped.length === 0) {
    return {
      ok: true,
      covered: [],
      missing: [],
      report: "PROVE coverage: no scoped rules \u2014 the merge-back needs no prove receipts.",
    };
  }

  const loaded = loadWorkflowPolicy(receiptsRoot);
  if (!loaded.ok) {
    return {
      ok: false,
      covered: [],
      missing: scoped,
      report:
        "PROVE coverage REFUSED: " + workflowPolicyRefusal(loaded.reason) +
        " (scoped rules: " + scoped.join(", ") + ")",
    };
  }

  const dir = path.join(receiptsRoot, loaded.policy.receiptsDir);
  const { records, files } = await readAllReceipts(dir);
  const proveRecords = records.filter((record) => isProveReceipt(record));
  const expectedHeadSha = opts.expectedHeadSha ? opts.expectedHeadSha.trim().toLowerCase() : "";

  const covered: string[] = [];
  const missing: string[] = [];
  const notes: string[] = [];
  for (const rule of scoped) {
    const pattern = ruleFlagPattern(rule);
    const candidates = proveRecords.filter((record) => receiptMentionsRule(record, pattern));
    if (candidates.length === 0) {
      missing.push(rule);
      notes.push("- " + rule + ": MISSING \u2014 no prove receipt at all");
      continue;
    }
    const verified = candidates.filter((record) => record.ok === true && receiptVerifiesRule(record, pattern));
    if (verified.length === 0) {
      missing.push(rule);
      notes.push("- " + rule + ": MISSING \u2014 receipt exists but non-VERIFIED (the scoped run did not exit 0)");
      continue;
    }
    if (expectedHeadSha) {
      const atReviewedHead = verified.filter((record) => record.headSha.toLowerCase() === expectedHeadSha);
      if (atReviewedHead.length === 0) {
        const recorded = verified.map((record) => record.headSha || "(not recorded)").join(", ");
        missing.push(rule);
        notes.push(
          "- " + rule + ": MISSING \u2014 receipt stale (headSha mismatch: expected " + expectedHeadSha +
          ", recorded " + recorded + ")",
        );
        continue;
      }
    }
    covered.push(rule);
    notes.push("- " + rule + ": covered by a VERIFIED prove receipt");
  }

  const lines: string[] = ["\u2500\u2500 PROVE COVERAGE (merge-back) \u2500\u2500"];
  lines.push("receipts root (host): " + receiptsRoot);
  if (opts.worktreePath) lines.push("worktree: " + opts.worktreePath);
  lines.push(
    "receipts: " + dir + " (" + String(files) + " jsonl file(s), " + String(records.length) + " record(s))",
  );
  if (expectedHeadSha) lines.push("reviewed HEAD: " + expectedHeadSha);
  for (const note of notes) lines.push(note);
  lines.push(
    missing.length === 0
      ? "status: ok \u2014 every scoped rule has a VERIFIED prove receipt."
      : "status: BLOCKED \u2014 missing VERIFIED prove receipts for " + missing.join(", ") + ".",
  );
  return { ok: missing.length === 0, covered, missing, report: lines.join("\n") };
}

// ── Hook registration: the merge path's two entry points ────────────────────
//
// Moved out of index.ts by B3.4 (pure motion). These are the two places the
// extension can OBSERVE a merge-back, and both defer to the policy above:
// `registerMergePathProveHook` blocks an executing merge, and
// `registerProveReceiptHook` writes the evidence the block reads back.

/**
 * MERGE-PATH PROVE enforcement (sandbox era). This hook closes the
 * typed-in-pane bypass: a confirm-handoff user can type "approved" straight
 * into the orchestrator's tmux pane, which never passes through job_approve.
 * The precondition therefore lives on the MERGE EXECUTION PATH, not on the
 * approval path: in an orchestrator host session, any `git merge …` (including
 * the `git -C <worktree> merge` form) is blocked when the job's RECORDED spec
 * scope names rules that lack VERIFIED prove receipts host-side. A job with no
 * recorded SPEC-SCOPE merges freely. Host-side only — an in-VM instance never
 * registers this (the inert-mode early return in the root).
 *
 * The scope is `resolveJobRuleIds` — THE resolver (claims first, registry task
 * text second), the same call the approval relay in jobs.ts makes, so this
 * block and a relay approval can never disagree about the job's scope
 * (defect 2).
 */
export function registerMergePathProveHook(pi: ExtensionAPI): void {
  pi.on("tool_call", async (event, ctx) => {
    if (process.env.PI_DELEGATE_ROLE !== "orchestrator") return;
    if (isSandboxedChild()) return;
    if (event.toolName !== "bash") return;
    const input = event.input as { command?: unknown };
    if (typeof input.command !== "string" || input.command.trim().length === 0) return;
    const merges = detectMergeBackCommands(input.command).filter((entry) => !entry.controlOnly);
    if (merges.length === 0) return;

    const jobId = jobIdForEnv();
    const rules = await resolveJobRuleIds(ctx.cwd, jobId);
    if (rules.length === 0) return;

    let coverage: Awaited<ReturnType<typeof checkProveCoverage>>;
    try {
      coverage = await checkProveCoverage(ctx.cwd, rules, { worktreePath: merges[0].worktree ?? undefined });
    } catch (error) {
      // Fail closed: an internal error is a refusal, never a pass.
      coverage = {
        ok: false,
        covered: [],
        missing: rules,
        report:
          "PROVE coverage check crashed: " + (error instanceof Error ? error.message : String(error)) +
          " (scoped rules: " + rules.join(", ") + ")",
      };
    }
    if (coverage.ok) return;

    return {
      block: true,
      reason:
        "MERGE BLOCKED \u2014 PROVE receipts missing for this merge-back (HANDOFF_BLOCKED). " +
        "The job's recorded SPEC-SCOPE names " + rules.join(", ") + ", so the merge execution path " +
        "requires a VERIFIED host-side prove receipt for EVERY scoped rule \u2014 no matter how the " +
        "approval arrived (typed in the pane or relayed via job_approve). Missing: " +
        coverage.missing.join(", ") + ". " +
        "Run `make prove args='--rule <id>'` once per scoped rule (the bash gate hook " +
        "dispatches it into the gate VM; the extension records the receipt in the host receipts dir), " +
        "then retry the merge.\n" + coverage.report,
    };
  });
}

/**
 * The directory of a leading `cd <dir>` segment, or null. Only the FIRST
 * segment counts: the orchestrator's handoff form is
 * `cd ../lt-api-wt-<id> && make prove args='--rule <id>'`, and a cd later in
 * the chain says nothing about the command that followed.
 *
 * IDENTIFICATION ONLY. In the sandbox era a `cd` never redirects the receipt
 * (receipts resolve against the host session's own root): routing evidence into
 * the worktree would let a worker pre-forge a VERIFIED receipt inside its mount.
 */
function leadingCdTarget(segment: string): string | null {
  const words = splitCommandLine(segment);
  if (words.length !== 2 || words[0] !== "cd") return null;
  return words[1];
}

/** Non-negative integer or null for anything else (never assume a code). */
function exitCodeNumber(value: unknown): number | null {
  if (typeof value === "number" && Number.isInteger(value)) return value;
  return null;
}

/**
 * Exit code of a bash tool result. pi 1.5.2 carries it in the result's
 * `structuredContent.exit_code`; the details object is typed without it, so
 * every location is read defensively and a status line in the content is the
 * last resort. Unreadable ⇒ null (a null exitCode is never ok:true), never 0.
 */
function bashExitCodeOf(event: ToolResultEvent): number | null {
  const details = event.details as { exitCode?: unknown; exit_code?: unknown } | undefined;
  const fromDetails = exitCodeNumber(details?.exitCode) ?? exitCodeNumber(details?.exit_code);
  if (fromDetails !== null) return fromDetails;
  const structured = event.structuredContent as { exit_code?: unknown } | undefined;
  const fromStructured = exitCodeNumber(structured?.exit_code);
  if (fromStructured !== null) return fromStructured;
  for (const part of event.content) {
    if (part.type !== "text") continue;
    const match = /Command exited with code (-?\d+)\s*$/.exec(part.text.trim());
    if (match) return Number(match[1]);
  }
  return null;
}

/**
 * PROVE receipt writer (merge-back evidence). The extension records every
 * scoped prove run, because the merge-back gates (`checkProveCoverage`, called
 * by the jobs.ts approval relay and the merge-path hook above) trust host
 * receipts and nothing else — and no worker ever writes one. Active in CHILD
 * sessions only (orchestrators): prove is the orchestrator's merge-back step,
 * and a main session has no job to prove for. HOST-ONLY resolution: the
 * receipt always lands in THIS session's own receipts dir. There is
 * deliberately NO leading-`cd` redirect — routing evidence into the worktree
 * would let a worker pre-forge a VERIFIED receipt inside its own mount.
 */
export function registerProveReceiptHook(pi: ExtensionAPI): void {
  pi.on("tool_result", async (event, ctx) => {
    if (process.env.PI_DELEGATE_CHILD !== "1") return;
    if (isSandboxedChild()) return;
    if (event.toolName !== "bash") return;
    const input = event.input as { command?: unknown };
    if (typeof input.command !== "string" || input.command.trim().length === 0) return;
    const command = input.command;
    const ruleIds = proveRuleIdsIn(command);
    if (ruleIds.length === 0) return;

    const segments = splitTopLevelSegments(command)
      .map((segment) => segment.trim())
      .filter((segment) => segment.length > 0);
    if (segments.length === 0) return;

    // A leading `cd <worktree>` is still tolerated for COMMAND IDENTIFICATION
    // (the orchestrator's handoff form), but it never moves the receipt: proofs
    // are host artifacts.
    const cdTarget = leadingCdTarget(segments[0]);
    const rest = cdTarget !== null ? segments.slice(1) : segments;
    const receiptCwd = ctx.cwd;

    // The prove command is DECLARED (WORKFLOW.md `prove_cmd`), never
    // hardcoded: an unavailable policy means the gate set is undeclared, so no
    // run can be identified as prove — log it and write nothing (the
    // merge-back gate then refuses for the missing policy anyway).
    const loaded = loadWorkflowPolicy(receiptCwd);
    if (!loaded.ok) {
      void logGateDebug(receiptCwd, "prove receipt skipped (policy unavailable): " + command);
      return;
    }
    const proveTokens = splitCommandLine(loaded.policy.proveCmd);
    // The command may already be the REWRITTEN `sbx exec` line (the gate-VM
    // tool_call hook ran before this tool_result). Unwrap it: the in-VM argv is
    // what identifies a prove run, and `-w` names the worktree whose gate VM
    // executed it. A plain (unrewritten) command keeps the original matching.
    const unwrapped = parseGateVmShellCommand(command);
    const proves =
      unwrapped !== null
        ? isProveSegment(unwrapped.argv.join(" "), proveTokens)
        : rest.some((segment) => isProveSegment(segment, proveTokens));
    if (!proves) return;

    const exitCode = bashExitCodeOf(event);
    try {
      // The prove command ran in the gate VM (the bash tool_call hook rewrites
      // a declared gate command to `sbx exec`), so the receipt names the VM it
      // executed in. A missing VM entry simply means the receipt predates the
      // rewrite (or the command was identified after the fact) — no key.
      const proveCwd =
        unwrapped && unwrapped.worktree
          ? unwrapped.worktree
          : cdTarget !== null
            ? path.resolve(ctx.cwd, cdTarget)
            : ctx.cwd;
      await writeReceipt({
        cwd: receiptCwd,
        agent: process.env.PI_DELEGATE_ROLE ?? "worker",
        session: process.env.PI_SESSION_ID,
        // The FULL command line, so the receipt names the run a human/checker
        // reads back (including the `cd`); its exit code decides VERIFIED.
        commands: [{ command, exitCode, timedOut: false, durationMs: 0 }],
        ok: exitCode === 0,
        note: "prove rules=" + ruleIds.join(","),
        sandbox: gateSandboxFor(proveCwd)?.name,
      });
    } catch (error) {
      console.error("[delegate] prove receipt failed:", error);
    }
  });
}

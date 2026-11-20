/**
 * Claims log + completion broadcasts (batch 2).
 *
 * Why: parallel workers (and a child orchestrator's workers) can be handed
 * overlapping scopes by mistake — two spawns touching the same rule ids, the
 * same module, or the same paths, each rewriting the other's files and then
 * failing the settle gate for reasons neither can see. The claims log is a
 * shared, append-only JSONL file under the job directory:
 *
 *   <cwd>/.pi/jobs/<jobId>/claims.jsonl
 *
 * with two record kinds:
 *   {type:"claim",     claimId, worker, sessionId, ruleIds, module, ownedPaths, startedAt, baseline}
 *   {type:"completed", claimId, worker, sessionId, ruleIds, changedPaths, finishedAt, ok, note?}
 *
 * A claim is ACTIVE until a completion with the same claimId appears. Before a
 * spawn the extension reads the log and BLOCKS overlap (rule ids, module,
 * owned paths); after a worker returns it appends the completion with the paths
 * that changed relative to the claim's `git status --porcelain` baseline, and
 * the NEXT worker's prompt carries a sibling-context broadcast of the most
 * recent completions.
 *
 * The extension writes this log; workers never do (their `.pi/**` writes stay
 * denied by gates.ts gate-file protection). Everything is telemetry: git
 * failures fail OPEN (`changedPaths: []` + `note: "git-status-failed"`), while
 * the gate verdicts stay fail-closed where they live.
 */

import * as fs from "node:fs";
import * as path from "node:path";
import { GIT_TIMEOUT_MS, matchGlob, parseSpecScope, runGate } from "./gates.ts";

/** How many recent completions ride along in a task prompt. */
export const SIBLING_CONTEXT_MAX = 3;
/** Line prefix that carries a spawn's owned paths in the task text. */
export const CLAIMS_PATHS_PREFIX = "CLAIMS-PATHS:";

export interface ChangedPath {
  /** Porcelain status letter(s): "M", "A", "??", "D", … */
  status: string;
  path: string;
}

export interface ClaimRecord {
  type: "claim";
  claimId: string;
  worker: string;
  sessionId: string;
  ruleIds: string[];
  module?: string;
  ownedPaths: string[];
  startedAt: string;
  /** `git status --porcelain` lines at spawn time (empty when git failed). */
  baseline: string[];
}

export interface CompletionRecord {
  type: "completed";
  claimId: string;
  worker: string;
  sessionId: string;
  ruleIds: string[];
  changedPaths: ChangedPath[];
  finishedAt: string;
  ok: boolean;
  note?: string;
}

export type ClaimsRecord = ClaimRecord | CompletionRecord;

/** One spawn the caller is about to make (worker name + task text). */
export interface SpawnRequest {
  worker: string;
  task: string;
}

/** The claim-relevant subset of a task text. */
export interface ClaimCandidate {
  worker: string;
  ruleIds: string[];
  module?: string;
  ownedPaths: string[];
}

// ── Identity / paths ────────────────────────────────────────────────────────

/**
 * Job id for the claims log: `PI_DELEGATE_JOB_ID` (exported into a background
 * orchestrator's env by jobs.ts) when present, else a session-derived id.
 */
export function jobIdForEnv(env: NodeJS.ProcessEnv = process.env): string {
  const jobId = env.PI_DELEGATE_JOB_ID;
  if (typeof jobId === "string" && jobId.trim()) return sanitizeJobId(jobId.trim());
  const session = env.PI_SESSION_ID;
  const base = typeof session === "string" && session.trim() ? session.trim() : String(process.pid);
  return sanitizeJobId("session-" + base);
}

function sanitizeJobId(value: string): string {
  const cleaned = value.replace(/[^A-Za-z0-9._-]+/g, "_").slice(0, 80).replace(/^_+|_+$/g, "");
  return cleaned || "session-unknown";
}

/** `<cwd>/.pi/jobs/<jobId>/claims.jsonl`. */
export function claimsLogPath(cwd: string, jobId: string): string {
  return path.join(cwd, ".pi", "jobs", sanitizeJobId(jobId), "claims.jsonl");
}

// ── Task-text parsing ───────────────────────────────────────────────────────

/**
 * Parse an optional `CLAIMS-PATHS: <comma-separated globs>` line. Same shape
 * as the SPEC-SCOPE convention: only trimmed lines starting with the prefix
 * count, the first usable one wins, and unusable entries are dropped.
 */
export function parseClaimsPaths(text: string): string[] {
  for (const rawLine of text.split("\n")) {
    const line = rawLine.trim();
    if (!line.toUpperCase().startsWith(CLAIMS_PATHS_PREFIX)) continue;
    const body = line.slice(CLAIMS_PATHS_PREFIX.length);
    const paths = body
      .split(",")
      .map((entry) => entry.trim().replace(/^\.\//, "").replace(/\/+$/, ""))
      .filter((entry) => entry.length > 0 && !/[\s]/.test(entry));
    if (paths.length > 0) return paths;
  }
  return [];
}

/** Claim-relevant fields of one task text (SPEC-SCOPE + CLAIMS-PATHS). */
export function claimCandidateOf(worker: string, task: string): ClaimCandidate {
  const scope = parseSpecScope(task);
  return {
    worker,
    ruleIds: scope ? [...scope.rules] : [],
    module: scope?.module,
    ownedPaths: parseClaimsPaths(task),
  };
}

// ── Glob intersection (approximate, in the safe direction) ──────────────────

function normalizeGlob(value: string): string {
  return value.split(path.sep).join("/").replace(/^\.\//, "").replace(/\/+$/, "");
}

/** Literal leading segments of a glob (everything before the first wildcard). */
export function literalPrefix(glob: string): string {
  const segments = normalizeGlob(glob).split("/");
  const literal: string[] = [];
  for (const segment of segments) {
    if (/[*?]/.test(segment)) break;
    literal.push(segment);
  }
  return literal.join("/");
}

/**
 * Do two repo-relative globs plausibly touch the same path? Approximate by
 * design (exact glob-glob intersection is not decidable cheaply) and
 * deliberately biased toward "yes": a false positive blocks one spawn and the
 * operator can override, a false negative corrupts a worktree.
 */
export function globsIntersect(first: string, second: string): boolean {
  const a = normalizeGlob(first);
  const b = normalizeGlob(second);
  if (!a || !b) return false;
  if (a === b) return true;
  const prefixA = literalPrefix(a);
  const prefixB = literalPrefix(b);
  if (prefixA && matchGlob(b, prefixA)) return true;
  if (prefixB && matchGlob(a, prefixB)) return true;
  if (prefixA && prefixB) {
    if (prefixA === prefixB) return true;
    if ((prefixA + "/").startsWith(prefixB + "/")) return true;
    if ((prefixB + "/").startsWith(prefixA + "/")) return true;
  }
  return false;
}

// ── Overlap check ───────────────────────────────────────────────────────────

export interface ClaimConflict {
  claim: ClaimRecord;
  /** Every overlapping dimension, e.g. ["ruleIds R-1", "module game"]. */
  reasons: string[];
}

/**
 * First ACTIVE claim the candidate overlaps, or null. Overlap dimensions:
 * shared rule id, equal non-empty module, intersecting owned-path globs.
 */
export function findClaimConflict(candidate: ClaimCandidate, active: ClaimRecord[]): ClaimConflict | null {
  const ruleIds = new Set(candidate.ruleIds);
  for (const claim of active) {
    const reasons: string[] = [];
    const sharedRules = claim.ruleIds.filter((rule) => ruleIds.has(rule));
    if (sharedRules.length > 0) reasons.push("ruleIds " + sharedRules.join(","));
    if (candidate.module && claim.module && candidate.module === claim.module) {
      reasons.push("module " + candidate.module);
    }
    const sharedPaths: string[] = [];
    for (const owned of candidate.ownedPaths) {
      for (const claimed of claim.ownedPaths) {
        if (globsIntersect(owned, claimed)) {
          sharedPaths.push(owned + " ~ " + claimed);
          break;
        }
      }
    }
    if (sharedPaths.length > 0) reasons.push("paths " + sharedPaths.join("; "));
    if (reasons.length > 0) return { claim, reasons };
  }
  return null;
}

/** Operator-facing block text, including the override instruction. */
export function formatClaimConflict(conflict: ClaimConflict): string {
  const claim = conflict.claim;
  const scope: string[] = [];
  if (claim.ruleIds.length > 0) scope.push("rules=" + claim.ruleIds.join(","));
  if (claim.module) scope.push("module=" + claim.module);
  if (claim.ownedPaths.length > 0) scope.push("paths=" + claim.ownedPaths.join(","));
  return (
    "\u2500\u2500 SPAWN BLOCKED \u2014 CLAIM OVERLAP \u2500\u2500\n" +
    "An active claim already owns this work:\n" +
    "- worker: " + claim.worker + " \u00b7 session: " + (claim.sessionId || "(none)") + "\n" +
    "- scope: " + (scope.join(" ") || "(none)") + "\n" +
    "- overlap: " + conflict.reasons.join("; ") + "\n" +
    "- startedAt: " + claim.startedAt + " (claimId " + claim.claimId + ")\n" +
    "Wait for that worker to finish (its completion releases the claim), narrow this spawn's " +
    "SPEC-SCOPE/CLAIMS-PATHS, or set PI_DELEGATE_CLAIMS_OVERRIDE=1 in the main session to override.\n" +
    "No child was launched and no claim was recorded."
  );
}

/** `process.env.PI_DELEGATE_CLAIMS_OVERRIDE === "1"` — only the orchestrating
 * session can set it (a worker child cannot set its own env). */
export function claimsOverrideEnabled(env: NodeJS.ProcessEnv = process.env): boolean {
  return env.PI_DELEGATE_CLAIMS_OVERRIDE === "1";
}

// ── Log I/O ─────────────────────────────────────────────────────────────────

/** All readable records (append order); malformed lines are skipped. */
export async function readClaims(cwd: string, jobId: string): Promise<ClaimsRecord[]> {
  const file = claimsLogPath(cwd, jobId);
  let raw: string;
  try {
    raw = await fs.promises.readFile(file, "utf8");
  } catch {
    return [];
  }
  const records: ClaimsRecord[] = [];
  for (const line of raw.split("\n")) {
    if (!line.trim()) continue;
    let parsed: Record<string, unknown>;
    try {
      const value: unknown = JSON.parse(line);
      if (!value || typeof value !== "object" || Array.isArray(value)) continue;
      parsed = value as Record<string, unknown>;
    } catch {
      continue; /* malformed claims line — skip */
    }
    const claimId = asString(parsed.claimId);
    if (!claimId) continue;
    if (parsed.type === "claim") {
      records.push({
        type: "claim",
        claimId,
        worker: asString(parsed.worker),
        sessionId: asString(parsed.sessionId),
        ruleIds: asStringArray(parsed.ruleIds),
        module: asString(parsed.module) || undefined,
        ownedPaths: asStringArray(parsed.ownedPaths),
        startedAt: asString(parsed.startedAt),
        baseline: asStringArray(parsed.baseline),
      });
      continue;
    }
    if (parsed.type === "completed") {
      records.push({
        type: "completed",
        claimId,
        worker: asString(parsed.worker),
        sessionId: asString(parsed.sessionId),
        ruleIds: asStringArray(parsed.ruleIds),
        changedPaths: asChangedPaths(parsed.changedPaths),
        finishedAt: asString(parsed.finishedAt),
        ok: parsed.ok === true,
        note: asString(parsed.note) || undefined,
      });
    }
  }
  return records;
}

function asString(value: unknown): string {
  return typeof value === "string" ? value : "";
}

function asStringArray(value: unknown): string[] {
  if (!Array.isArray(value)) return [];
  return value.filter((entry): entry is string => typeof entry === "string");
}

function asChangedPaths(value: unknown): ChangedPath[] {
  if (!Array.isArray(value)) return [];
  const paths: ChangedPath[] = [];
  for (const entry of value) {
    if (!entry || typeof entry !== "object") continue;
    const record = entry as Record<string, unknown>;
    const changedPath = asString(record.path);
    if (!changedPath) continue;
    paths.push({ status: asString(record.status) || "?", path: changedPath });
  }
  return paths;
}

async function appendRecords(cwd: string, jobId: string, records: ClaimsRecord[]): Promise<string | null> {
  if (records.length === 0) return null;
  const file = claimsLogPath(cwd, jobId);
  try {
    await fs.promises.mkdir(path.dirname(file), { recursive: true });
    const body = records.map((record) => JSON.stringify(record)).join("\n") + "\n";
    await fs.promises.appendFile(file, body, "utf8");
    return file;
  } catch (error) {
    console.error("[delegate] claims log write failed (" + file + "):", error);
    return null;
  }
}

/** Claims whose claimId has no completion record — the blocking set. */
export function activeClaims(records: ClaimsRecord[]): ClaimRecord[] {
  const completed = new Set<string>();
  for (const record of records) {
    if (record.type === "completed") completed.add(record.claimId);
  }
  return records.filter((record): record is ClaimRecord => record.type === "claim" && !completed.has(record.claimId));
}

// ── git status snapshots (fail open) ────────────────────────────────────────

/** `git status --porcelain` lines, or null when git failed (fail open). */
export async function gitStatusLines(cwd: string): Promise<string[] | null> {
  const result = await runGate("git", ["status", "--porcelain"], { cwd, timeoutMs: GIT_TIMEOUT_MS });
  if (!result.ok) return null;
  return result.stdout
    .split("\n")
    .map((line) => line.replace(/\s+$/, ""))
    .filter((line) => line.length > 0);
}

/** Porcelain v1 lines are `XY<space>path` (renames: `orig -> new`). */
export function parsePorcelainLine(line: string): ChangedPath | null {
  const trimmed = line.replace(/\s+$/, "");
  if (trimmed.length < 4) return null;
  const status = trimmed.slice(0, 2).replace(/\s/g, "") || "?";
  let rawPath = trimmed.slice(3);
  const arrow = rawPath.indexOf(" -> ");
  if (arrow >= 0) rawPath = rawPath.slice(arrow + 4);
  const cleaned = rawPath.replace(/^"|"$/g, "").trim();
  if (!cleaned) return null;
  return { status, path: cleaned };
}

/** Paths present now but absent from the baseline snapshot. */
export function changedPathsFromPorcelain(baseline: string[], current: string[]): ChangedPath[] {
  const seen = new Set(baseline);
  const changed: ChangedPath[] = [];
  const added = new Set<string>();
  for (const line of current) {
    if (seen.has(line)) continue;
    const parsed = parsePorcelainLine(line);
    if (!parsed || added.has(parsed.path)) continue;
    added.add(parsed.path);
    changed.push(parsed);
  }
  return changed;
}

// ── Sibling-context broadcast ───────────────────────────────────────────────

/**
 * Prompt block for the most recent completions (newest first, capped), or ""
 * when there is nothing to broadcast.
 */
export function buildSiblingContext(records: ClaimsRecord[], max: number = SIBLING_CONTEXT_MAX): string {
  if (!Number.isFinite(max) || max <= 0) return "";
  const completions = records.filter((record): record is CompletionRecord => record.type === "completed");
  if (completions.length === 0) return "";
  const recent = completions.slice(-max).reverse();
  const lines: string[] = [];
  for (const completion of recent) {
    const changed = completion.changedPaths.map((entry) => entry.path);
    const shown = changed.slice(0, 8);
    const more = changed.length - shown.length;
    const listed = shown.length > 0 ? shown.join(", ") + (more > 0 ? " (+" + String(more) + " more)" : "") : "no tracked paths";
    const note = completion.note ? " [" + completion.note + "]" : "";
    lines.push(
      "Sibling context: worker " + completion.worker + " completed \u2014 changed " + listed +
        note + "; expect these states.",
    );
  }
  return lines.join("\n");
}

/** Read the log and build the broadcast block ("" when the log is absent). */
export async function siblingContextFor(cwd: string, jobId: string): Promise<string> {
  const records = await readClaims(cwd, jobId);
  return buildSiblingContext(records);
}

// ── Prepare / complete (the two hook-facing operations) ─────────────────────

export interface PrepareClaimsResult {
  /** Non-empty ⇒ the caller MUST NOT spawn (nothing was written). */
  blocked: ClaimConflict[];
  /** Records appended for the accepted spawns (same order as the requests). */
  claims: ClaimRecord[];
  logPath: string;
}

/**
 * Claim every spawn of one tool call, or block the whole call. Order matters:
 * detect ALL conflicts against the active log AND against the earlier
 * candidates of this same call first, then append — a partially claimed
 * parallel batch would be worse than no claims at all.
 */
export async function prepareSpawnClaims(opts: {
  cwd: string;
  jobId: string;
  sessionId: string;
  requests: SpawnRequest[];
  env?: NodeJS.ProcessEnv;
  /** Test seam: replace the git baseline probe. */
  baseline?: string[];
  /** Test seam: clock. */
  now?: () => Date;
}): Promise<PrepareClaimsResult> {
  const logPath = claimsLogPath(opts.cwd, opts.jobId);
  if (opts.requests.length === 0) return { blocked: [], claims: [], logPath };

  const records = await readClaims(opts.cwd, opts.jobId);
  const active = activeClaims(records);
  const override = claimsOverrideEnabled(opts.env);
  const startedAt = (opts.now ?? (() => new Date()))().toISOString();
  const baseline = opts.baseline ?? ((await gitStatusLines(opts.cwd)) ?? []);

  const blocked: ClaimConflict[] = [];
  const claims: ClaimRecord[] = [];
  for (let index = 0; index < opts.requests.length; index++) {
    const request = opts.requests[index];
    const candidate = claimCandidateOf(request.worker, request.task);
    const conflict = findClaimConflict(candidate, [...active, ...claims]);
    if (conflict && !override) {
      blocked.push(conflict);
      continue;
    }
    claims.push({
      type: "claim",
      claimId: request.worker + "@" + startedAt + "#" + String(index),
      worker: request.worker,
      sessionId: opts.sessionId,
      ruleIds: candidate.ruleIds,
      module: candidate.module,
      ownedPaths: candidate.ownedPaths,
      startedAt,
      baseline,
    });
  }
  if (blocked.length > 0) return { blocked, claims: [], logPath };
  await appendRecords(opts.cwd, opts.jobId, claims);
  return { blocked: [], claims, logPath };
}

/**
 * Append the completion record for a finished spawn: fresh `git status`, the
 * changed paths relative to the claim's baseline, and a fail-open note when
 * git could not be read.
 */
export async function completeSpawnClaim(opts: {
  cwd: string;
  jobId: string;
  claim: ClaimRecord;
  ok: boolean;
  note?: string;
  env?: NodeJS.ProcessEnv;
  /** Test seam: replace the fresh git status probe. */
  current?: string[] | null;
  now?: () => Date;
}): Promise<CompletionRecord | null> {
  const current = opts.current !== undefined ? opts.current : await gitStatusLines(opts.cwd);
  let changedPaths: ChangedPath[] = [];
  let note = opts.note;
  if (current === null) {
    note = note ? note + "; git-status-failed" : "git-status-failed";
  } else {
    changedPaths = changedPathsFromPorcelain(opts.claim.baseline, current);
  }
  const record: CompletionRecord = {
    type: "completed",
    claimId: opts.claim.claimId,
    worker: opts.claim.worker,
    sessionId: opts.claim.sessionId,
    ruleIds: opts.claim.ruleIds,
    changedPaths,
    finishedAt: (opts.now ?? (() => new Date()))().toISOString(),
    ok: opts.ok,
  };
  if (note) record.note = note;
  await appendRecords(opts.cwd, opts.jobId, [record]);
  return record;
}

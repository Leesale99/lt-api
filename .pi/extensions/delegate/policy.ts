/**
 * WORKFLOW.md — the declarative, machine-checked workflow policy (batch 3).
 *
 * Why this module exists: every mechanical rule of the delegate workflow used
 * to live in two places at once — hardcoded in this extension (gate commands,
 * protected paths, settle caps, the stall timeout, the approval tokens) and
 * restated as English prose in the orchestrator prompts. The two drifted. Now
 * the repo-root `WORKFLOW.md` front matter is the SINGLE declared source for
 * those values, and this loader is the parser/enforcer:
 *
 *   - the file exists and parses (a hand-rolled YAML subset: flat scalars,
 *     `- item` string lists, quoted strings, `#` comments) → ok
 *   - `version` present and supported                                    → ok
 *   - no unknown fields (typo protection, the spirit of yaml KnownFields) → ok
 *   - the approval constants agree with the code's parsers                → ok
 *
 * Anything else is `ok:false` with a loud, specific reason, and every gate
 * path REFUSES rather than running unpoliced (see `workflowPolicyRefusal` in
 * gates.ts). Fail-closed is the whole point: a repo without a valid policy
 * file has no gates, and a worker must never be told it "passed".
 *
 * Migration choice (deliberate): DEFAULTS-WHEN-OMITTED. An omitted or
 * invalid-typed field falls back to the batch-1/2 code default (and, for the
 * env-tunable knobs, to the env override) — exactly today's behavior, so a
 * policy file can be adopted incrementally without weakening a gate. A field
 * that is PRESENT and well-typed overrides both the default and the env knob.
 * Only `version` (and the file itself) is hard-required.
 *
 * Fresh reads, no cache: `readFileSync` per call, the same precedent as
 * `readGateAllowlist` — a policy edit applies without restarting a session.
 *
 * Loader gotcha (pi 1.5.2 extension parser): no `?:` ternaries with
 * double-quoted strings inside a template literal's `${}`; this module sticks
 * to string concatenation throughout.
 */

import * as fs from "node:fs";
import * as path from "node:path";
import {
  APPROVAL_BINDING,
  APPROVAL_HEAD_SHA_PREFIX,
  APPROVAL_MARKER,
  HANDOFF_TERMINAL_TOKENS,
} from "./contracts.ts";

// ── File + schema ───────────────────────────────────────────────────────────

/** Repo-root-relative policy file name. */
export const WORKFLOW_FILE_REL_PATH = "WORKFLOW.md";
/** Schema versions this loader understands. */
export const SUPPORTED_POLICY_VERSIONS: readonly number[] = [1];

// ── Code-side defaults (the batch-1/2 behavior, migrated verbatim) ──────────

/** Denied to delegated workers unless a pi_editable_path/allowlist glob grants it. */
export const DEFAULT_PROTECTED_PATHS: readonly string[] = [
  "Makefile",
  "cmd/spec",
  "cmd/spec/**",
  "docs/spec/**",
  ".pi/**",
];
/** The .pi/** prose a worker may still edit. */
export const DEFAULT_PI_EDITABLE_PATHS: readonly string[] = [
  ".pi/agents/**",
  ".pi/prompts/**",
  ".pi/delegate/**",
  ".pi/plans/**",
];
/** Default gate commands (what the extension actually runs today). */
export const DEFAULT_AUDIT_STATIC_CMD = "make audit/static";
export const DEFAULT_AUDIT_CMD = "make audit";
export const DEFAULT_PROVE_CMD = "make prove";
export const DEFAULT_SCOPED_CHECK_CMD = "go run ./cmd/spec check --strict";
/** Single file-based escape hatch from the protected set; always self-denied. */
export const DEFAULT_ALLOWLIST_PATH = ".pi/delegate/gate-allowlist.json";
/** Gate receipts (extension-written, gitignored). */
export const DEFAULT_RECEIPTS_DIR = ".pi/receipts";
/** Claims logs: <claims_dir>/<job-id>/claims.jsonl. */
export const DEFAULT_CLAIMS_DIR = ".pi/jobs";
/** Failed settle-gate attempts before the terminal hard-stop turn. */
export const DEFAULT_SETTLE_MAX_ATTEMPTS = 5;
/** Attempts that retry with no wait (batch-1 semantics). */
export const DEFAULT_SETTLE_IMMEDIATE_ATTEMPTS = 3;
/** First backoff wait; doubles per attempt past the immediate window. */
export const DEFAULT_SETTLE_BACKOFF_BASE_MS = 2_000;
/** Cap on backoff doublings. */
export const DEFAULT_SETTLE_BACKOFF_MAX_DOUBLINGS = 5;
/** Foreground-worker silence before the stall watchdog aborts the child. */
export const DEFAULT_WORKER_STALL_TIMEOUT_MS = 600_000;
/** Cap on tasks[] in one subagent call (MAX_PARALLEL_TASKS). */
export const DEFAULT_MAX_CONCURRENT_WORKERS = 8;

// ── Resolved policy ─────────────────────────────────────────────────────────

export interface WorkflowPolicy {
  version: number;
  auditStaticCmd: string;
  auditCmd: string;
  proveCmd: string;
  scopedCheckCmd: string;
  protectedPaths: string[];
  piEditablePaths: string[];
  allowlistPath: string;
  receiptsDir: string;
  claimsDir: string;
  settleMaxAttempts: number;
  settleImmediateAttempts: number;
  settleBackoffBaseMs: number;
  settleBackoffMaxDoublings: number;
  workerStallTimeoutMs: number;
  maxConcurrentWorkers: number;
  approvalBinding: string;
  redApprovalMarker: string;
  redApprovalHeadShaLine: string;
  handoffTerminalTokens: string[];
}

export interface PolicyResultOk {
  ok: true;
  path: string;
  policy: WorkflowPolicy;
  /**
   * Fields actually present AND well-typed in the file. Wiring uses this to
   * decide "policy wins vs env wins": a declared field overrides the env knob,
   * an omitted/invalid-typed one keeps the env/default path.
   */
  declared: ReadonlySet<string>;
}

export interface PolicyResultErr {
  ok: false;
  path: string;
  reason: string;
}

export type PolicyResult = PolicyResultOk | PolicyResultErr;

/** `workflow policy unavailable: <why> — gates refuse rather than run unpoliced`. */
export function workflowPolicyRefusal(reason: string): string {
  return "workflow policy unavailable: " + reason + " \u2014 gates refuse rather than run unpoliced";
}

// ── Minimal YAML-subset front-matter parser ─────────────────────────────────

export type PolicyValue = string | string[];

export interface FrontMatterParseResult {
  ok: boolean;
  values: Record<string, PolicyValue>;
  reason?: string;
}

function fail(reason: string): FrontMatterParseResult {
  return { ok: false, values: {}, reason };
}

/** Drop a trailing ` # comment` that is outside quotes. */
function stripInlineComment(value: string): string {
  let quote: string | null = null;
  for (let index = 0; index < value.length; index++) {
    const char = value[index];
    if (quote) {
      if (char === "\\") {
        index++;
        continue;
      }
      if (char === quote) quote = null;
      continue;
    }
    if (char === '"' || char === "'") {
      quote = char;
      continue;
    }
    if (char === "#" && (index === 0 || /\s/.test(value[index - 1]))) return value.slice(0, index);
  }
  return value;
}

/** Unquote one scalar; null means "malformed quoting". */
function unquoteScalar(raw: string): string | null {
  const text = raw.trim();
  if (text === "") return "";
  const first = text[0];
  if (first === '"' || first === "'") {
    if (text.length < 2 || text[text.length - 1] !== first) return null;
    const body = text.slice(1, -1);
    if (first === "'") return body;
    // Only the escapes the policy file actually needs.
    return body.replace(/\\(["\\])/g, "$1");
  }
  if (text.includes('"') || text.includes("'")) return null;
  return text;
}

/**
 * Parse the `---` fenced front matter at the top of WORKFLOW.md. Supports the
 * minimal subset the file uses: `key: scalar`, `key:` + `- item` lists,
 * single/double quoted scalars, `#` comments. Everything else is a parse error
 * (never a silently dropped rule).
 */
export function parseWorkflowFrontMatter(raw: string): FrontMatterParseResult {
  const lines = raw.split(/\r?\n/);
  let index = 0;
  while (index < lines.length && lines[index].replace(/^\uFEFF/, "").trim() === "") index++;
  if (index >= lines.length) return fail("empty file");
  if (lines[index].replace(/^\uFEFF/, "").trim() !== "---") {
    return fail(
      "no YAML front matter \u2014 the file must open with a --- fenced block (line " + String(index + 1) + ")",
    );
  }
  index++;

  const values: Record<string, PolicyValue> = {};
  let currentKey: string | null = null;
  let closed = false;
  for (; index < lines.length; index++) {
    const rawLine = lines[index];
    if (rawLine.trim() === "---") {
      closed = true;
      break;
    }
    const line = rawLine.trim();
    if (line === "" || line.startsWith("#")) continue;
    const lineNo = "line " + String(index + 1);

    if (line.startsWith("- ") || line === "-") {
      if (currentKey === null) return fail("list item without a field key (" + lineNo + ")");
      const item = unquoteScalar(stripInlineComment(line.slice(1)));
      if (item === null) return fail("unterminated quote in list item (" + lineNo + ")");
      const value = values[currentKey];
      if (Array.isArray(value)) value.push(item);
      else if (value === "") values[currentKey] = [item];
      else return fail("field `" + currentKey + "` mixes a scalar and a list (" + lineNo + ")");
      continue;
    }

    const match = /^([A-Za-z0-9_]+)\s*:\s*(.*)$/.exec(line);
    if (!match) return fail("not a `key: value` line (" + lineNo + "): " + line);
    const key = match[1];
    if (Object.prototype.hasOwnProperty.call(values, key)) {
      return fail("duplicate field `" + key + "` (" + lineNo + ")");
    }
    currentKey = key;
    const rest = match[2].trim();
    if (rest === "" || rest.startsWith("#")) {
      // May become a list (items follow) or stay an empty value.
      values[key] = "";
      continue;
    }
    const scalar = unquoteScalar(stripInlineComment(rest));
    if (scalar === null) return fail("unterminated or stray quote in field `" + key + "` (" + lineNo + ")");
    values[key] = scalar;
  }
  if (!closed) return fail("front matter block is not closed with a second --- line");

  // A bare `key:` with no items is an explicitly empty value, not a list.
  return { ok: true, values };
}

// ── Field table + typed resolution ──────────────────────────────────────────

type FieldKind = "string" | "integer" | "string-list" | "repo-relative-path";

const FIELD_KINDS: Record<string, FieldKind> = {
  version: "integer",
  audit_static_cmd: "string",
  audit_cmd: "string",
  prove_cmd: "string",
  scoped_check_cmd: "string",
  protected_paths: "string-list",
  pi_editable_paths: "string-list",
  allowlist_path: "repo-relative-path",
  receipts_dir: "repo-relative-path",
  claims_dir: "repo-relative-path",
  settle_max_attempts: "integer",
  settle_immediate_attempts: "integer",
  settle_backoff_base_ms: "integer",
  settle_backoff_max_doublings: "integer",
  worker_stall_timeout_ms: "integer",
  max_concurrent_workers: "integer",
  approval_binding: "string",
  red_approval_marker: "string",
  red_approval_head_sha_line: "string",
  handoff_terminal_tokens: "string-list",
};

function warn(message: string): void {
  console.error("[delegate] " + WORKFLOW_FILE_REL_PATH + ": " + message);
}

/** Repo-relative, never absolute, never escaping the repo. */
function isSafeRepoRelative(value: string): boolean {
  if (value === "" || path.isAbsolute(value)) return false;
  const segments = value.split(/[\\/]+/);
  return segments.every((segment) => segment !== "..");
}

function takeString(
  values: Record<string, PolicyValue>,
  key: string,
  fallback: string,
  declared: Set<string>,
): string {
  const raw = values[key];
  if (raw === undefined) return fallback;
  if (typeof raw !== "string" || raw.trim() === "") {
    warn("field `" + key + "` must be a non-empty scalar \u2014 using the default (" + fallback + ")");
    return fallback;
  }
  declared.add(key);
  return raw.trim();
}

function takeInteger(
  values: Record<string, PolicyValue>,
  key: string,
  fallback: number,
  declared: Set<string>,
): number {
  const raw = values[key];
  if (raw === undefined) return fallback;
  const text = typeof raw === "string" ? raw.trim() : "";
  const value = Number(text);
  if (text === "" || !Number.isInteger(value) || value < 0) {
    warn("field `" + key + "` must be a non-negative integer \u2014 using the default (" + String(fallback) + ")");
    return fallback;
  }
  declared.add(key);
  return value;
}

function takePath(
  values: Record<string, PolicyValue>,
  key: string,
  fallback: string,
  declared: Set<string>,
): string {
  const raw = values[key];
  if (raw === undefined) return fallback;
  if (typeof raw !== "string" || !isSafeRepoRelative(raw.trim())) {
    warn("field `" + key + "` must be a repo-root-relative path without `..` \u2014 using the default (" + fallback + ")");
    return fallback;
  }
  declared.add(key);
  return raw.trim();
}

function takeList(
  values: Record<string, PolicyValue>,
  key: string,
  fallback: readonly string[],
  declared: Set<string>,
): string[] {
  const raw = values[key];
  if (raw === undefined) return [...fallback];
  const items = Array.isArray(raw) ? raw.map((item) => item.trim()).filter((item) => item !== "") : [];
  if (items.length === 0) {
    // Never an empty deny set by accident: an empty/invalid list keeps the default.
    warn("field `" + key + "` must be a non-empty `- item` list \u2014 using the default (" + fallback.join(", ") + ")");
    return [...fallback];
  }
  declared.add(key);
  return items;
}

// ── Loader ─────────────────────────────────────────────────────────────────

/**
 * Load and validate `<cwd>/WORKFLOW.md`. Pure read, no cache, no throw: every
 * failure is `ok:false` with a reason a gate can print verbatim.
 */
export function loadWorkflowPolicy(cwd: string = process.cwd()): PolicyResult {
  const filePath = path.join(cwd, WORKFLOW_FILE_REL_PATH);
  let raw: string;
  try {
    raw = fs.readFileSync(filePath, "utf8");
  } catch (error) {
    const code = (error as NodeJS.ErrnoException | undefined)?.code;
    const message = error instanceof Error ? error.message : String(error);
    if (code === "ENOENT") {
      return { ok: false, path: filePath, reason: WORKFLOW_FILE_REL_PATH + " not found at " + filePath };
    }
    return { ok: false, path: filePath, reason: "cannot read " + WORKFLOW_FILE_REL_PATH + ": " + message };
  }

  const parsed = parseWorkflowFrontMatter(raw);
  if (!parsed.ok) {
    return { ok: false, path: filePath, reason: parsed.reason ?? "unparseable front matter" };
  }
  const values = parsed.values;

  // version: the ONE hard-required field.
  const versionRaw = values.version;
  if (versionRaw === undefined) {
    return {
      ok: false,
      path: filePath,
      reason:
        "missing required field `version` in the front matter (schema version; supported: " +
        SUPPORTED_POLICY_VERSIONS.join(", ") + ")",
    };
  }
  const versionText = typeof versionRaw === "string" ? versionRaw.trim() : "";
  const version = Number(versionText);
  if (versionText === "" || !Number.isInteger(version)) {
    return { ok: false, path: filePath, reason: "field `version` must be an integer (line: version: 1)" };
  }
  if (!SUPPORTED_POLICY_VERSIONS.includes(version)) {
    return {
      ok: false,
      path: filePath,
      reason:
        "unsupported policy version " + String(version) + " \u2014 this extension supports " +
        SUPPORTED_POLICY_VERSIONS.join(", ") + " (refusing rather than guessing at a newer schema)",
    };
  }

  // Unknown fields: typo protection (a misspelled cap must not silently pass).
  const unknown = Object.keys(values).filter((key) => !Object.prototype.hasOwnProperty.call(FIELD_KINDS, key));
  if (unknown.length > 0) {
    return {
      ok: false,
      path: filePath,
      reason:
        "unknown field(s) in the front matter: " + unknown.join(", ") +
        " \u2014 refusing rather than ignoring them (typo protection; known fields: " +
        Object.keys(FIELD_KINDS).join(", ") + ")",
    };
  }

  const declared = new Set<string>();
  const policy: WorkflowPolicy = {
    version,
    auditStaticCmd: takeString(values, "audit_static_cmd", DEFAULT_AUDIT_STATIC_CMD, declared),
    auditCmd: takeString(values, "audit_cmd", DEFAULT_AUDIT_CMD, declared),
    proveCmd: takeString(values, "prove_cmd", DEFAULT_PROVE_CMD, declared),
    scopedCheckCmd: takeString(values, "scoped_check_cmd", DEFAULT_SCOPED_CHECK_CMD, declared),
    protectedPaths: takeList(values, "protected_paths", DEFAULT_PROTECTED_PATHS, declared),
    piEditablePaths: takeList(values, "pi_editable_paths", DEFAULT_PI_EDITABLE_PATHS, declared),
    allowlistPath: takePath(values, "allowlist_path", DEFAULT_ALLOWLIST_PATH, declared),
    receiptsDir: takePath(values, "receipts_dir", DEFAULT_RECEIPTS_DIR, declared),
    claimsDir: takePath(values, "claims_dir", DEFAULT_CLAIMS_DIR, declared),
    settleMaxAttempts: takeInteger(values, "settle_max_attempts", DEFAULT_SETTLE_MAX_ATTEMPTS, declared),
    settleImmediateAttempts: takeInteger(
      values,
      "settle_immediate_attempts",
      DEFAULT_SETTLE_IMMEDIATE_ATTEMPTS,
      declared,
    ),
    settleBackoffBaseMs: takeInteger(values, "settle_backoff_base_ms", DEFAULT_SETTLE_BACKOFF_BASE_MS, declared),
    settleBackoffMaxDoublings: takeInteger(
      values,
      "settle_backoff_max_doublings",
      DEFAULT_SETTLE_BACKOFF_MAX_DOUBLINGS,
      declared,
    ),
    workerStallTimeoutMs: takeInteger(values, "worker_stall_timeout_ms", DEFAULT_WORKER_STALL_TIMEOUT_MS, declared),
    maxConcurrentWorkers: takeInteger(
      values,
      "max_concurrent_workers",
      DEFAULT_MAX_CONCURRENT_WORKERS,
      declared,
    ),
    approvalBinding: takeString(values, "approval_binding", APPROVAL_BINDING, declared),
    redApprovalMarker: takeString(values, "red_approval_marker", APPROVAL_MARKER, declared),
    redApprovalHeadShaLine: takeString(values, "red_approval_head_sha_line", APPROVAL_HEAD_SHA_PREFIX, declared),
    handoffTerminalTokens: takeList(values, "handoff_terminal_tokens", HANDOFF_TERMINAL_TOKENS, declared),
  };

  // Approval machinery: the code stays the parser; the file declares what the
  // parser expects. Any disagreement is a REFUSAL, never a silent override —
  // these strings are what jobs.ts greps for in a child's last message.
  const mismatches: string[] = [];
  if (policy.redApprovalMarker !== APPROVAL_MARKER) {
    mismatches.push(
      "red_approval_marker: declared \"" + policy.redApprovalMarker + "\", code expects \"" + APPROVAL_MARKER + "\"",
    );
  }
  if (policy.redApprovalHeadShaLine !== APPROVAL_HEAD_SHA_PREFIX) {
    mismatches.push(
      "red_approval_head_sha_line: declared \"" + policy.redApprovalHeadShaLine + "\", code expects \"" +
        APPROVAL_HEAD_SHA_PREFIX + "\"",
    );
  }
  const declaredTokens = policy.handoffTerminalTokens.join(",");
  const codeTokens = HANDOFF_TERMINAL_TOKENS.join(",");
  if (declaredTokens !== codeTokens) {
    mismatches.push(
      "handoff_terminal_tokens: declared [" + declaredTokens + "], code expects [" + codeTokens + "]",
    );
  }
  if (policy.approvalBinding !== APPROVAL_BINDING) {
    mismatches.push(
      "approval_binding: declared \"" + policy.approvalBinding + "\", code implements \"" + APPROVAL_BINDING + "\"",
    );
  }
  if (mismatches.length > 0) {
    return {
      ok: false,
      path: filePath,
      reason:
        "approval contract mismatch between " + WORKFLOW_FILE_REL_PATH + " and jobs.ts (" +
        mismatches.join("; ") + ") \u2014 the approval parser would stop recognizing these blocks",
    };
  }

  return { ok: true, path: filePath, policy, declared };
}

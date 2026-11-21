/**
 * Bash write-channel scanner (batch 3, stage 1).
 *
 * Why this module exists: the batch-1 gate-file deny hook (index.ts) intercepts
 * the edit/write TOOLS only. `bash` is a second, ungated write channel — a
 * delegated child could `echo x > WORKFLOW.md` (or `sed -i`, or `tee`, or
 * `git checkout -- docs/spec/rules.yaml`) and bypass the whole protected set.
 * This module closes that channel deterministically, BEFORE execution: the
 * tool_call hook hands the command string here, and a blocked decision refuses
 * the call.
 *
 * Design rules (deliberate, and the reason the code looks conservative):
 *
 *   - Pure and synchronous. No LLM, no fuzzy scoring, no network, no state.
 *     Same command + same repo ⇒ same verdict, always.
 *   - Pattern classes, not ad-hoc greps. Detection dispatches on COMMAND WORDS
 *     ("sed" is a command word; "sed -i" inside an argument is not) so that
 *     `grep -n "sed -i" notes.md` passes. The vocabulary is the frozen
 *     `BASH_WRITE_PATTERN_CLASSES` table in contracts.ts; WORKFLOW.md's
 *     `bash_write_patterns` selects which classes run, and an unknown class
 *     makes the policy refuse (typo protection, same spirit as unknown fields).
 *     A class a human omitted is not scanned — that is the documented escape.
 *   - One classifier, not two. Every extracted target goes through
 *     `evaluateGateFileWrite` — the exact same protected-set semantics
 *     (protected_paths, pi_editable_paths, the allowlist, the never-grantable
 *     self-deny of WORKFLOW.md and the allowlist file). This module never
 *     re-implements path classification.
 *   - Fail closed on ambiguity. A write target carrying command substitution,
 *     a variable, a glob metacharacter, an unbalanced quote, or anything else
 *     that cannot be resolved to ONE literal path is DENIED with "write target
 *     could not be determined confidently (fail closed)". Guessing is the one
 *     thing a gate must never do.
 *   - Fail closed on internal error. `scanBashCommand` wraps its body: a throw
 *     becomes a blocked decision, never an allow.
 *   - Allowed commands stay allowed. Every negative in the suite (`make audit`,
 *     `go test ./...`, `git diff`, `ls | grep foo`, `make prove 2>/dev/null`)
 *     is a regression test against over-matching; a scanner that blocks ordinary
 *     work gets switched off by a human and protects nothing.
 *
 * Segmentation and quoting: the command is split at TOP LEVEL only — `;`, `&&`,
 * `||`, `|`, newlines outside quotes and outside `$(...)`/backticks — and each
 * segment is scanned independently (any blocked segment blocks the command).
 * Subshell parens are transparent (`(echo x > Makefile)` still names `Makefile`).
 * Heredoc BODIES are stripped before segmentation: a heredoc body is data, not a
 * command list, so its lines must never be scanned as commands. A heredoc handed
 * to a shell (`| sh`, `bash <<EOF`) or an eval-like consumer is ambiguous ⇒ deny.
 *
 * Loader gotcha (pi 1.5.2 extension parser): no `?:` ternaries with
 * double-quoted strings inside a template literal's `${}`; this module sticks to
 * string concatenation throughout.
 */

import * as path from "node:path";
import { type GatePathDecision, evaluateGateFileWrite } from "./gates.ts";
import {
  KNOWN_BASH_WRITE_PATTERNS,
  loadWorkflowPolicy,
  workflowPolicyRefusal,
} from "./policy.ts";

// ── Public shape ────────────────────────────────────────────────────────────

export interface BashScanDecision {
  blocked: boolean;
  /** Why (names the pattern class, the command and the offending target). */
  reason?: string;
  /** Pattern class that produced the verdict (undefined for policy/internal). */
  pattern?: string;
  /** The write target that triggered it, when one was extracted. */
  target?: string;
}

/** Protected-path classification seam (default: `evaluateGateFileWrite`). */
export type TargetEvaluator = (repoRoot: string, target: string) => GatePathDecision;

export interface BashScanOptions {
  /** Session/project root; every target resolves against it. */
  repoRoot: string;
  /** Enabled pattern classes. Default: the full canonical set. */
  enabledPatterns?: readonly string[];
  /** Protected-path classifier. Default: `evaluateGateFileWrite`. */
  evaluateTarget?: TargetEvaluator;
  /** Internal: recursion depth for `eval` / `sh -c` payloads. */
  depth?: number;
}

/** Recursion cap for nested `eval` / `sh -c` / `bash -c` payloads. */
export const BASH_SCAN_MAX_DEPTH = 4;

/** Write-placeholder arguments that mean stdin/stdout, never a file. */
const STDIO_PLACEHOLDERS = ["-"];

/** Device paths a redirect may name without writing a protected file. */
const SAFE_REDIRECT_TARGETS = [
  "/dev/null",
  "/dev/stdout",
  "/dev/stderr",
  "/dev/tty",
  "/dev/fd/1",
  "/dev/fd/2",
];

/** Command words that are POSIX shells (they READ a script from argv/stdin). */
const SHELL_WORDS = ["sh", "bash", "dash", "zsh", "ksh", "ash"];

/** Transparent command prefixes: `sudo cp x y` still copies. */
const WRAPPER_WORDS = ["sudo", "env", "nohup", "command", "builtin", "exec", "nice", "time"];

/** Wrapper options that consume the NEXT token as their value. */
const WRAPPER_VALUE_OPTS = ["-u", "-g", "-p", "-C", "-U", "-r", "-D", "-n", "--user", "--group"];

/** `git checkout|restore` options that consume the NEXT token as their value. */
const GIT_VALUE_OPTS = ["-b", "-B", "--branch", "--source", "-s", "--conflict", "--orphan"];

/** `find` actions that run a nested command over the traversal. */
const FIND_EXEC_ACTIONS = ["-exec", "-execdir", "-ok", "-okdir"];

/** Longest reason-embedded command excerpt. */
const COMMAND_EXCERPT_MAX = 160;

/**
 * Probe file name used to ask the protected-path classifier about a DIRECTORY
 * destination (`cp x -t docs/spec`): the files written inside it are unknown, so
 * the directory is tested through one synthetic child. Reusing the classifier
 * keeps path semantics in exactly one place.
 */
const DIRECTORY_PROBE_FILE = ".bashscan-probe";

// ── Small helpers ───────────────────────────────────────────────────────────

function clip(text: string, max: number): string {
  const collapsed = text.replace(/\s+/g, " ").trim();
  return collapsed.length <= max ? collapsed : collapsed.slice(0, max - 3) + "...";
}

function basename(word: string): string {
  const parts = word.split("/");
  const last = parts[parts.length - 1];
  return last === "" ? word : last;
}

function isAssignment(word: string): boolean {
  return /^[A-Za-z_][A-Za-z0-9_]*=/.test(word);
}

function patternPrefix(pattern: string, command: string): string {
  return "bash write-channel scanner: pattern class `" + pattern + "` in `" + clip(command, COMMAND_EXCERPT_MAX) + "`";
}

function ambiguousReason(pattern: string, command: string, detail: string): BashScanDecision {
  return {
    blocked: true,
    pattern,
    reason: patternPrefix(pattern, command) + " \u2014 write target could not be determined confidently (fail closed): " + detail,
  };
}

function ambiguousTargetReason(
  pattern: string,
  command: string,
  target: string,
  detail: string,
): BashScanDecision {
  return {
    blocked: true,
    pattern,
    target,
    reason:
      patternPrefix(pattern, command) + " \u2014 write target `" + target +
      "` could not be determined confidently (fail closed): " + detail,
  };
}

// ── Heredoc stripping ───────────────────────────────────────────────────────

interface HeredocMarker {
  marker: string;
  stripTabs: boolean;
  /** Index just past the marker word in the line. */
  endIndex: number;
}

/** `<<`, `<<-` markers on one line, found outside quotes (`<<<` is a here-string). */
function findHeredocMarkers(line: string): HeredocMarker[] {
  const found: HeredocMarker[] = [];
  let quote: string | null = null;
  let index = 0;
  while (index < line.length) {
    const char = line[index];
    if (quote) {
      if (char === "\\" && quote === '"') {
        index += 2;
        continue;
      }
      if (char === quote) quote = null;
      index++;
      continue;
    }
    if (char === "'" || char === '"') {
      quote = char;
      index++;
      continue;
    }
    if (char === "\\") {
      index += 2;
      continue;
    }
    if (char === "<" && line[index + 1] === "<") {
      if (line[index + 2] === "<") {
        // Here-string: data on the command line, no body to strip.
        index += 3;
        continue;
      }
      let cursor = index + 2;
      let stripTabs = false;
      if (line[cursor] === "-") {
        stripTabs = true;
        cursor++;
      }
      while (cursor < line.length && (line[cursor] === " " || line[cursor] === "\t")) cursor++;
      const open = line[cursor];
      let marker = "";
      if (open === "'" || open === '"') {
        const end = line.indexOf(open, cursor + 1);
        if (end < 0) {
          marker = line.slice(cursor + 1);
          cursor = line.length;
        } else {
          marker = line.slice(cursor + 1, end);
          cursor = end + 1;
        }
      } else {
        const match = /^[A-Za-z0-9_.-]+/.exec(line.slice(cursor));
        if (match) {
          marker = match[0];
          cursor += marker.length;
        }
      }
      if (marker !== "") found.push({ marker, stripTabs, endIndex: cursor });
      index = cursor;
      continue;
    }
    index++;
  }
  return found;
}

/** Does the rest of a line feed a heredoc into a shell or an eval-like consumer? */
function looksLikeShellConsumer(rest: string): boolean {
  // `| sh`, `| bash -s`, `| /bin/dash` — a shell reading the payload as a script.
  if (/\|\s*(?:[A-Za-z0-9_./-]*\/)?(?:sh|bash|dash|zsh|ksh|ash)(?:\s|$)/.test(rest)) return true;
  // `| eval`, `; source x` — an eval-like consumer treating the body as code.
  return /(?:^|[\s;|&])(?:eval|source)\s/.test(rest);
}

interface StripResult {
  /** Command text with heredoc bodies removed (operators/markers kept). */
  text: string;
  /** A heredoc in this command is consumed as a script (shell / eval). */
  shellConsumed: boolean;
}

/**
 * Remove heredoc BODIES from a command: everything from the line after the
 * `<<MARKER` to the terminator line is data. Termination follows the shell rule
 * (`<<-` strips leading tabs). Unterminated bodies are dropped to end of input.
 */
function stripHeredocBodies(command: string): StripResult {
  const lines = command.split("\n");
  const kept: string[] = [];
  let shellConsumed = false;
  let index = 0;
  while (index < lines.length) {
    const line = lines[index];
    kept.push(line);
    const markers = findHeredocMarkers(line);
    index++;
    for (const marker of markers) {
      const head = line.slice(0, line.indexOf("<<")).trim();
      const headWord = basename((head.split(/\s+/)[0] ?? "").replace(/^[(!]+/, ""));
      if (SHELL_WORDS.indexOf(headWord) >= 0) shellConsumed = true;
      if (looksLikeShellConsumer(line.slice(marker.endIndex))) shellConsumed = true;
      while (index < lines.length) {
        const bodyLine = marker.stripTabs ? lines[index].replace(/^\t+/, "") : lines[index];
        index++;
        if (bodyLine === marker.marker) break;
      }
    }
  }
  return { text: kept.join("\n"), shellConsumed };
}

// ── Tokenizer ───────────────────────────────────────────────────────────────

interface TokenizedSegment {
  tokens: string[];
  /** Set when quoting/substitution could not be balanced. */
  ambiguous: string | null;
}

/** Consume one `$( ... )` substitution (index at the `$`); returns it verbatim. */
function readSubstitution(text: string, start: number): { text: string; next: number } {
  let depth = 0;
  let index = start + 1;
  while (index < text.length) {
    const char = text[index];
    if (char === "(") depth++;
    else if (char === ")") {
      depth--;
      if (depth === 0) {
        index++;
        break;
      }
    }
    index++;
  }
  return { text: text.slice(start, index), next: index };
}

/**
 * Split one top-level segment into shell-ish words. Quotes are stripped (so
 * `> "Makefile"` names Makefile), `$(...)` and backtick substitutions are kept
 * verbatim inside their token (so the ambiguity check sees them), and subshell
 * parens are dropped (so `(echo x > Makefile)` still yields a plain command).
 */
function tokenizeSegment(segment: string): TokenizedSegment {
  const tokens: string[] = [];
  let current = "";
  let started = false;
  let ambiguous: string | null = null;
  const flush = () => {
    if (started) {
      tokens.push(current);
      current = "";
      started = false;
    }
  };
  let index = 0;
  while (index < segment.length) {
    const char = segment[index];
    // A `#` at a WORD START opens a shell comment: the rest is prose, not a
    // command (and not a write target). `a#b` stays a literal word.
    if (char === "#" && !started) break;
    if (char === " " || char === "\t" || char === "\r") {
      flush();
      index++;
      continue;
    }
    if (char === "'") {
      const end = segment.indexOf("'", index + 1);
      if (end < 0) {
        if (ambiguous === null) ambiguous = "unbalanced single quote";
        current += segment.slice(index + 1);
        started = true;
        index = segment.length;
        continue;
      }
      current += segment.slice(index + 1, end);
      started = true;
      index = end + 1;
      continue;
    }
    if (char === '"') {
      index++;
      let closed = false;
      while (index < segment.length) {
        const inner = segment[index];
        if (inner === "\\" && index + 1 < segment.length) {
          current += segment[index + 1];
          index += 2;
          continue;
        }
        if (inner === '"') {
          closed = true;
          index++;
          break;
        }
        current += inner;
        index++;
      }
      started = true;
      if (!closed && ambiguous === null) ambiguous = "unbalanced double quote";
      continue;
    }
    if (char === "\\" && index + 1 < segment.length) {
      current += segment[index + 1];
      started = true;
      index += 2;
      continue;
    }
    if (char === "$" && segment[index + 1] === "(") {
      const substitution = readSubstitution(segment, index);
      current += substitution.text;
      started = true;
      index = substitution.next;
      continue;
    }
    if (char === "`") {
      const end = segment.indexOf("`", index + 1);
      if (end < 0) {
        if (ambiguous === null) ambiguous = "unbalanced backtick";
        current += segment.slice(index);
        started = true;
        index = segment.length;
        continue;
      }
      current += segment.slice(index, end + 1);
      started = true;
      index = end + 1;
      continue;
    }
    if (char === "(" || char === ")") {
      flush();
      index++;
      continue;
    }
    current += char;
    started = true;
    index++;
  }
  flush();
  return { tokens, ambiguous };
}

/**
 * Split a command into top-level segments (`;`, `&&`, `||`, `|`, newline),
 * ignoring separators inside quotes, `$(...)` and backticks. Subshell parens do
 * NOT split: `(a > f)` stays one segment with a visible redirect.
 */
export function splitTopLevelSegments(command: string): string[] {
  const segments: string[] = [];
  let current = "";
  let parenDepth = 0;
  let quote: string | null = null;
  let index = 0;
  while (index < command.length) {
    const char = command[index];
    if (quote === "'") {
      current += char;
      if (char === "'") quote = null;
      index++;
      continue;
    }
    if (quote === '"') {
      if (char === "\\" && index + 1 < command.length) {
        current += char + command[index + 1];
        index += 2;
        continue;
      }
      current += char;
      if (char === '"') quote = null;
      index++;
      continue;
    }
    if (char === "\\" && index + 1 < command.length) {
      current += char + command[index + 1];
      index += 2;
      continue;
    }
    if (char === "'" || char === '"') {
      quote = char;
      current += char;
      index++;
      continue;
    }
    if (char === "`") {
      current += char;
      index++;
      while (index < command.length && command[index] !== "`") {
        current += command[index];
        index++;
      }
      if (index < command.length) {
        current += "`";
        index++;
      }
      continue;
    }
    if (char === "$" && command[index + 1] === "(") {
      const substitution = readSubstitution(command, index);
      current += substitution.text;
      index = substitution.next;
      continue;
    }
    if (char === "(") {
      parenDepth++;
      current += char;
      index++;
      continue;
    }
    if (char === ")") {
      if (parenDepth > 0) parenDepth--;
      current += char;
      index++;
      continue;
    }
    if (parenDepth === 0 && (char === ";" || char === "\n" || char === "|" || char === "&")) {
      segments.push(current);
      current = "";
      index++;
      while (index < command.length && (command[index] === "|" || command[index] === "&" || command[index] === ";")) {
        index++;
      }
      continue;
    }
    current += char;
    index++;
  }
  segments.push(current);
  return segments.map((segment) => segment.trim()).filter((segment) => segment !== "");
}

// ── Target checks ───────────────────────────────────────────────────────────

/** Why a target literal cannot be resolved to exactly one path (null = it can). */
function confidenceProblem(target: string): string | null {
  if (target === "") return "the target is empty";
  if (target.indexOf("`") >= 0) return "it contains a backtick command substitution";
  if (target.indexOf("$") >= 0) return "it contains a variable/command substitution (`$VAR`, `${...}`, `$(...)`)";
  if (/[*?[]/.test(target)) return "it contains a glob metacharacter (`*`, `?`, `[`)";
  if (/[{}]/.test(target)) return "it contains a shell/find placeholder or a brace expansion (`{}`, `{a,b}`)";
  return null;
}

interface ScanContext {
  repoRoot: string;
  enabled: ReadonlySet<string>;
  evaluateTarget: TargetEvaluator;
  depth: number;
  /**
   * Directory the current segment runs in, repo-relative ("" = repo root).
   * Tracked across top-level segments only (`cd docs && …`), because a bash
   * tool call is one shell: `cd` inside it does change relative targets.
   */
  cwd: string;
}

interface SegmentInfo {
  /** The top-level segment text (used in reasons and for recursion). */
  text: string;
  /** The whole original command (reasons quote the command the child typed). */
  command: string;
  ambiguous: string | null;
}

function isEnabled(ctx: ScanContext, pattern: string): boolean {
  return ctx.enabled.has(pattern) && KNOWN_BASH_WRITE_PATTERNS.indexOf(pattern) >= 0;
}

/** Resolve a target against the segment's tracked working directory. */
function resolveAgainstCwd(cwd: string, target: string): string {
  if (path.isAbsolute(target)) return target;
  if (cwd === "") return path.posix.normalize(target);
  return path.posix.normalize(cwd + "/" + target);
}

/** A target that names the repository root itself covers EVERY protected path. */
function isRepoRootTarget(resolved: string): boolean {
  const trimmed = resolved.replace(/\/+$/, "");
  return trimmed === "" || trimmed === ".";
}

function protectedTargetReason(
  segment: SegmentInfo,
  pattern: string,
  shownTarget: string,
  why: string,
): BashScanDecision {
  return {
    blocked: true,
    pattern,
    target: shownTarget,
    reason: patternPrefix(pattern, segment.command) + " would write `" + shownTarget + "` \u2014 " + why,
  };
}

/**
 * The single funnel every extracted target goes through: ambiguity check first
 * (fail closed), then the SAME protected-path classifier edit/write uses.
 *
 * `probeDirectory` additionally tests one synthetic child, so a directory
 * DESTINATION (`cp x -t docs/spec`) cannot launder an unknown filename into a
 * protected tree.
 */
function checkTarget(
  ctx: ScanContext,
  segment: SegmentInfo,
  pattern: string,
  target: string,
  probeDirectory: boolean = false,
): BashScanDecision | null {
  if (segment.ambiguous !== null) {
    return ambiguousReason(pattern, segment.command, segment.ambiguous + " in the command");
  }
  if (STDIO_PLACEHOLDERS.indexOf(target) >= 0) return null;
  const problem = confidenceProblem(target);
  if (problem !== null) return ambiguousTargetReason(pattern, segment.command, target, problem);

  const resolved = resolveAgainstCwd(ctx.cwd, target);
  if (isRepoRootTarget(resolved)) {
    return ambiguousTargetReason(
      pattern,
      segment.command,
      target,
      "it names the repository root, so every protected path underneath it could be written",
    );
  }
  const direct = ctx.evaluateTarget(ctx.repoRoot, resolved);
  if (direct.blocked) {
    return protectedTargetReason(
      segment,
      pattern,
      target,
      direct.reason ?? "gate file is protected from delegated writes",
    );
  }
  if (!probeDirectory) return null;
  const probeDir = resolved.replace(/\/+$/, "");
  if (probeDir === "") return null;
  const probe = ctx.evaluateTarget(ctx.repoRoot, probeDir + "/" + DIRECTORY_PROBE_FILE);
  if (!probe.blocked) return null;
  return protectedTargetReason(
    segment,
    pattern,
    target,
    "everything written inside it is protected (" + (probe.reason ?? "gate file is protected from delegated writes") + ")",
  );
}

// ── Pattern class: redirect ─────────────────────────────────────────────────

interface RedirectParse {
  operator: string;
  rest: string;
}

/**
 * Parse a write-redirect operator out of one token. `<` (input) is never a
 * write; `<<`/`<<<` are heredocs/here-strings handled by their own class.
 */
function parseWriteRedirect(token: string): RedirectParse | null {
  let prefix = "";
  let body = token;
  const fdMatch = /^([0-9])(.*)$/.exec(token);
  if (fdMatch) {
    prefix = fdMatch[1];
    body = fdMatch[2];
  }
  const operators = ["&>>", "&>", ">>", ">|", ">", "<>"];
  for (const operator of operators) {
    if (body.indexOf(operator) === 0) {
      return { operator: prefix + operator, rest: body.slice(operator.length) };
    }
  }
  return null;
}

function scanRedirect(
  ctx: ScanContext,
  segment: SegmentInfo,
  tokens: string[],
): BashScanDecision | null {
  for (let index = 0; index < tokens.length; index++) {
    const parsed = parseWriteRedirect(tokens[index]);
    if (parsed === null) continue;
    let target: string | null = null;
    if (parsed.rest !== "") {
      target = parsed.rest;
    } else if (index + 1 < tokens.length) {
      target = tokens[index + 1];
      index++;
    }
    if (target === null) continue;
    // `2>&1`, `>&2`, `>&-` duplicate an existing fd: no file is written.
    if (/^&[0-9-]+$/.test(target)) continue;
    if (target.indexOf("&") === 0) target = target.slice(1);
    if (SAFE_REDIRECT_TARGETS.indexOf(target) >= 0) continue;
    const blocked = checkTarget(ctx, segment, "redirect", target);
    if (blocked !== null) return blocked;
  }
  return null;
}

// ── Pattern class: tee ──────────────────────────────────────────────────────

function scanTee(ctx: ScanContext, segment: SegmentInfo, args: string[]): BashScanDecision | null {
  for (const arg of args) {
    if (arg.indexOf("-") === 0 && arg !== "-") continue;
    const blocked = checkTarget(ctx, segment, "tee", arg);
    if (blocked !== null) return blocked;
  }
  return null;
}

// ── Pattern class: sed -i ───────────────────────────────────────────────────

function isSedInPlaceFlag(arg: string): boolean {
  if (arg.indexOf("--") === 0) return arg === "--in-place" || arg.indexOf("--in-place=") === 0;
  if (arg.indexOf("-") !== 0 || arg === "-") return false;
  return arg.slice(1).indexOf("i") >= 0;
}

function scanSedInPlace(ctx: ScanContext, segment: SegmentInfo, args: string[]): BashScanDecision | null {
  let inPlace = false;
  let expressionFromOption = false;
  const positional: string[] = [];
  for (let index = 0; index < args.length; index++) {
    const arg = args[index];
    if (isSedInPlaceFlag(arg)) {
      inPlace = true;
      continue;
    }
    if (arg === "-e" || arg === "--expression" || arg === "-f" || arg === "--file") {
      expressionFromOption = true;
      index++;
      continue;
    }
    if (arg.indexOf("--expression=") === 0 || arg.indexOf("--file=") === 0) {
      expressionFromOption = true;
      continue;
    }
    if (arg.indexOf("-") === 0 && arg !== "-") continue;
    positional.push(arg);
  }
  if (!inPlace) return null;
  // Without -e/-f the FIRST positional is the script expression, not a file.
  const targets = expressionFromOption ? positional : positional.slice(1);
  for (const target of targets) {
    const blocked = checkTarget(ctx, segment, "sed-in-place", target);
    if (blocked !== null) return blocked;
  }
  return null;
}

// ── Pattern class: file transfer (cp / mv / install / dd) ────────────────────

function scanFileTransfer(
  ctx: ScanContext,
  segment: SegmentInfo,
  commandWord: string,
  args: string[],
): BashScanDecision | null {
  if (commandWord === "dd") {
    for (const arg of args) {
      if (arg.indexOf("of=") !== 0) continue;
      const blocked = checkTarget(ctx, segment, "file-transfer", arg.slice(3));
      if (blocked !== null) return blocked;
    }
    return null;
  }

  const positional: string[] = [];
  let targetDirectory: string | null = null;
  let makeDirectories = false;
  for (let index = 0; index < args.length; index++) {
    const arg = args[index];
    if (arg === "--") {
      for (const rest of args.slice(index + 1)) positional.push(rest);
      break;
    }
    // `install -d a b c` creates each named directory: every operand is a target.
    if (arg === "--directory" || (arg.length > 1 && arg[0] === "-" && arg[1] !== "-" && arg.slice(1).indexOf("d") >= 0)) {
      makeDirectories = true;
      continue;
    }
    if (arg === "-t" || arg === "--target-directory") {
      const value = args[index + 1];
      if (value !== undefined) {
        targetDirectory = value;
        index++;
      }
      continue;
    }
    if (arg.indexOf("--target-directory=") === 0) {
      targetDirectory = arg.slice("--target-directory=".length);
      continue;
    }
    if (arg.indexOf("-") === 0 && arg !== "-") {
      // Attached short form: `-tDIR`.
      if (arg.length > 2 && arg[1] === "t") targetDirectory = arg.slice(2);
      continue;
    }
    positional.push(arg);
  }

  const targets: string[] = [];
  if (targetDirectory !== null) targets.push(targetDirectory);
  else if (makeDirectories) {
    for (const dir of positional) targets.push(dir);
  } else if (positional.length >= 2) targets.push(positional[positional.length - 1]);
  for (const target of targets) {
    const blocked = checkTarget(ctx, segment, "file-transfer", target, true);
    if (blocked !== null) return blocked;
  }
  return null;
}

// ── Pattern class: heredoc ──────────────────────────────────────────────────

function scanHeredoc(
  ctx: ScanContext,
  segment: SegmentInfo,
  commandWord: string,
  text: string,
): BashScanDecision | null {
  const markers = findHeredocMarkers(text);
  if (markers.length === 0) return null;
  // `bash <<EOF` / `eval` consumers execute the body as code, so its lines ARE
  // commands — and we refuse to guess what they write.
  if (SHELL_WORDS.indexOf(commandWord) >= 0) {
    return ambiguousReason(
      "heredoc",
      segment.command,
      "the here-document is fed to `" + commandWord + "`, so its body is executed as a script",
    );
  }
  return null;
}

// ── Pattern class: git-mutating ─────────────────────────────────────────────

/** Non-option positional arguments, skipping `git` options that take a value. */
function gitPositionals(args: string[]): string[] {
  const positional: string[] = [];
  let index = 0;
  while (index < args.length) {
    const arg = args[index];
    if (arg === "--") {
      for (const rest of args.slice(index + 1)) positional.push(rest);
      break;
    }
    if (arg.indexOf("-") === 0) {
      const opt = arg.split("=")[0];
      if (GIT_VALUE_OPTS.indexOf(opt) >= 0 && arg.indexOf("=") < 0) index += 2;
      else index += 1;
      continue;
    }
    positional.push(arg);
    index++;
  }
  return positional;
}

/** Target list after an explicit `--` separator; null when there is none. */
function afterDoubleDash(args: string[]): string[] | null {
  const separator = args.indexOf("--");
  return separator < 0 ? null : args.slice(separator + 1);
}

function scanGitMutating(
  ctx: ScanContext,
  segment: SegmentInfo,
  args: string[],
): BashScanDecision | null {
  // Global git options before the subcommand: -C <path>, -c <k=v>, --git-dir, …
  let index = 0;
  let gitCwd = ctx.cwd;
  while (index < args.length) {
    const arg = args[index];
    if (arg === "-C") {
      const dir = args[index + 1];
      if (dir !== undefined) gitCwd = resolveAgainstCwd(gitCwd, dir);
      index += 2;
      continue;
    }
    if (arg === "-c") {
      index += 2;
      continue;
    }
    if (arg.indexOf("--git-dir") === 0 || arg.indexOf("--work-tree") === 0 || arg.indexOf("--namespace") === 0) {
      index += arg.indexOf("=") >= 0 ? 1 : 2;
      continue;
    }
    if (arg.indexOf("-") === 0) {
      index++;
      continue;
    }
    break;
  }
  const subcommand = args[index] ?? "";
  const rest = args.slice(index + 1);
  // Pathspecs resolve from `git -C <dir>`, not from the shell's directory.
  const targetCtx: ScanContext = gitCwd === ctx.cwd ? ctx : { ...ctx, cwd: gitCwd };

  if (subcommand === "apply" || subcommand === "am") {
    return ambiguousReason(
      "git-mutating",
      segment.command,
      "`git " + subcommand + "` writes whatever paths the patch content names, so the target set is decided by data",
    );
  }

  if (subcommand === "clean") {
    return ambiguousReason(
      "git-mutating",
      segment.command,
      "`git clean` deletes whatever the working tree happens to contain, which is not knowable from the command line",
    );
  }

  if (subcommand === "reset" && rest.some((arg) => arg === "--hard" || arg === "--merge" || arg === "--keep")) {
    return ambiguousReason(
      "git-mutating",
      segment.command,
      "`git reset --hard|--merge|--keep` rewrites tracked files from the commit, not from a named pathspec",
    );
  }

  if (subcommand === "checkout" || subcommand === "restore" || subcommand === "rm" || subcommand === "mv") {
    if (rest.some((arg) => arg.indexOf("--pathspec-from-file") >= 0)) {
      return ambiguousReason(
        "git-mutating",
        segment.command,
        "`--pathspec-from-file` reads the pathspecs from a file at run time",
      );
    }
    const explicit = afterDoubleDash(rest);
    const targets = explicit !== null ? explicit : gitPositionals(rest);
    for (const target of targets) {
      const blocked = checkTarget(targetCtx, segment, "git-mutating", target);
      if (blocked !== null) return blocked;
    }
    return null;
  }

  if (subcommand === "stash") {
    const action = rest[0] ?? "";
    if (action !== "push" && action !== "save") return null;
    const explicit = afterDoubleDash(rest.slice(1));
    if (explicit === null) return null;
    for (const target of explicit) {
      const blocked = checkTarget(targetCtx, segment, "git-mutating", target);
      if (blocked !== null) return blocked;
    }
    return null;
  }

  // git add / commit / diff / status / merge / log / … never write one of the
  // protected paths by name, so they are deliberately not flagged.
  return null;
}

// ── Segment scan ────────────────────────────────────────────────────────────

/** Strip env assignments and transparent wrapper commands from the word list. */
function normalizeWords(tokens: string[]): string[] {
  const words = tokens.slice();
  let guard = 0;
  while (guard++ < 16) {
    while (words.length > 0 && isAssignment(words[0])) words.shift();
    if (words.length < 2) break;
    if (WRAPPER_WORDS.indexOf(basename(words[0])) < 0) break;
    words.shift();
    while (words.length > 0 && words[0].indexOf("-") === 0) {
      const option = words[0].split("=")[0];
      words.shift();
      if (WRAPPER_VALUE_OPTS.indexOf(option) >= 0 && words.length > 0 && words[0].indexOf("-") !== 0) words.shift();
    }
  }
  return words;
}

function withDecision(
  ctx: ScanContext,
  segment: SegmentInfo,
  inner: string,
): BashScanDecision | null {
  if (ctx.depth >= BASH_SCAN_MAX_DEPTH) {
    return {
      blocked: true,
      reason:
        "bash write-channel scanner: `" + clip(segment.command, COMMAND_EXCERPT_MAX) +
        "` nests shell payloads more than " + String(BASH_SCAN_MAX_DEPTH) +
        " levels deep, so the inner command could not be determined confidently (fail closed)",
    };
  }
  const decision = scanCommand(inner, ctx.depth + 1, {
    repoRoot: ctx.repoRoot,
    enabled: ctx.enabled,
    evaluateTarget: ctx.evaluateTarget,
    depth: ctx.depth + 1,
    cwd: ctx.cwd,
  });
  return decision.blocked ? decision : null;
}

function scanSegment(segment: SegmentInfo, ctx: ScanContext): BashScanDecision | null {
  const tokenized = tokenizeSegment(segment.text);
  if (tokenized.tokens.length === 0) return null;
  const info: SegmentInfo = {
    text: segment.text,
    command: segment.command,
    ambiguous: segment.ambiguous !== null ? segment.ambiguous : tokenized.ambiguous,
  };
  const words = normalizeWords(tokenized.tokens);
  if (words.length === 0) return null;
  const commandWord = basename(words[0]);
  const args = words.slice(1);

  // Nested payloads: `eval "<cmd>"` and `sh|bash|dash -c "<cmd>"` are scanned
  // recursively, because their argument IS a command line.
  if (commandWord === "eval") {
    const payload = args.join(" ");
    if (payload.trim() !== "") {
      const blocked = withDecision(ctx, info, payload);
      if (blocked !== null) return blocked;
    }
    return null;
  }
  if (SHELL_WORDS.indexOf(commandWord) >= 0) {
    for (let index = 0; index < args.length - 1; index++) {
      const arg = args[index];
      if (arg.indexOf("-") === 0 && arg.indexOf("c") > 0 && arg.indexOf("=") < 0) {
        const blocked = withDecision(ctx, info, args[index + 1]);
        if (blocked !== null) return blocked;
        return null;
      }
    }
  }

  // `xargs <cmd>` and `find … -exec <cmd>` run a nested command; scan its words.
  if (commandWord === "xargs") {
    const payload = args.filter((arg) => arg.indexOf("-") !== 0);
    if (payload.length > 0) {
      const blocked = withDecision(ctx, info, payload.join(" "));
      if (blocked !== null) return blocked;
    }
    return null;
  }
  if (commandWord === "find") {
    for (const action of FIND_EXEC_ACTIONS) {
      for (let start = 0; start < args.length; start++) {
        if (args[start] !== action) continue;
        const payload: string[] = [];
        for (let scan = start + 1; scan < args.length; scan++) {
          if (args[scan] === ";" || args[scan] === "+") break;
          payload.push(args[scan]);
        }
        if (payload.length > 0) {
          const blocked = withDecision(ctx, info, payload.join(" "));
          if (blocked !== null) return blocked;
        }
        start = args.length;
      }
    }
    return null;
  }

  // `cd docs && echo x > spec/rules.yaml` writes docs/spec/rules.yaml, so the
  // segment sequence carries its own working directory (one bash call = one
  // shell). `cd` itself writes nothing.
  if (commandWord === "cd" || commandWord === "pushd") {
    const destination = args[0];
    if (destination !== undefined && destination.indexOf("-") !== 0) {
      const next = resolveAgainstCwd(ctx.cwd, destination);
      if (next !== "") ctx.cwd = next;
    }
    return null;
  }

  if (isEnabled(ctx, "redirect")) {
    const blocked = scanRedirect(ctx, info, words);
    if (blocked !== null) return blocked;
  }
  if (isEnabled(ctx, "tee") && commandWord === "tee") {
    const blocked = scanTee(ctx, info, args);
    if (blocked !== null) return blocked;
  }
  if (isEnabled(ctx, "sed-in-place") && commandWord === "sed") {
    const blocked = scanSedInPlace(ctx, info, args);
    if (blocked !== null) return blocked;
  }
  if (
    isEnabled(ctx, "file-transfer") &&
    (commandWord === "cp" || commandWord === "mv" || commandWord === "install" || commandWord === "dd")
  ) {
    const blocked = scanFileTransfer(ctx, info, commandWord, args);
    if (blocked !== null) return blocked;
  }
  if (isEnabled(ctx, "heredoc")) {
    const blocked = scanHeredoc(ctx, info, commandWord, segment.text);
    if (blocked !== null) return blocked;
  }
  if (isEnabled(ctx, "git-mutating") && commandWord === "git") {
    const blocked = scanGitMutating(ctx, info, args);
    if (blocked !== null) return blocked;
  }
  return null;
}

// ── Command scan ────────────────────────────────────────────────────────────

function scanCommand(command: string, depth: number, ctx: ScanContext): BashScanDecision {
  if (depth > BASH_SCAN_MAX_DEPTH) {
    return {
      blocked: true,
      reason:
        "bash write-channel scanner: shell payloads nested more than " + String(BASH_SCAN_MAX_DEPTH) +
        " levels deep \u2014 refusing the command (fail closed)",
    };
  }

  const stripped = stripHeredocBodies(command);
  if (
    isEnabled(ctx, "heredoc") &&
    stripped.shellConsumed &&
    stripped.text.indexOf("<<") >= 0
  ) {
    return ambiguousReason(
      "heredoc",
      command,
      "a here-document in this command is piped/redirected into a shell or eval-like consumer, so the body is executed as a script",
    );
  }

  // One context per command: `cwd` must survive from segment to segment.
  const local: ScanContext = { ...ctx, depth, cwd: ctx.cwd };
  for (const text of splitTopLevelSegments(stripped.text)) {
    const segment: SegmentInfo = { text, command, ambiguous: null };
    const blocked = scanSegment(segment, local);
    if (blocked !== null) return blocked;
  }
  return { blocked: false };
}

/**
 * Scan one bash command line against the enabled pattern classes.
 *
 * Deterministic and side-effect free; every target is classified by
 * `evaluateGateFileWrite` unless `opts.evaluateTarget` overrides it (tests use a
 * stub so they need no WORKFLOW.md fixture). A throw is a DENIAL, never an allow.
 */
export function scanBashCommand(command: string, opts: BashScanOptions): BashScanDecision {
  const enabled = new Set<string>(opts.enabledPatterns ?? KNOWN_BASH_WRITE_PATTERNS);
  const ctx: ScanContext = {
    repoRoot: opts.repoRoot,
    enabled,
    evaluateTarget: opts.evaluateTarget ?? evaluateGateFileWrite,
    depth: opts.depth ?? 0,
    cwd: "",
  };
  try {
    return scanCommand(command, ctx.depth, ctx);
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    return {
      blocked: true,
      reason:
        "bash write-channel scanner internal error (" + message +
        ") \u2014 refusing the command (fail closed)",
    };
  }
}

/**
 * Gate entry point: load the live policy, then scan. An unavailable policy is a
 * DENIAL (the same refusal every other gate path returns) — a child must not run
 * bash under an undeclared rule set.
 */
export function evaluateBashWrite(repoRoot: string, command: string): BashScanDecision {
  let loaded: ReturnType<typeof loadWorkflowPolicy>;
  try {
    loaded = loadWorkflowPolicy(repoRoot);
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    return {
      blocked: true,
      reason: "bash write-channel scanner internal error (" + message + ") \u2014 refusing the command (fail closed)",
    };
  }
  if (!loaded.ok) {
    return { blocked: true, reason: "bash write-channel scanner: " + workflowPolicyRefusal(loaded.reason) };
  }
  return scanBashCommand(command, {
    repoRoot,
    enabledPatterns: loaded.policy.bashWritePatterns,
    evaluateTarget: evaluateGateFileWrite,
  });
}

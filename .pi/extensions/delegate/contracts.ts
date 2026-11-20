/**
 * Machine-parsed workflow contracts (batch 3).
 *
 * These strings are not configuration knobs — they are the wire format
 * between the orchestrator's final assistant message and the job registry,
 * plus the fixed vocabulary of the bash write-channel scanner.
 * `jobs.ts` greps for them (`APPROVAL_MARKER`, `APPROVAL_HEAD_SHA_PREFIX`,
 * `HANDOFF_TERMINAL_TOKENS`) and binds approval decisions to a HEAD SHA
 * (`APPROVAL_BINDING`); `bashscan.ts` dispatches only on
 * `BASH_WRITE_PATTERN_CLASSES`.
 *
 * They live in this dependency-free module so that BOTH sides can import them
 * without a cycle: `policy.ts` validates the WORKFLOW.md declaration against
 * them, `jobs.ts`/`bashscan.ts` enforce them. The policy file is the declared
 * source, this module is the code's expectation, and any disagreement is a
 * refusal (policy.ts → loadWorkflowPolicy) rather than a silent override.
 */

/** Exact marker a confirm-handoff orchestrator ends its approval round with. */
export const APPROVAL_MARKER = "AWAITING_APPROVAL";
/** Line prefix carrying the reviewed 40-hex worktree HEAD in an approval block. */
export const APPROVAL_HEAD_SHA_PREFIX = "HEAD SHA:";
/** What an approval decision is bound to; the only implemented binding mode. */
export const APPROVAL_BINDING = "head-sha";
/** Terminal handoff tokens; the orchestrator protocol puts one on the final line. */
export const HANDOFF_TERMINAL_TOKENS = ["HANDOFF_COMPLETE", "HANDOFF_BLOCKED"] as const;

/**
 * Bash write-channel scanner pattern classes (batch 3, stage 1).
 *
 * The `bash_write_patterns` field in WORKFLOW.md selects WHICH of these classes
 * the pre-execution bash scanner enforces. This frozen list is the canonical set
 * and the typo-protection authority: `policy.ts` refuses a policy whose list
 * names anything else, and `bashscan.ts` only ever dispatches on these strings.
 * It lives here (dependency-free) so both modules import it without a cycle.
 *
 *   - redirect:      `>`, `>>`, `<>`, `2>`, `&>` … whose target is a real file
 *   - tee:           `tee` / `tee -a` write targets
 *   - sed-in-place:  `sed -i` (any suffix form) in-place edits
 *   - file-transfer: `cp`, `mv`, `install` destinations and `dd of=`
 *   - heredoc:       heredoc payloads, and heredocs fed to a shell / eval
 *   - git-mutating:  `git checkout|restore|stash` pathspecs, `git apply|am`
 */
export const BASH_WRITE_PATTERN_CLASSES = [
  "redirect",
  "tee",
  "sed-in-place",
  "file-transfer",
  "heredoc",
  "git-mutating",
] as const;

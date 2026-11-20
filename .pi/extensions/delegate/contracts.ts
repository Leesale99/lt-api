/**
 * Machine-parsed workflow contracts (batch 3).
 *
 * These four strings are not configuration knobs — they are the wire format
 * between the orchestrator's final assistant message and the job registry.
 * `jobs.ts` greps for them (`APPROVAL_MARKER`, `APPROVAL_HEAD_SHA_PREFIX`,
 * `HANDOFF_TERMINAL_TOKENS`) and binds approval decisions to a HEAD SHA
 * (`APPROVAL_BINDING`).
 *
 * They live in this dependency-free module so that BOTH sides can import them
 * without a cycle: `policy.ts` validates the WORKFLOW.md declaration against
 * them, `jobs.ts` enforces them. The policy file is the declared source, this
 * module is the code's expectation, and any disagreement is a refusal
 * (policy.ts → loadWorkflowPolicy) rather than a silent override.
 */

/** Exact marker a confirm-handoff orchestrator ends its approval round with. */
export const APPROVAL_MARKER = "AWAITING_APPROVAL";
/** Line prefix carrying the reviewed 40-hex worktree HEAD in an approval block. */
export const APPROVAL_HEAD_SHA_PREFIX = "HEAD SHA:";
/** What an approval decision is bound to; the only implemented binding mode. */
export const APPROVAL_BINDING = "head-sha";
/** Terminal handoff tokens; the orchestrator protocol puts one on the final line. */
export const HANDOFF_TERMINAL_TOKENS = ["HANDOFF_COMPLETE", "HANDOFF_BLOCKED"] as const;

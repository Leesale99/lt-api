# S1 proposal — the sandbox-era WORKFLOW.md

`WORKFLOW.md` is the repo's machine-checked workflow policy, and it is protected from every delegated
session by design (self-denied). So the rewrite cannot be applied from inside a worker or orchestrator: this
directory holds the complete proposed file, and the host applies it by hand.

## What this is

- `WORKFLOW.md` (here) — the proposed complete replacement for the repo-root `WORKFLOW.md`. Body rewritten
  for the micro-VM world: the write boundary is the hypervisor plus the single mount, sandbox profiles per
  role, in-VM gates advisory, receipts/claims host-only, the integrity check
  (`git diff --exit-code <base> -- Makefile cmd/spec .github WORKFLOW.md`) replacing the deleted child-side
  deny paths + bash scanner + gate allowlist, PROVE at merge-back with merge-path enforcement, honest
  residuals and the not-built list.
- Front matter deliberately keeps today's values and today's shape: `version: 1`, the four gate commands, the
  two machine paths, the settle/stall/concurrency limits, and the approval block. It **omits**
  `bash_write_patterns`, `allowlist_path`, `protected_paths` and `pi_editable_paths` on purpose — the current
  loader falls back to today's full defaults when they are absent, so protection stays fully on during the
  transition, while the new loader drops those mechanisms entirely. It introduces no new fields (the loader
  refuses unknown ones) and `handoff_terminal_tokens` stays the pair the code validates
  (`HANDOFF_COMPLETE`, `HANDOFF_BLOCKED`); `AWAITING_APPROVAL` is declared as `red_approval_marker`, not as a
  terminal token.

Verified against the in-tree loader: `loadWorkflowPolicy()` on this file returns `ok: true` with every
protection default intact.

## How to apply it

From the worktree root, after the S1 code batch is in place:

    cp .pi/plans/delegate/workflow-s1-proposal/WORKFLOW.md WORKFLOW.md

Then re-run the gate suite once (`make audit/static`, plus the scoped spec check) to confirm the policy still
loads and nothing was gated differently. The copy is a plain overwrite — there is no merge step and no
template substitution; the file is self-contained.

## Why a human hand is required

`WORKFLOW.md` is self-denied to every delegated session (a code invariant, not an allowlist entry): a worker
may never rewrite the file that declares the gates binding it, and an orchestrator may not either. That
invariant is what makes the policy trustworthy, so the last write is always the human's.

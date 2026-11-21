# Workflow roadmap — durable session state

## Where things are
- Branch: `workflow/hardening` (cut from main) carries batch 1 (agentic workflow hardening) via cherry-pick of `delegate/orchestrator-20261120-215402-k6zqlq` (9 commits, `-x` origin SHAs recorded), plus a chore commit preserving the orchestrator-agent model frontmatter tweak. Full `make audit` + `make prove` (30/30 VERIFIED) were green in the original worktree before relocation; tree-diff against the old delegate branch at relocation time: NON-EMPTY — only `.pi/agents/orchestrator.md` (the intended frontmatter chore, not code/spec/docs), so the green verification carries over; audit/static re-run green here (0 errors, 18 report-only warnings).
- Superseded artifacts (delete only after batch 2 confirms green): worktree `../lt-api-wt-orchestrator-20261120-215402-k6zqlq`, branch `delegate/orchestrator-20261120-215402-k6zqlq`.
- Batch 2 runs as a background orchestrator job (newest `orchestrator-*` under `.pi/jobs/`; its full task text is that job's `.task` artifact). HANDOFF=confirm; merges back into `workflow/hardening`.

## What happens next
1. Batch 2 lands (approve via job_approve) → merge into `workflow/hardening`.
2. First dreaming/consolidation ritual: foreground librarian pass over the vault — replay the phase's sessions vs Lessons/ and phase notes, correct stale entries, merge duplicates, prune, keep Home.md Now to a few lines.
3. Then Phase 06 (R-LEDGER-001..007, spec-first) on the new machinery: red-commit approval → green → prove → refactor.

## Decisions ledger (stable)
- Spec-first loop = ordering + scope control; PROVE phase = quality gate (per-rule mutants, oracle assertions); red evidence bound to the red commit, never prose claims.
- Red-commit approval: stage-granular, content-bound (SHA), both channels (in-session stop + AWAITING_APPROVAL mid-job state).
- audit/static (no DB) gates workers; full `make audit` = single merge-boundary authority; `make prove` = slower second gate; VACUOUS/BROKEN-TEST never waivable.
- Receipts: every gate run emits a JSON receipt outside worker-writable space; worker reports = commands + exit codes + receipt paths.
- Reviewer: on-demand only, clean context, different model family preferred, capped ~2 rounds; no standing review ritual.
- AGENTS.md stays minimal (~15 lines): non-derivable facts + pointers.

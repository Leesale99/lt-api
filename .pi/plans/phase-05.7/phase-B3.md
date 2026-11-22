# Phase B3 — extension decomposition + contracts

HANDOFF=confirm. SPEC-SCOPE none.

## B3.1 — protocol.ts (defect 3)

- One module owns the task-packet grammar: `SPEC-SCOPE: none | module=<m> | rules=<id,...>` and `CLAIMS-PATHS: <paths>` — parsed once at the `subagent` tool boundary. Unparsable/garbage → tool-result error naming the bad line (fail-closed at spawn). Valid scope → `PI_DELEGATE_SPEC_SCOPE`. Whitespace-only `CLAIMS-PATHS` entries are errors, not silently dropped.
- Doc-of-record: a short "Task packets" section in WORKFLOW.md (grammar table, owned by `protocol.ts`). Delete the packet grammar from every other carrier — personas keep ONE line ("state the worker's scope on a SPEC-SCOPE line").

## B3.2 — mergegate.ts (defect 2)

- THE job-scope resolver: claims-first, registry task text fallback — one implementation. Consumers: `jobs.relayApprovalDecision` AND the merge-path prove block. Relay-approved-but-merge-blocked on the same scope becomes structurally impossible.
- Prove-receipt policy lives here: `proveRuleIdsIn`, `isProveSegment`, receipt `--rule` matching — one regex, one place.

## B3.3 — WORKFLOW.md `gate_file_paths` (decision 13)

- `policy.ts`: `FIELD_KINDS` gains `gate_file_paths` (repo-relative-path list, optional; default = the current four paths). `gates.ts` `GATE_FILE_PATHS` const dies → policy value. Integrity behavior unchanged. **One commit: field + code together.**
- Smoke: a fixture declares the field; unknown-field refusal still covered for other names.

## B3.4 — module extraction from index.ts (target root ≤ ~450 lines)

- Extract: `persona.ts` (prompt assembly: main.md + child orchestrator; no inline protocol prose), `spawn.ts` (`runSingleAgent` + invocation/verify), `surfacing.ts`, `tui/` (badges, viewers, widgets), `dispatch.ts` (mode branching, claim prepare/release, refusals), plus B3.1 `protocol.ts` and B3.2 `mergegate.ts`.
- The 11 exports that exist only as smoke-ui seams move to their new modules; smoke imports update (they are test seams, not an API).
- `sbx.ts` absorbs `GATE_DB_*` + the DB pre-steps (image knowledge out of the runner).
- Settle/backoff constants: `policy.ts` defaults become the single source; the `gates.ts` re-exports die.

## B3.5 — residual prose reconciliation

- Profile tables: WORKFLOW.md + `.pi/sandboxes/README.md` keep one pointer line each ("`sbx.ts` `SANDBOX_PROFILES` is the source; `smoke-sbx.mjs` asserts").
- Role-name drift: `go-worker` (WORKFLOW.md prose) vs `worker` (`SANDBOX_PROFILES`) → align on `worker` everywhere.
- `DEFAULT_*_CMD` documented in the `policy.ts` header as fixture-fallbacks only (no behavior change).

## Acceptance

`make smoke` green — all three harnesses. New smoke cases: (a) garbage `SPEC-SCOPE` line → spawn refused with a named error; (b) task text without SPEC-SCOPE + claim carrying ruleIds → both consumers answer identically. Note: live HANDOFF exercise happens in the session AFTER merge — this job verifies via smoke (the session running this job still executes the old extension code).

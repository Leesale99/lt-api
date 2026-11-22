# Phase C — documentation (after A + B land)

HANDOFF: decide when C starts. SPEC-SCOPE none.

Prerequisite: A, B1, B2, B3 merged. The shape below may shift with what lands — expand this file into full tasks when C kicks off.

## Repo: new-developer docs

- "The system at a glance" — one page (`docs/README.md` or `docs/architecture.md`): the four planes (application / spec system / delegate harness / knowledge), how one change flows end-to-end (rule → red → implement → prove → merge), where each gate lives and who owns each format.
- Per-plane deep dives, high level first:
  - Spec pipeline: rewrite `docs/spec/README.md` for the per-file format, `spec work`, the policy registry.
  - Delegate workflow: a narrative doc (WORKFLOW.md stays the terse machine-checked policy; the narrative lives in `docs/` — decide exact home at C start).
  - Application: `README.md` is already the model — light touch only.
- Every doc names its plane's single source of truth (code pointer or policy file).

## Gherkin styleguide rewrite

- Post-A2: rewrite `gherkin-style.md` present-tense for the new catalog format — no history, no `@known-defect`.

## Vault (via librarian)

- Rewrite `Design/Executable specification pipeline` and `Design/Delegate orchestration` to match the new reality; new ADRs for the per-file catalog format and the extension decomposition (follow the ADR sequence per conventions).
- Close the Phase 05.7 note (PR links, final HEAD); update Home `Now` → Phase 06 next.

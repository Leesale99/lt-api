# Phase C — documentation

HANDOFF=auto (docs-only; the user reviews prose post-merge and iterates freely).

Expanded at C start: A+B fully merged at `232c08f` (phase log in 00-plan.md), and a session restart has exercised the new extension live (persona from `.pi/agents/main.md`, `gate_file_paths` validation, `worker` roster).

## C1 — repo: the system at a glance

- New `docs/README.md`: the hub page for a new developer, plain prose, no diagrams. The four planes (application / executable specification / delegate harness / knowledge vault), the one-change flow (rule file + scenario → red → implement → prove → merge), where each gate lives and who owns each format. Every plane section names its single source of truth (code pointer or policy file) and links to the deep dive.

## C2 — repo: per-plane deep dives

- NEW `docs/delegate-workflow.md` — the narrative home (decision made at C start): how delegated work actually runs — the write boundary (the mount), advisory vs authoritative gates, RED/GREEN/PROVE, task packets, claims and receipts, handoff modes, the approval contract. WORKFLOW.md stays the terse machine-checked policy: mechanics are narrated here, policy stays there — link, never copy.
- `docs/spec/README.md`: full rewrite for the per-file format — high level first (what the spec system is for), then authoring a rule file (front matter incl. `write_scope`/`stale_pointer`/`tie`, `## Notes`/`## Refusal` body), the policy registry, `spec work`, `spec check`, the `spec/gen` dashboard, prove.
- Application plane: `README.md` light touch only — it is the model; fix only stale references (brief/queue → work, rules.yaml → rules/).

## C3 — gherkin styleguide rewrite

- `docs/spec/gherkin-style.md`: present-tense rewrite for the per-file catalog — no history, no `@known-defect`; grep recipes use filename conventions (`docs/spec/rules/<module>/R-XXX-nnn.md`).

## C4 — cross-reference sweep

- Repo-wide grep for stale terms: `rules.yaml`, `spec brief`, `spec queue`, `@known-defect`, `go-worker`, `.pi/delegate/orchestrator.md`, `views/`. Fix hits in tracked docs/prompts/personas; code hits = investigate, don't blind-replace. Known exception: `docs/league-tokens-learning-phases/phase-06-*.md:171` is parked in 00-plan.md as the first Phase 06 action — leave it.

## C5 — vault (via librarian worker)

- Rewrite `Design/Executable specification pipeline` and `Design/Delegate orchestration` to the new reality: per-file catalog + kernel + policy registry + `spec work`; protocol/mergegate/persona/spawn/surfacing/tui modules with a thin root; receipts schema v2 + catalogHash.
- ADRs per vault conventions, following the existing sequence: per-file catalog format (supersession chain from ADR-030 as needed), extension decomposition + contracts, receipts schema v2.
- Close `Phases/Phase 05.7 - Spec and delegate meta-refactor.md`: fill the PR/HEAD placeholders with the landed commits, outcome summary, status per the vault's completion convention.
- `Home.md` Now → Phase 06 planned next (its first action is the parked rules.yaml-line fix in the phase-06 doc).

## Worker split

- Worker A (general): C1 + C2 — the hub page, delegate-workflow.md, spec README rewrite. Authoring quality is the deliverable.
- Worker B (general): C3 + C4 — styleguide rewrite + sweep.
- Worker C (librarian): C5 — vault rewrites, ADRs, phase note close, Home update.
- Workers A/B touch disjoint files; C runs after A/B land (the vault notes describe the final repo state).

## Acceptance

- `make audit/static` green (catches accidental gate-file dirt); `make smoke` cheap insurance if anything under `.pi/` moved.
- The orchestrator reads the produced docs end-to-end and iterates once before merging — docs quality is the deliverable, exit codes are not.
- Post-merge sweep grep returns only intentional history or the parked phase-06 line.

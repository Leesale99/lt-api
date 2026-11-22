---
id: R-PLATFORM-013
title: Contexts depend one way and meet only at facades
statement: |
  A context uses another context only through the other's public operations, in one
  allowed direction, and never reaches into its tables. The context that owns the fact
  answers; the context that needs it declares what it needs and has it satisfied from
  outside; and where the two vocabularies meet, the translation happens in one place so
  neither context speaks the other's error language.
module: platform
entity: context_dependency
category: boundary
status: implemented
authority: composition-root
mechanism: null
write_scope: cross-table
enforcement:
  - site: cmd/api/main.go::main
    layer: service
    role: authoritative
    pinned_by: null
  - site: cmd/api/gamerefs.go::gameRefs.DeletePlayersForSeasonTx
    layer: service
    role: authoritative
    pinned_by: null
  - site: internal/identity/identity.go::Module
    layer: handler
    role: authoritative
    pinned_by: null
  - site: internal/kernel/httpx/middleware.go::UserLookup
    layer: handler
    role: defense-in-depth
    pinned_by: null
  - site: internal/kernel/httpx/middleware.go::PermissionLookup
    layer: handler
    role: defense-in-depth
    pinned_by: null
refusal: null
fixture: null
verification:
  unit:
    - internal/arch/arch_test.go::TestImportBoundaries
    - internal/arch/arch_test.go::TestNonModulesNeverImportModules
    - internal/arch/arch_test.go::TestModulesNeverImportPlatform
    - internal/arch/arch_test.go::TestKernelImportsOnlyKernel
  e2e:
    - internal/e2e/teams_test.go::TestDeleteTeamHandler
  gherkin: null
expert-ref: docs/league-tokens-learning-phases/phase-05-modular-monolith-boundaries.md
knowledge-ref: null
stale_pointer: |-
  phase-05 §"As built" names `cmd/server` as the composition root and the hoisted `internal/idempotency`, and lists "api — the HTTP gateway" as a component; the binary is cmd/api, the idempotency engine is internal/kernel/idempotency and there is no api package (each module owns its transport). The doc's §"Suggested ownership" is already flagged in the extraction as superseded by its own §"As built".
tie: |-
  the copies cannot drift silently — TestImportBoundaries walks every import in the tree against internal/arch's allowedImports, so an httpx -> identity edge or a schedule -> identity edge fails the build's test first.
---

## Notes

Authority is the composition root (ADR-028): it is the only place that may import everything, so it is where every direction is chosen — the game context receives the calendar facade (wrapped by its own anti-corruption layer), the identity facade (as a consumer-side lookup), the ledger and the idempotency engine; the calendar receives the game-reference port from outside rather than importing game. Layer note: the wiring has no layer of its own in the frozen enum; it is recorded as service. The pinned direction itself lives in internal/arch/arch_test.go's allowedImports matrix (test-time enforcement, so it appears under verification, not as a site): a module reaches another module's root only, nothing below the modules reaches a module, no module reaches the platform, the kernel reaches the kernel only. The gamerefs adapter is the single vocabulary translator (game's record-in-use becomes the calendar's), which is what keeps the delete refusals byte-identical. Merged with provisional R-IDENTITY-021 (Phase B3): identity's own statement of this rule — other parts reach identity through its published surface only (the two lookups httpx declares and the account read the player flow needs), never its models, repositories or tables, and the transport layer holds no identity import — is the same composition-root decision seen from one edge, so it is one rule with the union of sites and identity's slot is freed. The three identity sites are the facade the root builds once (identity.Module, authoritative) and the two consumer-side shapes that let httpx stay identity-free (defense-in-depth). Identity's facade also re-exports the model's types and sentinels as aliases, so callers name identity's vocabulary without importing its internals; game's player handler mapping identity.ErrNotFound onto its own validation error is the only caller that depends on one of those sentinels. No refusal of its own: the drift is a compile- or test-time failure, never a status a client sees — which is also why the layer value is approximate (the frozen enum has no composition-root layer; `handler` is the seam the facade satisfies). R-GAME-026 is the distinct publisher-language half of the same boundary at game's edge.

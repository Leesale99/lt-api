---
id: R-IDENTITY-010
title: A user row is only rewritten by a writer holding the version it read
statement: |
  Every write to an account carries the version the writer read, the store bumps it,
  and a write whose claim no longer matches the stored row is refused instead of
  overwriting. Two writers that read the same version cannot both win, so no update is
  silently lost.
module: identity
entity: user
category: invariant
status: implemented
authority: repository
mechanism: null
write_scope: null
enforcement:
  - site: internal/identity/internal/repository/user_repo.go::UserRepository.update
    layer: repository
    role: authoritative
    pinned_by: null
  - site: internal/identity/internal/service/activation.go::Service.Activate
    layer: service
    role: defense-in-depth
    pinned_by: null
refusal:
  code: synthetic
  constraint: null
  sentinel: ErrEditConflict
  http: 409
fixture: null
verification:
  unit:
    - internal/identity/internal/repository/user_repo_test.go::TestUpdateTx
    - internal/identity/internal/service/activation_test.go::TestActivate
  e2e: null
  gherkin: null
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: |-
  the service site fails loudly — Activate reads the row inside its own transaction precisely so the flagged write re-checks against the snapshot the read produced, and if the read moved outside the transaction the concurrent-activation subtest of TestActivate would start double-bumping.
---

## Notes

Authority is the repository, deviating from the invariant default (database): a compare-and-set on a version column is not expressible as a constraint, and it is path-dependent by nature — a new UPDATE that forgets `AND version = $n` bypasses it silently. Being honest about the cost: nothing pins the call set today beyond the single wrapper — there is one guarded write (UserRepository.update) and one exported door to it (UpdateTx); the non-transactional Update is gone, so the surface cannot grow a second caller that forgets the version. The repository trade-off the schema warns about is therefore cheap only because the surface is one method. The version counter is bumped by the same statement (`version = version + 1`) and returned by RETURNING, so the caller never has to remember to increment. Distinguishing the two zero-row causes: the same statement also maps a users_email_key violation to ErrDuplicateEmail before the no-rows case, so an email clash is never reported as an edit conflict (TestUpdateTx's duplicate-on-update subtest pins the order).

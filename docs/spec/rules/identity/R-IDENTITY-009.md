---
id: R-IDENTITY-009
title: Activating an account spends every activation token it holds
statement: |
  An activation token is single-use. The moment an account is activated, the activation
  flag is flipped and every activation token that account holds is deleted in the same
  unit of work — so a second presentation of the same token finds nothing, and a crash
  halfway through leaves neither the flip nor the cleanup behind.
module: identity
entity: user_token
category: orchestration
status: implemented
authority: service
mechanism: null
write_scope: null
enforcement:
  - site: internal/identity/internal/service/activation.go::Service.Activate
    layer: service
    role: authoritative
    pinned_by: null
  - site: internal/identity/internal/repository/user_repo.go::UserRepository.UpdateTx
    layer: repository
    role: defense-in-depth
    pinned_by: null
  - site: internal/identity/internal/repository/user_token_repo.go::UserTokenRepository.DeleteAllForUserTx
    layer: repository
    role: defense-in-depth
    pinned_by: null
refusal:
  code: synthetic
  constraint: null
  sentinel: ErrNotFound
  http: 422
fixture: null
verification:
  unit:
    - internal/identity/internal/service/activation_test.go::TestActivate
    - internal/identity/internal/repository/user_token_repo_test.go::TestDeleteAllForUserTx
  e2e: null
  gherkin:
    - docs/spec/features/identity/account.feature::The same activation token cannot be spent twice
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: |-
  they fail loudly, because leaving the transaction makes the test-only delete trigger's refusal commit a half-flip and TestActivate's "a failed cleanup rolls the flip back — nothing is left behind" subtest fails first.
---

## Notes

Orchestration, service authority, no deviation: the rule spans two repositories, so no single repository can own it, and the service holds the pool precisely to open that transaction (the ordering argument is in the package comment, and the rejected alternatives — a data-modifying CTE, or transaction choreography in the handler — are recorded there too). The two repository sites are the statements inside the unit, not decisions: The cleanup is by scope and user, not by the presented token, so an old activation mail cannot be replayed after a successful activation, and the deep-link case (two mails in flight) ends with both tokens dead — TestDeleteAllForUserTx pins that only the target scope of the target user goes. The concurrent-activation subtest pins the exactly-once shape: a committed flip, or ErrEditConflict, or ErrNotFound, never a double bump. The refusal is the lookup's miss (unknown, expired, wrong-purpose or already-spent token) rendered as "invalid or expired activation token" — 422, deliberately not a 401, because the client is activating, not authenticating. The non-transactional DeleteAllForUser variant is gone (misalignment-report §6.21): the cleanup exists only in the Tx form the activation use case runs and TestDeleteAllForUserTx commits, so there is no second way to delete tokens outside the unit.

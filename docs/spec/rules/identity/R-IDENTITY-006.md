---
id: R-IDENTITY-006
title: Only a token's hash is ever stored
statement: |
  The service keeps only a fingerprint of each token it issues; the token text itself
  exists in memory, travels to the user once, and is never written to the database.
  A presented token is fingerprinted the same way before the lookup, so a stolen copy
  of the table reveals nothing that works as a credential.
module: identity
entity: user_token
category: invariant
status: implemented
authority: database
mechanism: constraint
write_scope: row-local
enforcement:
  - site: migrations/000003_create_user_tokens_table.up.sql
    layer: db-constraint
    role: authoritative
    pinned_by: null
  - site: internal/identity/internal/repository/user_repo.go::UserRepository.getForToken
    layer: repository
    role: defense-in-depth
    pinned_by: null
  - site: internal/identity/internal/repository/user_token_repo.go::UserTokenRepository.insert
    layer: repository
    role: defense-in-depth
    pinned_by: null
refusal:
  code: "23505"
  constraint:
    - user_tokens_pkey
  sentinel: null
  http: null
fixture: null
verification:
  unit:
    - internal/identity/internal/repository/user_token_repo_test.go::TestNewAndGetForToken
    - internal/identity/internal/model/user_token_test.go::TestGenerateUserToken
  e2e: null
  gherkin:
    - docs/spec/features/identity/account.feature::The database refuses the same token a second time
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: |-
  they cannot drift silently, because a mismatch makes every token unresolvable and TestNewAndGetForToken's "New persists a resolvable token" subtest fails first.
---

## Notes

Database authority because the table's shape is the rule: user_tokens has hash, user_id, expiry and scope — there is no column a cleartext token could be stored in, and hash is the primary key, so the same fingerprint can never appear twice (a second row for one token is a 23505 on user_tokens_pkey, not a duplicate credential). That holds for every writer, including psql and a future service, which is exactly what the database-vs-repository distinction in the schema asks for. The two Go sites are the two halves of the fingerprint that must agree: GenerateUserToken hashes what it mints (SHA-256) and getForToken hashes what the caller presented before comparing — The e2e fixture mirrors the same discipline: only SHA-256 digests live in seed_test.go and the preimages live in auth_test.go. Refusal: the constraint's refusal is the 23505 on user_tokens_pkey, and it is unmapped — insert returns the driver error untouched, so a second row for one fingerprint falls through rather than becoming a translated status; the read path is the other surface: a token whose fingerprint is not in the table is simply "not found" — the authentication edge renders its sentinel as 401 (and the activation edge renders the same sentinel as a 422 field error).

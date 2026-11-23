---
id: R-IDENTITY-003
title: A password is stored only as its hash, and never leaves the process
statement: |
  The password a user types is never stored and never written down anywhere: the
  account row holds a deliberately slow, salted hash (bcrypt, work factor twelve)
  and nothing else. The cleartext exists only in memory while the request that
  carried it is in flight.
module: identity
entity: user
category: invariant
status: implemented
authority: domain
mechanism: null
write_scope: null
enforcement:
  - site: internal/identity/internal/model/user.go::password.Set
    layer: domain
    role: authoritative
    pinned_by: null
  - site: internal/identity/internal/repository/user_repo.go::UserRepository.Insert
    layer: repository
    role: defense-in-depth
    pinned_by: null
  - site: internal/identity/internal/repository/user_repo.go::UserRepository.update
    layer: repository
    role: defense-in-depth
    pinned_by: null
  - site: migrations/000002_create_users_table.up.sql
    layer: db-constraint
    role: defense-in-depth
    pinned_by: null
refusal:
  code: synthetic
  constraint: null
  sentinel: null
  http: 500
fixture: null
verification:
  unit:
    - internal/identity/internal/model/user_test.go::TestPasswordHashRoundTrip
    - internal/identity/internal/repository/user_repo_test.go::TestInsert
  e2e:
    - internal/e2e/users_test.go::TestRegisterUserHandlerSuccessState
  gherkin: null
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: |-
  both repository sites fail loudly — they never drift silently, because dropping the panic lets 000002's NOT NULL refuse the row (23502) and TestInsert's "hashes are stored, plaintext is not" subtest fails first.
---

## Notes

Authority is the domain, deviating from the invariant default (database) on purpose: no constraint can tell a hash from a cleartext, so the database can only demand that *something* is there. The decision "a password is only ever handed over hashed" is password.Set's, and it is unreachable from outside: the type is unexported, its fields are unexported, and callers can only Set (hash a cleartext), Matches (compare) or move an opaque hash in and out. The work factor twelve is part of this rule because it is the cost that makes the hash useless to an offline attacker at the project's chosen budget; the same 12 is what the kernel's DB budget comment warns about (hashing must run outside persistence.DBTimeout), and the seed fixtures store $2a$12$ hashes. Refusal surface: a nil hash is a programming error, not a client error — Insert/update panic ("missing password hash for the user") and the panic recovery renders 500; the migrations' password_hash NOT NULL is the last line of defence for a writer that bypasses Go. Coverage gap: no test asserts the work factor (a cost-10 hash would keep every named test green); a fixture asserting bcrypt.Cost(hash) == 12 closes it.

---
id: R-IDENTITY-011
title: An email address identifies exactly one account, whatever its case
statement: |
  Two accounts can never share an email address, and letter case does not distinguish
  them: registering ALICE@EXAMPLE.COM after alice@example.com is the same identity
  being asked for twice. The store refuses the second registration and the service
  answers it as a field error on the email, so the client sees one clear refusal rather
  than a server error. Logins and lookups compare the same way, case-insensitively.
module: identity
entity: user
category: invariant
status: implemented
authority: database
mechanism: constraint
write_scope: row-local
enforcement:
  - site: migrations/000002_create_users_table.up.sql
    layer: db-constraint
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
  - site: internal/identity/internal/repository/user_repo.go::UserRepository.GetByEmail
    layer: repository
    role: defense-in-depth
    pinned_by: null
refusal:
  code: "23505"
  constraint:
    - users_email_key
  sentinel: ErrDuplicateEmail
  http: 422
fixture: null
verification:
  unit:
    - internal/identity/internal/repository/user_repo_test.go::TestInsert
    - internal/identity/internal/repository/user_repo_test.go::TestUpdateTx
    - internal/identity/internal/repository/user_repo_test.go::TestGetByEmail
  e2e:
    - internal/e2e/users_test.go::TestRegisterUserHandlerDuplicateIsolatedFromValidation
  gherkin:
    - docs/spec/features/identity/account.feature::An email already taken in another case is refused as a duplicate
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: |-
  they cannot drift silently, because the translation is by constraint name (`persistence.IsUniqueViolation (err, "users_email_key")`), so renaming or replacing the index in a migration makes TestInsert's "duplicate email maps to ErrDuplicateEmail" subtest fail first, and GetByEmail's case-insensitive subtest fails the moment the column stops being citext.
---

## Notes

Database authority: one unique index on a citext column, so the refusal survives every writer (the app, psql, a future service, a backfill) and the case-insensitivity is a property of the column type rather than of any Go comparison. That is the whole reason the rule is not the repository's: a new INSERT cannot forget it. The three repository sites are the two write translations plus the read half — The refusal chain is the one the e2e test p10 was written for: 23505 → ErrDuplicateEmail → a single "email" field error with "a user with this email address already exists" and 422, with no other field errors alongside it (a nil *pgconn.PgError dereference used to panic here). Note the mapping is by name only: any other unique violation on the table falls through to a 500. Case-insensitivity has a second consequence outside this rule: the login lookup is case-insensitive too, so a user may type their address in any case and still reach the same account.

<!-- GENERATED — do not edit. -->
# Business rules — `identity`

> **GENERATED — do not edit.** This is the `identity` module's slice of the readable
> catalog view. The canonical, hand-edited catalog is `docs/spec/rules.yaml`.
>
> Change the YAML, never this file, then regenerate from the dashboard:

```sh
go run ./cmd/spec gen --rules docs/spec/rules.yaml --out docs/spec/business-rules.md
```

The rules themselves live in `../features/identity/`; the format authority is
`rules.schema.md` and the checker is `go run ./cmd/spec check`.

**22 rules** — 22 implemented.

## Module `identity`

### Entity `module`

#### R-IDENTITY-021 — Identity's tables are reachable only through identity's own database role

`implemented` · `boundary` · authority `composition-root`

Who logs in and what they may do lives in its own schema, writable and readable only by the role that speaks for that context. Every other context's role is denied on those tables, and identity's role is denied on theirs, so a cross-context read cannot sneak in through SQL even if someone forgets the import rule.

**Authority** `composition-root`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `migrations/000018_context_roles.up.sql` | `repository` | `authoritative` | — |
| `migrations/000015_split_context_schemas.up.sql` | `repository` | `defense-in-depth` | — |

**Refusal** `42501` → _(unnamed)_ → _(unmapped)_ → _(falls through to 500)_

**Verification**

- unit: `internal/testdb/boundary_test.go::TestIdentityAppBoundary`
- unit: `internal/testdb/boundary_test.go::TestIdentityAppInsertAllowed`

**Expert ref** `docs/league-tokens-learning-phases/phase-05-modular-monolith-boundaries.md`

### Entity `permission`

#### R-IDENTITY-013 — Every permission code is unique and every grant is one role-permission pair

`implemented` · `invariant` · authority `database`

A permission is identified by its code, and a code exists once: there is one row per thing a caller may do. A grant says "this role carries this permission", at most once per pair, and both sides of the pair must exist. So "does this role carry this code" has exactly one answer.

**Authority** `database` · mechanism `constraint` · scope `row-local`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `migrations/000004_add_permissions.up.sql` | `db-constraint` | `authoritative` | — |

**Refusal** `23505` → `permissions_code_key`, `roles_permissions_pkey` → _(unmapped)_ → _(falls through to 500)_

**Verification**

- e2e: `internal/e2e/auth_test.go::TestRequirePermissionMiddleware`

#### R-IDENTITY-014 — Two roles and eleven permission codes are seeded reference data

`implemented` · `policy` · authority `database`

The vocabulary of what a caller may do is fixed: eleven codes covering reading and writing the calendar, the players and the teams, plus the right to manage other people's players. Two roles exist — administrator, which holds all eleven, and the standard user, which holds the read codes plus the right to manage its own player. Nothing in the running service adds, removes or re-grants a permission.

**Authority** `database` · mechanism `constraint` · scope `row-local`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `migrations/000004_add_permissions.up.sql` | `db-constraint` | `authoritative` | — |

**Refusal** `23505` → `permissions_code_key`, `roles_name_key` → _(unmapped)_ → _(falls through to 500)_

**Verification**

- e2e: `internal/e2e/auth_test.go::TestRequirePermissionMiddleware`
- e2e: `internal/e2e/rides_test.go::TestRideRoutesPermissionGate`

#### R-IDENTITY-015 — A caller's permissions are its role's, resolved on every request

`implemented` · `policy` · authority `repository`

Permissions hang off roles, never off accounts: there is no per-account grant and no cached set, so two accounts holding the same role are indistinguishable to the gate and a grant change takes effect on the next request. An account whose role carries nothing resolves to an empty set rather than to an error.

**Authority** `repository`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/identity/internal/repository/permission_repo.go::PermissionRepository.GetAllForRole` | `repository` | `authoritative` | — |

**Verification**

- e2e: `internal/e2e/auth_test.go::TestRequirePermissionMiddleware`

#### R-IDENTITY-017 — A gated route requires the exact permission code it names

`implemented` · `policy` · authority `handler`

A gated route is served only to a principal whose role carries the one permission code the route names; anything else is refused before the handler runs. The resolved set travels with the request so a handler can ask about further codes without a second lookup, and the codes are matched exactly, one at a time.

**Authority** `handler`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/kernel/httpx/middleware.go::Middleware.RequirePermission` | `handler` | `authoritative` | — |
| `internal/kernel/httpx/context.go::Permissions.Include` | `handler` | `authoritative` | — |
| `internal/kernel/httpx/errors.go::MissingPermission` | `handler` | `authoritative` | — |
| `internal/game/internal/adapter/inbound/http/routes.go::Handler.RegisterRoutes` | `handler` | `defense-in-depth` | — |

**Refusal** `synthetic` → _(unnamed)_ → _(unmapped)_ → `403`

**Verification**

- e2e: `internal/e2e/auth_test.go::TestRequirePermissionMiddleware`
- e2e: `internal/e2e/rides_test.go::TestRideRoutesPermissionGate`

### Entity `user`

#### R-IDENTITY-001 — Identity requests are framed before anything else happens

`implemented` · `interface` · authority `handler`

A registration must carry a non-empty name of at most five hundred bytes and an email that is non-empty and syntactically well formed. Every request that carries an email — registration, activation-token request and login alike — is held to the same email shape. A request that fails is refused with the field errors and creates or changes nothing.

**Authority** `handler`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/identity/internal/model/user.go::ValidateRegistration` | `validator` | `authoritative` | — |
| `internal/identity/internal/model/user.go::ValidateEmail` | `validator` | `authoritative` | — |

**Refusal** `synthetic` → _(unnamed)_ → _(unmapped)_ → `422`

**Verification**

- unit: `internal/identity/internal/model/user_test.go::TestValidateRegistration`
- e2e: `internal/e2e/users_test.go::TestRegisterUserHandler`

#### R-IDENTITY-002 — A password is eight to seventy-two bytes, checked before it is hashed

`implemented` · `interface` · authority `handler`

A password presented at registration or login must be at least eight bytes and at most seventy-two bytes long. The ceiling is not cosmetic: the password hashing step refuses anything longer than seventy-two bytes outright, while the comparison step ignores everything past the seventy-second — so an over-long password is either a server error or silently interchangeable with its own prefix. Both bounds are checked before any hashing runs, which turns the over-long case into a field refusal instead.

**Authority** `handler`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/identity/internal/model/user.go::ValidatePasswordPlaintext` | `validator` | `authoritative` | — |

**Refusal** `synthetic` → _(unnamed)_ → _(unmapped)_ → `422`

**Verification**

- unit: `internal/identity/internal/model/user_test.go::TestValidateRegistration`
- e2e: `internal/e2e/users_test.go::TestRegisterUserHandler`

#### R-IDENTITY-003 — A password is stored only as its hash, and never leaves the process

`implemented` · `invariant` · authority `domain`

The password a user types is never stored and never written down anywhere: the account row holds a deliberately slow, salted hash (bcrypt, work factor twelve) and nothing else. The cleartext exists only in memory while the request that carried it is in flight.

**Authority** `domain`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/identity/internal/model/user.go::password.Set` | `domain` | `authoritative` | — |
| `internal/identity/internal/repository/user_repo.go::UserRepository.Insert` | `repository` | `defense-in-depth` | — |
| `internal/identity/internal/repository/user_repo.go::UserRepository.update` | `repository` | `defense-in-depth` | — |
| `migrations/000002_create_users_table.up.sql` | `db-constraint` | `defense-in-depth` | — |

**Refusal** `synthetic` → _(unnamed)_ → _(unmapped)_ → `500`

**Verification**

- unit: `internal/identity/internal/model/user_test.go::TestPasswordHashRoundTrip`
- unit: `internal/identity/internal/repository/user_repo_test.go::TestInsert`
- e2e: `internal/e2e/users_test.go::TestRegisterUserHandlerSuccessState`

#### R-IDENTITY-004 — An account response never carries the hash or the internal version

`implemented` · `boundary` · authority `handler`

What the service says about an account is its id, when it was created, its name, its email, whether it is activated and which role it holds. The stored hash and the internal version counter are never part of a response.

**Authority** `handler`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/identity/internal/handler/dto.go::newUserResponse` | `handler` | `authoritative` | — |

**Verification**

- e2e: `internal/e2e/users_test.go::TestRegisterUserHandlerSuccessState`
- e2e: `internal/e2e/users_test.go::TestRegisterUserHandler`

#### R-IDENTITY-010 — A user row is only rewritten by a writer holding the version it read

`implemented` · `invariant` · authority `repository`

Every write to an account carries the version the writer read, the store bumps it, and a write whose claim no longer matches the stored row is refused instead of overwriting. Two writers that read the same version cannot both win, so no update is silently lost.

**Authority** `repository`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/identity/internal/repository/user_repo.go::UserRepository.update` | `repository` | `authoritative` | — |
| `internal/identity/internal/service/activation.go::Service.Activate` | `service` | `defense-in-depth` | — |

**Refusal** `synthetic` → _(unnamed)_ → `ErrEditConflict` → `409`

**Verification**

- unit: `internal/identity/internal/repository/user_repo_test.go::TestUpdateTx`
- unit: `internal/identity/internal/service/activation_test.go::TestActivate`

#### R-IDENTITY-011 — An email address identifies exactly one account, whatever its case

`implemented` · `invariant` · authority `database`

Two accounts can never share an email address, and letter case does not distinguish them: registering ALICE@EXAMPLE.COM after alice@example.com is the same identity being asked for twice. The store refuses the second registration and the service answers it as a field error on the email, so the client sees one clear refusal rather than a server error. Logins and lookups compare the same way, case-insensitively.

**Authority** `database` · mechanism `constraint` · scope `row-local`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `migrations/000002_create_users_table.up.sql` | `db-constraint` | `authoritative` | — |
| `internal/identity/internal/repository/user_repo.go::UserRepository.Insert` | `repository` | `defense-in-depth` | — |
| `internal/identity/internal/repository/user_repo.go::UserRepository.update` | `repository` | `defense-in-depth` | — |
| `internal/identity/internal/repository/user_repo.go::UserRepository.GetByEmail` | `repository` | `defense-in-depth` | — |

**Refusal** `23505` → `users_email_key` → `ErrDuplicateEmail` → `422`

**Verification**

- unit: `internal/identity/internal/repository/user_repo_test.go::TestInsert`
- unit: `internal/identity/internal/repository/user_repo_test.go::TestUpdateTx`
- unit: `internal/identity/internal/repository/user_repo_test.go::TestGetByEmail`
- e2e: `internal/e2e/users_test.go::TestRegisterUserHandlerDuplicateIsolatedFromValidation`

#### R-IDENTITY-012 — Registration assigns the standard user role, and a client cannot choose one

`implemented` · `policy` · authority `repository`

An account always holds exactly one role, and an account created through registration always holds the standard user role. The client never supplies, hints at or upgrades the role — administrator rights are given, never self-granted — and a body that tries to carry one is refused before it reaches the store.

**Authority** `repository`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/identity/internal/repository/user_repo.go::UserRepository.Insert` | `repository` | `authoritative` | — |
| `internal/identity/internal/handler/dto.go::registerUserInput` | `handler` | `defense-in-depth` | — |
| `migrations/000004_add_permissions.up.sql` | `db-constraint` | `defense-in-depth` | — |

**Refusal** `synthetic` → _(unnamed)_ → _(unmapped)_ → `400`

#### R-IDENTITY-016 — An unactivated account is refused at every gated route

`implemented` · `policy` · authority `handler`

An account that has not completed activation may authenticate, but it may do nothing else: every gated route refuses it before any permission is even resolved, with a refusal that says the account must be activated. Activation is therefore the gate between holding a credential and being allowed to act.

**Authority** `handler`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/kernel/httpx/middleware.go::Middleware.RequirePermission` | `handler` | `authoritative` | — |
| `internal/kernel/httpx/errors.go::AuthenticationRequired` | `handler` | `defense-in-depth` | — |

**Refusal** `synthetic` → _(unnamed)_ → _(unmapped)_ → `403`

**Verification**

- e2e: `internal/e2e/auth_test.go::TestRequirePermissionMiddleware`
- e2e: `internal/e2e/rides_test.go::TestRideRoutesPermissionGate`

#### R-IDENTITY-019 — An account is born unactivated

`implemented` · `invariant` · authority `service`

A newly registered account is never active: activation is a separate, token-driven step that the user completes later from the mail. The flag a caller supplies is overwritten on the way in, so no registration — however framed — can create a working account.

**Authority** `service`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/identity/internal/service/activation.go::Service.Register` | `service` | `authoritative` | — |
| `internal/identity/internal/handler/dto.go::registerUserInput` | `handler` | `defense-in-depth` | — |
| `internal/identity/internal/repository/user_repo.go::UserRepository.Insert` | `repository` | `defense-in-depth` | — |

**Refusal** `synthetic` → _(unnamed)_ → _(unmapped)_ → `400`

**Verification**

- e2e: `internal/e2e/users_test.go::TestRegisterUserHandlerSuccessState`
- e2e: `internal/e2e/users_test.go::TestRegisterUserHandler`

#### R-IDENTITY-020 — An account's identity facts are stamped by the store

`implemented` · `invariant` · authority `database`

An account's id, its creation time and its version counter are produced by the store, never by the caller — a client cannot choose who it is or how old its row looks. The store hands them back with the write, so the service never has to invent one.

**Authority** `database` · mechanism `constraint` · scope `row-local`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `migrations/000002_create_users_table.up.sql` | `db-constraint` | `authoritative` | — |
| `internal/identity/internal/repository/user_repo.go::UserRepository.Insert` | `repository` | `defense-in-depth` | — |
| `internal/identity/internal/handler/dto.go::registerUserInput` | `handler` | `defense-in-depth` | — |

**Refusal** `synthetic` → _(unnamed)_ → _(unmapped)_ → `400`

**Verification**

- unit: `internal/identity/internal/repository/user_repo_test.go::TestInsert`
- e2e: `internal/e2e/users_test.go::TestRegisterUserHandler`

**Knowledge ref** `docs/league-tokens-learning-phases/phase-01-postgresql-persistence.md`

#### R-IDENTITY-022 — Activation happens once and asking discloses nothing

`implemented` · `policy` · authority `service`

Activation happens once. An account that is already active cannot be activated again, and a token that was already spent, has expired or was minted for another purpose is no longer an activation credential. Asking for a fresh activation mail is answered the same way whoever asks: an address no account holds and an address whose account is already active receive exactly the answer a genuine request receives — the same status, the same body — and neither mints a token nor sends a mail, so the endpoint never says whether an address is registered or whether its account is active.

**Authority** `service`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/identity/internal/service/activation.go::Service.RequestActivationToken` | `service` | `authoritative` | — |
| `internal/identity/internal/handler/user_tokens.go::Handler.createActivationToken` | `handler` | `defense-in-depth` | — |
| `internal/identity/internal/service/activation.go::Service.Activate` | `service` | `defense-in-depth` | — |

**Refusal** `synthetic` → _(unnamed)_ → `ErrNotFound` → `422`

**Verification**

- unit: `internal/identity/internal/service/activation_test.go::TestActivate`
- unit: `internal/identity/internal/service/activation_test.go::TestRequestActivationToken`

### Entity `user_token`

#### R-IDENTITY-005 — A token is twenty-six random characters

`implemented` · `invariant` · authority `domain`

Every token the service mints is twenty-six characters drawn from the crypto-grade random source — a hundred and twenty-eight bits and a bit of slack, so a token cannot be guessed and two tokens never collide. A presented credential that is not that shape is refused before any lookup runs.

**Authority** `domain`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/identity/internal/model/user_token.go::GenerateUserToken` | `domain` | `authoritative` | — |
| `internal/identity/internal/model/user_token.go::ValidateUserTokenPlaintext` | `validator` | `defense-in-depth` | — |
| `internal/identity/identity.go::Module.UserForToken` | `handler` | `advisory-copy` | — |

**Refusal** `synthetic` → _(unnamed)_ → `ErrInvalidToken` → `401`

**Verification**

- unit: `internal/identity/internal/model/user_token_test.go::TestGenerateUserToken`
- e2e: `internal/e2e/auth_test.go::TestAuthenticateMiddleware`

#### R-IDENTITY-006 — Only a token's hash is ever stored

`implemented` · `invariant` · authority `database`

The service keeps only a fingerprint of each token it issues; the token text itself exists in memory, travels to the user once, and is never written to the database. A presented token is fingerprinted the same way before the lookup, so a stolen copy of the table reveals nothing that works as a credential.

**Authority** `database` · mechanism `constraint` · scope `row-local`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `migrations/000003_create_user_tokens_table.up.sql` | `db-constraint` | `authoritative` | — |
| `internal/identity/internal/repository/user_repo.go::UserRepository.getForToken` | `repository` | `defense-in-depth` | — |
| `internal/identity/internal/repository/user_token_repo.go::UserTokenRepository.insert` | `repository` | `defense-in-depth` | — |

**Refusal** `synthetic` → _(unnamed)_ → `ErrNotFound` → `401`

**Verification**

- unit: `internal/identity/internal/repository/user_token_repo_test.go::TestNewAndGetForToken`
- unit: `internal/identity/internal/model/user_token_test.go::TestGenerateUserToken`

#### R-IDENTITY-007 — Activation tokens live three days, authentication tokens one day

`implemented` · `policy` · authority `service`

A token minted to activate an account is good for three days — long enough for the user to find the mail — and a token minted by a successful login is good for one day. The lifetime is decided once per flow and stamped on the row at mint time, so every token of a kind lives exactly as long as its flow says.

**Authority** `service`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/identity/internal/service/activation.go::Service.Register` | `service` | `authoritative` | — |
| `internal/identity/internal/service/activation.go::Service.RequestActivationToken` | `service` | `authoritative` | — |
| `internal/identity/internal/service/activation.go::Service.Authenticate` | `service` | `authoritative` | — |

#### R-IDENTITY-008 — A token only works for what it was minted for, while it is fresh

`implemented` · `policy` · authority `repository`

A presented token resolves to its account only if it was minted for the purpose the presenter is using it for and its lifetime has not run out. Both facts are part of the same lookup, so a spent, wrong-purpose or expired token is not a distinguishable state — it is simply "no such token", and the caller is refused as unauthenticated.

**Authority** `repository` · scope `row-local`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/identity/internal/repository/user_repo.go::UserRepository.getForToken` | `repository` | `authoritative` | — |
| `internal/identity/identity.go::Module.UserForToken` | `handler` | `defense-in-depth` | — |

**Refusal** `synthetic` → _(unnamed)_ → `ErrNotFound` → `401`

**Verification**

- unit: `internal/identity/internal/repository/user_token_repo_test.go::TestNewAndGetForToken`
- e2e: `internal/e2e/auth_test.go::TestAuthenticateMiddleware`

#### R-IDENTITY-009 — Activating an account spends every activation token it holds

`implemented` · `orchestration` · authority `service`

An activation token is single-use. The moment an account is activated, the activation flag is flipped and every activation token that account holds is deleted in the same unit of work — so a second presentation of the same token finds nothing, and a crash halfway through leaves neither the flip nor the cleanup behind.

**Authority** `service`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/identity/internal/service/activation.go::Service.Activate` | `service` | `authoritative` | — |
| `internal/identity/internal/repository/user_repo.go::UserRepository.UpdateTx` | `repository` | `defense-in-depth` | — |
| `internal/identity/internal/repository/user_token_repo.go::UserTokenRepository.DeleteAllForUserTx` | `repository` | `defense-in-depth` | — |

**Refusal** `synthetic` → _(unnamed)_ → `ErrNotFound` → `422`

**Verification**

- unit: `internal/identity/internal/service/activation_test.go::TestActivate`
- unit: `internal/identity/internal/repository/user_token_repo_test.go::TestDeleteAllForUserTx`

#### R-IDENTITY-018 — A failed login is one refusal, whichever half failed

`implemented` · `policy` · authority `service`

An email that no account holds and a password that does not match the account produce the same refusal: the same status, the same body, and the same sentence. The service never says which half was wrong, so a caller cannot use login to learn whether an address is registered.

**Authority** `service`

**Enforcement**

| Site | Layer | Role | Pinned by |
| --- | --- | --- | --- |
| `internal/identity/internal/service/activation.go::Service.Authenticate` | `service` | `authoritative` | — |

**Refusal** `synthetic` → _(unnamed)_ → `ErrInvalidCredentials` → `401`

**Verification**

- unit: `internal/identity/internal/model/user_test.go::TestPasswordHashRoundTrip`
- unit: `internal/identity/internal/service/authenticate_test.go::TestAuthenticate`

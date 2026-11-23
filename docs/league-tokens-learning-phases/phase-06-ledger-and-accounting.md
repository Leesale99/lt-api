# Phase 6 — Ledger and Accounting Authority

## Goal

Create a single place responsible for currency and token movement.

This is the first phase where a more specialized internal subsystem is justified by a strong domain invariant.

## New concept

### One authority for balances

If several modules can directly change balances, it becomes difficult to answer a critical question:

> Where is the rule that guarantees balances cannot be corrupted?

Instead:

```text
          Game
           |
           | intent
           v
        Ledger
        /    \
       v      v
   journal  balances
```

The ledger owns balance mutation. Other modules request operations.

## Start simple

You do not need a full accounting product.

Use:

```text
accounts
journal_entries
balances
```

An account could represent:

- player currency
- player tokens for a team
- team reserve
- common pool

## Double-entry idea

A movement has two sides.

Example: buying tokens.

```text
Player currency      -10
Common pool           +10
Player tokens         +10
Team reserve          -10
```

You can model the journal as pairs of entries that describe the movement.

## Example API

```go
type Ledger struct {
    db *sql.DB
}

func (l *Ledger) TransferCurrency(
    ctx context.Context,
    tx *sql.Tx,
    playerID int64,
    amount Decimal,
) error {
    // validate balance
    // append journal entries
    // update balance projection
    return nil
}
```

The exact shape should evolve with your database model.

## Important rule

The game should never do this:

```sql
UPDATE ledger.balances
SET balance = balance - $1
```

It should call something like:

```go
err := app.ledger.DeductCurrency(...)
```

This gives the ledger one place to enforce:

- no negative player balances
- journal consistency
- idempotency
- balance projection updates

## Transaction boundary

For now keep game + ledger in the same PostgreSQL transaction.

```mermaid
sequenceDiagram
    participant Game
    participant Ledger
    participant DB as PostgreSQL

    Game->>DB: BEGIN
    Game->>Game: validate ride
    Game->>Ledger: burn intent
    Ledger->>DB: journal entry
    Ledger->>DB: balance update
    Game->>DB: ride -> Burned
    Game->>DB: COMMIT
```

No asynchronous messaging is needed yet.

## Tasks

1. Introduce account identities.
2. Create journal entries.
3. Create balance projections.
4. Move all balance writes into the ledger.
5. Add database constraints for non-negative balances.
6. Make balance operations transactional.
7. Add concurrency tests.
8. Prove that game code cannot bypass the ledger in normal usage.

## Research topics

- double-entry bookkeeping
- accounting ledgers
- balance projection
- invariants at database level
- transaction consistency
- idempotent financial operations

## Do not build yet

- separate ledger service
- Kafka
- eventual consistency between game and ledger
- complex event sourcing
- general accounting abstractions unrelated to the game

## Orientation graph

```mermaid
flowchart LR
    HTTP --> Game
    Game -->|intent| Ledger
    Ledger --> Accounts[(accounts)]
    Ledger --> Journal[(journal_entries)]
    Ledger --> Balances[(balances)]
    Game -.->|must not write| Balances
```

## Done when

You can trace a buy, lock, loss, and burn operation from the API to the ledger journal and back to the player's visible balance.

## What this prepares you for

Once the core operation is correct, you can safely introduce work that happens outside the request lifecycle, such as automatic burns.

## How this phase is tracked in the repo — roadmap Phase F, spec-first

This phase is a separate roadmap effort from the executable specification, and its
first real pilot: the ledger is built *through* that pipeline rather than beside it.
The catalog already carries the phase's rules. The catalog lives at
`docs/spec/rules/<module>/<ID>.md` — one authored Markdown file per rule — and
R-LEDGER-001..007 are authored there in that per-file format, at
`docs/spec/rules/ledger/`. Each is marked `specified-only` with an `expert-ref`
back to this document, so every rule above is stated once in machine-checkable
form while this doc stays its design rationale. R-LEDGER-001/002 were the phase's
first two catalog entries; the remaining ledger rules join them the same way as
the design firms up.

The order of work is fixed: **spec first, code second.**

1. Write scenarios for the ledger rules from this doc — the buy, lock, loss and burn
   movements, and the negative-balance and balanced-pair invariants — starting with
   R-LEDGER-001/002.
2. Run them against the current code and watch them fail. A scenario that was never
   red has not proved anything.
3. Build the tables and the ledger operation until they go green, one rule at a time,
   then let `make audit` hold the rule there. The workflow and the rule-change
   protocol are in `docs/spec/README.md`.

"Done" for this phase is therefore two things together: the trace described under
§"Done when" end to end, and every R-LEDGER rule green in the executable
specification. The accounting invariant is only real when the design, the catalog, the
scenarios and the database agree — one of them alone is a promise.

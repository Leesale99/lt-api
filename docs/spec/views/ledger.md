<!-- GENERATED — do not edit. -->
# Business rules — `ledger`

> **GENERATED — do not edit.** This is the `ledger` module's slice of the readable
> catalog view. The canonical, hand-edited catalog is `docs/spec/rules.yaml`.
>
> Change the YAML, never this file, then regenerate from the dashboard:

```sh
go run ./cmd/spec gen --rules docs/spec/rules.yaml --out docs/spec/business-rules.md
```

The rules themselves live in `../features/ledger/`; the format authority is
`rules.schema.md` and the checker is `go run ./cmd/spec check`.

**7 rules** — 7 specified-only.

## Module `ledger`

### Entity `account`

#### R-LEDGER-004 — Every balance sits in an account of a known kind

`specified-only` · `invariant` · authority `database`

Money is not held as a free-standing number: every balance belongs to an account, and every account is one of the ledger's kinds — a player's currency, a player's tokens for a team, a team reserve, or the common pool.

**Authority** `database` · mechanism `constraint` · scope `row-local`

**Expert ref** `docs/league-tokens-learning-phases/phase-06-ledger-and-accounting.md`

### Entity `balance`

#### R-LEDGER-001 — No balance goes negative

`specified-only` · `invariant` · authority `database`

A player's currency or token balance can never fall below zero. A debit that would overdraw is refused before any journal entry or balance projection is written, so a negative balance is not a repairable state — it is unrepresentable.

**Authority** `database` · mechanism `constraint` · scope `row-local`

**Expert ref** `docs/league-tokens-learning-phases/phase-06-ledger-and-accounting.md`

#### R-LEDGER-003 — Only the ledger moves money

`specified-only` · `boundary` · authority `composition-root`

A balance changes only through a ledger operation. Other modules state what should happen to the money — deduct, lock, burn — and never compute or write a balance themselves, so there is exactly one place where the corruption rule can be enforced.

**Authority** `composition-root`

**Expert ref** `docs/league-tokens-learning-phases/phase-06-ledger-and-accounting.md`

#### R-LEDGER-005 — A balance is the journal's projection

`specified-only` · `invariant` · authority `database`

A balance is only ever an explanation of what the journal says. Every balance change is accompanied, in the same transaction, by the journal entries for it, so a player's visible balance can always be traced back to the postings that produced it.

**Authority** `database` · mechanism `trigger` · scope `cross-table`

**Expert ref** `docs/league-tokens-learning-phases/phase-06-ledger-and-accounting.md`

### Entity `journal_entry`

#### R-LEDGER-002 — Every money movement posts a balanced journal pair

`specified-only` · `invariant` · authority `database`

Every movement of money is recorded as a pair of journal entries whose debits equal their credits, in the same transaction as the balance projection update. A single-sided entry never commits, so the journal always explains every balance.

**Authority** `database` · mechanism `trigger` · scope `cross-row`

**Expert ref** `docs/league-tokens-learning-phases/phase-06-ledger-and-accounting.md`

#### R-LEDGER-006 — A money operation repeats without moving money twice

`specified-only` · `orchestration` · authority `service`

A ledger operation that is retried — after a crash, a restart or a duplicate request — applies its movement once: the retry is recognised as the same operation and changes no balance and no journal entry.

**Authority** `service`

**Expert ref** `docs/league-tokens-learning-phases/phase-06-ledger-and-accounting.md`

#### R-LEDGER-007 — Buying tokens posts both sides of the purchase

`specified-only` · `orchestration` · authority `service`

Buying tokens is one movement with two sides: the player pays currency into the common pool and receives tokens from the team reserve, and both sides are posted together, so no leg of a purchase can survive on its own.

**Authority** `service`

**Expert ref** `docs/league-tokens-learning-phases/phase-06-ledger-and-accounting.md`

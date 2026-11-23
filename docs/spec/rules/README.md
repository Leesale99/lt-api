# docs/spec/rules — the business-rule catalog

This directory is the canonical, hand-edited catalog of the project's business rules: one
authored Markdown file per rule at `<module>/<ID>.md`, where the directory names the
module and the file stem names the id.

- **The format** — the rule file's fields, the frozen enums and the loader's strictness:
  [`../rules.schema.md`](../rules.schema.md) (§1 the frozen enums, §2 the rule file, §5
  how to add a rule).
- **The policies** — the catalog's 18 machine-checked invariants, one section each:
  [`../policies.md`](../policies.md).
- **The workflow** — rule-change protocol, `spec work`, check, gen and prove:
  [`../README.md`](../README.md).

The retired single-file catalog grouped rules under banner comments; the module
directories carry that grouping now:

- `schedule` — calendar authority (teams, seasons, rounds, matches)
- `game` — rides, players, settlement, the ride state machine, the event seam
- `identity` — accounts, tokens, permissions and the HTTP gate
- `ledger` — money movement authority (specified, not built)
- `kernel` — transactional outbox, idempotency, persistence, the HTTP chain, httpx,
  the in-process eventbus, validator and list framing
- `platform` — configuration, the composition root, mailer, logging, server and
  worker lifecycle

Retired ids (kept as a note because ids are never reused):

> Retired (dupe policy): R-GAME-019 merged into R-KERNEL-003, R-GAME-022
> merged into R-KERNEL-014 — kernel owns the statement, game call-sites are
> enforcement sites on it. Ids are never reused.

## Deliberately NOT catalogued yet

- the accepted crash gap between a committed round close and the settlement
  transaction that consumes RoundCompleted (close durable, settlement may be
  dropped) — Phase 07/08 work, not a rule the code holds today.
- rankings / leaderboard rules — no code exists and the module enum has no
  value for the context; phase 05 defers them to Phase 09+.
- the phase-03 "inject a failure halfway through any major operation" test
  obligation — a verification gap across several rules, not a business
  statement.
- the doc-vs-doc conflict over the buy-tokens legs (phase-03's three legs vs
  phase-06's four incl. the common pool) — needs a product decision before a
  rule can be stated; recorded in R-LEDGER-007's notes.

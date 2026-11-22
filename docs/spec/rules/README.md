# docs/spec/rules — the business-rule catalog, one file per rule

This directory is the CANONICAL, HAND-EDITED business-rule catalog. Each rule
lives in its own Markdown file at `<module>/<ID>.md`: a strict YAML front matter
between two `---` fences (the machine fields, exactly the fields `cmd/spec`
decodes — no `scope:` and no `notes:` key any more), followed by an optional
authored body whose `## Notes` section is the rule's `Notes`. `<module>` must be a
value of the frozen module enum and `<ID>` must equal the front matter's `id`.
The layout, the strict decoding and the ordering (module enum order, then id) are
documented in `cmd/spec/catalog.go`; the field schema, enums and policies live in
`../rules.schema.md`, and the workflow in `../README.md` §2.

`../business-rules.md` and `../views/*.md` are GENERATED from these files — never
edit them. Regenerate with `go run ./cmd/spec gen` (or `make spec/gen`).

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

> Retired at gate B5 (dupe policy): R-GAME-019 merged into R-KERNEL-003, R-GAME-022
> merged into R-KERNEL-014 — kernel owns the statement, game call-sites are
> enforcement sites on it. Ids are never reused.

## Deliberately NOT catalogued yet (still deferred after the Phase B merge)

- the accepted crash gap between a committed round close and the settlement
  transaction that consumes RoundCompleted (close durable, settlement may be
  dropped) — Phase 07/08 work, not a rule the code holds today.
- rankings / leaderboard rules — no code exists and the module enum has no
  value for the context; phase 05 defers them to Phase 09+.
- the phase-03 "inject a failure halfway through any major operation" test
  obligation — a verification gap across several rules, not a business
  statement (see the misalignment report).
- the doc-vs-doc conflict over the buy-tokens legs (phase-03's three legs vs
  phase-06's four incl. the common pool) — needs a product decision before a
  rule can be stated; recorded in R-LEDGER-007's notes.

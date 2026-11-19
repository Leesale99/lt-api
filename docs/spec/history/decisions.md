> **Superseded — historical record.** Nothing reads docs/spec/history/; kept for provenance.
> Extracted from rules.schema.md §6 (whose section number is preserved there as a pointer).

# Gate-decision minutes — archive of rules.schema.md §6

> **Resolved at gate A2** (D1–D6). D1 — keep the taxonomy enum, rename `store` →
> `repository`. D2 — `drifted` is content-only; rotted pointers keep `implemented` with
> a `stale-pointer:` note. D3 — add the `validator` layer and the `composition-root`
> authority (items 2 and 5; the SQL guard functions of item 6 stay `repository`).
> D4 — the granularity principle of item 7 is the catalog's working rule. D5 —
> `refusal` stays a field on every rule; the `refusal-mapping` category stays reserved
> (item 4). D6 — the `R-GAME-004` verification gap is consciously deferred to Phase B
> (item 8). Item 9 is moot: `docs/spec/plan.md` now exists.

1. **`authority` enum conflict.** This schema uses the taxonomy's
   `database|domain|service|repository|handler` (plus `composition-root` for boundary
   rules); the phase plan sketch had `db|app|both`.
   `both` cannot coexist with "exactly one authority" — the plan's own note says one
   authority plus N enforcement entries. Mapping is documented in §1. **D1: confirmed**
   — the five-value enum (plus `composition-root`) is what `cmd/spec` should validate
   in Phase B.
2. **`boundary` rules have no authority value.** The taxonomy says such a rule's
   authority is the *composition root*.
   **D3: `composition-root` added to the authority enum** (see §1); boundary rules use it
   and cite ADR-028 in `notes`.
3. **Does stale prose in an applied migration count as `drifted`?**
   **D2: no.** `drifted` means the rule's *content* disagrees between expert docs and
   actual behavior. Rotted pointers (change-detectors naming dead files, comments
   pointing at deleted tests, enforcement moved away from where docs claim) keep
   `status: implemented` and are recorded in `notes` with a `stale-pointer:` prefix;
   `R-GAME-005` and `R-GAME-010` were reclassified accordingly. Both `drifted` rules and
   `stale-pointer` notes surface in the Phase B misalignment report — attention comes
   from the report, not from a falsely unhealthy status.
4. **`refusal-mapping` as category vs field.** `refusal` is a field on every rule; the
   taxonomy also makes `refusal-mapping` a *category* (rules whose subject is the
   mapping and its classification order, e.g. the repository's
   round-status → version/state → predicate ordering, where a no-cause read is a loud
   error). **D5: keep the field on every rule; the category stays reserved** for a rule
   *about* the mapping. No such rule is seeded yet.
5. **`layer` enum vs the plan's `validator|middleware`.** **D3: `validator` added** to
   the layer enum (§1); `middleware` maps to `handler`. Existing entries keep their
   current layer until `cmd/spec` is able to check a migrated `R-GAME-004` second site.
6. **DB guard functions have no layer value.** `round_open_guard` /
   `destination_open_guard` are `SECURITY DEFINER` SQL functions that take the row lock
   inside the caller's transaction. They stay `layer: repository` (the taxonomy's
   "ordering/locking/atomicity mechanics → repository") even though the code is SQL;
   a new `db-function` value is not added (D1/D3).
7. **How coarse should one rule be?** **D4: the principle below is adopted** — one rule
   per authority, split when the authority differs. I split "transitions **and** phase gates" (one
   candidate in the plan) into `R-GAME-002` (the graph, DB authority) and `R-GAME-007`
   (the windows, domain authority) because the authorities differ — the catalog's whole
   point. Similarly `R-GAME-007` and `R-GAME-008` list one authoritative site per ride
   command (disjoint cases) rather than four rules. **D4: adopted** as the catalog's
   granularity principle.
8. **Verification gap on `R-GAME-004`.** No test probes the four `rides` CHECKs
   directly (only `TestValidateRide` and the request edge), and an unmapped `23514`
   surfaces as 500. **D6: deferred** — the gap is recorded knowingly (the catalog notes
   it) and a direct-DB constraints test / fixture lands in Phase B.
9. **`docs/spec/plan.md` did not exist in the repo** when this catalog was written
   (`docs/spec/` did not exist at all, and no copy of the plan was found by `find`). The
   schema and seed were built from the task brief plus the code. Resolved: `plan.md`
   now exists; its Artifacts list matches this file.

### Gate B5 resolutions

Gate B5 reviewed the catalog and the misalignment report and closed the three schema gaps
that report filed; the enums are otherwise unchanged since A2.

- **`composition-root` joins the `enforcement[].layer` enum** (§1). Meaning: cross-context
  wiring / facade binding — the composition-root layer where modules are handed their
  collaborators; distinguished from `handler`, which is transport framing. Sites that were
  apologising in their notes as `handler`, `repository`, `service` or `db-constraint` may
  now use it. `cmd/spec` validates the 8-value layer enum (`composition-root` added).
- **The refusal-code list is illustrative, not closed** (§2 `refusal.code`). The house
  list `{P0001, 23514, 23503, 23001}` plus the reserved word `synthetic` is what the
  mapping table shows most often; any 5-character SQLSTATE is valid, so `23505`, `23502`,
  `22003` and `42501` stay honestly catalogued. `cmd/spec` warns — never errors — on a
  code outside the illustrative set; that behaviour matches the decision and is kept.
- **Seeded reference data keeps `mechanism: constraint`** (§1, mechanism table). No
  `seed` / `reference-data` value is added: what refuses a conflicting seed is the keys,
  and migration immutability keeps the seeded values true, so the declarative half is the
  mechanism. `R-IDENTITY-014`'s roles/permissions/grants stay `constraint`.
- **Retirement replaces marking** (§2 `id`). A retired rule's entry is removed from the
  catalog and recorded in `rules.yaml`'s header note; ids are never reused. First use:
  the gate B5 dupe merges (`R-GAME-019 → R-KERNEL-003`, `R-GAME-022 → R-KERNEL-014` — one
  statement per rule, the kernel rule owns it, the game call-sites are enforcement sites
  on it).

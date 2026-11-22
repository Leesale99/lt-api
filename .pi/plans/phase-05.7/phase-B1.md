# Phase B1 — rules per file + cmd/spec kernel

HANDOFF=confirm. Whole-catalog mechanical migration: SPEC-SCOPE none (per-rule proves are vacuous over a 126-rule format swap; the gate is the full audit + the round-trip test in B1.3).

## B1.1 — kernel (same package, named files; no subpackage — decision 5)

- `catalog.go`: keeps the types + the ONE strict loader.
- `scope.go` (new): `type Scope struct{ Module string; Rules []string }` with `ParseCLI / ParseTaskLine / ParseEnv / RuleIDs(Catalog) / String`. Replaces the five re-encodings: check's `--module`/`--rule` filter, brief's module filter, queue's, gen's, prove's map-building. The TS side keeps its parser (B3 `protocol.ts`), but the packet grammar is documented once, next to this type.
- `finding.go` (new): `Finding{Policy, Subject, Severity, Message}` — `Subject` (rule id | feature pointer | annotation site) replaces `RuleID`. The behavior change lands in B2; B1 only introduces the type and converts collectors to emit it.
- `features.go` (new): `featureIndex` + `indexFeatures` — the ONE feature-tree parse (vocabulary and oracle migrate onto it in B2.4).
- `render.go` (new): `mdCell`, `normalizeProse`, `displayPath`, `finishMarkdown`, `escapeQuoteLine`, `sortedRules`, `catalogModules`, and `moduleEnum` out of `check.go` — so `gen`/`work` stop reaching into the policy engine for shared symbols.
- Kill duplicate parsers: `vocabulary.loadRealObjectNames` → uses `loadCatalog`; `prove.changedRuleIDs`' base parse → per-file git diff (B1.3); `loadProveRegistry` → strict decode.

## B1.2 — format: `docs/spec/rules/<module>/<ID>.md`

- Front matter = today's machine fields, with the agreed renames (decision 11): `scope:` → `write_scope:`; `stale-pointer:` / `Tie:` notes markers → `stale_pointer:` / `tie:` fields (design the exact YAML with policies 5/7 in view and update those policies); human `notes:` prose moves to the markdown body. **The statement stays in front matter** — ADR-030 preserved.
- Body: authored markdown (`## Notes`, `## Refusal` prose where present). Never generated.
- `gen` shrinks to the thin dashboard (totals by module/status + links to `rules/<module>/`); `views/*.md` and their renderers (`renderView`, `writeModule`, `writeRule`) die; `spec/diff` = dashboard staleness; the orphan check dies (the loader walks the dir — a file with an unresolvable id is a load error, orphans cannot exist).
- Docs rewritten for the format: `rules.schema.md` (front-matter authority), `docs/spec/README.md` §1-§3, `gherkin-style.md` grep recipes (§3/§10 — `- id:` grep → filename convention), `AGENTS.md`, Makefile help lines.

## B1.3 — migration (mechanical, verifiable)

- New one-shot `spec migrate` subcommand: parse old `rules.yaml` → emit per-file md. **Round-trip test first**: old `Rule` struct → md → new `Rule` struct, `reflect.DeepEqual` per rule, all 126 — then run the migration, commit, delete the subcommand (git history keeps it).
- `prove` adapts: `deriveProveScope` — `docs/spec/rules/` prefix match replaces the `file == relRules` string equality; `changedRuleIDs` → per-file git diff (`git show base:<path>` per rule file; add/remove/rewrite).
- Extension (rides B1 per decision 12): `hashSpecRules` → `catalogHash` = sha256 manifest over sorted (relpath + content) of `docs/spec/rules/**`; `ReceiptRecord` gains `schema_version: 2`; `diffReceiptAgainst` treats v1 receipts (no schema_version) as superseded — no hash comparison, one-time note. One noisy reconciliation day is accepted.
- Smoke fixtures: `smoke-gates.mjs` rules.yaml stubs (~3 sites) → `docs/spec/rules/` dir stubs.
- Acceptance: `make audit` green before AND after; ERROR count 0 both sides (record WARN deltas — message shapes change with paths, expected); 126 rules in, 126 files out; scoped prove still derives (`make prove args='--rule <one id>'` on a green rule); `make smoke` green.

## Files touched (re-verify at job start)

`catalog.go`, `check.go`, `gen.go`, `brief.go`, `queue.go`, `prove.go`, `vocabulary.go`, `main.go` (migrate subcommand, then removed), `gates.ts`, `smoke-gates.mjs`, `docs/spec/**` (rules.yaml deleted → `rules/` created; `views/*.md` deleted; schema/README/styleguide rewritten), `Makefile` (help lines; `spec/gen` semantics), `AGENTS.md`.

## Risks

- This is the phase that touches everything at once — do it as ONE migration commit preceded by the kernel extraction as separate commits (kernel first, format second), so review can bisect.
- Reconciliation noise (decision 12) — expected, documented, do not "fix" by weakening the hash.

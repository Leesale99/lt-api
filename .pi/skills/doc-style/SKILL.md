---
name: doc-style
description: Documentation patterns and voice rules for lt-api repo Markdown. Load before creating, editing, or restructuring any documentation, instructions, hub, reference, learning guide, README, agent card, prompt file, or rule-file Notes body in this repo. Not for reading tasks, not for the Obsidian vault (that is the lt-api-kb skill), and never for generated files.
---

# lt-api documentation style

One reader, one promise: a brand-new developer lands on any page cold and must understand it without asking anyone. Every rule below serves that promise.

Pattern lineage, for the curious: Diátaxis (types), Google and Microsoft style guides (voice), Mark Baker's *Every Page Is Page One* (self-containment), John Carroll's minimalism (task-first). The repo-specific contracts below are local law and win on any conflict.

## Voice rules — every file, no exceptions

1. **Active voice.** Imperative verbs for steps: "Run `make db/up`", never "the database should be started".
2. **Sentence-case section headings.** Descriptive, never formatting-only. Document H1 titles keep their existing form; every `##`/`###` below them is sentence case. One case convention across sibling files.
3. **Bullets of 2–7 parallel items.** Numbered lists only for true sequence or priority; one action per numbered step — a step carrying six commands is six steps. Exception: rule-file Notes paragraphs are single-line by renderer contract (see rule 15).
4. **Paragraphs of 3–7 lines**, leading with the point. A paragraph that enumerates is a list pretending not to be one — make it one.
5. **Self-contained sections.** Restate the needed context where it is needed; "see above", "see below" and "the header note above" are banned — and a pointer must name its target's current file. Landing test: any section must survive being read first.
6. **Expand every id inline at first use.** Never a bare "policy 12" — always "policy 12 (enforcement sites must exist in code today)". A §-citation may accompany the fact, never carry it: "§5 (how to add a rule)" passes; "it is a merge-back gate (§9)" fails.
7. **Name every artifact by a repo-relative path, and one display name only.** Paths start at the repo root (`WORKFLOW.md`, not `/WORKFLOW.md`). No "the report", no "that doc". No line numbers or patch offsets anywhere — including Notes and code comments (`~line 195` rots like any pointer); name the file and symbol. One artifact, one name: it is the **prove registry** (not "mutant registry"), the **coverage dashboard**, `make spec/diff` (not `spec/gen --diff`). A reference to a deleted file is a defect.
8. **No history in living documents.** Gate labels ("gate A2/B5"), phase provenance ("since Phase G", "Phase 07/08 work", "roadmap Phase F"), commit hashes, PR numbers, "resolved at…" narratives, temporal framing ("used to", "was renamed", "now stated in domain terms"), and id archaeology ("was provisional R-GAME-202", "Renumbered at merge", "retired R-…") — git owns history; the document states the current invariant. Exceptions, and only these: the retired-ids record in `docs/spec/rules/README.md` (the schema names it canonical) and the schema's documented retired-key warnings. Roadmap claims ("remediation is planned for the next phase") belong to the vault phase note, not the rule.
9. **No assumed knowledge.** Define at first use; the file itself explains every term it relies on. An external book or an unwritten document never gates the normative path — restate what the reader needs.
10. **Facts, not adjectives.** "simple", "easy", "just", "obvious" add nothing — cut them.
11. **One skeleton per document type.** The reader recognizes the shape before reading the words. Harmonize one-off sections a sibling lacks instead of keeping them. A new document joins a family; if no family fits, ask before inventing a new skeleton.
12. **Cross-file contradiction is a defect.** If two documents state different facts (a count, a gate's availability, a name), one is wrong — fix both to the truth; never leave both standing.
13. **Templates are fenced.** A report or output template inside an instruction file sits in a fenced code block, never as headings — otherwise the template becomes document structure.
14. **Hard-wrap narrative prose at column 110.** Every prose paragraph in `docs/**` and the root `README.md` wraps at 110 columns — git-diff-friendly on a 14-inch editor, and the de-facto ruler the widest workflow docs already follow. Never break inside a link URL, an inline code span or a table row. Exempt: rule files (`docs/spec/rules/**/R-*.md`, whose Notes paragraphs are single-line by renderer contract), instruction cards whose one-line-per-item form is deliberate (`.pi/agents/*.md`, `.pi/prompts/*.md`, `.pi/skills/*/SKILL.md`, `AGENTS.md`), tables, fenced code, YAML front matter and headings.
15. **A file a machine reads is rewritten only after reading its consumer.** `agents.ts`/`persona.ts` (agent cards), `jobs.ts` (wire markers), `policy.ts` (`WORKFLOW.md` front matter), `catalog.go` (rule files), `prove.go` (registry). Prose is negotiable; wire format is not.

## Document families and their skeletons

Pick by the reader's need, then keep the shape fixed:

| Family (repo files) | Pattern | Skeleton |
|---|---|---|
| Hubs / reading paths: `README.md`, `docs/README.md`, `docs/league-tokens-learning-phases/README.md` | Orientation Brief | one-paragraph thesis → routing table or sequence → How it fits together (table) → **What it does not do** (scope limits belong on every front page) → Where to go next. Existing section names may differ; every function must be present |
| Domain & format reference: `domain-knowledge.md`, `domain-knowledge-game-engine.md`, `docs/spec/rules.schema.md`, `docs/spec/gherkin-style.md`, `docs/spec/policies.md` | Reference | fixed numbered `## N.` spine (section numbers are a stable API — external files cite them; never renumber) → every section opens with 1–2 self-contained sentences stating what it covers → tables for enumerations → a terminal review checklist where the type is a standard |
| Workflow map: `docs/spec/README.md` | Workflow map | one-paragraph Goal at the top → fixed numbered topical sections (§N numbers are a stable API) → targets/quickstart table. Ten topical sections are correct; a Goal sentence and self-contained sections are not optional |
| Narrative deep dive: `docs/delegate-workflow.md` | Explanation | fixed spine (two planes → write boundary → gates → RED/GREEN/PROVE → task packets → receipts → handoff → failure → code locations); paragraphs 3–7 lines; history only in a marked "Design history" section, if anywhere |
| Learning-phase guides: `phase-00…08` | First-Contact Tour | fixed spine: Goal / New concept / [free middle] / Tasks / Research topics / Do not build yet / Orientation graph / Done when / What this prepares you for. The Orientation graph sits immediately after "Do not build yet" and orients to where this phase's code sits in the repo — not a domain diagram |
| Directory maps: `docs/spec/rules/README.md`, `docs/spec/prove/README.md` | Directory map | what this directory is → the per-entry bullets (module purposes, entry shape) → pointers to the format authority, the policies, and the workflow. No procedures, no history |
| Workflow / procedure docs (task instructions) | Numbered Path | Goal (one observable sentence) → Before you start → Steps → Check it worked → Troubleshooting (symptom → cause → fix) → Next |
| Risky or irreversible ops: sandbox rebuild, prove and merge-back | Checkpoint Runbook | Goal → Prerequisites → per step: Run: / Expect: / If not: → Rollback → Done when |
| Real forks, 2–3 max, single axis | Branching Recipe | Goal → Pick your path (one-line table) → Path A (runnable alone) → Path B (runnable alone) → shared Check it worked. `rules.schema.md` §5's specify-first vs catalog-existing paths are the canonical example |
| Agent cards: `.pi/agents/*.md` except `main.md` | Role card | YAML front matter (name/description/tools/model/thinking/background) → one-line persona (stated once) → `##` sections covering only the card's single job → the report template fenced |
| Main persona: `.pi/agents/main.md` | Role card, host-appended form | no front matter, must not start with `---` (a smoke suite asserts both); `#` H1 sections are its convention — it is a persona appended to a system prompt, not a discovered card |
| Prompt files: `.pi/prompts/*.md` | Prompt | YAML front matter (`description`, optional `argument-hint`) → one to three imperative instruction lines. `implement.md`'s chained form is the current maximum |
| Instruction files: `AGENTS.md` | Instruction card | a few lines, one topic per bullet, every pointer repo-relative and inline-expanded |
| Machine-policy prose: `WORKFLOW.md` body | Policy prose | state each invariant once, tersely; never paraphrase or restate a front-matter value; no judgment prose (that lives in the agent cards) |
| New concept notes | Concept Card | What this is → When you will meet it → How it behaves (3–5 bullets) → One worked example → Limits and failure modes → Related |

## Repo-specific anchors — citations must agree with these

- **`docs/spec/policies.md`** is the statement of record for the 18 machine-checked catalog policies; `go run ./cmd/spec check --strict` is the enforcer. Cite inline-expanded with its anchor.
- **`docs/spec/coverage.md`** is GENERATED from the rule catalog — never edit it; `make spec/gen` rewrites it. The same rule covers every file carrying a GENERATED banner.
- **`docs/spec/rules.schema.md`** is the rule-file format authority. Rule files carry strict machine front matter plus a required `## Notes` body; further `##` body sections are allowed as documentation (schema §2 owns the exact contract).
- **The prove registry** lives at `docs/spec/prove/<module>.yaml`, one file per module that has provable rules today — modules enter as their rules become executable (three exist now: game, identity, schedule). Editing one module's registry scopes that module's entries.
- **The domain sources win.** `docs/league-tokens-learning-phases/domain-knowledge.md` and `domain-knowledge-game-engine.md` are the domain experts' source of truth; phase guides, the rule catalog and the features derive from them; on conflict the sources win and the rule is drift to fix (invariant recorded in AGENTS.md and the schema's status enum).
- **Wire-format literals are load-bearing.** `orchestrator.md` teaches the child to emit `HANDOFF_COMPLETE` / `HANDOFF_BLOCKED` / `AWAITING_APPROVAL` / `HEAD SHA:`; `WORKFLOW.md` front matter is machine-checked. Preserve them verbatim through any rewrite.
- **The Obsidian vault is out-of-repo.** Repo documents never cite a vault note as the sole carrier of meaning — restate the fact (an ADR pointer may accompany, never replace). Vault writing follows the lt-api-kb skill, not this one.

## Verification after edits — the minimum that must actually run

- Any `docs/spec/**` edit: `go run ./cmd/spec check --strict` and `make audit/static`; if the catalog changed, `make spec/gen` too (the dashboard's freshness is part of the gate).
- Rule-file Notes bodies: `go test ./cmd/spec/...` — the canonical renderer byte-compares every file; match its formatting (Notes paragraphs are single-line) or the round-trip fails.
- A wrap pass over `docs/**` (rule 14) is a whitespace-only reflow: `git diff -- 'docs/spec/rules/**/R-*.md' docs/spec/coverage.md` must stay empty and `go run ./cmd/spec check --strict` must stay green afterwards.
- Gate files (`Makefile`, `cmd/spec/**`, `.github/**`, `WORKFLOW.md`): host-side edits with a prompt commit — gates refuse while they are dirty.
- `.pi/extensions/**`: the three smoke suites (`make smoke`) plus `npm run typecheck` on the host.
- A claimed check is not a run check. Report what ran, with exit codes.

---
name: lt-api-kb
description: Write mechanics and rules routing for the lt-api Obsidian knowledge base (vault ~/Projects/vaults/lt-api). Load when creating, editing, or restructuring notes in that vault, or when sweeping Deferred items. Not needed for reading tasks.
---

# lt-api knowledge base

## Tool layering (hard rule)

Obsidian tool for EVERYTHING on vault paths — reads, greps, listings, counts, not just writes. Bash commands naming a vault path are rejected by a guard ("Command targets Obsidian vault …"); do not retry with cosmetic tweaks or route around via `cd` — that is the guard working, not a bug.

## Primitives, ranked by reliability (verified 2026-10-01)

1. **`eval code='…'` + `app.vault.modify(f, text)`** — the most reliable edit primitive. Actually applies, survives re-read, handles multi-line edits. Rendered snippets fail on literal apostrophes and dense syntax: avoid `'` in strings (or escape `''`), prefer `for` loops over `forEach`, use plain `String.replace(literal, literal)`.
2. **Full `write` / `create`** — for new notes or full rewrites. Works, but see failure modes.
3. **`prepend` / `append`** — top/bottom of file only; `append` cannot target a section (verified 2026-09-18: a line meant for `## http` landed under `## transactions` — for sectioned notes use full write).
4. **`search`/`replace`** — last resort. No dry-run; replaces in every matching file vault-wide; see Blast radius.

## Failure modes (each verified; re-read after every write, check the TAIL)

| Behavior | Detail | Verified |
|---|---|---|
| Write reports success, did not land | `write path=…` lost content twice in a row (Home.md, 2026-10-01); a long em-dash filename created an empty file (2026-09-22). Retry with a shorter/ASCII name; verify by `read` | 09-22, 10-01 |
| CLI `write file=` strips frontmatter | Overwriting an existing note starts it at the `# H1`; `updated:` gone. Repair: `prepend` re-attaches, but glues `---# Heading` (no closing separator). Fix with eval+modify | 10-01 |
| `eval` result echo dropped | Same construct sometimes returns its value, sometimes `(eval ran; result echo was dropped by Obsidian 1.13.x)`. Never depend on the return value; verify the side effect with a `read`. To *know* a computed fact: write it to a probe note, read, delete | 10-01 |
| eval replace acts global | A `c.replace(old, new)` on a unique anchor also flipped an unrelated checkbox in the same file. After ANY edit (eval, search, write) re-read the full region + neighbors | 09-30 |
| `search` results stale | search (no replace) listed a phrase that existed in zero files (cross-checked on disk and by eval over all files). Treat as a hint about past state; verify load-bearing claims by re-read | 10-01 |
| Bare-name `read` / `files` serves stale content | `read Home` (no `file=`) returned a Sept-23 version of Home.md while `read file="Home"` returned the current one; `files` listed 3 entries while the vault holds the full tree. Always read with the explicit `file=` form (with `.md`); treat bare-name output as a hint and re-verify before acting on it | 10-05 |
| `search`/`replace` operator errors | Plain-text queries whose text before the first `: ` is not an operator error with `Operator "…" not recognized` — frontmatter lines (`status: open`) and task bullets (`- [ ] …`) both trip it. Use eval+modify for those targets | 09-23 |
| Multi-line `replace=` not delivered | `\n`-escaped queries DO match (use that for uniqueness), but multi-line `\n` replacement through `search` reported `0 file(s)` and did nothing. Multi-line repairs: eval+modify or full write | 10-01 |
| Parallel writes → empty files | Writes must be sequential | 09-1x |

## Syntax rules (pi-obsidian tool)

- `write`/`create`/`move` need the explicit `.md` (`path=`/`file=` do not add it).
- `content=` (and `search query=`/`replace=`) must be ONE double-quoted value; raw newlines silently truncate — emit `\n` escapes. Only `\"` `\n` `\t` `\r` are escapes; other backslashes pass through.
- `delete` moves to trash (recoverable).
- Reads: always the explicit `file=` form (with `.md`) — bare-name reads (`read Home`) and the bare `files` listing have served stale content (verified 2026-10-05, see Failure modes).

## Blast radius (`search`/`replace`)

- Before replacing, run the query WITHOUT `replace` — that list is the blast radius; verify each listed file afterwards by full `read`.
- Only replace on long, content-anchored strings that are unique vault-wide. Never short generic keys (`status: open`, `type: phase`), even with `file=` — one scoped call flipped `status:` in five other files (2026-09-17).
- Multi-file edits: full `write` or eval+modify per file, instead of one replace.
- A `0 file(s)` report proves nothing — re-read and diff the region.

## Content rules (unchanged)

- Read `Conventions.md` before writing; start from the matching `_Templates/` note (delete its comment block on use).
- Never write unasked. At closure points ask two questions — did we decide something (ADR)? did we learn something (Lesson)? — propose, wait for approval.
- No approval needed: task checkboxes, `Home.md` `Now`, `_Index.md` lines, phase `Knowledge` sections.
- Keep `Home.md` `Now` current at session end or task transition — it is the next session's entire briefing.

## Recipe: in-place edit of an existing note

1. `read` the note — confirm the exact anchor text and current frontmatter.
2. `eval code='const f=app.vault.getAbstractFileByPath("<path>"); let t=await app.vault.read(f); t=t.replace("<exact unique old>","<new>"); await app.vault.modify(f, t);'`
3. `read` again — verify the changed region AND its neighbors (global-replace hazard).
4. If frontmatter was involved and the last write was `write file=` — expect stripped/glued frontmatter; repair with eval+modify, not another blind write.

## A message to future agents

This file is the cross-session memory for vault write mechanics — findings die with the session unless they land here. If the tool behaves differently from what this file promises: (1) verify the actual behavior (scope the failure, don't extrapolate from one sample), (2) correct or delete the false rule, (3) add the finding with the date verified. Describe the tool as it behaves, not as we wish it did.

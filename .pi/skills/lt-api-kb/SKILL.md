---
name: lt-api-kb
description: Write mechanics and rules routing for the lt-api Obsidian knowledge base (vault ~/Projects/vaults/lt-api). Load when creating, editing, or restructuring notes in that vault, or when sweeping Deferred items. Not needed for reading tasks.
---

# lt-api knowledge base

## Write mechanics (pi-obsidian tool)

- Use the pi-obsidian tool's commands (`read`, `write`, `create`, `append`, `move`, `delete`, `search`, …) — never bash or sandbox file tools on vault paths.
- Writes must be sequential, never parallel — parallel writes can silently produce empty files. Re-read every note after writing and verify the TAIL of the file, not just the head — writes have been observed to truncate silently.
- `write`/`create` do not add the `.md` extension — use `path=` with the explicit `.md` (same for `move`).
- `content=` (and `search query=`/`replace=`) must be one double-quoted value; raw newlines silently truncate the write — emit `\n` escapes instead. Inside, only `\"` `\n` `\t` `\r` are escapes; other backslashes pass through literally (so backticks stay plain, `\"` for `"`, single-quoted YAML scalars in frontmatter).
- `delete` moves to trash (recoverable).
- `eval code=` quoting: inside double-quoted values, `\n`/`\t`/`\r` escapes are decoded into real characters, breaking JS string/regex literals. Single-quote the code value, or avoid escapes entirely.

## search/replace — vault-wide blast radius (verified 2026-09-17)

- `search query=x replace=y` replaces the matched substring in **every vault file whose content contains the query**. `file=` does NOT reliably scope the replace — a call scoped to one note flipped `status:` fields in five other files (templates and `Conventions.md` included).
- Rules of engagement:
  - Before replacing, run the same `query` WITHOUT `replace` — the output lists every file it would touch. That list is your blast radius; verify each file by full `read` afterwards.
  - Only replace on queries that are unique vault-wide: long, content-anchored strings (a trigger wikilink line, a full sentence). Never short generic keys (`status: open`, `type: phase`), even when you pass `file=`.
  - If the edit touches more than one file, or the query cannot be made unique — do a full `write` per file instead. Full-file `write` with `\n` escapes is the reliable path; use `search/replace` only for tiny surgical edits you have proven unique.
- There is no dry-run mode: `preview=true` switches `search` to operator-query parsing (`query=status: active` errors with "Operator not recognized"). Do not treat it as a preview.
- Multiline `\n`-escaped queries work and match exactly — this is how you make a query unique (e.g. span from `status: open` through the trigger line).

## Write discipline

- Before writing any note: read the vault's `Conventions.md` (tags, skeletons, writing style), then start from the matching `_Templates/` note — its comment block carries the rules; delete it on use.
- Never write unasked. At closure points ask two independent questions — did we decide something (ADR)? did we learn something (Lesson)? — propose entries, wait for approval.
- Mechanical updates need no approval: task checkboxes, the `Now` section of `Home.md`, `Lessons/_Index.md` lines, phase `Knowledge` sections.
- Keep the `Now` section of `Home.md` current at session end or task transition — the next session's briefing is that section and nothing more.

## A message to future agents

This skill file is the cross-session memory for vault write mechanics — whatever you learn about tool misbehavior here dies with your session unless it lands in this file. If a command behaves differently from what this file promises, do not silently work around it: (1) verify the actual behavior (scope the failure, don't guess from one sample), (2) correct or delete the false rule, (3) add the finding with the date it was verified. This file must describe the tool as it behaves, not as we wish it did — stale rules here cause the same corruption again in a session that cannot see yours.

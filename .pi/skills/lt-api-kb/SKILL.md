---
name: lt-api-kb
description: Write mechanics and rules routing for the lt-api Obsidian knowledge base (vault ~/Projects/vaults/lt-api). Load when creating, editing, or restructuring notes in that vault, or when sweeping Deferred items. Not needed for reading tasks.
---

# lt-api knowledge base

## Write mechanics (pi-obsidian tool)

- Use the pi-obsidian tool's commands (`read`, `write`, `create`, `append`, `move`, `delete`, `search`, …) — never bash or sandbox file tools on vault paths.
- Writes must be sequential, never parallel — parallel writes can silently produce empty files. Re-read every note after writing and verify the TAIL of the file, not just the head — writes have been observed to truncate silently.
- `write`/`create` do not add the `.md` extension — use `path=` with the explicit `.md` (same for `move`).
- `delete` moves to trash (recoverable).
- `search query=x replace=y` replaces only the exact matched substring — query the full text you want gone, not a prefix.
- `eval code=` quoting: inside double-quoted values, `\n`/`\t`/`\r` escapes are decoded into real characters, breaking JS string/regex literals. Single-quote the code value, or avoid escapes entirely.

## Write discipline

- Before writing any note: read the vault's `Conventions.md` (tags, skeletons, writing style), then start from the matching `_Templates/` note — its comment block carries the rules; delete it on use.
- Never write unasked. At closure points ask two independent questions — did we decide something (ADR)? did we learn something (Lesson)? — propose entries, wait for approval.
- Mechanical updates need no approval: task checkboxes, the `Now` section of `Home.md`, `Lessons/_Index.md` lines, phase `Knowledge` sections.
- Keep the `Now` section of `Home.md` current at session end or task transition — the next session's briefing is that section and nothing more.

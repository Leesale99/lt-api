---
name: lt-api-kb
description: Write mechanics and content rules for the lt-api Obsidian vault (~/Projects/vaults/lt-api), used by the sandboxed librarian that works the vault with plain file tools. Load before creating, editing, moving or restructuring notes, or when sweeping Deferred items. Not needed for reading tasks.
---

# lt-api knowledge base

## Where you are

- You are a **file-tool agent**: read, bash, edit, write, grep, find, ls. No Obsidian tool, no MCP, no network — the vault arrives as a read-write mount and plain file operations on that path are the entire surface. Nothing caches for you, and nothing refreshes for you: what you `read` is what is on disk.
- `Conventions.md` is the content authority: the closed tag vocabulary, frontmatter fields, note skeletons, `assets/`, the index rules, the writing rules, the Deferred sweep. Read it before writing. This skill adds only mechanics and safety.
- Load only what the task needs: `Home.md` (its `Now` section declares the current task) → the phase note it names → that note's links. Never preload the vault.
- Vault map: `Home.md`, `Conventions.md`, `Phases/`, `Decisions/` (`ADR-NNN - title.md`), `Design/`, `Lessons/` (with `Lessons/_Index.md`), `Deferred/`, `_Templates/`, `assets/`.

## Content rules (always)

- Read `Conventions.md` first; start from the matching `_Templates/` note (`Decision`, `Lesson`, `Design`, `Home`, `Phase MOC`, `Deferred item`) and delete its comment block on use.
- Frontmatter survives every edit. Every note carries `type`, `tags`, `date`; Lessons and ADRs add `phase: "[[Phase NN - name]]"` (a wikilink, never plain text); ADRs read `status: proposed` until the work is reviewed, then `accepted`. A whole-file `write` must include the entire frontmatter block — check the head afterwards.
- Never write unasked. At closure points ask two questions — did we decide something (ADR)? did we learn something (Lesson)? — propose, wait for approval.
- No approval needed (mechanical): phase task checkboxes, `Home.md` `Now`, a `Lessons/_Index.md` line for a new lesson, a phase note's `Knowledge` section.
- `Home.md` `Now` is three lines max and stays current at session end or task transition — it is the next session's entire briefing. No status narration anywhere; decisions go in the note that owns them.
- Never invent paths, tags, note types or frontmatter keys. If a note does not exist, say so instead of creating a plausible one.

## File-op conventions

1. **ASCII filenames.** Plain letters, digits, spaces, hyphens. No em dashes, no exotic punctuation — those have produced empty files before.
2. **One file at a time.** Finish a note (write → verify) before touching the next. Parallel or interleaved writes on the same vault land truncated or empty content.
3. **Verify after every write.** Read the changed region back, plus its neighbours; re-grep the anchors and links you touched. A write that reports success is a claim, not evidence.
4. **Smallest edit wins.** `edit` on a unique anchor for a change; `write` only for a new note or a genuine whole-file rewrite. Never leave a half-written note: frontmatter head, body, trailing newline.
5. **Bulk edits: list first, then edit.** Grep for the pattern, list every matching file, then edit them one by one. Never a vault-wide substitution whose blast radius you have not enumerated.
6. **Move / rename / delete needs explicit approval plus a link sweep.** Before: grep the vault for `[[<name>` and list every reference in the report; after: re-grep to prove nothing dangles. One file per operation, never a batch you cannot describe.
7. **Recovery net.** This vault has no git history (checked: no `.git`) and the librarian never commits — the host/user does. Recovery is `.trash/` (Obsidian's deletions are recoverable), Obsidian's own file-recovery snapshots, and the user's backups. If a `.git` ever appears, `git log -- <path>` (read-only) is the fast way to see a note's history; you still never commit. Because the net is thin, verify before a destructive op rather than after.
8. **Report path + one line per touched note.** The parent may read the vault with the same plain file tools (verified 2026-11-23: the host runs no obsidian tool or MCP, so reads are ordinary file reads), but every write stays with you — a summary without exact paths cannot be checked.

## vault-check (pre-write sanity)

`node <repo>/.pi/skills/lt-api-kb/vault-check.mjs ~/Projects/vaults/lt-api [--strict] [--keys] [--all]`

Read-only, no dependencies. Reports `[[wikilinks]]` that resolve to nothing (notes and `assets/` files both count) and notes with no frontmatter block; `--keys` also checks `type`/`tags`/`date`; `--all` includes `_Templates/` and `Conventions.md`, where placeholder links are intentional. Advisory (exit 0) unless `--strict`. Run it before a restructure and after a move/rename burst — it is the mechanical version of rules 3 and 6.

## Recipe: in-place edit of an existing note

1. `read` the note — confirm the exact anchor text and the current frontmatter, then `read` its neighbours if you are about to change a heading.
2. `edit` with the smallest unique anchor (or `write` the whole note, frontmatter included).
3. Read the changed region back and grep for the old text: one edit can silently hit a second, identical line.
4. If frontmatter was involved, confirm the opening `---` and its closing fence both survived.

## A message to future agents

This file is the cross-session memory for vault write mechanics — findings die with the session unless they land here. If a file operation behaves differently from what this file promises: (1) verify the actual behavior (scope the failure, don't extrapolate from one sample), (2) correct or delete the false rule, (3) add the finding with the date verified. Describe the operation as it behaves, not as we wish it did.

---
description: Single librarian call for knowledge-base ops
argument-hint: "<task>"
---
Use the subagent tool (single mode, agent "librarian") to perform this knowledge-base task: $@

The librarian runs sandboxed with the vault (`/Users/aleksrdvn/Projects/vaults/lt-api`) mounted read-write and the project repo read-only, no network. There is no obsidian MCP tool inside that sandbox: it works the vault through plain file tools (read/edit/write/grep/find/ls, plus bash for mv/cp/rm-style moves), and the `lt-api-kb` skill carries the writing mechanics and content rules.

Return its summary of what it did/found. Do not touch vault paths yourself.

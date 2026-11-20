---
description: Scout recon → go-worker implements the task, chained
argument-hint: "<task>"
---
Use the subagent tool with the chain parameter to execute this workflow:

1. First, use the "scout" agent to find all code relevant to: $@
2. Then, use the "go-worker" agent to implement "$@" using the recon context from the previous step (use {previous} placeholder). go-worker applies changes directly, works the RED→GREEN→PROVE→REFACTOR loop and runs the gates before reporting.

Fill the go-worker task text as a contract — every part, unless it truly does not apply:

1. **Brief** — paste the output of `go run ./cmd/spec brief R-XXX-NNN` (or `--module <m>`) for the rule(s): statement, sites, refusal map, verification pointers, linked scenarios.
2. **Rule IDs + scope line** — the ids, plus exactly one machine-readable line, verbatim in the task text:
   `SPEC-SCOPE: rules=R-XXX-NNN,R-YYY-NNN` and/or `SPEC-SCOPE: module=<m>`
   The delegate extension parses that line out of the task text and scopes the worker's gates to it; a worker without it gates on the full spec check.
3. **Owned paths** — the exact files/dirs it may edit; nothing else is in scope (gate files are denied to workers regardless).
4. **acceptance_cmd** — the exact commands that must exit 0: scoped check (`go run ./cmd/spec check --strict --module <m>` / `--rule <id>`), `make audit/static`, `make prove args='--rule <id>'` for touched rules, and the full `make audit`.
5. **Budget** — 3 attempts per phase, then stop and report.

Execute as a chain, passing output between steps via {previous}. Do NOT implement anything yourself — delegate both steps. When done, give me the go-worker's final report verbatim, plus a one-line note on what scout found.

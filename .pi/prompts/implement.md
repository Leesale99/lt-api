---
description: Scout recon → go-worker implements the task to green, chained
argument-hint: "<task>"
---
Use the subagent tool with the chain parameter to execute this workflow:

1. First, use the "scout" agent to find all code relevant to: $@
2. Then, use the "go-worker" agent to implement "$@" using the recon context from the previous step (use {previous} placeholder). go-worker applies changes directly and works a GREEN-first loop: RED already exists (host-authored), PROVE is not its step, and it iterates against the in-VM advisory scoped check + audit/static before reporting.

Both steps run inside per-task sandboxes (worktree mounted at the same absolute path, git-less, no DSN, no host reach). RED already exists and is host-committed before this prompt runs; the worker never authors it. The host plane — not the worker — runs the authoritative gates and writes the receipts.

Fill the go-worker task text as a contract — every part, unless it truly does not apply:

1. **Brief** — paste the output of `go run ./cmd/spec brief R-XXX-NNN` (or `--module <m>`) for the rule(s): statement, sites, refusal map, verification pointers, linked scenarios.
2. **Rule IDs + scope line** — the ids, plus exactly one machine-readable line, verbatim in the task text:
   `SPEC-SCOPE: rules=R-XXX-NNN,R-YYY-NNN` and/or `SPEC-SCOPE: module=<m>`
   The delegate extension parses that line out of the task text and scopes the worker's in-VM advisory gates to it; a worker without it gates on the full spec check.
3. **Owned paths** — the exact files/dirs it may edit; nothing else is in scope. Gate files (Makefile, cmd/spec/**, .github/**, WORKFLOW.md) are outside every task: the host's integrity check (`git diff --exit-code <base> -- Makefile cmd/spec .github WORKFLOW.md`) voids the batch if they change.
4. **acceptance_cmd** — split by plane. In-VM (advisory, the worker runs these and reports exit codes): scoped check (`go run ./cmd/spec check --strict --module <m>` / `--rule <id>`), `make spec/gen` when the catalog changed, then `make audit/static`. Host-side (you, after the worker exits): the same scoped check, `make audit/static`, `make audit` for a rule-change batch, `make prove args='--rule <id>'` for touched rules, then the merge-back. Never ask the worker for `make audit` or `make prove` — it has no DSN and no receipts.
5. **Budget** — 3 attempts per phase, then stop and report.

Execute as a chain, passing output between steps via {previous}. Do NOT implement anything yourself — delegate both steps. When done, give me the go-worker's final report verbatim, plus a one-line note on what scout found. Treat the worker's gate results as advisory claims: the authoritative verdict and its receipt paths come from the host plane.

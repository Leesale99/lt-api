# lt-api — the developer hub

This repository is one backend service and three support systems around it. Each
system answers a different question, each has exactly one source of truth, and each
has one deep-dive document. Start here, then open only the deep dive you need.

| Plane | The question it answers | Single source of truth | Deep dive |
| --- | --- | --- | --- |
| **Application** | What does the service do, and where does a business decision live? | `cmd/api/main.go` (composition root) and `internal/arch/arch_test.go` (the import matrix) | [`../README.md`](../README.md) |
| **Executable specification** | Which business rules does this system hold, who decides each one, and how do we know it still holds? | `docs/spec/rules/` — one authored file per rule | [`spec/README.md`](spec/README.md) |
| **Delegate harness** | Who is allowed to change what, which gates certify the change, and who records the verdict? | `WORKFLOW.md` front matter (the machine-checked policy) | [`delegate-workflow.md`](delegate-workflow.md) |
| **Knowledge vault** | Why was this decided, and what did we learn? | the vault's own `Home.md` and `Conventions.md` | [`../.pi/skills/lt-api-kb/SKILL.md`](../.pi/skills/lt-api-kb/SKILL.md) |

## Application plane

A modular monolith in Go: three bounded contexts (`schedule`, `game`, `identity`)
plus a thin `ledger`, composed by `cmd/api` over a kernel (`internal/kernel/`) and a
platform (`internal/platform/`). A context root package is the only package outsiders
may import, and those edges are pinned in code rather than agreed in prose —
`internal/arch/arch_test.go` is the boundary's source of truth, so a new import either
fits the matrix or fails the build. Each context also owns a Postgres schema and a
NOLOGIN role, so the same boundary exists at the database.

The root [`README.md`](../README.md) is the model for how the whole repo documents
itself: a layout block, then the why. Read it before changing how a module is
composed or configured.

## Executable specification plane

`docs/spec/` is the catalog of business rules, the Gherkin scenarios that show them
from outside, and the checker that keeps catalog, code, tests and scenarios tied
together. The rule files are authored: one Markdown file per rule at
`docs/spec/rules/<module>/<ID>.md`, front matter for the machine fields, body for the
notes. Everything else in the tree is either generated from them (the dashboard
`docs/spec/coverage.md`) or checked against them (`cmd/spec check --strict`).
The format authority is [`spec/rules.schema.md`](spec/rules.schema.md); the workflow,
the commands and the rule-change protocol are [`spec/README.md`](spec/README.md).

What this plane buys you is three mechanical guarantees, each one a numbered policy in
the catalog's own invariants (the list itself is
[`spec/policies.md`](spec/policies.md), one section per policy):

- **Policy 12 (`12-features`) — no dangling tag, no ambiguous scenario.** The feature
  tree is cross-checked in both directions — every `@rule` tag names a catalog rule,
  and every scenario names exactly one — so a tag pointing at a missing rule and a
  scenario naming zero or two rules are both errors.
- **Policy 15 (`15-annotations`) — no unannotated enforcement site, no orphan
  annotation.** Every Go enforcement site carries its rule's `//rule:R-XXX-NNN`
  annotation, and every annotation in any non-vendor Go file names a catalog rule —
  in both directions.
- **Policy 18 (`18-oracle`) — no vacuous assertion, no copied expectation.** Scenarios
  must assert something an observer can see, and the assertion must match the rule's
  category — no vacuous Then steps ('runs without throwing'), and no expected value
  copied verbatim from the implementation's source literals.

All three fail `make audit` rather than a review, so the guarantee is a gate verdict,
not a reviewer's opinion.

## Delegate harness plane

Work that is separable gets delegated to a sandboxed worker. The worker is an agent
inside a per-task micro-VM with a single worktree mount (read-only, or absent, for the
read-only and research roles), no git and no database credential; the human-facing
session and its child orchestrators stay host-side, execute the authoritative gates in
a locked-down gate VM, and are the only place a verdict, receipt, claim, commit or
merge is produced.

The policy is [`WORKFLOW.md`](../WORKFLOW.md) front matter — gate commands, the machine
paths, the retry and stall limits, the approval tokens — and it is read fresh on every
gate decision; a missing, unparseable or unknown-field policy refuses all gates rather
than degrading quietly. The narrative — the write boundary, advisory vs authoritative
gates, RED/GREEN/PROVE, task packets, receipts, handoff modes — is
[`delegate-workflow.md`](delegate-workflow.md). Never restate the policy from that
document or from a prompt: link it.

## Knowledge vault plane

The Obsidian vault (`~/Projects/vaults/lt-api`) holds the ADRs, the lessons, the
design notes and the phase record. It is mounted read-write only for the `librarian`
worker; nothing else in the system writes it. Its `Conventions.md` owns the tag
vocabulary, the frontmatter keys and the note skeletons, and its `Home.md` `Now`
section is the briefing for the next session — the load-only-what-you-need rule
starts there.

Two links point into it, and neither is a copy: a rule file's `expert-ref` and
`knowledge-ref` name the in-repo expert document a rule came from (under
`docs/league-tokens-learning-phases/`), and the vault's `Design/` notes describe how
these planes work at the level above this repo.

## The one change, end to end

A behaviour change that is a *rule* change travels the whole system, and the order is
what makes it trustworthy — the spec goes red before the code goes green, and the
proof is a gate, not a claim:

1. **Write the rule file and its scenario first.**
   `docs/spec/rules/<module>/<ID>.md` (front matter from the code as it is today, plus
   the statement you want) and a `@rule:<ID>` scenario under
   `docs/spec/features/<module>/`. New step vocabulary is a deliberate
   `docs/spec/glossary.yaml` edit in the same change.
2. **Show it red for the right reason.** `make audit/static` fails because the
   scenario cannot pass and `spec check --strict` names the untraceable entry. This is
   the host's job, on the working branch, before any worker exists.
3. **Implement to green.** The code, the `//rule:<ID>` annotation on each new
   enforcement site, and the verification pointers the rule file needs. Scoped
   `spec check --strict --module <m>` in the worker's loop is advisory; it shortens
   the loop and certifies nothing.
4. **Regenerate the dashboard.** `make spec/gen` after any catalog change; the
   freshness diff is part of `audit/static`, so a hand-edited
   `docs/spec/coverage.md` fails the gate.
5. **Prove it.** `make prove args='--rule <ID>'` runs the rule's mapped bypass
   scenarios — the ones that attempt the forbidden write or action outside the
   application — and must report VERIFIED. Registry mappings whose scenarios assert
   nothing observable are rejected as VACUOUS before anything runs. Prove needs
   Postgres and is never part of `make audit`.
6. **Run the authoritative gates.** The full `make audit` (static half plus the whole
   test tree, DB-backed). Driven from a pi session it executes in the gate VM; on a
   plain checkout or in CI it runs directly, after `make db/up`.
7. **Merge.** In a delegated batch the merge is blocked mechanically unless the
   scoped rules carry VERIFIED prove receipts; in a direct change the same two gates
   are your own precondition. Commit the rule file, the scenario, the code, the
   pointers and the regenerated dashboard together.

A change that is *not* a rule change (a refactor, a test, a dependency) skips steps 1,
2 and 5 and keeps 3–4, 6 and 7. If you find yourself writing code whose rule does not
exist in the catalog, that is a rule change — start at step 1.

## Where each gate lives

| Gate | What it refuses | Where it runs |
| --- | --- | --- |
| `make audit/static` | unformatted Go, vet or staticcheck findings, a stale dashboard, any catalog policy error (`spec check --strict`) | gate VM in a pi session; directly on a plain checkout or in CI. No database |
| `make audit` | the above plus any failing test, including the DB-backed suites (`-count=1`, never the test cache) | gate VM with its baked Postgres; directly on a plain checkout or in CI after `make db/up` |
| `make prove` | a touched rule whose mapped scenario does not detect its own bypass — VERIFIED is the only verdict that needs nothing more | gate VM with Postgres; never inside `make audit` |
| `spec check --strict --module <m>` / `--rule <ID>` | the same policies, scoped to one module or rule | a worker's own loop (advisory) and the host's ladder (authoritative) |
| the integrity check (`git diff --exit-code` over the declared gate paths) | a batch that touched `Makefile`, `cmd/spec`, `.github` or `WORKFLOW.md` — it voids its own verdict | the host, before any gate command |
| `make smoke` | broken extension seams (gate runner, policy loader, receipts, `sbx` argv, integrity check) | any host, no sandbox, no network |

Two rules of thumb. A gate is only a gate when something authoritative runs it: a
worker's in-VM run is a hint, and a green claim without an exit code is prose. And if
a gate blocks you, fix the tree or change the policy file — never the gate.

## Who owns each format

One owner per format, one parser, and a refusal (not a fallback) when the two
disagree. That is the pattern the whole repo follows, so when two documents seem to
describe the same thing, this table says which one is the source.

| Format | Authored / owned by | Checked by |
| --- | --- | --- |
| the rule catalog (`docs/spec/rules/**`) | you; schema in `docs/spec/rules.schema.md`, loader in `cmd/spec/catalog.go` | `cmd/spec check` (policies in `cmd/spec/policies.go`) |
| the generated dashboard | `cmd/spec gen` — never hand-edited | `spec/gen --diff`, wired into `audit/static` |
| the Gherkin tree and the step glossary | you; style in `docs/spec/gherkin-style.md` | policy 13 — every step in `docs/spec/features/**` is bounded by the declared vocabulary, the transport/SQL denylist plus the `glossary.yaml` allowlist (`cmd/spec/vocabulary.go`) — and policy 12 — every `@rule` tag names a catalog rule and every scenario names exactly one (feature tags) |
| the mutant registry (`docs/spec/prove.yaml`) | you, one entry per provable rule | `cmd/spec prove`, at merge-back |
| the workflow policy (`WORKFLOW.md` front matter) | you; parser `.pi/extensions/delegate/policy.ts` | the loader refuses unknown fields, and the approval constants must equal `contracts.ts` |
| the task-packet grammar | `.pi/extensions/delegate/protocol.ts` (doc-of-record: `WORKFLOW.md` § Task packets) | one strict parse at spawn; a malformed packet spawns nothing |
| the sandbox profiles | `.pi/extensions/delegate/sbx.ts` | `smoke-sbx.mjs` |
| the receipt and claim record shapes | `.pi/extensions/delegate/gates.ts` and `claims.ts` | host-only writers; readers tolerate old shapes by design |
| the agent roster and prompts | `.pi/agents/*.md` (main persona `main.md`) | `persona.ts` loads a named-path, stderr-announcing fallback when the main persona is missing |
| the knowledge vault | the vault's `Conventions.md` | `.pi/skills/lt-api-kb/vault-check.mjs` |

## Where to start

- New to the service: the root [`README.md`](../README.md), then `cmd/api/main.go`.
- New to the rules: [`spec/README.md`](spec/README.md) §1 for the change protocol,
  §2 for the file you are about to write, then one real rule file.
- About to delegate work: [`delegate-workflow.md`](delegate-workflow.md), then
  `WORKFLOW.md`.
- Looking for why: a rule file's `knowledge-ref`, then the vault.

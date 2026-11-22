# Delegated work — how it actually runs

Delegation exists for one reason: a task whose context cost exceeds its explanation
cost should not burn the session that talks to the human. Everything else in this
document follows from taking that seriously — isolation, a single write boundary, a
gate ladder whose verdict does not belong to the worker, and a record of what actually
ran.

[`WORKFLOW.md`](../WORKFLOW.md) is the policy of record: its front matter is the
machine-checked declaration (gate commands, machine paths, retry and stall limits,
approval tokens) and the extension reads it fresh on every gate decision. This document
is the narrative
around that policy: what the machinery is, why it is shaped that way, and where to look
when something blocks. When the two disagree, the policy wins and this file is the bug
— but nothing here restates a policy value, by design. If you need the number, field or
token, it is in `WORKFLOW.md`, not here.

## The two planes

A delegated task has a **worker plane** and a **verifier plane**, and the split is
physical rather than behavioural.

A worker is a `pi` process started by the host with `sbx exec` into a per-task Docker
Sandbox micro-VM (`sbx`, pinned in `sbx.ts`). It gets its own context window, its own
mounts, its own egress policy and no host reach past the mount. The in-VM copy of the
extension loads and immediately goes inert: it registers no gate, issues no verdict,
writes no receipt, and exists only to label the worker's own gate runs as advisory.

The verifier plane is the human-facing main session and its child orchestrators,
running on the host. It owns the sequence — decompose, spawn, collect, verify, commit,
merge — and it owns every artifact that says something was verified. Its own execution
surface for worktree code is deliberately tiny: `git` for the integrity check and
`sbx exec` for the gates. It does not compile, test or fmt the tree itself, so a
poisoned worktree cannot become host reach.

That asymmetry is the whole safety argument. There is no path classifier, no bash write
scanner and no command allowlist anywhere in the system; those mechanisms were replaced
by two things that cannot be argued with — a hypervisor mount and a detector at the
merge boundary.

## The write boundary is the mount

What a worker can write is decided by what the VM mounts, not by what the agent says.
The profile table in `sbx.ts` is the policy, and `smoke-sbx.mjs` asserts it:

| Role | Mounts | Egress |
| --- | --- | --- |
| `worker`, `general` | worktree, read-write | model provider + Go module proxies |
| `scout` | disposable scratch dir (rw) + worktree (ro) | model provider only |
| `librarian` | vault (rw) + worktree (ro) | model provider only |
| `researcher` | none at all | model provider, docs host, host search relay |

An unknown role falls back to the `worker` profile — narrower, never a host
passthrough. No role is passthrough today; the field exists so that a future role could
be declared host-side deliberately instead of by accident.

Three consequences shape everything downstream.

**Workers are git-less.** The worktree arrives as a mount, so `.git` is unresolvable
inside it: no status, diff, commit, stash, checkout, branch or merge. A worker reports
a change set; the host names the commit. This is also why `make audit/static` is a
poor in-VM gate — its `gofmt` step enumerates `git ls-files` — and why a worker that
hits that wall reports the failing line instead of editing the Makefile.

**Workers are DSN-less.** No sandbox gets `LT_API_TEST_DSN`, so the DB-backed half of
`make audit` and all of `make prove` simply cannot run in a worker VM, even if a
worker typed them.

**Workers are vault-less**, except `librarian`, whose read-write mount is the vault
and whose repo mount is read-only. That is a profile, not a special case in a prompt.

Egress is default-deny per profile, and a worker's model credential reaches the VM
through the `sbx` secret sentinel rather than a value written into a file. A dependency
outside the allowlist is a blocked, explained report — never a reason to vendor or
disable something.

## Three kinds of gate, three owners

The word "gate" covers three different things here, and conflating them is the mistake
that makes a green-looking report untrustworthy.

**Advisory gates run in the worker VM.** The declared scoped spec check (`go run
./cmd/spec check --strict --module <m>` or `--rule <id>`) plus a direct `gofmt -l` over
the files the worker changed. They shorten the loop; they certify nothing. The in-VM
extension runs them, labels them advisory, and refuses to write a receipt for them. A
worker that reports "green" is reporting exit codes it observed, not a verdict.

**Authoritative gates run in a gate VM.** The forwarded gate commands are the ones
declared in `WORKFLOW.md` — the scoped spec check, `make audit/static`, `make audit`,
`make prove` — and the host does not execute them either. It creates a second,
locked-down VM from the `pi-gate:v1` template, with one read-write mount of the
worktree (gates legitimately regenerate artifacts), the main repo root mounted
read-only when the worktree is linked so `git` resolves in-VM, and egress for the Go
module proxy only: no model, no secret sentinel. Gate VMs are session-scoped and keyed
by worktree path, so a background job's fresh worktree gets its own, and all of them
are torn down when the session ends. A gate command typed into a host bash session is
recognised, checked, DB-pre-sequenced and rewritten into the equivalent `sbx exec`
line, so there is no path where the host runs the tree's code.

The gate VM carries its own Postgres on the port the repo documents; the DSN reaches
the make target as a command-line override, which is the only form that survives the
Makefile's `-include .envrc`. That is why a DB-backed gate needs no credential on the
host at all.

**The integrity check runs on the host, before any gate.** One `git diff --exit-code`
against `HEAD` over the paths declared as `gate_file_paths` (plus the index variant, so
a staged deviation counts too). Those paths are the definition of green — the gate
commands, the checker, CI config, the policy file — so a batch that touched one has
invalidated its own verdict, and the host stops instead of reasoning about it. The
trade is stated honestly: this is a detector at the merge boundary, not a write barrier
inside the VM. A worker can still write a non-gate file in its read-write mount; the
full audit at merge plus the orchestrator's diff review is the backstop.

## RED, GREEN, PROVE

The three phases belong to three different owners, which is what makes the sequence
mean something.

**RED is host-authored, before the task exists.** The rule file under
`docs/spec/rules/<module>/` and its `@rule:<ID>` scenario under
`docs/spec/features/**` are written and committed on the delegate branch on the host
plane, and the batch is shown red *for the right reason* there: the new scenario fails
and `spec check --strict` names the untraceable entry. Only then is a task text
written. A missing or unjustified RED is a stop, not a task — and a worker never
authors RED nor edits a scenario into agreement with its code; a scenario that looks
wrong is a report.

**GREEN is the worker loop.** Implement, annotate the new enforcement sites with
`//rule:<ID>`, resolve the verification pointers the rule file names, regenerating the
dashboard when the catalog changed, and iterate against the advisory gates. The budget
is small by design: a few attempts per phase, then a stop and a report that explains
how it failed. A silent retry is worse than a blocked report, because the orchestrator
cannot tell the difference between progress and thrash from the outside.

**PROVE is a merge-back precondition, not a step.** `make prove args='--rule <id>'`
runs the rule's mapped bypass scenarios — the ones that attempt the forbidden write
around the application — and requires them to pass, so VERIFIED means the rule's refusal
actually happened when something tried to break it. VACUOUS (the mapping asserts
nothing observable) and BROKEN-TEST (a scenario fails against an intact tree) are hard
fails and cannot be waived; UNVALIDATABLE is reported and stops for human sign-off
against the registry's git-attributed signoff. It needs Postgres, so it lives in the
gate VM. And it is enforced where it matters: the extension blocks a merge command in
an orchestrator session when the job's scoped rules lack VERIFIED receipts,
independently of how the approval arrived.

## Task packets

A task text handed to the `subagent` tool is a task packet, and it is parsed exactly
once, at the spawn boundary, by the one owner of that grammar
(`.pi/extensions/delegate/protocol.ts`; the doc-of-record is `WORKFLOW.md` § Task
packets — the table of line forms lives there and is not repeated here).

Why one parse and one owner: the grammar used to be prose in several prompts, and the
failure mode of prose is a line that looks meaningful and silently means nothing. Now
the parse is strict and it is the spawn gate — `SPEC-SCOPE:` at most once, in a fixed
token grammar; `CLAIMS-PATHS:` optional, comma-separated, every entry non-empty. A
malformed packet writes no claim, creates no sandbox, starts no worker, and the
refusal naming the offending line *is* the tool result. A worker carrying a well-formed
scope gets its normalized value in `PI_DELEGATE_SPEC_SCOPE`; a packet with no scope
line gets no variable, which means the full spec check rather than a silently empty
scope.

The grammar is not decoration. The scope is what narrows the worker's advisory gates,
what the claims log records, and what the merge-back and the approval relay resolve
against — so a line the parser cannot read would have become a job that is
relay-approved and merge-blocked on the same scope. That was a real defect; it is
closed by having one resolver, described below.

## Claims and receipts are host-only

Two append-only records, both written by the host extension instance and only for
things the host actually did.

**Claims** live under the job directory and record two kinds of event: a `claim` at
spawn (worker, session, rule ids, module, owned paths, the `git status --porcelain`
baseline) and a `completed` after the worker returns (the paths that changed against
that baseline). The point is parallelism safety: before a spawn the extension reads
the log and blocks an overlapping claim — same rule id, same module, same owned path —
so two workers cannot rewrite each other's files and then both fail for reasons
neither can see. The next worker's prompt carries a short broadcast of the most recent
completions, so siblings start with context instead of duplicating it. Claims are
telemetry: a `git` failure records an empty change set and a note rather than failing
a spawn.

**Receipts** live in the receipts directory, one JSON line per gate run, appended per
ISO date. A receipt records the agent, the session, every command with its exit code,
timeout flag and duration, an `ok` flag, a free-text note and — additively, when the
run happened there — the gate VM's name. Two metadata fields make the record useful
across time: the `HEAD` SHA it ran at, and a **catalog hash** — a manifest SHA-256 over
`docs/spec/rules/**`, so a reader can tell two receipts apart when the rule files
changed between them. Receipts carry a `schema_version`; a receipt from an older
schema is treated as superseded by the reconciliation comparison rather than
"corrected", because guessing what an old hash meant is worse than saying it cannot be
compared. Receipt metadata is fail-open (a missing catalog or a broken `git` records an
empty field) while the commands stay fail-closed — only the record degrades, never a
verdict.

Host-only is not a stylistic choice. Both directories are gitignored, so a fresh
worktree — and therefore every worker VM — has no `.pi/receipts/` and no `.pi/jobs/`.
A worker citing a receipt is a category error: a worker's report is claims plus exit
codes, and the orchestrator's report cites the receipts it caused. A prove receipt
written inside a mount could be forged, which is why the prove receipt writer records
in the host session's own repository root and ignores a `cd` inside the command.

**One resolver, one answer.** "Which rules does this job own?" is answered in exactly
one place (`mergegate.ts`): the union of the rule ids in the job's spawn claims, and
only when the claims name none, a fallback to the `SPEC-SCOPE` line of the job's task
text. No scope at all means no rule ids, which means the merge-back needs no prove
receipts — a non-rule-change batch merges freely. Both consumers call it: the approval
relay (`job_approve`) and the merge-path block, so "relay-approved but merge-blocked on
the same scope" is structurally impossible instead of merely unlikely.

## Handoff modes and the approval contract

A child orchestrator runs as a background job and ends its round with a handoff
decision. The mode is read from the task text (`HANDOFF=...`) or, for a single-mode
background job, from the `handoff` parameter, which is appended to the prompt after the
task text so it wins. Omitted means the task-text convention applies, and no mention
means `confirm` — the safer default.

**`auto`** runs the merge-back immediately after verification and reports completion
with the merge summary and the receipt paths. **`confirm`** stops after verification
and commits: it emits the approval block and ends the round with no further tool calls,
leaving the worktree and branch in place for a decision.

The approval block is a machine-readable contract, not a pleasant-looking message. It
must carry the `AWAITING_APPROVAL` marker and the reviewed worktree's `HEAD SHA:`
line, and the review content the human needs — plan summary, branch, worktree path,
files changed, gate results, whether prove is required. The decision arrives by typing
in the job's pane or by relaying the same message from the main session through the
`job_approve` tool; both land as a user message in the orchestrator's own session.

**Approval is bound to a commit, not to a moment.** The decision is bound to the
40-hex SHA the block named, so a worktree that moved after review needs a fresh
approval — approving a diff you did not review is exactly the failure this prevents.
Approving runs the merge-back; rejecting sends the orchestrator back to iterate and
produce a new block. A rule-change batch has one extra stop in the same contract: the
spec-only RED commit is the thing reviewed before GREEN starts, and its SHA is the one
bound, so GREEN, PROVE, REFACTOR and the merge-back proceed only while `HEAD` still
matches it.

The terminal state is always the last line of the round's final message, drawn from
the declared token set. The registry reads them: a round that ends on the approval
marker becomes `awaiting_approval` — a first-class waiting status rather than "done"
with a strange last message — and a terminal token wins over the marker, so a report
that mentions the approval gate in passing is still a finished report.

The main session's own edit/write confirmation is a separate, host-side thing and
stays: it is the human's, and no sandbox policy replaces it.

## Failure, limits and escalation

Retry, backoff, stall and concurrency limits are policy fields, not tunables in the
code: how many settle attempts a worker gets, how many happen immediately before
backoff starts and how the wait grows, how long a silent worker may run before the
watchdog tears its VM down, how many workers may be in flight. The extension reads
them from `WORKFLOW.md` per decision and refuses to gate at all if the policy is
unavailable — a tree whose policy cannot be parsed has no gates, and telling a worker
it passed would be a lie.

Escalation goes upward, never sideways. A worker reports; an orchestrator coordinates
and decides within its task; a human decides anything the tree cannot settle — a dirty
integrity check, a red approval stop, an UNVALIDATABLE rule, a missing capability, an
ambiguous requirement. The reporting norm that makes that workable: a blocked,
well-explained report with the failing command and its output is worth more than a
green-looking summary, because the orchestrator can act on the first and has to
distrust the second.

Delegation is one level deep. A worker has no `subagent` tool at all and never runs in
the background; a child orchestrator may spawn foreground workers but not another
orchestrator or a background job. That bound keeps a failure attributable to one
context and one set of receipts.

## Where the code lives

`.pi/extensions/delegate/index.ts` is the extension root and nothing else: the entry
point, the environment switches, and the order in which modules register their hooks,
tools and commands. Any logic found there beyond that is a layering bug. Everything else
is a module with one job:

| Module | Owns |
| --- | --- |
| `policy.ts` | the `WORKFLOW.md` parser and its refusals — the only reader of the policy file |
| `contracts.ts` | the approval constants; `policy.ts` enforces equality against them |
| `protocol.ts` | the task-packet grammar — one parse, at the spawn boundary |
| `dispatch.ts` | the `subagent` tool (modes, the packet parse, claims, refusals), `job_status`, `job_approve` |
| `spawn.ts` | one child run: agent resolution, `sbx exec`, child env, host verification after exit |
| `sbx.ts` | the only module that speaks the `sbx` CLI: VM lifecycle, profiles, the gate VM image and its database |
| `gates.ts` | the gate runner, the integrity check, receipts, settle state and the hooks that enforce them |
| `mergegate.ts` | the job-scope resolver and the prove-receipt policy — the merge block and the approval relay both call it |
| `claims.ts` | the claims log, overlap detection, sibling-context broadcasts |
| `jobs.ts` | the background job registry, status refresh, the approval relay mechanics |
| `persona.ts` | prompt assembly: the main persona, the child prompt, the tool description |
| `surfacing.ts`, `tui/` | the chat surface (job cards, result injection, exactly-once surfacing) and the live UI |
| `runtime.ts`, `output.ts`, `agents.ts` | small shared planes: process helpers, child-output parsing, agent discovery |

The personas and prompts are data: `.pi/agents/*.md` is the roster and each agent's
rules, `.pi/agents/main.md` is the human-facing persona, `.pi/prompts/` carries the
reusable workflows. The extension reads them and degrades loudly when the main persona
is missing — a named-path fallback plus a stderr line, never a silent generic-rules
session — because a prompt that disappears is a broken install, not a quieter one.

## What this leaves out

Documented limitations, kept visible on purpose:

- The integrity check is the net for poisoned gate files, and only for the paths it
  declares. A worker can still poison a non-gate file in its read-write mount; the full
  audit at the merge boundary plus a human reading the diff is the backstop.
- Hard links can cross out of a mount on the same filesystem. Nothing here detects it.
- The librarian can write the vault; the bound is its profile plus the vault skill's
  rules, not a gate. A librarian mistake there is caught by a human.
- Gate commands are single commands. A compound command that mixes a declared gate with
  other segments is deliberately not rewritten into the gate VM, so write one gate per
  command.
- The researcher's web search rides a host relay. Its profile is mountless and its
  egress is narrow; when the relay is down, search fails closed with a blocked report
  rather than host reach.

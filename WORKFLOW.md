---
version: 1

audit_static_cmd: make audit/static
audit_cmd: make audit
prove_cmd: make prove
scoped_check_cmd: "go run ./cmd/spec check --strict"

receipts_dir: .pi/receipts
claims_dir: .pi/jobs

settle_max_attempts: 5
settle_immediate_attempts: 3
settle_backoff_base_ms: 2000
settle_backoff_max_doublings: 5
worker_stall_timeout_ms: 600000
max_concurrent_workers: 8

approval_binding: head-sha
red_approval_marker: AWAITING_APPROVAL
red_approval_head_sha_line: "HEAD SHA:"
handoff_terminal_tokens:
  - HANDOFF_COMPLETE
  - HANDOFF_BLOCKED
---

# Workflow policy

This front matter is the single machine-checked source for how delegated work is gated: the commands that
must pass, the retry/backoff/stall limits, the machine paths, and the approval tokens. The extension reads it
fresh on every gate decision and **refuses to run any gate when the file is missing, unparseable, carries an
unknown field, or declares a token that disagrees with the code**. If a gate blocks you, fix the tree or change
this file — never work around a gate.

## The write boundary is the hypervisor and the mount

Every delegated worker runs fully inside a per-task Docker Sandbox micro-VM (`sbx`, v0.54.0) built from a
saved template. A host-side orchestrator creates the sandbox, mounts the worktree at the same absolute path,
and runs a foreground `sbx exec` of pi with `-e` forwarding (`PI_DELEGATE_CHILD`, `PI_DELEGATE_ROLE`,
`PI_DELEGATE_SPEC_SCOPE`). What a worker can write is decided by what is mounted, not by a path classifier in
the agent: outside the mount there is no filesystem, no host process table, no SSH agent, and — per profile —
no network at all beyond the model provider. Egress is default-deny, SSH agent forwarding is disabled
host-wide, and a worker's model API key reaches the VM through the `sbx` secret sentinel, never as a
handwritten value in a file.

Because the worktree arrives as a mount, `.git` is unresolvable inside it: **workers are git-less**. They
cannot commit, stash, checkout, reset or merge. The host commits, the host merges, and the host owns every
ref.

Sandbox profiles are declared in `.pi/extensions/delegate/sbx.ts` and asserted by `smoke-sbx.mjs`. No sandbox
gets `LT_API_TEST_DSN`, so `make audit` and `make prove` cannot run in one.

## In-VM gates are advisory; the gate VM executes; the host decides and records

The main session and the background orchestrators stay host-side. The extension also loads inside a worker
VM, but it sees `PI_DELEGATE_SANDBOX=1` and is **inert**: it registers no gate, issues no verdict, writes no
receipt, and exists only so a worker's own gate runs are labelled advisory. The authoritative gates —
`make audit/static`, the scoped spec check, `make audit`, `make prove` — do not execute on the host: each runs
in a second, locked-down **gate VM** (`pi-gate:v1`: git, Go, make, an apt-baked Postgres on the documented port
5433, no model). The host's whole execution surface for the tree is `git` (the integrity check — a trusted
host binary reading paths, never worktree code) and `sbx exec` into the gate VM. The verdict stays the host's:
it consumes the gate VM's exit codes, and the receipts the approval and merge machinery reads remain
host-extension-written. DB-backed gates run against the gate VM's own Postgres, with the DSN passed as a make
command-line override — the only form that survives the Makefile's `-include .envrc`.

## The integrity check

One host-side check runs in the worktree BEFORE any gate:

    git diff --exit-code HEAD -- Makefile cmd/spec .github WORKFLOW.md

(the index variant runs too, so a staged deviation counts). Dirty → the host refuses to run the gates and
routes the batch to human review. The gate files ARE the definition of green, so a worker that touches them
has invalidated its own verdict, and we stop rather than reason about it.

The shape of that trade, honestly: it is a *detector at the merge boundary*, not a write barrier inside the
VM. A worker can still write a non-gate file in its read-write mount; the full audit at merge plus the
orchestrator's diff review is the backstop for that. What does not change is the main session's edit/write UI
confirmation: that stays host-side, and it stays the human's.

## Receipts and claims are host-only

Receipts (`receipts_dir`) and claims (`claims_dir`) are written by the **host extension instance only**, to
host paths, and only for verdicts the host actually produced. A fresh worktree excludes `.pi/receipts/` and
`.pi/jobs/` (both gitignored), so a sandbox never sees them. A worker citing a receipt is a category error; a
worker's report is a set of claims plus command exit codes, and the orchestrator's report cites the receipts
it caused.

## The worker loop: RED is host-authored, GREEN is the loop, PROVE is at merge-back

RED is authored on the host plane BEFORE a task begins: the catalog entry and its `@rule:` scenario are
written and committed on the delegate branch, and the batch is shown red for the right reason there. The
worker never authors RED. Its loop is GREEN-first: implement to green, iterate against the advisory in-VM
scoped check and `make audit/static` (`make spec/gen` first when the catalog changed, since the freshness diff
is part of that gate), then report. PROVE is not a worker step: it is a merge-back gate the host runs, and
`make prove` is required for a rule-change batch before the merge.

That requirement is enforced in the **merge execution path**, mechanically and independently of how the
approval arrived: the extension blocks a merge command in an orchestrator session when the scoped rules lack
VERIFIED prove receipts. VERIFIED is the only verdict that needs nothing more; VACUOUS and BROKEN-TEST are
hard fails and cannot be waived; UNVALIDATABLE is reported and stops for human sign-off.

## Judgment

Everything below is judgment, not policy. Delegate when the work is separable and the context you would burn
is larger than the context you would spend explaining it; do the task yourself when it is one edit, one
lookup, or a decision only you can make. Prefer one foreground worker over a fan-out you cannot verify, and a
fan-out over a long serial chain when the scopes are genuinely disjoint.

Escalate upward, not sideways: a worker reports, an orchestrator coordinates, a human decides. When a gate
fails for a reason the tree cannot fix by itself (a missing capability, an ambiguous requirement, a dirty
integrity check, a red approval stop), stop and say so plainly with the failing command and its output — a
blocked, well-explained report beats a green-looking summary.

Go one level deep. Workers do not delegate and never run in the background; an orchestrator may spawn workers
but not another orchestrator. That limit keeps a failure attributable to a single context and a single set of
receipts.

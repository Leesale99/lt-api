---
# ── Schema ─────────────────────────────────────────────────────────────────
# Schema version of this front matter. Required; this extension supports 1.
version: 1

# ── Gate commands ──────────────────────────────────────────────────────────
# Executed verbatim (whitespace-split argv, no shell) by the extension.
# Host-side only: every authoritative verdict is produced by the host plane,
# AFTER the worker's sandbox has exited. The identical commands exist inside a
# worker VM, but there they are advisory (see "In-VM gates are advisory").
audit_static_cmd: make audit/static
# Full audit gate (DB-backed). The host has LT_API_TEST_DSN; sandboxes never do,
# so this is a host-only, merge-boundary gate.
audit_cmd: make audit
# Behavioral proof gate: the PROVE step, executed at merge-back (host-only).
prove_cmd: make prove
# Scoped spec-check base invocation; --strict is part of the declared policy.
scoped_check_cmd: "go run ./cmd/spec check --strict"

# ── Machine paths ──────────────────────────────────────────────────────────
# Host-only. Gate receipts (host-extension-written, gitignored):
# <receipts_dir>/<date>.jsonl. No worker can write here — a fresh worktree
# carries no receipts dir, and an in-VM extension instance is inert.
receipts_dir: .pi/receipts
# Host-only. Claims logs: <claims_dir>/<job-id>/claims.jsonl.
claims_dir: .pi/jobs

# ── Loop / retry limits ────────────────────────────────────────────────────
# Failed settle-gate attempts before the terminal (hard-stop) turn.
settle_max_attempts: 5
# Attempts that retry with no wait (the batch-1 behavior).
settle_immediate_attempts: 3
# First backoff wait past the immediate window; doubles per attempt.
settle_backoff_base_ms: 2000
# Cap on backoff doublings, so a huge max cannot park a worker for hours.
settle_backoff_max_doublings: 5
# Foreground-worker silence that aborts the child; 0 disables the guard. This
# is also the orchestrator's wall-clock bound on a sandbox.
worker_stall_timeout_ms: 600000
# Cap on tasks[] in one subagent call (the tool may run fewer at once).
max_concurrent_workers: 8

# ── Approval machinery ─────────────────────────────────────────────────────
# These strings are machine-parsed out of an orchestrator's last message.
# jobs.ts VALIDATES this block for equality and refuses on any mismatch: the
# code is the parser, this file is the declared source. Do not "improve" the
# wording of a marker or a token.
# What an approval decision is bound to (the worktree HEAD it was reviewed at).
approval_binding: head-sha
# Exact marker a confirm-handoff orchestrator ends its approval round with.
red_approval_marker: AWAITING_APPROVAL
# Line prefix carrying the reviewed 40-hex worktree HEAD in that block.
red_approval_head_sha_line: "HEAD SHA:"
# Tokens that end a handoff; one of them must be the LAST line of a round.
# AWAITING_APPROVAL is NOT a member of this list: it is a first-class waiting
# state declared by red_approval_marker above, and jobs.ts validates this list
# for equality against HANDOFF_TERMINAL_TOKENS.
handoff_terminal_tokens:
  - HANDOFF_COMPLETE
  - HANDOFF_BLOCKED
---

# Workflow policy

This front matter is the single machine-checked source for how delegated work is gated: the commands that
must pass, the retry/backoff/stall limits, the machine paths, and the approval tokens. The extension reads
it fresh on every gate decision and **refuses to run any gate when this file is missing, unparseable, has an
unknown field, or declares an approval token that disagrees with the code**. If a gate blocks you, fix the
tree or ask the orchestrator to change this file — never work around it.

## The write boundary is the hypervisor and the mount

Every delegated worker runs fully inside a per-task Docker Sandbox micro-VM (`sbx`, v0.54.0), created from a
saved sandbox template. A host-side orchestrator creates the sandbox, mounts the worktree at the same
absolute path, and runs a foreground `sbx exec` of pi with `-e` forwarding (`PI_DELEGATE_CHILD`,
`PI_DELEGATE_ROLE`, `PI_DELEGATE_SPEC_SCOPE`). What a worker can write is therefore decided by what is
mounted, not by a path classifier in the agent: outside the mount there is no filesystem, no host process
table, no SSH agent, and — per profile — no network at all. Egress is default-deny, SSH agent forwarding is
disabled host-wide, and a worker's model API key reaches the VM through the `sbx` secret sentinel mechanism,
never as a handwritten value in a file.

Because the worktree arrives as a mount, `.git` is unresolvable inside it: **workers are git-less**. They
cannot commit, stash, checkout, reset or merge. The host commits, the host merges, and the host owns every
ref.

## Sandbox profiles as code

The profile is the policy. Each role gets exactly this, and nothing else:

| Role | Mounts | Network egress (default-deny) | Secrets |
|---|---|---|---|
| `go-worker`, `general` | worktree, RW, same absolute path | model provider endpoint + `proxy.golang.org`, `sum.golang.org`, `storage.googleapis.com` | model API key via the `sbx` sentinel |
| `scout` | project worktree, read-only | none | none |
| `librarian` | vault `~/Projects/vaults/lt-api` RW + project repo read-only | none | none |
| `researcher` | — (still host-side this batch; profile deferred) | — | — |

No DB: no `LT_API_TEST_DSN` is exported into any sandbox, so `make audit` and `make prove` cannot run there
even if someone types them. A worker that needs a host, a dependency outside its egress allowlist, or the
vault is a blocked, explained report — not a workaround.

## In-VM gates are advisory; the host is the verifier plane

The main session and the background orchestrators stay host-side. The extension also loads inside the VM,
but it sees `PI_DELEGATE_SANDBOX=1` and is **inert**: it registers no gate, issues no verdict, writes no
receipt, and exists only so a worker's own gate runs are labelled advisory. All authoritative verdicts —
`make audit/static`, the scoped spec check, `make prove`, the full `make audit` — are produced on the host
AFTER the worker has exited, and their results are what the approval and merge machinery consumes.

## The integrity check replaced the scanner and the allowlist

Three mechanisms from the previous generation are **deleted**, not disabled:

- the **child-side deny paths** (the edit/write tool's `protected_paths` / `pi_editable_paths`
  classification) — the mount is the boundary now;
- the **bash write-channel scanner** (`bashscan`: redirect/tee/sed-in-place/file-transfer/heredoc/
  git-mutating detection on delegated children) — a shell is Turing-complete, so it was always
  best-effort, and a best-effort scanner beside a real boundary is a false friend;
- the **gate allowlist** (`.pi/delegate/gate-allowlist.json`, the single file-based escape hatch that was
  always self-denied) — nothing left for it to authorize.

Their replacement is one host-side check, run in the worktree BEFORE any gate:

    git diff --exit-code <base> -- Makefile cmd/spec .github WORKFLOW.md

Dirty → the host refuses to run the gates and routes the batch to human review. That is the whole net: the
gate files ARE the definition of green, so a worker that touches them has invalidated its own verdict, and
we stop rather than reason about it. Note the shape of this trade honestly — it is a *detector at the merge
boundary*, not a write barrier inside the VM. A worker can still write a non-gate file in its read-write
mount; the full audit at merge plus the orchestrator's diff review is the backstop for that.

What did NOT change: the main session's edit/write UI confirmation stays. It is host-side, it is the human's,
and no sandbox policy replaces it.

## Receipts and claims are host-only

Receipts (`receipts_dir`) and claims (`claims_dir`) are written by the **host extension instance only**, to
host-side paths, and only for verdicts the host actually produced. A fresh worktree excludes `.pi/receipts/`
and `.pi/jobs/` (both gitignored), so a sandbox never even sees them. A worker citing a receipt is a
category error; a worker's report is a set of claims plus command exit codes, and the orchestrator's report
cites the receipts it caused.

## The worker loop: RED is host-authored, GREEN is the loop, PROVE is at merge-back

RED is written on the host plane BEFORE a task begins — the catalog entry and its `@rule:` scenario are
authored and committed on the delegate branch, and the batch is shown to be red for the right reason there.
The worker never authors RED. Its loop is GREEN-first: implement to green, iterate against the in-VM
advisory scoped check and `make audit/static` (`make spec/gen` first when the catalog changed, since the
freshness diff is part of that gate), then report. PROVE is not a worker step at all: it is a
merge-back gate the host runs, and `make prove` is required for a rule-change batch before the merge.

That requirement is enforced in the **merge execution path**, mechanically and independently of how the
approval arrived: the extension blocks the merge command in an orchestrator session when the scoped rules
lack VERIFIED prove receipts. VERIFIED is the only verdict that needs nothing more; VACUOUS and BROKEN-TEST
are hard fails and cannot be waived; UNVALIDATABLE is reported and stops for human sign-off.

## Honest residuals

These are documented limitations, not hidden ones:

- **The researcher is still host-side.** Its sandbox profile is deferred and not built in this batch, so it
  runs with host reach and host network. Treat its items as the one un-sandboxed role.
- **The librarian has the vault read-write.** The vault is not a git repo under this policy, so no integrity
  check covers it; a librarian mistake in the vault is caught by the human, not by a gate.
- **Hard links can cross out of a mount** on the same filesystem. A link created inside the worktree can
  point at an inode the mount was not meant to expose. Nothing here detects that.
- **The integrity check is the net for poisoned gate files** — and only for the four paths it names
  (`Makefile`, `cmd/spec`, `.github`, `WORKFLOW.md`). A worker can still poison a non-gate file in its
  read-write mount; the full audit at the merge boundary is the backstop, and the orchestrator is expected
  to read the diff rather than trust the exit codes alone.

## Not built yet

- **Gate-VM**: running the authoritative gates inside a second, locked-down VM instead of on the host. Today
  the host is trusted and the gates inherit that trust.
- **Sealed fleet mode**: one mountless sandbox for everything, with no worktree handed to workers at all.
  Deferred by the user for POC use.
- **Researcher sandbox profile**: until it exists, web/doc research stays on the host plane.

Everything below is judgment, not policy. Delegate when the work is separable and the context you would
burn is larger than the context you would spend explaining it; do the task yourself when it is one edit, one
lookup, or a decision only you can make. Prefer one foreground worker over a fan-out you cannot verify, and
prefer a fan-out over a long serial chain when the scopes are genuinely disjoint.

Escalate upward, not sideways: a worker reports, an orchestrator coordinates, a human decides. When a gate
fails for a reason the tree cannot fix by itself (a missing capability, an ambiguous requirement, an
integrity check that came back dirty, a red approval stop), stop and say so plainly with the failing command
and its output — a blocked, well-explained report beats a green-looking summary.

Go one level deep. Workers do not delegate and never run in the background; an orchestrator may spawn
workers but not another orchestrator. That limit is what keeps a failure attributable to a single context
and a single set of receipts.

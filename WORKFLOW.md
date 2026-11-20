---
# ── Schema ─────────────────────────────────────────────────────────────────
# Schema version of this front matter. Required; this extension supports 1.
version: 1

# ── Gate commands ──────────────────────────────────────────────────────────
# Executed verbatim (whitespace-split argv, no shell) by the extension.
# Cheap DB-free ladder: the post-write gate and the settle gate both run it.
audit_static_cmd: make audit/static
# Full audit gate (DB-backed). Declared policy for the rule-change protocol;
# worker envs have no test DSN, so the extension runs only audit_static_cmd.
audit_cmd: make audit
# Behavioral proof gate: the PROVE step of the rule-change protocol.
prove_cmd: make prove
# Scoped spec-check base invocation; --strict is part of the declared policy.
scoped_check_cmd: "go run ./cmd/spec check --strict"

# ── Protected paths (repo-root-relative globs) ─────────────────────────────
# DENIED to delegated workers. The escape hatch is allowlist_path below.
protected_paths:
  - Makefile
  - cmd/spec
  - cmd/spec/**
  - docs/spec/**
  - .pi/**
# The only paths inside the protected set a worker may still edit: the prose
# the orchestrator rewrites (agents, prompts, delegate config, plans).
pi_editable_paths:
  - .pi/agents/**
  - .pi/prompts/**
  - .pi/delegate/**
  - .pi/plans/**

# ── Bash write-channel scanner ─────────────────────────────────────────────
# Which pattern classes the pre-execution bash scanner enforces for delegated
# children. Every class is a deterministic detector over the command word and
# write operators; a command that would write a protected path (or whose write
# target cannot be resolved to ONE literal path) is denied before it runs.
# Only the classes listed here are scanned — a class omitted from this list is
# not scanned, which is the documented escape (a human policy choice).
# An UNKNOWN item refuses the policy (`unknown bash_write_patterns item ...`),
# so a typo can never silently switch a class of write channel off.
bash_write_patterns:
  # `>`, `>>`, `<>`, `2>`/`1>`, `&>`, `>>&`, `2>>`, `&>>`, `>|` whose target is a
  # real file; fd duplicates (`2>&1`) and /dev/null|stdout|stderr|tty are skipped.
  - redirect
  # `tee` / `tee -a`: every non-option argument is written.
  - tee
  # `sed -i`, `-i.suffix`, `--in-place`, `--in-place=suffix`: the trailing operand
  # files (after the script expression) are written in place.
  - sed-in-place
  # `cp`, `mv`, `install` destinations (incl. -t/--target-directory and install -d)
  # and `dd of=`: the destination path is written, even if it is a directory.
  - file-transfer
  # heredoc payloads: the body is DATA (never scanned as commands), but a heredoc
  # fed into a shell (`| sh`, `bash <<EOF`) or eval-like consumer is ambiguous.
  - heredoc
  # `git checkout|restore|rm|mv` pathspecs, `git stash push|save --` pathspecs,
  # and `git apply|am|clean|reset --hard` (their targets are not knowable ⇒ deny).
  - git-mutating

# ── Machine paths ──────────────────────────────────────────────────────────
# Single file-based escape hatch from protected_paths. ALWAYS self-denied, so
# a worker can never authorize itself. WORKFLOW.md is self-denied too (a code
# invariant, not this list): a worker may not rewrite the gates that bind it.
allowlist_path: .pi/delegate/gate-allowlist.json
# Gate receipts (extension-written, gitignored): <receipts_dir>/<date>.jsonl.
receipts_dir: .pi/receipts
# Claims logs: <claims_dir>/<job-id>/claims.jsonl.
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
# Foreground-worker silence that aborts the child; 0 disables the guard.
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
handoff_terminal_tokens:
  - HANDOFF_COMPLETE
  - HANDOFF_BLOCKED
---

# Workflow policy

This front matter is the single machine-checked source for how delegated work is gated: the commands that
must pass, the paths workers may not touch, the retry/backoff/stall limits, and the approval tokens. The
extension reads it fresh on every gate decision and **refuses to run any gate when this file is missing,
unparseable, has an unknown field, or declares an approval token that disagrees with the code**. If a gate
blocks you, fix the tree or ask the orchestrator to change this file — never work around it.

Everything below is judgment, not policy. Delegate when the work is separable and the context you would
burn is larger than the context you would spend explaining it; do the task yourself when it is one edit, one
lookup, or a decision only you can make. Prefer one foreground worker over a fan-out you cannot verify, and
prefer a fan-out over a long serial chain when the scopes are genuinely disjoint.

Escalate upward, not sideways: a worker reports, an orchestrator coordinates, a human decides. When a gate
fails for a reason the tree cannot fix by itself (a missing capability, an ambiguous requirement, a red
approval stop), stop and say so plainly with the failing command and its output — a blocked, well-explained
report beats a green-looking summary.

Go one level deep. Workers do not delegate and never run in the background; an orchestrator may spawn
workers but not another orchestrator. That limit is what keeps a failure attributable to a single context
and a single set of receipts.

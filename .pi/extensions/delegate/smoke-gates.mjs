// Standalone smoke test for .pi/extensions/delegate/gates.ts.
//
// Imports the REAL module (node's built-in type stripping runs the .ts file
// directly — no pi, no build step) and exercises it against temp dirs. This is
// a harness for headless verification only: nothing here is imported by the
// extension. Run:  node .pi/extensions/delegate/smoke-gates.mjs
// (needs Node >= 22.18 for unflagged .ts type stripping).
import * as assert from "node:assert/strict";
import { execFileSync, spawn } from "node:child_process";
import * as fs from "node:fs";
import { registerHooks } from "node:module";
import * as os from "node:os";
import * as path from "node:path";
import { pathToFileURL } from "node:url";
import {
  SETTLE_BACKOFF_BASE_MS,
  SETTLE_GATE_MAX_ATTEMPTS,
  bumpSettleGateAttempts,
  describeWaitMs,
  diffReceiptAgainst,
  evaluateGateFileWrite,
  formatGateReport,
  formatSettleEscalation,
  formatSettleTrail,
  formatSpecScope,
  isGateRelevantWrite,
  latestPassingReceipt,
  matchGlob,
  parseSpecScope,
  parseSpecScopeEnv,
  planSettleGateAttempt,
  receiptsFileFor,
  receiptCommandsOf,
  recentSettleFailures,
  reconciliationGateRun,
  recordSettleGateAttempt,
  resetSettleGateAttempts,
  runGate,
  runReconciliation,
  runScopedGate,
  settleBackoffBaseMs,
  settleBackoffMsFor,
  settleGateMaxAttempts,
  settleGateStateFor,
  settleImmediateAttempts,
  shouldRunPostWriteGate,
  splitCommandLine,
  markPostWriteGateEnd,
  markPostWriteGateStart,
  writeReceipt,
} from "./gates.ts";
import {
  WORKER_STALL_TIMEOUT_MS,
  formatStallReason,
  stallDecision,
  startStallWatchdog,
  workerStallTimeoutMs,
} from "./runtime.ts";
import {
  DEFAULT_AUDIT_STATIC_CMD,
  DEFAULT_MAX_CONCURRENT_WORKERS,
  DEFAULT_PROTECTED_PATHS,
  DEFAULT_SETTLE_BACKOFF_BASE_MS,
  DEFAULT_SETTLE_MAX_ATTEMPTS,
  DEFAULT_WORKER_STALL_TIMEOUT_MS,
  loadWorkflowPolicy,
  parseWorkflowFrontMatter,
  workflowPolicyRefusal,
} from "./policy.ts";
import {
  activeClaims,
  buildSiblingContext,
  changedPathsFromPorcelain,
  claimCandidateOf,
  claimsLogPath,
  completeSpawnClaim,
  formatClaimConflict,
  globsIntersect,
  parseClaimsPaths,
  prepareSpawnClaims,
  readClaims,
  siblingContextFor,
} from "./claims.ts";

let failures = 0;
let checks = 0;
async function check(name, fn) {
  checks += 1;
  try {
    await fn();
    console.log("PASS " + name);
  } catch (error) {
    failures += 1;
    console.log("FAIL " + name + " — " + (error instanceof Error ? error.message : String(error)));
  }
}

const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "gates-smoke-"));

// ── WORKFLOW.md fixtures ───────────────────────────────────────────────────
// Batch 3: gate paths load the repo-root policy and REFUSE when it is absent.
// Every fixture repo that runs a gate needs one; the minimal form (version
// only) exercises the omitted-field defaults.
const MINIMAL_POLICY = "---\nversion: 1\n---\n\n# Workflow policy\n";

function writeWorkflow(dir, body = MINIMAL_POLICY) {
  fs.mkdirSync(dir, { recursive: true });
  const file = path.join(dir, "WORKFLOW.md");
  fs.writeFileSync(file, body);
  return file;
}

/** A directory with NO WORKFLOW.md: env knobs stay the fallback there. */
const policyFree = path.join(tmp, "policy-free");
fs.mkdirSync(policyFree, { recursive: true });


// ── runGate: fail-closed contract ───────────────────────────────────────────
await check("runGate: exit 0 is ok", async () => {
  const r = await runGate("sh", ["-c", "echo hi"], { cwd: tmp, timeoutMs: 5000 });
  assert.equal(r.ok, true);
  assert.equal(r.exitCode, 0);
  assert.match(r.stdout, /hi/);
});

await check("runGate: nonzero exit is not ok", async () => {
  const r = await runGate("sh", ["-c", "echo boom >&2; exit 3"], { cwd: tmp, timeoutMs: 5000 });
  assert.equal(r.ok, false);
  assert.equal(r.exitCode, 3);
  assert.match(r.stderr, /boom/);
  assert.match(r.reason, /exited with code 3/);
});

await check("runGate: missing binary (ENOENT) is not ok", async () => {
  const r = await runGate("definitely-not-a-binary-xyz", [], { cwd: tmp, timeoutMs: 5000 });
  assert.equal(r.ok, false);
  assert.equal(r.exitCode, null);
  assert.ok(r.spawnError, "spawnError recorded");
});

await check("runGate: non-runnable file is not ok", async () => {
  const file = path.join(tmp, "not-runnable.txt");
  fs.writeFileSync(file, "nope\n", { mode: 0o644 });
  const r = await runGate(file, [], { cwd: tmp, timeoutMs: 5000 });
  assert.equal(r.ok, false);
  assert.ok(r.spawnError || r.exitCode !== 0, "non-runnable reported as failure");
});

await check("runGate: timeout is not ok and kills the child", async () => {
  const started = Date.now();
  const r = await runGate("sh", ["-c", "sleep 5"], { cwd: tmp, timeoutMs: 300 });
  assert.equal(r.ok, false);
  assert.equal(r.timedOut, true);
  assert.match(r.reason, /timed out/);
  assert.ok(Date.now() - started < 4000, "did not wait for the child");
});

await check("runGate: missing cwd is not ok", async () => {
  const r = await runGate("sh", ["-c", "exit 0"], { cwd: path.join(tmp, "nope"), timeoutMs: 5000 });
  assert.equal(r.ok, false);
});

// ── glob matcher ────────────────────────────────────────────────────────────
await check("matchGlob: ** and * semantics", async () => {
  assert.equal(matchGlob("cmd/spec/**", "cmd/spec/a/b.go"), true);
  assert.equal(matchGlob("cmd/spec/**", "cmd/spec"), false);
  assert.equal(matchGlob("Makefile", "Makefile"), true);
  assert.equal(matchGlob("*.json", "a/b.json"), false);
  assert.equal(matchGlob(".pi/*/x.json", ".pi/delegate/x.json"), true);
  assert.equal(matchGlob(".pi/*/x.json", ".pi/a/b/x.json"), false);
  assert.equal(matchGlob("**/rules.schema.md", "docs/spec/rules.schema.md"), true);
  assert.equal(matchGlob("docs/spec/rules.schema.md", "docs/spec/rules.schema.md"), true);
  assert.equal(matchGlob("a+b/**", "a+b/c"), true, "regex metachars are literal");
});

// ── SPEC-SCOPE parsing ──────────────────────────────────────────────────────
await check("parseSpecScope: module + rules line", async () => {
  const scope = parseSpecScope("Do the thing.\nSPEC-SCOPE: module=game rules=R-GAME-001,R-GAME-002\nmore");
  assert.deepEqual(scope, { module: "game", rules: ["R-GAME-001", "R-GAME-002"] });
  assert.equal(formatSpecScope(scope), "module=game rules=R-GAME-001,R-GAME-002");
});

await check("parseSpecScope: rules-only / module-only / absent", async () => {
  assert.deepEqual(parseSpecScope("spec-scope: rules=R-A-1"), { module: undefined, rules: ["R-A-1"] });
  assert.deepEqual(parseSpecScope("SPEC-SCOPE: module=identity"), { module: "identity", rules: [] });
  assert.equal(parseSpecScope("no scope line here"), null);
  assert.equal(parseSpecScope("SPEC-SCOPE: (nothing usable)"), null);
});

await check("parseSpecScopeEnv: env round-trip", async () => {
  assert.deepEqual(parseSpecScopeEnv("module=game rules=R-1"), { module: "game", rules: ["R-1"] });
  assert.equal(parseSpecScopeEnv(""), null);
  assert.equal(parseSpecScopeEnv(undefined), null);
});

// ── gate-file protection ───────────────────────────────────────────────────
await check("evaluateGateFileWrite: deny list + exceptions + fail-closed allowlist", async () => {
  const repo = path.join(tmp, "repo");
  fs.mkdirSync(path.join(repo, ".pi", "delegate"), { recursive: true });
  fs.mkdirSync(path.join(repo, "docs", "spec"), { recursive: true });
  fs.mkdirSync(path.join(repo, "cmd", "spec"), { recursive: true });
  fs.writeFileSync(path.join(repo, "Makefile"), "all:\n");
  fs.writeFileSync(path.join(repo, "docs", "spec", "rules.schema.md"), "# schema\n");
  fs.writeFileSync(path.join(repo, "main.go"), "package main\n");
  writeWorkflow(repo);

  // Ordinary files stay editable.
  assert.equal(evaluateGateFileWrite(repo, "main.go").blocked, false);
  assert.equal(evaluateGateFileWrite(repo, "internal/x/y.go").blocked, false);
  // Gate files denied.
  assert.equal(evaluateGateFileWrite(repo, "Makefile").blocked, true);
  assert.equal(evaluateGateFileWrite(repo, "cmd/spec/main.go").blocked, true);
  assert.equal(evaluateGateFileWrite(repo, "docs/spec/rules.schema.md").blocked, true);
  assert.equal(evaluateGateFileWrite(repo, "docs/spec/rules.yaml").blocked, true);
  assert.equal(evaluateGateFileWrite(repo, ".pi/extensions/delegate/index.ts").blocked, true);
  // Prose exceptions.
  assert.equal(evaluateGateFileWrite(repo, ".pi/agents/go-worker.md").blocked, false);
  assert.equal(evaluateGateFileWrite(repo, ".pi/prompts/x.md").blocked, false);
  assert.equal(evaluateGateFileWrite(repo, ".pi/plans/plan.md").blocked, false);
  assert.equal(evaluateGateFileWrite(repo, ".pi/delegate/orchestrator.md").blocked, false);
  // Allowlist file itself is always denied, even with a matching allowlist.
  fs.writeFileSync(path.join(repo, ".pi", "delegate", "gate-allowlist.json"), '["**"]');
  assert.equal(evaluateGateFileWrite(repo, ".pi/delegate/gate-allowlist.json").blocked, true);
  assert.equal(evaluateGateFileWrite(repo, "Makefile").blocked, false, "allowlist grants Makefile");
  // Invalid JSON ⇒ fail closed (deny).
  fs.writeFileSync(path.join(repo, ".pi", "delegate", "gate-allowlist.json"), "{not json");
  assert.equal(evaluateGateFileWrite(repo, "cmd/spec/main.go").blocked, true);
  // Missing file ⇒ deny.
  fs.rmSync(path.join(repo, ".pi", "delegate", "gate-allowlist.json"));
  assert.equal(evaluateGateFileWrite(repo, "cmd/spec/main.go").blocked, true);
  // Outside the repo is not a gate file.
  assert.equal(evaluateGateFileWrite(repo, "../elsewhere.go").blocked, false);
});

await check("isGateRelevantWrite", async () => {
  const repo = path.join(tmp, "repo2");
  assert.equal(isGateRelevantWrite(repo, "internal/a/b.go"), true);
  assert.equal(isGateRelevantWrite(repo, "docs/spec/features/x.feature"), true);
  assert.equal(isGateRelevantWrite(repo, "config.yaml"), true);
  assert.equal(isGateRelevantWrite(repo, "docs/spec/README.md"), true);
  assert.equal(isGateRelevantWrite(repo, "docs/spec"), false);
  assert.equal(isGateRelevantWrite(repo, "README.md"), false);
});

// ── receipts ───────────────────────────────────────────────────────────────
await check("writeReceipt: JSONL line with sha/hash fields", async () => {
  const repo = path.join(tmp, "receipt-repo");
  fs.mkdirSync(path.join(repo, "docs", "spec"), { recursive: true });
  fs.writeFileSync(path.join(repo, "docs", "spec", "rules.yaml"), "rules: []\n");
  const file = await writeReceipt({
    cwd: repo,
    agent: "worker",
    session: "s-1",
    commands: [{ command: "make audit/static", exitCode: 1, timedOut: false, durationMs: 12 }],
    ok: false,
    note: "smoke",
  });
  assert.ok(file, "receipt path returned");
  const lines = fs.readFileSync(file, "utf8").trim().split("\n");
  assert.equal(lines.length, 1);
  const record = JSON.parse(lines[0]);
  assert.equal(record.agent, "worker");
  assert.equal(record.session, "s-1");
  assert.equal(record.commands[0].exitCode, 1);
  assert.match(record.rulesHash, /^[0-9a-f]{64}$/);
  assert.equal(typeof record.headSha, "string"); // no git repo ⇒ "" (fail-open)
  await writeReceipt({ cwd: repo, agent: "worker", commands: [] });
  assert.equal(fs.readFileSync(file, "utf8").trim().split("\n").length, 2, "appends");
});

// ── debounce ───────────────────────────────────────────────────────────────
await check("shouldRunPostWriteGate: 10s window", async () => {
  const key = "smoke-" + String(Date.now());
  assert.equal(shouldRunPostWriteGate(key, 1_000), "run");
  markPostWriteGateStart(key, 1_000);
  assert.equal(shouldRunPostWriteGate(key, 2_000), "skip-inflight");
  markPostWriteGateEnd(key);
  assert.equal(shouldRunPostWriteGate(key, 5_000), "skip-window");
  assert.equal(shouldRunPostWriteGate(key, 1_000 + 10_001), "run");
});

// ── scoped gate: real commands in an empty dir (fail closed) ───────────────
await check("runScopedGate: missing Makefile targets fail closed", async () => {
  const empty = path.join(tmp, "empty-repo");
  fs.mkdirSync(empty, { recursive: true });
  writeWorkflow(empty);
  const run = await runScopedGate({
    cwd: empty,
    scopeEnv: "module=game",
    makeTimeoutMs: 20_000,
    specTimeoutMs: 20_000,
  });
  assert.equal(run.ok, false, "no Makefile/go module ⇒ failure");
  const commands = run.steps.map((s) => s.command);
  assert.ok(commands.includes("make audit/static"), "make audit/static attempted: " + commands.join(" | "));
  assert.ok(commands.some((c) => c.includes("--module game")), "scoped check attempted");
  assert.ok(commands[commands.length - 1].startsWith("go run ./cmd/spec check --strict"), "full fallback ran last");
  const report = formatGateReport(run, { header: "── SCOPED GATE FAILED ──", footer: "fix" });
  assert.match(report, /SCOPED GATE FAILED/);
});

await check("runScopedGate: unset scope ⇒ full check only", async () => {
  writeWorkflow(tmp);
  const run = await runScopedGate({ cwd: tmp, makeTimeoutMs: 10_000, specTimeoutMs: 10_000 });
  assert.equal(run.steps.length, 2);
  assert.equal(run.steps[1].command, "go run ./cmd/spec check --strict");
});

// ── Batch 2: settle-gate backoff past the cap ───────────────────────────────

function fakeFailedRun(command = "make audit/static", exitCode = 1) {
  return {
    ok: false,
    steps: [
      {
        command,
        argv: [command],
        ok: false,
        exitCode,
        timedOut: false,
        durationMs: 5,
        stdout: "",
        stderr: "boom",
        spawnError: null,
        reason: "exited with code " + String(exitCode),
      },
    ],
  };
}

await check("planSettleGateAttempt: 3 immediate, then 2s/4s backoff, terminal at 5", async () => {
  assert.equal(SETTLE_GATE_MAX_ATTEMPTS, 5, "total attempts raised from 3 to 5");
  assert.equal(SETTLE_BACKOFF_BASE_MS, 2000, "backoff base is 2s");
  for (let attempt = 1; attempt <= 3; attempt++) {
    const plan = planSettleGateAttempt(attempt, { maxAttempts: 5, baseMs: 2000 });
    assert.equal(plan.waitMs, 0, "attempt " + String(attempt) + " retries immediately");
    assert.equal(plan.terminal, false);
    assert.equal(plan.kind, "immediate");
    assert.equal(settleBackoffMsFor(attempt, 2000), 0);
  }
  const fourth = planSettleGateAttempt(4, { maxAttempts: 5, baseMs: 2000 });
  assert.equal(fourth.waitMs, 2000, "attempt 4 waits 2s");
  assert.equal(fourth.terminal, false);
  assert.equal(fourth.kind, "backoff");
  const fifth = planSettleGateAttempt(5, { maxAttempts: 5, baseMs: 2000 });
  assert.equal(fifth.waitMs, 4000, "attempt 5 waits 4s (doubling)");
  assert.equal(fifth.terminal, true, "attempt 5 is the hard stop");
  assert.equal(fifth.kind, "terminal");
  // A tiny base keeps the same shape (test seam for real sleeps).
  assert.equal(planSettleGateAttempt(4, { maxAttempts: 5, baseMs: 20 }).waitMs, 20);
  assert.equal(describeWaitMs(20), "20ms");
  assert.equal(describeWaitMs(2000), "2s");
  assert.equal(describeWaitMs(0), "none");
});

await check("settle knobs: env overrides clamp like pollCooldownMs", async () => {
  const previousMax = process.env.PI_DELEGATE_SETTLE_MAX_ATTEMPTS;
  const previousBase = process.env.PI_DELEGATE_BACKOFF_BASE_MS;
  try {
    delete process.env.PI_DELEGATE_SETTLE_MAX_ATTEMPTS;
    delete process.env.PI_DELEGATE_BACKOFF_BASE_MS;
    assert.equal(settleGateMaxAttempts(policyFree), 5);
    assert.equal(settleBackoffBaseMs(policyFree), 2000);
    process.env.PI_DELEGATE_SETTLE_MAX_ATTEMPTS = "2";
    assert.equal(settleGateMaxAttempts(policyFree), 5, "below the immediate window clamps to the default");
    process.env.PI_DELEGATE_SETTLE_MAX_ATTEMPTS = "7";
    assert.equal(settleGateMaxAttempts(policyFree), 7);
    process.env.PI_DELEGATE_SETTLE_MAX_ATTEMPTS = "garbage";
    assert.equal(settleGateMaxAttempts(policyFree), 5);
    process.env.PI_DELEGATE_BACKOFF_BASE_MS = "20";
    assert.equal(settleBackoffBaseMs(policyFree), 20);
    process.env.PI_DELEGATE_BACKOFF_BASE_MS = "-1";
    assert.equal(settleBackoffBaseMs(policyFree), 2000, "negative base falls back to the default");
  } finally {
    if (previousMax === undefined) delete process.env.PI_DELEGATE_SETTLE_MAX_ATTEMPTS;
    else process.env.PI_DELEGATE_SETTLE_MAX_ATTEMPTS = previousMax;
    if (previousBase === undefined) delete process.env.PI_DELEGATE_BACKOFF_BASE_MS;
    else process.env.PI_DELEGATE_BACKOFF_BASE_MS = previousBase;
  }
});

await check("formatSettleEscalation: text escalates per attempt, terminal names HARD STOP", async () => {
  resetSettleGateAttempts();
  bumpSettleGateAttempts();
  recordSettleGateAttempt({ run: fakeFailedRun(), receiptPath: "/tmp/r-1.jsonl" });
  const immediate = formatSettleEscalation(planSettleGateAttempt(1, { maxAttempts: 5, baseMs: 2000 }));
  assert.match(immediate, /^attempt 1\/5/);
  assert.match(immediate, /immediate retry/);
  assert.match(immediate, /previous failures: attempt 1: make audit\/static/);

  const backoff = formatSettleEscalation(planSettleGateAttempt(4, { maxAttempts: 5, baseMs: 2000 }), [
    "attempt 1: make audit/static \u2192 exited with code 1",
  ]);
  assert.match(backoff, /attempt 4\/5/);
  assert.match(backoff, /retrying after 2s backoff/);
  assert.match(backoff, /previous failures: attempt 1:/);
  assert.notEqual(backoff, immediate, "escalation text differs between attempts");

  const terminal = formatSettleEscalation(planSettleGateAttempt(5, { maxAttempts: 5, baseMs: 2000 }), []);
  assert.match(terminal, /attempt 5\/5/);
  assert.match(terminal, /retrying after 4s backoff, then HARD STOP/);
  resetSettleGateAttempts();
});

await check("settle attempt state: 5 failures exhaust into a full receipt trail", async () => {
  resetSettleGateAttempts();
  for (let attempt = 1; attempt <= 5; attempt++) {
    const plan = planSettleGateAttempt(attempt, { maxAttempts: 5, baseMs: 20 });
    bumpSettleGateAttempts();
    const record = recordSettleGateAttempt({
      run: fakeFailedRun("make audit/static", attempt),
      receiptPath: "/tmp/receipt-" + String(attempt) + ".jsonl",
    });
    assert.equal(record.attempt, attempt, "recorded attempt number matches");
    assert.equal(plan.terminal, attempt === 5, "only the last attempt is terminal");
  }
  const state = settleGateStateFor();
  assert.equal(state.attempts, 5);
  assert.equal(state.history.length, 5, "every attempt lands in the trail");
  assert.equal(state.lastAttemptAt > 0, true, "lastAttemptAt recorded");
  const trail = formatSettleTrail(state);
  assert.match(trail, /Settle gate trail/);
  for (let attempt = 1; attempt <= 5; attempt++) {
    assert.match(trail, new RegExp("attempt " + String(attempt) + " .*receipt: /tmp/receipt-" + String(attempt) + "\\.jsonl"));
  }
  assert.match(trail, /exit 1/);
  assert.equal(recentSettleFailures().length, 3, "previous-failures tail is capped");
  assert.match(recentSettleFailures()[2], /attempt 5:/);
  resetSettleGateAttempts();
  assert.equal(settleGateStateFor().attempts, 0, "a passing gate resets the state");
  assert.equal(formatSettleTrail(), "Settle gate trail: (no failed attempt recorded)");
});

// ── Batch 2: worker stall timeout ───────────────────────────────────────────

await check("stallDecision: pure threshold cases (0 disables)", async () => {
  assert.equal(WORKER_STALL_TIMEOUT_MS, 600000, "default is 10 minutes");
  assert.deepEqual(stallDecision(1_000, 2_000, 600_000), { stalled: false, idleMs: 1_000 });
  assert.deepEqual(stallDecision(1_000, 601_001, 600_000), { stalled: true, idleMs: 600_001 });
  assert.deepEqual(stallDecision(1_000, 601_000, 600_000), { stalled: false, idleMs: 600_000 }, "strictly greater");
  assert.equal(stallDecision(1_000, 999_999_999, 0), null, "0 disables");
  assert.equal(stallDecision(1_000, 2_000, -5), null);
  assert.equal(stallDecision(Number.NaN, 2_000, 600_000), null, "bad clock never aborts");
  const previous = process.env.PI_DELEGATE_WORKER_STALL_TIMEOUT_MS;
  try {
    delete process.env.PI_DELEGATE_WORKER_STALL_TIMEOUT_MS;
    assert.equal(workerStallTimeoutMs(process.env, policyFree), 600_000);
    process.env.PI_DELEGATE_WORKER_STALL_TIMEOUT_MS = "0";
    assert.equal(workerStallTimeoutMs(process.env, policyFree), 0);
    process.env.PI_DELEGATE_WORKER_STALL_TIMEOUT_MS = "1500";
    assert.equal(workerStallTimeoutMs(process.env, policyFree), 1500);
    process.env.PI_DELEGATE_WORKER_STALL_TIMEOUT_MS = "nope";
    assert.equal(workerStallTimeoutMs(process.env, policyFree), 600_000);
  } finally {
    if (previous === undefined) delete process.env.PI_DELEGATE_WORKER_STALL_TIMEOUT_MS;
    else process.env.PI_DELEGATE_WORKER_STALL_TIMEOUT_MS = previous;
  }
  assert.equal(
    formatStallReason(600_000, 600_000),
    "stalled: no child activity for 10min (timeout 600s)",
  );
});

await check("startStallWatchdog: injected clock fires once, disabled stays inert", async () => {
  let fired = 0;
  let decision = null;
  const watchdog = startStallWatchdog({
    timeoutMs: 1_000,
    getLastActivityMs: () => 0,
    onStall: (value) => {
      fired += 1;
      decision = value;
    },
    now: () => 5_000,
  });
  try {
    assert.equal(watchdog.check(), true);
    assert.equal(watchdog.check(), false, "fires at most once");
    assert.equal(fired, 1);
    assert.deepEqual(decision, { stalled: true, idleMs: 5_000 });
  } finally {
    watchdog.stop();
  }
  let disabledFired = false;
  const disabled = startStallWatchdog({
    timeoutMs: 0,
    getLastActivityMs: () => 0,
    onStall: () => {
      disabledFired = true;
    },
    now: () => 10_000_000,
  });
  assert.equal(disabled.check(), false);
  assert.equal(disabledFired, false, "0 disables the guard");
  disabled.stop();
  disabled.stop(); // idempotent
});

await check("stall watchdog: fires on a real silent process, spared by activity", async () => {
  // Real process, real clock: `sleep` emits nothing → the watchdog must fire.
  const silent = spawn("sh", ["-c", "sleep 5"], { stdio: ["ignore", "pipe", "pipe"] });
  const silentActivity = Date.now();
  try {
    const stalled = await new Promise((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error("watchdog never fired")), 4000);
      const watchdog = startStallWatchdog({
        timeoutMs: 200,
        getLastActivityMs: () => silentActivity,
        onStall: (value) => {
          clearTimeout(timer);
          watchdog.stop();
          resolve(value);
        },
        tickMs: 25,
      });
    });
    assert.equal(stalled.stalled, true);
    assert.equal(stalled.idleMs >= 200, true, "idle exceeds the timeout");
    assert.match(formatStallReason(stalled.idleMs, 200), /^stalled: no child activity for .*\(timeout 0s\)$/);
  } finally {
    silent.kill("SIGKILL");
  }

  // A process that keeps writing keeps the guard quiet.
  const chatty = spawn("sh", ["-c", "for i in 1 2 3 4 5 6 7 8; do echo x; sleep 0.05; done"], {
    stdio: ["ignore", "pipe", "pipe"],
  });
  let chattyActivity = Date.now();
  chatty.stdout?.on("data", () => {
    chattyActivity = Date.now();
  });
  let chattyStalled = false;
  const chattyWatchdog = startStallWatchdog({
    timeoutMs: 300,
    getLastActivityMs: () => chattyActivity,
    onStall: () => {
      chattyStalled = true;
    },
    tickMs: 25,
  });
  try {
    await new Promise((resolve) => setTimeout(resolve, 500));
    assert.equal(chattyStalled, false, "activity prevents the stall");
  } finally {
    chattyWatchdog.stop();
    chatty.kill("SIGKILL");
  }
});

// ── Batch 2: claims log ─────────────────────────────────────────────────────

function gitExec(cwd, args) {
  return execFileSync("git", args, { cwd, encoding: "utf8" }).trim();
}

function makeGitRepo(dir) {
  fs.mkdirSync(dir, { recursive: true });
  gitExec(dir, ["init", "-q"]);
  gitExec(dir, ["config", "user.email", "smoke@example.com"]);
  gitExec(dir, ["config", "user.name", "smoke"]);
  gitExec(dir, ["config", "commit.gpgsign", "false"]);
  fs.writeFileSync(path.join(dir, ".gitignore"), ".pi/\n");
  fs.mkdirSync(path.join(dir, "internal", "game"), { recursive: true });
  fs.mkdirSync(path.join(dir, "internal", "identity"), { recursive: true });
  fs.writeFileSync(path.join(dir, "internal", "game", "state.go"), "package game\n");
  fs.writeFileSync(path.join(dir, "internal", "identity", "id.go"), "package identity\n");
  gitExec(dir, ["add", "-A"]);
  gitExec(dir, ["commit", "-q", "-m", "init"]);
  return dir;
}

await check("claims: CLAIMS-PATHS parsing + glob intersection", async () => {
  assert.deepEqual(parseClaimsPaths("do it\nCLAIMS-PATHS: internal/game/**, docs/spec/x.yaml\nmore"), [
    "internal/game/**",
    "docs/spec/x.yaml",
  ]);
  assert.deepEqual(parseClaimsPaths("claims-paths: ./internal/a/ , internal/b"), ["internal/a", "internal/b"]);
  assert.deepEqual(parseClaimsPaths("no claims line"), []);
  const candidate = claimCandidateOf("w1", "SPEC-SCOPE: module=game rules=R-1\nCLAIMS-PATHS: internal/game/**");
  assert.deepEqual(candidate, {
    worker: "w1",
    ruleIds: ["R-1"],
    module: "game",
    ownedPaths: ["internal/game/**"],
  });
  assert.equal(globsIntersect("internal/game/**", "internal/game/state.go"), true);
  assert.equal(globsIntersect("internal/*/state.go", "internal/game/state.go"), true);
  assert.equal(globsIntersect("internal/game", "internal/game/**"), true);
  assert.equal(globsIntersect("internal/a/**", "internal/b/**"), false);
  assert.equal(globsIntersect("internal/a/**", "internal/ab/**"), false, "segment-aware, not string-prefix");
});

await check("claims: overlap blocks ruleIds/module/paths, non-overlap passes", async () => {
  const repo = path.join(tmp, "claims-block");
  fs.mkdirSync(repo, { recursive: true });
  const jobId = "job-block-1";
  const sessionId = "s-block";

  const first = await prepareSpawnClaims({
    cwd: repo,
    jobId,
    sessionId,
    requests: [{ worker: "w1", task: "SPEC-SCOPE: module=game rules=R-GAME-001\nCLAIMS-PATHS: internal/game/**" }],
  });
  assert.equal(first.blocked.length, 0);
  assert.equal(first.claims.length, 1);
  assert.equal(first.logPath, claimsLogPath(repo, jobId));
  assert.equal(fs.existsSync(first.logPath), true, "claims.jsonl written under .pi/jobs/<id>/");
  const written = await readClaims(repo, jobId);
  assert.equal(written.length, 1);
  assert.equal(written[0].type, "claim");
  assert.deepEqual(written[0].baseline, [], "no git repo ⇒ empty baseline (fail open)");

  // ruleIds overlap.
  const rules = await prepareSpawnClaims({
    cwd: repo,
    jobId,
    sessionId,
    requests: [{ worker: "w2", task: "SPEC-SCOPE: rules=R-GAME-001" }],
  });
  assert.equal(rules.blocked.length, 1);
  assert.equal(rules.claims.length, 0, "a blocked call writes nothing");
  assert.match(rules.blocked[0].reasons.join(" "), /ruleIds R-GAME-001/);
  const blockText = formatClaimConflict(rules.blocked[0]);
  assert.match(blockText, /SPAWN BLOCKED/);
  assert.match(blockText, /worker: w1/);
  assert.match(blockText, /PI_DELEGATE_CLAIMS_OVERRIDE=1/);
  assert.equal((await readClaims(repo, jobId)).length, 1, "no claim appended on block");

  // module overlap (no shared rule, paths distinct).
  const moduleBlock = await prepareSpawnClaims({
    cwd: repo,
    jobId,
    sessionId,
    requests: [{ worker: "w3", task: "SPEC-SCOPE: module=game rules=R-OTHER" }],
  });
  assert.equal(moduleBlock.blocked.length, 1);
  assert.match(moduleBlock.blocked[0].reasons.join(" "), /module game/);

  // path overlap.
  const pathBlock = await prepareSpawnClaims({
    cwd: repo,
    jobId,
    sessionId,
    requests: [{ worker: "w4", task: "SPEC-SCOPE: rules=R-OTHER-2\nCLAIMS-PATHS: internal/game/state.go" }],
  });
  assert.equal(pathBlock.blocked.length, 1);
  assert.match(pathBlock.blocked[0].reasons.join(" "), /paths internal\/game\/state.go/);

  // Non-overlapping spawn passes.
  const disjoint = await prepareSpawnClaims({
    cwd: repo,
    jobId,
    sessionId,
    requests: [{ worker: "w5", task: "SPEC-SCOPE: module=identity rules=R-ID-1\nCLAIMS-PATHS: internal/identity/**" }],
  });
  assert.equal(disjoint.blocked.length, 0);
  assert.equal(disjoint.claims.length, 1);
  assert.equal(activeClaims(await readClaims(repo, jobId)).length, 2, "two active claims");
});

await check("claims: in-batch overlap and the override env bypass", async () => {
  const repo = path.join(tmp, "claims-override");
  fs.mkdirSync(repo, { recursive: true });
  const jobId = "job-override";
  // Two tasks of ONE call overlapping each other → the whole call blocks.
  const batch = await prepareSpawnClaims({
    cwd: repo,
    jobId,
    sessionId: "s",
    requests: [
      { worker: "w1", task: "SPEC-SCOPE: rules=R-1" },
      { worker: "w2", task: "SPEC-SCOPE: rules=R-1" },
    ],
  });
  assert.equal(batch.blocked.length, 1);
  assert.match(batch.blocked[0].reasons.join(" "), /ruleIds R-1/);
  assert.deepEqual(await readClaims(repo, jobId), [], "nothing written for a blocked batch");
  // Override bypasses the block and still records the claim.
  const overridden = await prepareSpawnClaims({
    cwd: repo,
    jobId,
    sessionId: "s",
    env: { ...process.env, PI_DELEGATE_CLAIMS_OVERRIDE: "1" },
    requests: [
      { worker: "w1", task: "SPEC-SCOPE: rules=R-1" },
      { worker: "w2", task: "SPEC-SCOPE: rules=R-1" },
    ],
  });
  assert.equal(overridden.blocked.length, 0, "override bypasses overlap blocking");
  assert.equal(overridden.claims.length, 2);
  assert.equal((await readClaims(repo, jobId)).length, 2);
});

await check("claims: completion records changedPaths from a real temp git repo", async () => {
  const repo = makeGitRepo(path.join(tmp, "claims-git"));
  const jobId = "job-git";
  const prepared = await prepareSpawnClaims({
    cwd: repo,
    jobId,
    sessionId: "s-git",
    requests: [
      { worker: "w-git", task: "SPEC-SCOPE: rules=R-G-1\nCLAIMS-PATHS: internal/game/**" },
    ],
  });
  assert.equal(prepared.blocked.length, 0);
  assert.deepEqual(prepared.claims[0].baseline, [], "clean tree ⇒ empty baseline");

  // The "worker" changes the tree.
  fs.appendFileSync(path.join(repo, "internal", "game", "state.go"), "// touched\n");
  fs.writeFileSync(path.join(repo, "internal", "game", "new.go"), "package game\n");

  const completion = await completeSpawnClaim({
    cwd: repo,
    jobId,
    claim: prepared.claims[0],
    ok: true,
  });
  const paths = completion.changedPaths.map((entry) => entry.path).sort();
  assert.deepEqual(paths, ["internal/game/new.go", "internal/game/state.go"]);
  const statuses = Object.fromEntries(completion.changedPaths.map((entry) => [entry.path, entry.status]));
  assert.equal(statuses["internal/game/new.go"], "??", "untracked file status");
  assert.equal(statuses["internal/game/state.go"], "M", "modified file status");

  // The completion releases the claim: the same scope can now be spawned.
  const released = await prepareSpawnClaims({
    cwd: repo,
    jobId,
    sessionId: "s-git",
    requests: [{ worker: "w-git-2", task: "SPEC-SCOPE: rules=R-G-1" }],
  });
  assert.equal(released.blocked.length, 0, "completed claim no longer blocks");
  assert.equal(activeClaims(await readClaims(repo, jobId)).length, 1, "only the new claim is active");

  // changedPathsFromPorcelain directly.
  assert.deepEqual(changedPathsFromPorcelain([" M a.go"], [" M a.go", "?? b.go"]), [{ status: "??", path: "b.go" }]);
  // git failure fails open.
  const failed = await completeSpawnClaim({
    cwd: repo,
    jobId,
    claim: prepared.claims[0],
    ok: false,
    current: null,
  });
  assert.deepEqual(failed.changedPaths, []);
  assert.equal(failed.note, "git-status-failed");
  assert.equal(failed.ok, false);
});

await check("claims: sibling broadcast renders (newest first, cap 3)", async () => {
  const records = [];
  for (let index = 1; index <= 4; index++) {
    records.push({
      type: "completed",
      claimId: "c" + String(index),
      worker: "worker-" + String(index),
      sessionId: "s",
      ruleIds: [],
      changedPaths: [{ status: "M", path: "internal/a" + String(index) + ".go" }],
      finishedAt: "2026-01-0" + String(index) + "T00:00:00.000Z",
      ok: true,
    });
  }
  const block = buildSiblingContext(records);
  const lines = block.split("\n");
  assert.equal(lines.length, 3, "cap 3");
  assert.match(lines[0], /Sibling context: worker worker-4 completed \u2014 changed internal\/a4\.go; expect these states\./);
  assert.match(lines[2], /worker-2/);
  assert.equal(buildSiblingContext([]), "", "no completions ⇒ no block");
  assert.equal(buildSiblingContext([records[0]], 0), "", "cap 0 disables");

  // End-to-end: the block is read back out of a real claims log.
  const repo = path.join(tmp, "claims-broadcast");
  fs.mkdirSync(repo, { recursive: true });
  const jobId = "job-broadcast";
  assert.equal(await siblingContextFor(repo, jobId), "", "missing log ⇒ empty broadcast");
  const prepared = await prepareSpawnClaims({
    cwd: repo,
    jobId,
    sessionId: "s",
    requests: [{ worker: "w-b", task: "SPEC-SCOPE: rules=R-B-1" }],
  });
  await completeSpawnClaim({ cwd: repo, jobId, claim: prepared.claims[0], ok: true, current: [] });
  const broadcast = await siblingContextFor(repo, jobId);
  assert.match(broadcast, /Sibling context: worker w-b completed/);
});

await check("claims: listJobIdsSync unaffected by the per-job subdirectory", async () => {
  // The registry globs flat `*.json` files; a `<id>/claims.jsonl` directory
  // must not appear as a job. Import the REAL jobs.ts with a stub for the pi
  // package (jobs.ts imports CONFIG_DIR_NAME from it; the smoke test runs
  // without pi installed).
  const stubDir = path.join(tmp, "pi-stub");
  fs.mkdirSync(stubDir, { recursive: true });
  const stubPath = path.join(stubDir, "pi-coding-agent.mjs");
  fs.writeFileSync(
    stubPath,
    "export const CONFIG_DIR_NAME = '.pi';\n" +
      "export function getAgentDir() { return '.pi'; }\n" +
      "export function parseFrontmatter() { return {}; }\n",
  );
  registerHooks({
    resolve(specifier, context, nextResolve) {
      if (specifier === "@earendil-works/pi-coding-agent") {
        return { url: pathToFileURL(stubPath).href, shortCircuit: true };
      }
      return nextResolve(specifier, context);
    },
  });
  const jobs = await import("./jobs.ts");
  const repo = path.join(tmp, "jobs-registry");
  const jobsDir = path.join(repo, ".pi", "jobs");
  fs.mkdirSync(jobsDir, { recursive: true });
  const entry = {
    id: "job-0001",
    agent: "orchestrator",
    task: "t",
    startedAt: "2026-01-01T00:00:00.000Z",
    runner: "fallback",
    status: "done",
  };
  fs.writeFileSync(path.join(jobsDir, "job-0001.json"), JSON.stringify(entry));
  const prepared = await prepareSpawnClaims({
    cwd: repo,
    jobId: "job-0001",
    sessionId: "s",
    requests: [{ worker: "w", task: "SPEC-SCOPE: rules=R-1" }],
  });
  assert.equal(fs.existsSync(path.join(jobsDir, "job-0001", "claims.jsonl")), true);
  assert.equal(prepared.logPath, path.join(jobsDir, "job-0001", "claims.jsonl"));
  assert.deepEqual(jobs.listJobIdsSync(repo), ["job-0001"], "the claims directory is not a job id");
  assert.equal(jobs.jobsDirFor(repo), jobsDir);
  // JobEntry survives a round trip with the batch-2 reconciliation field.
  assert.equal(typeof jobs.getJob, "function");
});

// ── Batch 2: reconciliation-lite ────────────────────────────────────────────

await check("diffReceiptAgainst: names each discrepancy, empty when matching", async () => {
  const receipt = {
    timestamp: "2026-01-01T00:00:00.000Z",
    agent: "worker",
    session: "s",
    commands: [{ command: "make audit/static", exitCode: 0, timedOut: false, durationMs: 5 }],
    rulesHash: "hash-a",
    headSha: "a".repeat(40),
    ok: true,
    note: "",
  };
  const current = {
    headSha: "a".repeat(40),
    rulesHash: "hash-a",
    commands: [{ command: "make audit/static", exitCode: 0, timedOut: false, durationMs: 7 }],
  };
  assert.deepEqual(diffReceiptAgainst(receipt, current), []);
  assert.deepEqual(diffReceiptAgainst(null, current), [], "no receipt ⇒ nothing to diff");

  const movedHead = diffReceiptAgainst(receipt, { ...current, headSha: "b".repeat(40) });
  assert.equal(movedHead.length, 1);
  assert.match(movedHead[0], /^headSha: receipt a{40}, now b{40}$/);

  const movedRules = diffReceiptAgainst(receipt, { ...current, rulesHash: "hash-b" });
  assert.match(movedRules[0], /^rulesHash: receipt hash-a, now hash-b$/);

  const flipped = diffReceiptAgainst(receipt, {
    ...current,
    commands: [{ command: "make audit/static", exitCode: 1, timedOut: false, durationMs: 7 }],
  });
  assert.match(flipped[0], /^make audit\/static: exit 0 recorded, exit 1 now$/);

  const missing = diffReceiptAgainst(receipt, { ...current, commands: [] });
  assert.match(missing[0], /not run now/);

  // A failed reconciliation becomes a failing GateRun (shared report path).
  const run = reconciliationGateRun({ ok: false, report: "MISMATCH: headSha", steps: [], receipt: null });
  assert.equal(run.ok, false);
  assert.equal(run.steps[0].command, "(reconciliation)");
  assert.match(run.steps[0].stderr, /headSha/);
  assert.equal(reconciliationGateRun({ ok: true, report: "", steps: [], receipt: null }).ok, true);
});

await check("latestPassingReceipt: only passing receipts are checkpoints", async () => {
  const repo = path.join(tmp, "checkpoint-repo");
  fs.mkdirSync(path.join(repo, "docs", "spec"), { recursive: true });
  fs.writeFileSync(path.join(repo, "docs", "spec", "rules.yaml"), "rules: []\n");
  await writeReceipt({ cwd: repo, agent: "worker", session: "s", commands: [], ok: false, note: "first failed" });
  assert.equal(await latestPassingReceipt(repo), null, "a failing receipt is not a checkpoint");
  await writeReceipt({ cwd: repo, agent: "worker", session: "s", commands: [], ok: true, note: "passing" });
  await writeReceipt({ cwd: repo, agent: "worker", session: "s", commands: [], ok: false, note: "later failed" });
  const checkpoint = await latestPassingReceipt(repo);
  assert.ok(checkpoint, "the passing receipt is found");
  assert.equal(checkpoint.note, "passing", "the newest PASSING receipt wins, not the newest receipt");
});

await check("runReconciliation: matching checkpoint ok, moved HEAD reported", async () => {
  const repo = makeGitRepo(path.join(tmp, "recon-repo"));
  writeWorkflow(repo);
  fs.writeFileSync(path.join(repo, "Makefile"), "audit/static:\n\t@true\n");
  fs.writeFileSync(path.join(repo, "go.mod"), "module recontmp\n\ngo 1.21\n");
  fs.mkdirSync(path.join(repo, "cmd", "spec"), { recursive: true });
  fs.writeFileSync(path.join(repo, "cmd", "spec", "main.go"), "package main\n\nfunc main() {}\n");
  fs.mkdirSync(path.join(repo, "docs", "spec"), { recursive: true });
  fs.writeFileSync(path.join(repo, "docs", "spec", "rules.yaml"), "rules: []\n");
  gitExec(repo, ["add", "-A"]);
  gitExec(repo, ["commit", "-q", "-m", "gates"]);

  const receiptPath = await writeReceipt({
    cwd: repo,
    agent: "worker",
    session: "recon",
    commands: [{ command: "make audit/static", exitCode: 0, timedOut: false, durationMs: 1 }],
    ok: true,
    note: "checkpoint",
  });
  assert.equal(receiptPath, receiptsFileFor(repo));
  assert.equal((await latestPassingReceipt(repo)) !== null, true, "a passing receipt is the checkpoint");

  const matching = await runReconciliation(repo, {
    agent: "worker",
    makeTimeoutMs: 60_000,
    specTimeoutMs: 60_000,
  });
  assert.equal(matching.ok, true, "matching tree reconciles: " + matching.report);
  assert.match(matching.report, /status: ok/);
  assert.equal(matching.steps.some((step) => step.command === "make audit/static"), true);

  // Move HEAD without updating the receipt ⇒ headSha mismatch.
  fs.writeFileSync(path.join(repo, "extra.txt"), "x\n");
  gitExec(repo, ["add", "-A"]);
  gitExec(repo, ["commit", "-q", "-m", "move head"]);
  const drifted = await runReconciliation(repo, { agent: "worker", makeTimeoutMs: 60_000, specTimeoutMs: 60_000 });
  assert.equal(drifted.ok, false, "stale checkpoint must not reconcile");
  assert.match(drifted.report, /MISMATCH headSha:/);
  assert.match(drifted.report, /status: MISMATCH/);
  const asGateRun = reconciliationGateRun(drifted);
  assert.match(formatGateReport(asGateRun, { header: "── SETTLE GATE BLOCKED ──", footer: "fix" }), /RECONCILIATION/);
});

// ── Batch 3: WORKFLOW.md is the declarative policy source ───────────────────

/** A front matter body from field lines (no fences), for policy fixtures. */
function fieldsPolicy(fields) {
  return "---\nversion: 1\n" + fields.join("\n") + "\n---\n\nprose\n";
}

const FULL_POLICY_BODY = fieldsPolicy([
  "audit_static_cmd: make audit/static",
  "audit_cmd: make audit",
  "prove_cmd: make prove",
  'scoped_check_cmd: "go run ./cmd/spec check --strict"',
  "protected_paths:",
  "  - Makefile",
  "  - cmd/spec",
  "  - cmd/spec/**",
  "  - docs/spec/**",
  "  - .pi/**",
  "pi_editable_paths:",
  "  - .pi/agents/**",
  "  - .pi/prompts/**",
  "  - .pi/delegate/**",
  "  - .pi/plans/**",
  "allowlist_path: .pi/delegate/gate-allowlist.json",
  "receipts_dir: .pi/receipts",
  "claims_dir: .pi/jobs",
  "settle_max_attempts: 5",
  "settle_immediate_attempts: 3",
  "settle_backoff_base_ms: 2000",
  "settle_backoff_max_doublings: 5",
  "worker_stall_timeout_ms: 600000",
  "max_concurrent_workers: 8",
  "approval_binding: head-sha",
  "red_approval_marker: AWAITING_APPROVAL",
  'red_approval_head_sha_line: "HEAD SHA:"',
  "handoff_terminal_tokens:",
  "  - HANDOFF_COMPLETE",
  "  - HANDOFF_BLOCKED",
]);

await check("policy: full front matter loads and every field reads back", async () => {
  const dir = path.join(tmp, "policy-full");
  writeWorkflow(dir, FULL_POLICY_BODY);
  const loaded = loadWorkflowPolicy(dir);
  assert.equal(loaded.ok, true, loaded.ok ? "" : loaded.reason);
  const p = loaded.policy;
  assert.equal(p.version, 1);
  assert.equal(p.auditStaticCmd, "make audit/static");
  assert.equal(p.auditCmd, "make audit");
  assert.equal(p.proveCmd, "make prove");
  assert.equal(p.scopedCheckCmd, "go run ./cmd/spec check --strict");
  assert.deepEqual(p.protectedPaths, ["Makefile", "cmd/spec", "cmd/spec/**", "docs/spec/**", ".pi/**"]);
  assert.deepEqual(p.piEditablePaths, [".pi/agents/**", ".pi/prompts/**", ".pi/delegate/**", ".pi/plans/**"]);
  assert.equal(p.allowlistPath, ".pi/delegate/gate-allowlist.json");
  assert.equal(p.receiptsDir, ".pi/receipts");
  assert.equal(p.claimsDir, ".pi/jobs");
  assert.equal(p.settleMaxAttempts, 5);
  assert.equal(p.settleImmediateAttempts, 3);
  assert.equal(p.settleBackoffBaseMs, 2000);
  assert.equal(p.settleBackoffMaxDoublings, 5);
  assert.equal(p.workerStallTimeoutMs, 600000);
  assert.equal(p.maxConcurrentWorkers, 8);
  assert.equal(p.approvalBinding, "head-sha");
  assert.equal(p.redApprovalMarker, "AWAITING_APPROVAL");
  assert.equal(p.redApprovalHeadShaLine, "HEAD SHA:");
  assert.deepEqual(p.handoffTerminalTokens, ["HANDOFF_COMPLETE", "HANDOFF_BLOCKED"]);
  assert.equal(loaded.declared.size, 19, "every declared field is marked present");
});

await check("policy: parser handles quotes, comments, lists and the command splitter", async () => {
  const parsed = parseWorkflowFrontMatter(
    "---\nversion: 1\n# a comment\ncmd: \"a b c\"  # trailing comment\nlist:\n  - one\n  - 'two'\nempty:\n---\nbody text is not front matter\n",
  );
  assert.equal(parsed.ok, true, parsed.reason);
  assert.equal(parsed.values.cmd, "a b c");
  assert.deepEqual(parsed.values.list, ["one", "two"]);
  assert.equal(parsed.values.empty, "");
  assert.deepEqual(splitCommandLine("go run ./cmd/spec check --strict"), [
    "go",
    "run",
    "./cmd/spec",
    "check",
    "--strict",
  ]);
  assert.deepEqual(splitCommandLine('sh -c "echo hi"'), ["sh", "-c", "echo hi"]);
  assert.deepEqual(splitCommandLine("./audit.sh"), ["./audit.sh"]);
});

await check("policy: omitted fields take the batch-1/2 defaults", async () => {
  const dir = path.join(tmp, "policy-minimal");
  writeWorkflow(dir);
  const loaded = loadWorkflowPolicy(dir);
  assert.equal(loaded.ok, true, loaded.ok ? "" : loaded.reason);
  assert.equal(loaded.policy.auditStaticCmd, DEFAULT_AUDIT_STATIC_CMD);
  assert.equal(loaded.policy.settleMaxAttempts, DEFAULT_SETTLE_MAX_ATTEMPTS);
  assert.equal(loaded.policy.settleBackoffBaseMs, DEFAULT_SETTLE_BACKOFF_BASE_MS);
  assert.equal(loaded.policy.workerStallTimeoutMs, DEFAULT_WORKER_STALL_TIMEOUT_MS);
  assert.equal(loaded.policy.maxConcurrentWorkers, DEFAULT_MAX_CONCURRENT_WORKERS);
  assert.deepEqual(loaded.policy.protectedPaths, [...DEFAULT_PROTECTED_PATHS]);
  assert.equal(loaded.declared.size, 0, "nothing is declared in a version-only file");
  // The gate knobs fall through to the env override when the field is omitted.
  assert.equal(settleGateMaxAttempts(dir), 5);
  assert.equal(settleImmediateAttempts(dir), 3);
  assert.equal(workerStallTimeoutMs({}, dir), 600_000);
});

await check("policy: missing WORKFLOW.md refuses with a specific reason", async () => {
  const dir = path.join(tmp, "policy-missing");
  fs.mkdirSync(dir, { recursive: true });
  const loaded = loadWorkflowPolicy(dir);
  assert.equal(loaded.ok, false);
  assert.match(loaded.reason, /WORKFLOW\.md not found/);
  assert.match(loaded.reason, /policy-missing/);
});

await check("policy: malformed front matter refuses (no fence / unclosed fence / dup key)", async () => {
  const dir = path.join(tmp, "policy-malformed");
  const cases = [
    ["no fence", "version: 1\n"],
    ["unclosed", "---\nversion: 1\n"],
    ["duplicate", "---\nversion: 1\nversion: 1\n---\n"],
    ["not a key-value", "---\nversion: 1\njust some text\n---\n"],
    ["unterminated quote", '---\nversion: 1\naudit_cmd: "make audit\n---\n'],
  ];
  for (const [name, body] of cases) {
    writeWorkflow(dir, body);
    const loaded = loadWorkflowPolicy(dir);
    assert.equal(loaded.ok, false, name + " must refuse");
    assert.ok(loaded.reason.length > 0, name + " names a reason");
  }
});

await check("policy: missing version and unknown version refuse", async () => {
  const dir = path.join(tmp, "policy-version");
  writeWorkflow(dir, "---\naudit_cmd: make audit\n---\n");
  const missing = loadWorkflowPolicy(dir);
  assert.equal(missing.ok, false);
  assert.match(missing.reason, /missing required field `version`/);
  writeWorkflow(dir, "---\nversion: 2\n---\n");
  const newer = loadWorkflowPolicy(dir);
  assert.equal(newer.ok, false);
  assert.match(newer.reason, /unsupported policy version 2/);
  writeWorkflow(dir, "---\nversion: one\n---\n");
  assert.equal(loadWorkflowPolicy(dir).ok, false);
});

await check("policy: unknown field refuses (typo protection)", async () => {
  const dir = path.join(tmp, "policy-unknown");
  writeWorkflow(dir, fieldsPolicy(["setle_max_attempts: 3"]));
  const loaded = loadWorkflowPolicy(dir);
  assert.equal(loaded.ok, false);
  assert.match(loaded.reason, /unknown field\(s\).*setle_max_attempts/);
});

await check("policy: invalid-typed field falls back to the default and keeps the env path", async () => {
  const dir = path.join(tmp, "policy-invalid");
  writeWorkflow(dir, fieldsPolicy(["settle_max_attempts: lots", "worker_stall_timeout_ms: -3"]));
  const loaded = loadWorkflowPolicy(dir);
  assert.equal(loaded.ok, true, loaded.ok ? "" : loaded.reason);
  assert.equal(loaded.policy.settleMaxAttempts, DEFAULT_SETTLE_MAX_ATTEMPTS);
  assert.equal(loaded.policy.workerStallTimeoutMs, DEFAULT_WORKER_STALL_TIMEOUT_MS);
  assert.equal(loaded.declared.has("settle_max_attempts"), false, "invalid ⇒ not declared");
  const previous = process.env.PI_DELEGATE_SETTLE_MAX_ATTEMPTS;
  try {
    process.env.PI_DELEGATE_SETTLE_MAX_ATTEMPTS = "7";
    assert.equal(settleGateMaxAttempts(dir), 7, "the env override still applies");
  } finally {
    if (previous === undefined) delete process.env.PI_DELEGATE_SETTLE_MAX_ATTEMPTS;
    else process.env.PI_DELEGATE_SETTLE_MAX_ATTEMPTS = previous;
  }
});

await check("policy: a present field overrides both the default and the env knob", async () => {
  const dir = path.join(tmp, "policy-override");
  writeWorkflow(
    dir,
    fieldsPolicy([
      "settle_max_attempts: 9",
      "settle_immediate_attempts: 4",
      "settle_backoff_base_ms: 50",
      "settle_backoff_max_doublings: 1",
      "worker_stall_timeout_ms: 1234",
    ]),
  );
  const previousMax = process.env.PI_DELEGATE_SETTLE_MAX_ATTEMPTS;
  const previousBase = process.env.PI_DELEGATE_BACKOFF_BASE_MS;
  const previousStall = process.env.PI_DELEGATE_WORKER_STALL_TIMEOUT_MS;
  try {
    process.env.PI_DELEGATE_SETTLE_MAX_ATTEMPTS = "7";
    process.env.PI_DELEGATE_BACKOFF_BASE_MS = "99";
    process.env.PI_DELEGATE_WORKER_STALL_TIMEOUT_MS = "5";
    assert.equal(settleGateMaxAttempts(dir), 9, "policy beats the env override");
    assert.equal(settleBackoffBaseMs(dir), 50, "policy beats the env override");
    assert.equal(workerStallTimeoutMs(process.env, dir), 1234, "policy beats the env override");
    assert.equal(settleImmediateAttempts(dir), 4);
    // 4 immediate attempts, then one doubling (cap 1): 50ms, 100ms, 100ms.
    assert.equal(settleBackoffMsFor(4, 50, dir), 0);
    assert.equal(settleBackoffMsFor(5, 50, dir), 50);
    assert.equal(settleBackoffMsFor(6, 50, dir), 100);
    assert.equal(settleBackoffMsFor(7, 50, dir), 100, "doublings cap holds");
  } finally {
    if (previousMax === undefined) delete process.env.PI_DELEGATE_SETTLE_MAX_ATTEMPTS;
    else process.env.PI_DELEGATE_SETTLE_MAX_ATTEMPTS = previousMax;
    if (previousBase === undefined) delete process.env.PI_DELEGATE_BACKOFF_BASE_MS;
    else process.env.PI_DELEGATE_BACKOFF_BASE_MS = previousBase;
    if (previousStall === undefined) delete process.env.PI_DELEGATE_WORKER_STALL_TIMEOUT_MS;
    else process.env.PI_DELEGATE_WORKER_STALL_TIMEOUT_MS = previousStall;
  }
});

await check("policy: the declared gate commands are the ones executed", async () => {
  const dir = path.join(tmp, "policy-commands");
  fs.mkdirSync(dir, { recursive: true });
  fs.writeFileSync(path.join(dir, "audit.sh"), "#!/bin/sh\nexit 0\n");
  fs.writeFileSync(path.join(dir, "spec.sh"), "#!/bin/sh\necho scoped \"$@\"\nexit 0\n");
  fs.chmodSync(path.join(dir, "audit.sh"), 0o755);
  fs.chmodSync(path.join(dir, "spec.sh"), 0o755);
  writeWorkflow(dir, fieldsPolicy(["audit_static_cmd: ./audit.sh", "scoped_check_cmd: ./spec.sh"]));
  const full = await runScopedGate({ cwd: dir, makeTimeoutMs: 10_000, specTimeoutMs: 10_000 });
  assert.equal(full.ok, true, "declared commands pass: " + full.steps.map((s) => s.command).join(" | "));
  assert.deepEqual(full.steps.map((step) => step.command), ["./audit.sh", "./spec.sh"]);
  const scoped = await runScopedGate({
    cwd: dir,
    scopeEnv: "rules=R-A-1",
    makeTimeoutMs: 10_000,
    specTimeoutMs: 10_000,
  });
  assert.deepEqual(scoped.steps.map((step) => step.command), ["./audit.sh", "./spec.sh --rule R-A-1"]);
});

await check("policy: protected/editable path sets come from the file, not the hardcode", async () => {
  const dir = path.join(tmp, "policy-paths");
  writeWorkflow(dir, fieldsPolicy(["protected_paths:", "  - src/**"]));
  assert.equal(evaluateGateFileWrite(dir, "src/a.go").blocked, true, "declared path is denied");
  assert.equal(evaluateGateFileWrite(dir, "Makefile").blocked, false, "hardcoded Makefile is NOT denied by this policy");
  assert.equal(evaluateGateFileWrite(dir, "docs/spec/rules.yaml").blocked, false, "hardcoded docs/spec is NOT denied by this policy");
  assert.equal(evaluateGateFileWrite(dir, "main.go").blocked, false);

  const piDir = path.join(tmp, "policy-pi-editable");
  writeWorkflow(
    piDir,
    fieldsPolicy(["protected_paths:", "  - .pi/**", "pi_editable_paths:", "  - .pi/delegate/**", "  - .pi/plans/**"]),
  );
  assert.equal(evaluateGateFileWrite(piDir, ".pi/agents/x.md").blocked, true, "not in the declared editable set");
  assert.equal(evaluateGateFileWrite(piDir, ".pi/delegate/orchestrator.md").blocked, false);
  assert.equal(evaluateGateFileWrite(piDir, ".pi/plans/p.md").blocked, false);
  assert.equal(evaluateGateFileWrite(piDir, "docs/spec/rules.yaml").blocked, false, "outside the declared set");
  // The allowlist file is ALWAYS self-denied, even when the file declares a
  // different protected set and even with a matching allowlist glob.
  fs.mkdirSync(path.join(piDir, ".pi", "plans"), { recursive: true });
  assert.equal(evaluateGateFileWrite(piDir, ".pi/delegate/gate-allowlist.json").blocked, true);
  // So is the policy file itself: a worker must not rewrite its own gates.
  assert.equal(evaluateGateFileWrite(dir, "WORKFLOW.md").blocked, true, "policy file is self-denied");
  fs.mkdirSync(path.join(dir, ".pi", "delegate"), { recursive: true });
  fs.writeFileSync(path.join(dir, ".pi", "delegate", "gate-allowlist.json"), '["WORKFLOW.md"]');
  assert.equal(evaluateGateFileWrite(dir, "WORKFLOW.md").blocked, true, "not grantable via the allowlist");
});

await check("policy: approval-contract mismatch refuses (marker / sha line / tokens / binding)", async () => {
  const dir = path.join(tmp, "policy-approval");
  const cases = [
    ["marker", fieldsPolicy(["red_approval_marker: NEEDS_APPROVAL"]), /red_approval_marker/],
    ["sha line", fieldsPolicy(['red_approval_head_sha_line: "HEAD:"']), /red_approval_head_sha_line/],
    ["tokens", fieldsPolicy(["handoff_terminal_tokens:", "  - HANDOFF_DONE"]), /handoff_terminal_tokens/],
    ["binding", fieldsPolicy(["approval_binding: tree-hash"]), /approval_binding/],
  ];
  for (const [name, body, pattern] of cases) {
    writeWorkflow(dir, body);
    const loaded = loadWorkflowPolicy(dir);
    assert.equal(loaded.ok, false, name + " mismatch must refuse");
    assert.match(loaded.reason, /approval contract mismatch/);
    assert.match(loaded.reason, pattern);
  }
});

await check("policy: gate paths refuse loudly when the policy is missing", async () => {
  const dir = path.join(tmp, "policy-refuse");
  fs.mkdirSync(dir, { recursive: true });
  const run = await runScopedGate({ cwd: dir, makeTimeoutMs: 5_000, specTimeoutMs: 5_000 });
  assert.equal(run.ok, false, "a missing policy is a failed gate");
  assert.equal(run.steps.length, 1, "NO command ran: " + run.steps.map((s) => s.command).join(" | "));
  assert.equal(run.steps[0].command, "(workflow policy)");
  assert.match(run.steps[0].reason, /workflow policy unavailable: WORKFLOW\.md not found/);
  assert.match(run.steps[0].reason, /gates refuse rather than run unpoliced/);
  assert.equal(workflowPolicyRefusal("x"), "workflow policy unavailable: x \u2014 gates refuse rather than run unpoliced");
  const report = formatGateReport(run, { header: "\u2500\u2500 SETTLE GATE BLOCKED \u2500\u2500", footer: "fix" });
  assert.match(report, /workflow policy unavailable/);
  // The deny hook refuses every in-repo write under an undeclared policy.
  const decision = evaluateGateFileWrite(dir, "main.go");
  assert.equal(decision.blocked, true, "writes are denied, not silently policed by an old hardcode");
  assert.match(decision.reason, /workflow policy unavailable/);
  // The refusal is RECORDED (ok:false), so a settle gate counts it as a failed attempt.
  const receipt = await writeReceipt({
    cwd: dir,
    agent: "worker",
    commands: receiptCommandsOf(run),
    ok: run.ok,
    note: "policy refuse",
  });
  assert.ok(receipt, "a receipt is written even under a missing policy");
  const record = JSON.parse(fs.readFileSync(receipt, "utf8").trim().split("\n").pop());
  assert.equal(record.ok, false);
  assert.equal(record.commands[0].command, "(workflow policy)");
});

fs.rmSync(tmp, { recursive: true, force: true });
console.log("\n" + String(checks - failures) + "/" + String(checks) + " checks passed");
process.exit(failures === 0 ? 0 : 1);
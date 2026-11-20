// Standalone smoke test for .pi/extensions/delegate/gates.ts.
//
// Imports the REAL module (node's built-in type stripping runs the .ts file
// directly — no pi, no build step) and exercises it against temp dirs. This is
// a harness for headless verification only: nothing here is imported by the
// extension. Run:  node .pi/extensions/delegate/smoke-gates.mjs
// (needs Node >= 22.18 for unflagged .ts type stripping).
import * as assert from "node:assert/strict";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import {
  evaluateGateFileWrite,
  formatGateReport,
  formatSpecScope,
  isGateRelevantWrite,
  matchGlob,
  parseSpecScope,
  parseSpecScopeEnv,
  runGate,
  runScopedGate,
  shouldRunPostWriteGate,
  markPostWriteGateEnd,
  markPostWriteGateStart,
  writeReceipt,
} from "./gates.ts";

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
  const run = await runScopedGate({ cwd: tmp, makeTimeoutMs: 10_000, specTimeoutMs: 10_000 });
  assert.equal(run.steps.length, 2);
  assert.equal(run.steps[1].command, "go run ./cmd/spec check --strict");
});

fs.rmSync(tmp, { recursive: true, force: true });
console.log("\n" + String(checks - failures) + "/" + String(checks) + " checks passed");
process.exit(failures === 0 ? 0 : 1);

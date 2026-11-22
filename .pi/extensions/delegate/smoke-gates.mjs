// Standalone smoke test for .pi/extensions/delegate/gates.ts.
//
// Imports the REAL module (node's built-in type stripping runs the .ts file
// directly — no pi, no build step) and exercises it against temp dirs. This is
// a harness for headless verification only: nothing here is imported by the
// extension. Run:  node .pi/extensions/delegate/smoke-gates.mjs
// (needs Node >= 22.18 for unflagged .ts type stripping).
// On a Node build WITHOUT type stripping (`ERR_UNKNOWN_FILE_EXTENSION`) load it
// through pi's bundled jiti instead — same pattern as smoke-sbx.mjs:
//   node -e 'const {createJiti}=require("<pi-install>/node_modules/jiti/lib/jiti.cjs");\
//     createJiti(process.cwd()+"/.pi/extensions/delegate/smoke-gates.mjs",{interopDefault:true})\
//       .import(process.cwd()+"/.pi/extensions/delegate/smoke-gates.mjs")'
//
// Sandbox era: the checks for the DELETED mechanisms are gone with them —
// evaluateGateFileWrite / the gate allowlist / protected+editable path sets /
// bash_write_patterns no longer exist in the modules under test. New suites for
// the sandbox seams (./sbx.ts exit mapping + argv shapes, the host integrity
// check, the merge-command detector, the in-VM advisory ladder) belong in their
// own files so this one stays a gate/receipt/policy suite.
import * as assert from "node:assert/strict";
import { execFileSync, spawn } from "node:child_process";
import * as fs from "node:fs";
import { registerHooks } from "node:module";
import * as os from "node:os";
import * as path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import {
  bumpSettleGateAttempts,
  catalogHash,
  describeWaitMs,
  diffReceiptAgainst,
  formatGateReport,
  formatSettleEscalation,
  formatSettleTrail,
  gateFilePathsFor,
  gateVmShellCommand,
  isGateRelevantWrite,
  latestPassingReceipt,
  matchGlob,
  parseGateVmShellCommand,
  planSettleGateAttempt,
  receiptsFileFor,
  RECEIPT_SCHEMA_VERSION,
  receiptCommandsOf,
  recentSettleFailures,
  reconciliationGateRun,
  recordSettleGateAttempt,
  resetSettleGateAttempts,
  resolveDeclaredGateCommand,
  runAuditGate,
  runDeclaredGateStep,
  runGate,
  runProveGate,
  runReconciliation,
  runScopedGate,
  settleBackoffBaseMs,
  settleBackoffMsFor,
  settleGateMaxAttempts,
  settleGateStateFor,
  settleImmediateAttempts,
  shellQuote,
  shouldRunPostWriteGate,
  splitCommandLine,
  markPostWriteGateEnd,
  markPostWriteGateStart,
  writeReceipt,
} from "./gates.ts";
import {
  isProveReceipt,
  isProveSegment,
  proveRuleIdsIn,
  resolveJobRuleIds,
  resolveJobScope,
  ruleFlagPattern,
  checkProveCoverage,
} from "./mergegate.ts";
import {
  formatSpecScope,
  formatTaskPacketRefusal,
  parseClaimsPaths,
  parseSpecScope,
  parseSpecScopeEnv,
  parseTaskPacket,
} from "./protocol.ts";
import {
  GATE_DB_DSN,
  GATE_DB_NAME,
  GATE_DB_USER,
  GATE_PG_READY_ATTEMPTS,
  execSandboxArgs,
  gateDbPresteps,
  gateVmExecEnv,
  prepareGateDbVm,
} from "./sbx.ts";
import {
  WORKER_STALL_TIMEOUT_MS,
  formatStallReason,
  stallDecision,
  startStallWatchdog,
  workerStallTimeoutMs,
} from "./runtime.ts";
import { resetGateSandboxRegistryForTests, setSbxProcessOps } from "./sbx.ts";
import {
  DEFAULT_AUDIT_STATIC_CMD,
  DEFAULT_GATE_FILE_PATHS,
  DEFAULT_MAX_CONCURRENT_WORKERS,
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

// ── Gate VM stub plumbing ──────────────────────────────────────────────────
// Authoritative gates now run via `sbx exec` in a gate VM (./gates.ts
// runStepInGateVm). These fixtures never touch the real CLI: every sbx call
// goes through the single injection point, and the gate VM registry is reset
// between fixtures so each one sees a predictable create.

/** A full SbxProcessResult from a sparse patch (same shape as smoke-sbx). */
function gateSbxResult(args, patch = {}) {
  const exitCode = patch.exitCode === undefined ? 0 : patch.exitCode;
  return {
    args: [...args],
    ok: patch.ok === undefined ? exitCode === 0 : patch.ok,
    exitCode,
    stdout: patch.stdout ?? "",
    stderr: patch.stderr ?? "",
    timedOut: patch.timedOut ?? false,
    spawnError: patch.spawnError ?? null,
    durationMs: 1,
    aborted: patch.aborted ?? false,
  };
}

/** Stub every sbx call (create/policy/exec/stop/rm) and record the argvs. */
function installGateVmStub(respond = () => ({})) {
  const calls = [];
  setSbxProcessOps({
    async run(args, opts) {
      calls.push({ args: [...args], opts: opts ?? {} });
      const patch = respond(args, opts);
      if (patch instanceof Error) throw patch;
      return gateSbxResult(args, patch ?? {});
    },
    exists() {
      return false;
    },
  });
  return { calls };
}

/** The exec argvs only (what the gate VM actually ran), in order. */
function gateVmExecCalls(stub) {
  return stub.calls.filter((call) => call.args[0] === "exec").map((call) => call.args);
}

/** Drop the gate VM registry + the stub (fixtures are independent). */
function resetGateVmStub() {
  resetGateSandboxRegistryForTests();
  setSbxProcessOps(null);
}


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


// ── rule catalog fixtures ─────────────────────────────────────────────────
// The catalog is one authored file per rule: docs/spec/rules/<module>/<ID>.md
// (docs/spec/rules.yaml is GONE). A receipt's catalogHash is the MANIFEST hash
// of that tree, so a fixture needs one file. It is kept well-formed (two `---`
// fences around real front matter) so a future change that PARSES the stub
// instead of only hashing it still works.
const STUB_RULE_FILE = [
  "---",
  "id: R-KERNEL-001",
  "title: A smoke fixture rule",
  "statement: |",
  "  A minimal but well-formed rule file: the catalog manifest has something",
  "  real to hash and a parser would find every required field.",
  "module: kernel",
  "entity: fixture",
  "category: invariant",
  "status: implemented",
  "authority: handler",
  "mechanism: null",
  "write_scope: row-local",
  "enforcement: []",
  "refusal: null",
  "fixture: null",
  "verification:",
  "  unit: []",
  "  e2e: []",
  "  gherkin: []",
  "expert-ref: null",
  "knowledge-ref: null",
  "stale_pointer: null",
  "tie: null",
  "---",
  "",
  "## Notes",
  "",
  "Smoke stub: exists so the catalog manifest has a file to hash.",
  "",
].join("\n");

/** Write the minimal one-file rule catalog under `<dir>/docs/spec/rules/`. */
function writeRuleCatalogStub(dir, ruleId = "R-KERNEL-001") {
  const moduleDir = path.join(dir, "docs", "spec", "rules", "kernel");
  fs.mkdirSync(moduleDir, { recursive: true });
  const file = path.join(moduleDir, ruleId + ".md");
  fs.writeFileSync(file, STUB_RULE_FILE);
  return file;
}


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
  // `none` is the normalized value for an EXPLICIT empty scope (B3.1): it must
  // parse to an empty scope, not to null (= full check, no scope at all).
  assert.deepEqual(parseSpecScopeEnv("none"), { module: undefined, rules: [] });
  assert.equal(formatSpecScope({ rules: [] }), "none");
  assert.deepEqual(parseSpecScope("SPEC-SCOPE: none"), { module: undefined, rules: [] });
});

// ── Task packets (B3.1: protocol.ts owns the grammar) ──────────────────────
// The subagent tool boundary calls parseTaskPacket once per task text and
// refunds the spawn on any error; these cases pin the grammar and the exact
// normalized PI_DELEGATE_SPEC_SCOPE value the boundary exports.
await check("parseTaskPacket: valid packets normalize to the expected scope env", async () => {
  const cases = [
    ["none", "none"],
    ["module=game", "module=game"],
    ["rules=R-GAME-001,R-GAME-002", "rules=R-GAME-001,R-GAME-002"],
    ["module=game rules=R-GAME-001,R-GAME-002", "module=game rules=R-GAME-001,R-GAME-002"],
  ];
  for (const [body, expected] of cases) {
    const parsed = parseTaskPacket("Do the thing.\nSPEC-SCOPE: " + body + "\nmore prose");
    assert.equal(parsed.ok, true, body + " is a valid packet");
    assert.equal(parsed.packet.scopeEnv, expected, "normalized env for " + body);
    // What index.ts exports into the sandbox is what the gate re-parses: the
    // round-trip must be lossless (including the explicit empty scope).
    assert.deepEqual(parseSpecScopeEnv(parsed.packet.scopeEnv), parsed.packet.scope, "env round-trip for " + body);
  }
  // Case-insensitive prefix, like today's lenient helper.
  assert.equal(parseTaskPacket("spec-scope: rules=R-A-1").packet.scopeEnv, "rules=R-A-1");
  // CLAIMS-PATHS still normalizes valid entries (./ prefix + trailing slash).
  const claimed = parseTaskPacket("SPEC-SCOPE: rules=R-1\nCLAIMS-PATHS: ./internal/a/ , internal/b");
  assert.equal(claimed.ok, true);
  assert.deepEqual(claimed.packet.ownedPaths, ["internal/a", "internal/b"]);
  assert.equal(claimed.packet.scopeEnv, "rules=R-1");
});

await check("parseTaskPacket: no SPEC-SCOPE line is valid and exports NO env var", async () => {
  const parsed = parseTaskPacket("Do the thing.\nCLAIMS-PATHS: internal/game/**");
  assert.equal(parsed.ok, true);
  assert.equal(parsed.packet.scope, null, "no scope parsed");
  assert.equal(Object.prototype.hasOwnProperty.call(parsed.packet, "scopeEnv"), false, "no scopeEnv key");
  assert.deepEqual(parsed.packet.ownedPaths, ["internal/game/**"]);
  // The env assembly in index.ts only sets the variable when the key exists.
  assert.equal(undefined, parsed.packet.scopeEnv);
});

await check("parseTaskPacket: garbage SPEC-SCOPE lines are refused, naming the line", async () => {
  const cases = [
    { text: "SPEC-SCOPE: none extra-words", line: "SPEC-SCOPE: none extra-words" },
    { text: "SPEC-SCOPE: modul=game", line: "SPEC-SCOPE: modul=game" },
    { text: "SPEC-SCOPE: rules=", line: "SPEC-SCOPE: rules=" },
    { text: "SPEC-SCOPE:", line: "SPEC-SCOPE:" },
    { text: "SPEC-SCOPE: module=", line: "SPEC-SCOPE: module=" },
    { text: "SPEC-SCOPE: module=game rules=", line: "SPEC-SCOPE: module=game rules=" },
    { text: "SPEC-SCOPE: module=game rules=,R-1", line: "SPEC-SCOPE: module=game rules=,R-1" },
    { text: "SPEC-SCOPE: (nothing usable)", line: "SPEC-SCOPE: (nothing usable)" },
    { text: "SPEC-SCOPE: module=game rules=R-1 rules=R-2", line: "SPEC-SCOPE: module=game rules=R-1 rules=R-2" },
    { text: "SPEC-SCOPE: module=game\nSPEC-SCOPE: module=identity", line: "SPEC-SCOPE: module=identity" },
  ];
  for (const { text, line } of cases) {
    const parsed = parseTaskPacket("Do the thing.\n" + text);
    assert.equal(parsed.ok, false, "refused: " + text);
    const joined = parsed.errors.join("\n");
    assert.ok(joined.includes(line), "error names the offending line for " + text + ": " + joined);
    const refusal = formatTaskPacketRefusal(parsed.errors);
    assert.match(refusal, /Refused: malformed task packet/);
    assert.match(refusal, /SPEC-SCOPE line/);
    assert.match(refusal, /WORKFLOW\.md § Task packets/);
  }
});

await check("parseTaskPacket: whitespace-only CLAIMS-PATHS entries are refused, naming the line", async () => {
  const cases = [
    "CLAIMS-PATHS: internal/a, , internal/b",
    "CLAIMS-PATHS: internal/a,",
    "CLAIMS-PATHS:   ",
  ];
  for (const line of cases) {
    const parsed = parseTaskPacket("Do the thing.\n" + line);
    assert.equal(parsed.ok, false, "refused: " + JSON.stringify(line));
    const joined = parsed.errors.join("\n");
    assert.match(joined, /CLAIMS-PATHS line/);
    assert.ok(joined.includes(line.trim()), "error names the offending line: " + joined);
    assert.match(formatTaskPacketRefusal(parsed.errors), /Refused: malformed task packet/);
  }
  // A whitespace-containing entry is malformed too (it can never be a path).
  const spaced = parseTaskPacket("CLAIMS-PATHS: internal/a b");
  assert.equal(spaced.ok, false);
  assert.match(spaced.errors.join("\n"), /contains whitespace/);
});

await check("subagent boundary: ONE packet parse refuses before any spawn", async () => {
  // The subagent execute() now lives in ./dispatch.ts (B3.4) and cannot be
  // imported headless (it imports the pi packages); smoke-sbx.mjs models wiring
  // checks like this at the source level. Assert the boundary shape instead: one
  // strict parse, an error tool result, and the env threading from the parsed
  // packet (no re-parse) — in the module that owns the tool.
  const source = fs.readFileSync(path.join(path.dirname(fileURLToPath(import.meta.url)), "dispatch.ts"), "utf8");
  assert.equal([...source.matchAll(/parseTaskPacket\(/g)].length, 1, "exactly one parseTaskPacket call site");
  assert.match(source, /TASK-PACKET BOUNDARY \(B3\.1\)/);
  const refusalAt = source.indexOf("text: formatTaskPacketRefusal(packetErrors)");
  assert.ok(refusalAt > 0, "boundary returns the refusal as the tool result");
  const resolveAt = source.indexOf("const resolvedChain = (params.chain ?? [])");
  assert.ok(resolveAt > refusalAt, "refusal runs before agent resolution");
  const claimsAt = source.indexOf("prepareSpawnClaims({");
  assert.ok(claimsAt > refusalAt, "refusal runs before any claim is written");
  const spawnAt = source.indexOf("await runSingleAgent(");
  assert.ok(spawnAt > refusalAt, "refusal runs before any spawn");
  // The validated env reaches the spawn; the task text is never re-parsed.
  assert.match(source, /chainPackets\[i\]\?\.scopeEnv/);
  assert.match(source, /taskPackets\[index\]\?\.scopeEnv/);
  assert.match(source, /singlePacket\?\.scopeEnv/);
  assert.doesNotMatch(source, /parseSpecScope\(task\)/);
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
await check("catalogHash: deterministic manifest hash over docs/spec/rules", async () => {
  const repo = path.join(tmp, "catalog-repo");
  fs.mkdirSync(repo, { recursive: true });
  assert.equal(await catalogHash(repo), "", "missing catalog dir ⇒ \"\" (fail-open)");

  writeRuleCatalogStub(repo);
  const first = await catalogHash(repo);
  assert.match(first, /^[0-9a-f]{64}$/);
  assert.equal(await catalogHash(repo), first, "same tree ⇒ same hash");

  // A dotfile is not catalog content; a real nested file is.
  fs.writeFileSync(path.join(repo, "docs", "spec", "rules", ".DS_Store"), "junk\n");
  assert.equal(await catalogHash(repo), first, "dotfiles are skipped");
  const nested = path.join(repo, "docs", "spec", "rules", "identity");
  fs.mkdirSync(nested, { recursive: true });
  fs.writeFileSync(path.join(nested, "R-IDENTITY-001.md"), STUB_RULE_FILE);
  const withNested = await catalogHash(repo);
  assert.notEqual(withNested, first, "a new rule file changes the manifest hash");

  // Same paths + same bytes ⇒ same hash regardless of write order.
  fs.rmSync(path.join(repo, "docs", "spec", "rules"), { recursive: true, force: true });
  fs.mkdirSync(nested, { recursive: true });
  fs.writeFileSync(path.join(nested, "R-IDENTITY-001.md"), STUB_RULE_FILE);
  writeRuleCatalogStub(repo);
  assert.equal(await catalogHash(repo), withNested, "the manifest is order-independent");

  // A content edit (same path) is caught.
  fs.appendFileSync(path.join(repo, "docs", "spec", "rules", "kernel", "R-KERNEL-001.md"), "\nedit\n");
  assert.notEqual(await catalogHash(repo), withNested, "content edits change the manifest hash");
});

await check("writeReceipt: JSONL line with sha/hash fields", async () => {
  const repo = path.join(tmp, "receipt-repo");
  writeRuleCatalogStub(repo);
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
  assert.equal(record.schema_version, 2, "receipts are catalog schema v2");
  assert.equal(RECEIPT_SCHEMA_VERSION, 2);
  assert.match(record.catalogHash, /^[0-9a-f]{64}$/);
  assert.equal(record.catalogHash, await catalogHash(repo), "the hash is the catalog manifest");
  assert.equal("rulesHash" in record, false, "the v1 rulesHash field is gone");
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
  // Every in-VM gate command exits non-zero: the failure must survive the
  // backend swap and still name the declared commands.
  const stub = installGateVmStub((args) => (args[0] === "exec" ? { exitCode: 2, stderr: "no rule to make target" } : {}));
  try {
    const run = await runScopedGate({
      cwd: empty,
      scopeEnv: "module=game",
      makeTimeoutMs: 20_000,
      specTimeoutMs: 20_000,
    });
    assert.equal(run.ok, false, "non-zero in-VM exits ⇒ failure");
    const commands = run.steps.map((s) => s.command);
    assert.ok(commands.includes("make audit/static"), "make audit/static attempted: " + commands.join(" | "));
    assert.ok(commands.some((c) => c.includes("--module game")), "scoped check attempted");
    assert.ok(commands[commands.length - 1].startsWith("go run ./cmd/spec check --strict"), "full fallback ran last");
    // Every step executed in the one gate VM for this worktree path.
    const gateSteps = run.steps.filter((step) => step.gateSandbox);
    assert.equal(gateSteps.length, run.steps.length, "every step names its gate VM");
    assert.equal(new Set(gateSteps.map((step) => step.gateSandbox)).size, 1, "one VM reused across the ladder");
    assert.equal(gateVmExecCalls(stub).length, run.steps.length, "one sbx exec per gate step");
    const report = formatGateReport(run, { header: "── SCOPED GATE FAILED ──", footer: "fix" });
    assert.match(report, /SCOPED GATE FAILED/);
  } finally {
    resetGateVmStub();
  }
});

await check("runScopedGate: unset scope ⇒ full check only", async () => {
  writeWorkflow(tmp);
  installGateVmStub();
  try {
    const run = await runScopedGate({ cwd: tmp, makeTimeoutMs: 10_000, specTimeoutMs: 10_000 });
    assert.equal(run.steps.length, 2);
    assert.equal(run.steps[1].command, "go run ./cmd/spec check --strict");
  } finally {
    resetGateVmStub();
  }
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
  // B3.4: policy.ts's DEFAULT_* are the single source (gates.ts keeps none).
  assert.equal(DEFAULT_SETTLE_MAX_ATTEMPTS, 5, "total attempts raised from 3 to 5");
  assert.equal(DEFAULT_SETTLE_BACKOFF_BASE_MS, 2000, "backoff base is 2s");
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
  // jobs.ts imports the pi package for CONFIG_DIR_NAME/getAgentDir. Under a
  // loader that cannot satisfy that bare specifier (jiti on a distro node
  // build without TS stripping), the native registerHooks stub above is not
  // consulted — degrade this ONE registry assertion to the filesystem layout
  // it describes instead of failing the whole suite. Native runs keep full
  // coverage.
  let jobs = null;
  try {
    jobs = await import("./jobs.ts");
  } catch (error) {
    if (!/pi-coding-agent/.test(String(error))) throw error;
    console.log("NOTE claims: jobs.ts needs the pi package under this loader — registry assertion degrades to the filesystem layout");
  }
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
  // The registry is the flat *.json set: the claims subdirectory is not a job.
  const flatJobFiles = fs.readdirSync(jobsDir).filter((name) => name.endsWith(".json")).sort();
  assert.deepEqual(flatJobFiles, ["job-0001.json"], "the claims directory is not a job id");
  if (jobs) {
    assert.deepEqual(jobs.listJobIdsSync(repo), ["job-0001"], "the claims directory is not a job id");
    assert.equal(jobs.jobsDirFor(repo), jobsDir);
  }
  // JobEntry survives a round trip with the batch-2 reconciliation field.
  if (jobs) assert.equal(typeof jobs.getJob, "function");
});

// ── Batch 2: reconciliation-lite ────────────────────────────────────────────

await check("diffReceiptAgainst: names each discrepancy, empty when matching", async () => {
  const receipt = {
    timestamp: "2026-01-01T00:00:00.000Z",
    agent: "worker",
    session: "s",
    commands: [{ command: "make audit/static", exitCode: 0, timedOut: false, durationMs: 5 }],
    schema_version: 2,
    catalogHash: "hash-a",
    headSha: "a".repeat(40),
    ok: true,
    note: "",
  };
  const current = {
    headSha: "a".repeat(40),
    catalogHash: "hash-a",
    commands: [{ command: "make audit/static", exitCode: 0, timedOut: false, durationMs: 7 }],
  };
  assert.deepEqual(diffReceiptAgainst(receipt, current), []);
  assert.deepEqual(diffReceiptAgainst(null, current), [], "no receipt ⇒ nothing to diff");

  const movedHead = diffReceiptAgainst(receipt, { ...current, headSha: "b".repeat(40) });
  assert.equal(movedHead.length, 1);
  assert.match(movedHead[0], /^headSha: receipt a{40}, now b{40}$/);

  // A v2 receipt's catalogHash mismatch is still a full discrepancy.
  const movedRules = diffReceiptAgainst(receipt, { ...current, catalogHash: "hash-b" });
  assert.equal(movedRules.length, 1);
  assert.match(movedRules[0], /^catalogHash: receipt hash-a, now hash-b$/);

  const flipped = diffReceiptAgainst(receipt, {
    ...current,
    commands: [{ command: "make audit/static", exitCode: 1, timedOut: false, durationMs: 7 }],
  });
  assert.match(flipped[0], /^make audit\/static: exit 0 recorded, exit 1 now$/);

  const missing = diffReceiptAgainst(receipt, { ...current, commands: [] });
  assert.match(missing[0], /not run now/);

  // ── v1 receipts are SUPERSEDED, not compared ──────────────────────────
  // A pre-v2 receipt hashed the deleted docs/spec/rules.yaml, so its hash can
  // never match the manifest hash. Exactly ONE note, even when the hashes
  // differ wildly; headSha and exit codes keep their meaning.
  const v1Receipt = {
    timestamp: "2026-01-01T00:00:00.000Z",
    agent: "worker",
    session: "s",
    commands: receipt.commands,
    rulesHash: "old-rules-yaml-hash",
    headSha: "a".repeat(40),
    ok: true,
    note: "",
  };
  const superseded = diffReceiptAgainst(v1Receipt, current);
  assert.equal(superseded.length, 1, "one superseded note: " + JSON.stringify(superseded));
  assert.match(superseded[0], /^catalogHash: receipt predates catalog schema v2 \u2014 superseded, not compared$/);

  // The same v1 receipt still reports headSha drift AND the one note.
  const supersededAndMoved = diffReceiptAgainst(v1Receipt, { ...current, headSha: "b".repeat(40) });
  assert.equal(supersededAndMoved.length, 2);
  assert.match(supersededAndMoved[0], /^headSha: /);
  assert.match(supersededAndMoved[1], /superseded, not compared$/);

  // schema_version 1 (explicitly old) is superseded too; 2 is not.
  assert.equal(diffReceiptAgainst({ ...receipt, schema_version: 1, catalogHash: "hash-b" }, current).length, 1);
  assert.match(
    diffReceiptAgainst({ ...receipt, schema_version: 1, catalogHash: "hash-b" }, current)[0],
    /superseded, not compared$/,
  );
  assert.match(
    diffReceiptAgainst({ ...receipt, catalogHash: "hash-b" }, current)[0],
    /^catalogHash: receipt hash-b, now hash-a$/,
  );

  // A failed reconciliation becomes a failing GateRun (shared report path).
  const run = reconciliationGateRun({ ok: false, report: "MISMATCH: headSha", steps: [], receipt: null });
  assert.equal(run.ok, false);
  assert.equal(run.steps[0].command, "(reconciliation)");
  assert.match(run.steps[0].stderr, /headSha/);
  assert.equal(reconciliationGateRun({ ok: true, report: "", steps: [], receipt: null }).ok, true);
});

await check("latestPassingReceipt: only passing receipts are checkpoints", async () => {
  const repo = path.join(tmp, "checkpoint-repo");
  writeRuleCatalogStub(repo);
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
  writeRuleCatalogStub(repo);
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

  const matching = await (async () => {
    installGateVmStub();
    try {
      return await runReconciliation(repo, {
        agent: "worker",
        makeTimeoutMs: 60_000,
        specTimeoutMs: 60_000,
      });
    } finally {
      resetGateVmStub();
    }
  })();
  assert.equal(matching.ok, true, "matching tree reconciles: " + matching.report);
  assert.match(matching.report, /status: ok/);
  assert.equal(matching.steps.some((step) => step.command === "make audit/static"), true);

  // Move HEAD without updating the receipt ⇒ headSha mismatch.
  fs.writeFileSync(path.join(repo, "extra.txt"), "x\n");
  gitExec(repo, ["add", "-A"]);
  gitExec(repo, ["commit", "-q", "-m", "move head"]);
  const drifted = await (async () => {
    installGateVmStub();
    try {
      return await runReconciliation(repo, { agent: "worker", makeTimeoutMs: 60_000, specTimeoutMs: 60_000 });
    } finally {
      resetGateVmStub();
    }
  })();
  assert.equal(drifted.ok, false, "stale checkpoint must not reconcile");
  assert.match(drifted.report, /MISMATCH headSha:/);
  assert.match(drifted.report, /status: MISMATCH/);
  assert.doesNotMatch(drifted.report, /catalogHash:/, "a v2 receipt's hash still matches the catalog");
  const asGateRun = reconciliationGateRun(drifted);
  assert.match(formatGateReport(asGateRun, { header: "── SETTLE GATE BLOCKED ──", footer: "fix" }), /RECONCILIATION/);
});

await check("runReconciliation: a v1 receipt (no schema_version) is superseded, not hash-mismatched", async () => {
  // The one-time transition: yesterday's receipt hashed the deleted
  // docs/spec/rules.yaml. HEAD and exit codes still line up, so the ONLY
  // discrepancy is the single superseded note — never a bogus hash mismatch.
  const repo = makeGitRepo(path.join(tmp, "recon-v1-repo"));
  writeWorkflow(repo);
  fs.writeFileSync(path.join(repo, "Makefile"), "audit/static:\n\t@true\n");
  fs.writeFileSync(path.join(repo, "go.mod"), "module reconv1tmp\n\ngo 1.21\n");
  fs.mkdirSync(path.join(repo, "cmd", "spec"), { recursive: true });
  fs.writeFileSync(path.join(repo, "cmd", "spec", "main.go"), "package main\n\nfunc main() {}\n");
  writeRuleCatalogStub(repo);
  gitExec(repo, ["add", "-A"]);
  gitExec(repo, ["commit", "-q", "-m", "gates"]);

  const head = gitExec(repo, ["rev-parse", "HEAD"]);
  const day = new Date().toISOString().slice(0, 10);
  fs.mkdirSync(path.dirname(receiptsFileFor(repo)), { recursive: true });
  fs.appendFileSync(
    receiptsFileFor(repo),
    JSON.stringify({
      timestamp: day + "T00:00:00.000Z",
      agent: "worker",
      session: "recon-v1",
      commands: [{ command: "make audit/static", exitCode: 0, timedOut: false, durationMs: 1 }],
      rulesHash: "legacy-rules-yaml-hash",
      headSha: head,
      ok: true,
      note: "v1 checkpoint",
    }) + "\n",
  );
  const checkpoint = await latestPassingReceipt(repo);
  assert.ok(checkpoint, "the v1 receipt is still a checkpoint");
  assert.equal(checkpoint?.schema_version, 0, "an absent schema_version reads as v1");
  assert.equal(checkpoint?.catalogHash, "", "a v1 receipt has no catalogHash");

  const superseded = await (async () => {
    installGateVmStub();
    try {
      return await runReconciliation(repo, { agent: "worker", makeTimeoutMs: 60_000, specTimeoutMs: 60_000 });
    } finally {
      resetGateVmStub();
    }
  })();
  assert.equal(superseded.ok, false, "the superseded note is reported, once: " + superseded.report);
  assert.match(superseded.report, /catalogHash: receipt predates catalog schema v2 \u2014 superseded, not compared/);
  assert.doesNotMatch(superseded.report, /MISMATCH headSha:/, "headSha still matches");
  assert.equal(superseded.report.match(/catalogHash:/g)?.length, 1, "exactly one catalogHash note");
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
  "receipts_dir: .pi/receipts",
  "claims_dir: .pi/jobs",
  "settle_max_attempts: 5",
  "settle_immediate_attempts: 3",
  "settle_backoff_base_ms: 2000",
  "settle_backoff_max_doublings: 5",
  "worker_stall_timeout_ms: 600000",
  "max_concurrent_workers: 8",
  "gate_file_paths:",
  "  - Makefile",
  "  - cmd/spec",
  "  - .github",
  "  - WORKFLOW.md",
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
  assert.deepEqual(p.gateFilePaths, ["Makefile", "cmd/spec", ".github", "WORKFLOW.md"]);
  assert.deepEqual(p.gateFilePaths, [...DEFAULT_GATE_FILE_PATHS]);
  assert.equal(loaded.declared.has("gate_file_paths"), true, "the declared list is marked present");
  assert.equal(loaded.declared.size, 17, "every declared field is marked present");
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

await check("policy: gate_file_paths defaults to the four canonical paths and feeds the integrity check", async () => {
  const dir = path.join(tmp, "policy-gate-paths-default");
  writeWorkflow(dir);
  const loaded = loadWorkflowPolicy(dir);
  assert.equal(loaded.ok, true, loaded.ok ? "" : loaded.reason);
  assert.deepEqual(loaded.policy.gateFilePaths, ["Makefile", "cmd/spec", ".github", "WORKFLOW.md"]);
  assert.equal(loaded.declared.has("gate_file_paths"), false, "an absent field is not declared");
  // The integrity check derives its git paths from the policy, so the undeclared
  // field must reproduce today's four paths exactly.
  assert.deepEqual(gateFilePathsFor(dir), [...DEFAULT_GATE_FILE_PATHS]);
});

await check("policy: a declared gate_file_paths list is what the integrity check covers", async () => {
  const dir = path.join(tmp, "policy-gate-paths-custom");
  writeWorkflow(dir, fieldsPolicy(["gate_file_paths:", "  - Makefile", "  - scripts/audit.sh", "  - WORKFLOW.md"]));
  const loaded = loadWorkflowPolicy(dir);
  assert.equal(loaded.ok, true, loaded.ok ? "" : loaded.reason);
  assert.deepEqual(loaded.policy.gateFilePaths, ["Makefile", "scripts/audit.sh", "WORKFLOW.md"]);
  assert.equal(loaded.declared.has("gate_file_paths"), true);
  assert.deepEqual(gateFilePathsFor(dir), ["Makefile", "scripts/audit.sh", "WORKFLOW.md"]);
  // The scalar spelling (comma separated) normalizes to the same entry list.
  writeWorkflow(dir, fieldsPolicy(["gate_file_paths: Makefile, scripts/audit.sh ,WORKFLOW.md"]));
  const scalar = loadWorkflowPolicy(dir);
  assert.equal(scalar.ok, true, scalar.ok ? "" : scalar.reason);
  assert.deepEqual(scalar.policy.gateFilePaths, ["Makefile", "scripts/audit.sh", "WORKFLOW.md"]);
});

await check("policy: an unsafe or empty gate_file_paths REFUSES (no silent fallback)", async () => {
  const dir = path.join(tmp, "policy-gate-paths-bad");
  const cases = [
    ["absolute", fieldsPolicy(["gate_file_paths:", "  - /etc/passwd"]), /not a repo-relative POSIX path/],
    ["absolute macos", fieldsPolicy(["gate_file_paths:", "  - /Users/me/repo/Makefile"]), /not a repo-relative POSIX path/],
    ["traversal", fieldsPolicy(["gate_file_paths:", "  - ../outside"]), /not a repo-relative POSIX path/],
    ["traversal segment", fieldsPolicy(["gate_file_paths:", "  - cmd/../../etc"]), /not a repo-relative POSIX path/],
    ["whitespace", fieldsPolicy(["gate_file_paths:", "  - cmd/spec dir"]), /not a repo-relative POSIX path/],
    ["empty entry", fieldsPolicy(["gate_file_paths:", "  - Makefile", "  -"]), /empty entry/],
    ["empty list", fieldsPolicy(["gate_file_paths:"]), /at least one repo-relative path/],
    ["empty scalar", fieldsPolicy(["gate_file_paths: \"\""]), /at least one repo-relative path/],
  ];
  for (const [name, body, pattern] of cases) {
    writeWorkflow(dir, body);
    const loaded = loadWorkflowPolicy(dir);
    assert.equal(loaded.ok, false, name + " must refuse");
    assert.match(loaded.reason, /gate_file_paths/);
    assert.match(loaded.reason, pattern);
  }
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
  writeWorkflow(dir, fieldsPolicy(["audit_static_cmd: ./audit.sh", "scoped_check_cmd: ./spec.sh"]));
  // Backend swap: the declared argv is what the GATE VM is asked to run. The
  // stub records the exec argc; the scripts themselves are host files that must
  // never be executed here (the VM would see them through its mount).
  const stub = installGateVmStub();
  try {
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
    // Each declared token is a separate in-VM argv entry — no shell re-parse.
    const execs = gateVmExecCalls(stub);
    assert.deepEqual(execs[0].slice(execs[0].indexOf("./audit.sh")), ["./audit.sh"]);
    const specAt = execs[3].indexOf("./spec.sh");
    assert.deepEqual(execs[3].slice(specAt), ["./spec.sh", "--rule", "R-A-1"]);
  } finally {
    resetGateVmStub();
  }
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

// ── PROVE coverage at merge-back ────────────────────────────────────────────
// The receipt the extension writes on a scoped `make prove` is the ONLY prove
// evidence; the merge-back gate (jobs.ts) refuses an approval without it, so
// these cases pin the coverage semantics rather than the git plumbing.
const PROVE_HEAD = "a".repeat(40);

/** A worktree fixture whose WORKFLOW.md loads (⇒ receipts_dir resolves). */
function proveWorktree(name) {
  const dir = path.join(tmp, name);
  writeWorkflow(dir);
  return dir;
}

/** Append one raw receipt record to `<dir>/.pi/receipts/<day>.jsonl`. */
function writeProveReceipt(dir, record, day = new Date().toISOString().slice(0, 10)) {
  const receipts = path.join(dir, ".pi", "receipts");
  fs.mkdirSync(receipts, { recursive: true });
  const full = {
    timestamp: day + "T00:00:00.000Z",
    agent: "orchestrator",
    session: "s-prove",
    commands: [],
    schema_version: RECEIPT_SCHEMA_VERSION,
    catalogHash: "hash",
    headSha: PROVE_HEAD,
    ok: true,
    note: "",
    ...record,
  };
  fs.appendFileSync(path.join(receipts, day + ".jsonl"), JSON.stringify(full) + "\n");
}

/** One green scoped prove command row for `rule` (the shape the writer records). */
function proveCommand(rule, exitCode = 0) {
  return {
    command: "cd ../lt-api-wt-job-1 && make prove args='--rule " + rule + "'",
    exitCode,
    timedOut: false,
    durationMs: 5,
  };
}

await check("checkProveCoverage: no receipts ⇒ every scoped rule missing", async () => {
  const dir = proveWorktree("prove-none");
  const result = await checkProveCoverage(dir, ["R-GAME-001", "R-GAME-002"], { expectedHeadSha: PROVE_HEAD });
  assert.equal(result.ok, false);
  assert.deepEqual(result.missing, ["R-GAME-001", "R-GAME-002"]);
  assert.deepEqual(result.covered, []);
  assert.match(result.report, /R-GAME-001/);
  assert.match(result.report, /R-GAME-002/);
  assert.match(result.report, /no prove receipt at all/);
  assert.match(result.report, /status: BLOCKED/);
});

await check("checkProveCoverage: unavailable policy refuses (fail closed)", async () => {
  const dir = path.join(tmp, "prove-no-policy");
  fs.mkdirSync(dir, { recursive: true });
  const result = await checkProveCoverage(dir, ["R-GAME-001"]);
  assert.equal(result.ok, false, "no policy ⇒ no receipts dir to trust");
  assert.deepEqual(result.missing, ["R-GAME-001"]);
  assert.match(result.report, /workflow policy unavailable/);
});

await check("checkProveCoverage: a failed prove run is not coverage", async () => {
  const dir = proveWorktree("prove-failed");
  writeProveReceipt(dir, {
    ok: false,
    note: "prove rules=R-GAME-001",
    commands: [proveCommand("R-GAME-001", 1)],
  });
  const result = await checkProveCoverage(dir, ["R-GAME-001"], { expectedHeadSha: PROVE_HEAD });
  assert.equal(result.ok, false);
  assert.deepEqual(result.missing, ["R-GAME-001"]);
  assert.match(result.report, /non-VERIFIED/);
});

await check("checkProveCoverage: green scoped receipts cover (all day files, both --rule forms)", async () => {
  const dir = proveWorktree("prove-covered");
  // Yesterday's file: jobs cross midnight, so the whole receipts dir is read.
  writeProveReceipt(dir, { note: "prove rules=R-GAME-001", commands: [proveCommand("R-GAME-001")] }, "2026-01-01");
  writeProveReceipt(dir, {
    note: "prove rules=R-GAME-002",
    commands: [
      {
        command: "go run ./cmd/spec prove --rule=R-GAME-002",
        exitCode: 0,
        timedOut: false,
        durationMs: 5,
      },
    ],
  });
  const result = await checkProveCoverage(dir, ["R-GAME-001", "R-GAME-002"], { expectedHeadSha: PROVE_HEAD });
  assert.equal(result.ok, true, result.report);
  assert.deepEqual(result.covered, ["R-GAME-001", "R-GAME-002"]);
  assert.deepEqual(result.missing, []);
  assert.match(result.report, /2 jsonl file\(s\)/);
  assert.match(result.report, /status: ok/);
});

await check("checkProveCoverage: a receipt at another commit is stale", async () => {
  const dir = proveWorktree("prove-stale");
  writeProveReceipt(dir, {
    headSha: "b".repeat(40),
    note: "prove rules=R-GAME-001",
    commands: [proveCommand("R-GAME-001")],
  });
  const stale = await checkProveCoverage(dir, ["R-GAME-001"], { expectedHeadSha: PROVE_HEAD });
  assert.equal(stale.ok, false, "prove must cover the exact reviewed commit");
  assert.deepEqual(stale.missing, ["R-GAME-001"]);
  assert.match(stale.report, /stale \(headSha mismatch/);
  // Without a binding the same receipt covers: the check is only as strict as asked.
  assert.equal((await checkProveCoverage(dir, ["R-GAME-001"])).ok, true);
});

await check("checkProveCoverage: rule ids match on a token boundary", async () => {
  const longerRecord = proveWorktree("prove-boundary-longer");
  writeProveReceipt(longerRecord, {
    note: "prove rules=R-GAME-0011",
    commands: [proveCommand("R-GAME-0011")],
  });
  const shorter = await checkProveCoverage(longerRecord, ["R-GAME-001"], { expectedHeadSha: PROVE_HEAD });
  assert.equal(shorter.ok, false, "R-GAME-0011 must not cover R-GAME-001");
  assert.match(shorter.report, /no prove receipt at all/);

  const shorterRecord = proveWorktree("prove-boundary-shorter");
  writeProveReceipt(shorterRecord, { note: "prove rules=R-GAME-001", commands: [proveCommand("R-GAME-001")] });
  const longer = await checkProveCoverage(shorterRecord, ["R-GAME-0011"], { expectedHeadSha: PROVE_HEAD });
  assert.equal(longer.ok, false, "R-GAME-001 must not cover R-GAME-0011");
});

await check("checkProveCoverage: an ordinary green receipt is not prove evidence", async () => {
  const dir = proveWorktree("prove-settle-only");
  writeProveReceipt(dir, {
    note: "settle gate attempt 1/5",
    commands: [proveCommand("R-GAME-001")],
  });
  const result = await checkProveCoverage(dir, ["R-GAME-001"], { expectedHeadSha: PROVE_HEAD });
  assert.equal(result.ok, false, "a green settle receipt does not launder into coverage");
  assert.deepEqual(result.missing, ["R-GAME-001"]);
  assert.match(result.report, /no prove receipt at all/);
});


// ── mergegate.ts: ONE job-scope resolver + ONE prove policy (defect 2) ──────
// Both merge-back consumers — the approval relay (jobs.ts
// `relayApprovalDecision`) and the merge-path prove block (index.ts) — call
// `resolveJobRuleIds`. Before this, the relay parsed the job's TASK TEXT while
// the merge path read the CLAIMS LOG, so "claim names rules, task text names
// none" could be relay-approved and merge-blocked on the identical merge-back.
// These cases pin the resolver's precedence, the identity of the two consumer
// answers, and the one-copy wiring (source-level, like the packet-boundary case
// above — index.ts cannot be imported headless).
const DELEGATE_DIR = path.dirname(fileURLToPath(import.meta.url));

/** A repo with a loadable policy, a job registry entry and an optional claim. */
async function mergeGateFixture(name, opts) {
  const repo = path.join(tmp, name);
  writeWorkflow(repo);
  const jobId = opts.jobId || "job-scope-1";
  // The registry entry: its `task` is the FALLBACK scope source only.
  const jobsDir = path.join(repo, ".pi", "jobs");
  fs.mkdirSync(jobsDir, { recursive: true });
  fs.writeFileSync(
    path.join(jobsDir, jobId + ".json"),
    JSON.stringify({ id: jobId, agent: "orchestrator", task: opts.task, startedAt: "2026-01-01T00:00:00.000Z", runner: "tmux", status: "awaiting_approval" }),
  );
  // The spawn claim: the CLAIMS-FIRST scope source.
  if (opts.claim) {
    await prepareSpawnClaims({
      cwd: repo,
      jobId,
      sessionId: "s-scope",
      requests: [{ worker: "w-scope", task: opts.claim }],
      baseline: [],
    });
  }
  return { repo, jobId };
}

await check("(b) resolveJobRuleIds: claim rules win when the TASK TEXT carries no SPEC-SCOPE", async () => {
  const { repo, jobId } = await mergeGateFixture("mergegate-claim-only", {
    task: "Do the thing. No machine line in this task text at all.",
    claim: "Implement it.\nSPEC-SCOPE: rules=R-CLAIM-001,R-CLAIM-002",
  });
  const rules = await resolveJobRuleIds(repo, jobId);
  assert.deepEqual(rules, ["R-CLAIM-001", "R-CLAIM-002"], "the claim is the scope");
  assert.equal((await resolveJobScope(repo, jobId)).source, "claim");

  // BOTH consumers resolve the SAME scope: the relay passes the entry it read
  // (`{ id }`), the merge path passes the env job id (a bare string).
  const relayScope = await resolveJobRuleIds(repo, { id: jobId });
  const mergePathScope = await resolveJobRuleIds(repo, jobId);
  assert.deepEqual(relayScope, mergePathScope, "relay and merge path see one scope");
  assert.deepEqual(relayScope, rules);

  // …and therefore reach the SAME verdict: no prove receipt yet ⇒ both refuse
  // and name the identical missing ids (relay: "PROVE receipts missing for this
  // merge-back"; merge path: "MERGE BLOCKED").
  const relayCoverage = await checkProveCoverage(repo, relayScope, { expectedHeadSha: PROVE_HEAD });
  const mergeCoverage = await checkProveCoverage(repo, mergePathScope, { expectedHeadSha: PROVE_HEAD });
  assert.equal(relayCoverage.ok, false);
  assert.deepEqual(relayCoverage.missing, mergeCoverage.missing);
  assert.deepEqual(relayCoverage.missing, ["R-CLAIM-001", "R-CLAIM-002"]);
  assert.ok(relayCoverage.report === mergeCoverage.report, "identical coverage report");

  // One scoped receipt covers its own rule only — the other stays missing.
  writeProveReceipt(repo, { note: "prove rules=R-CLAIM-001", commands: [proveCommand("R-CLAIM-001")] });
  const partial = await checkProveCoverage(repo, await resolveJobRuleIds(repo, jobId), { expectedHeadSha: PROVE_HEAD });
  assert.deepEqual(partial.covered, ["R-CLAIM-001"]);
  assert.deepEqual(partial.missing, ["R-CLAIM-002"]);
  writeProveReceipt(repo, { note: "prove rules=R-CLAIM-002", commands: [proveCommand("R-CLAIM-002")] });
  const full = await checkProveCoverage(repo, await resolveJobRuleIds(repo, jobId), { expectedHeadSha: PROVE_HEAD });
  assert.equal(full.ok, true, full.report);
  assert.deepEqual(full.covered, ["R-CLAIM-001", "R-CLAIM-002"]);
});

await check("resolveJobRuleIds: claims-first wins over a DISAGREEING task text", async () => {
  const { repo, jobId } = await mergeGateFixture("mergegate-disagree", {
    task: "Do the thing.\nSPEC-SCOPE: rules=R-TASK-009",
    claim: "SPEC-SCOPE: rules=R-CLAIM-003",
  });
  const scope = await resolveJobScope(repo, jobId);
  assert.equal(scope.source, "claim");
  assert.deepEqual(scope.ruleIds, ["R-CLAIM-003"], "the claim wins; the task text is not merged in");
  assert.deepEqual(await resolveJobRuleIds(repo, { id: jobId }), ["R-CLAIM-003"]);
});

await check("resolveJobRuleIds: registry task text is the fallback when the claim carries no scope", async () => {
  const { repo, jobId } = await mergeGateFixture("mergegate-task-fallback", {
    task: "Do the thing.\nSPEC-SCOPE: rules=R-TASK-001",
    claim: "No machine line in this claim's task.",
  });
  const scope = await resolveJobScope(repo, jobId);
  assert.equal(scope.source, "task");
  assert.deepEqual(scope.ruleIds, ["R-TASK-001"]);
  assert.deepEqual(await resolveJobRuleIds(repo, jobId), ["R-TASK-001"]);
});

await check("resolveJobRuleIds: no claim + no scope ⇒ no rules ⇒ no prove requirement", async () => {
  const { repo, jobId } = await mergeGateFixture("mergegate-no-scope", {
    task: "Do the thing. No machine line anywhere.",
  });
  const scope = await resolveJobScope(repo, jobId);
  assert.deepEqual(scope.ruleIds, []);
  assert.equal(scope.source, "none");
  // Both consumers early-return on an empty scope: nothing to prove.
  assert.deepEqual(await resolveJobRuleIds(repo, { id: jobId }), []);
  // And an unknown job id is the same answer, never a throw (fail-open-empty).
  assert.deepEqual(await resolveJobRuleIds(repo, "no-such-job"), []);
  assert.deepEqual(await resolveJobRuleIds(repo, ""), []);
  assert.deepEqual(await resolveJobRuleIds(repo, "../escape"), []);
  const none = await checkProveCoverage(repo, [], {});
  assert.equal(none.ok, true);
  assert.match(none.report, /needs no prove receipts/);
});

await check("mergegate: prove-receipt matching is ONE regex (argv scan, per-rule matcher, prove segment, prove note)", async () => {
  // The scanner and the per-rule matcher come from one source string.
  assert.deepEqual(proveRuleIdsIn("make prove args='--rule R-A-1'"), ["R-A-1"]);
  assert.deepEqual(proveRuleIdsIn("make prove args='--rule=R-A-1 --rule R-A-2'"), ["R-A-1", "R-A-2"]);
  assert.deepEqual(proveRuleIdsIn("make prove args='--rule=R-A-1 --rule=R-A-1'"), ["R-A-1"], "deduped");
  assert.deepEqual(proveRuleIdsIn("make audit/static"), []);
  // Token boundary, both directions, exactly as the coverage check enforces it.
  assert.equal(ruleFlagPattern("R-A-1").test("make prove args='--rule R-A-11'"), false);
  assert.equal(ruleFlagPattern("R-A-11").test("make prove args='--rule R-A-1'"), false);
  assert.equal(ruleFlagPattern("R-A-1").test("make prove args='--rule R-A-1'"), true);
  assert.equal(ruleFlagPattern("R-A-1").test("make prove args='--rule=R-A-1'"), true);
  // Prove-segment detection is word-exact on the DECLARED tokens.
  assert.equal(isProveSegment("make prove args='--rule R-A-1'", ["make", "prove"]), true);
  assert.equal(isProveSegment("make proven", ["make", "prove"]), false);
  assert.equal(isProveSegment("echo make prove", ["make", "prove"]), false);
  assert.equal(isProveSegment("make prove", []), false, "no declared prove command ⇒ never prove");
  // Only an extension-written "prove …" note is prove evidence.
  assert.equal(isProveReceipt({ note: "prove rules=R-A-1" }), true);
  assert.equal(isProveReceipt({ note: "settle gate attempt 1/5" }), false);
  assert.equal(isProveReceipt({ note: "  PROVE rules=R-A-1" }), true);
  // Source-level: exactly ONE place builds the `--rule` regex.
  const tsFiles = fs.readdirSync(DELEGATE_DIR).filter((name) => name.endsWith(".ts"));
  const offenders = [];
  for (const name of tsFiles) {
    const source = fs.readFileSync(path.join(DELEGATE_DIR, name), "utf8");
    const hits = [...source.matchAll(/--rule\(\?:=/g)].length;
    if (hits > 0) offenders.push(name + ":" + String(hits));
  }
  assert.deepEqual(offenders, ["mergegate.ts:1"], "the `--rule` matcher lives in one place");
});

await check("mergegate: both merge-back consumers call the ONE resolver (source-level wiring)", async () => {
  const indexSource = fs.readFileSync(path.join(DELEGATE_DIR, "index.ts"), "utf8");
  const jobsSource = fs.readFileSync(path.join(DELEGATE_DIR, "jobs.ts"), "utf8");
  const gatesSource = fs.readFileSync(path.join(DELEGATE_DIR, "gates.ts"), "utf8");
  const mergegateSource = fs.readFileSync(path.join(DELEGATE_DIR, "mergegate.ts"), "utf8");

  // Consumer B (merge execution path). The hook (and the whole prove policy)
  // lives in mergegate.ts since B3.4; the root only registers it.
  assert.match(mergegateSource, /const rules = await resolveJobRuleIds\(ctx\.cwd, jobId\);/, "the merge hook resolves via mergegate");
  assert.match(indexSource, /registerMergePathProveHook\(pi\)/, "the root only registers the merge hook");
  // Consumer A (approval relay).
  assert.match(jobsSource, /const scopedRules = await resolveJobRuleIds\(cwd, entry\.id\);/, "the relay resolves via mergegate");
  // No private scope derivation left in either consumer.
  assert.equal([...indexSource.matchAll(/parseSpecScope\(/g)].length, 0, "index.ts derives no scope of its own");
  assert.equal([...jobsSource.matchAll(/parseSpecScope\(/g)].length, 0, "jobs.ts derives no scope of its own");
  assert.doesNotMatch(indexSource, /recordedScopedRules/, "the old merge-path scope reader is gone");
  // The prove policy has no second home.
  assert.equal([...gatesSource.matchAll(/function checkProveCoverage/g)].length, 0, "gates.ts owns no prove policy");
  assert.equal([...indexSource.matchAll(/function proveRuleIdsIn|function isProveSegment/g)].length, 0, "index.ts owns no prove matcher");
  assert.match(mergegateSource, /export async function resolveJobRuleIds/, "mergegate exports the resolver");
  assert.match(mergegateSource, /export async function checkProveCoverage/, "mergegate exports the coverage check");
});

// ── Gate VM execution backend ───────────────────────────────────────────────
// The authoritative gates no longer spawn on the host: they run via
// `sbx exec --no-input -w <worktree> <gate-vm> [-e …] <argv…>`. These pins are
// the contract for that swap.

await check("gate VM: declared argv runs via sbx exec with the git-safe env, no DSN for static", async () => {
  const dir = path.join(tmp, "gatevm-static");
  writeWorkflow(dir, FULL_POLICY_BODY);
  const stub = installGateVmStub();
  try {
    const step = await runDeclaredGateStep("make audit/static", { cwd: dir, timeoutMs: 12_345 });
    assert.equal(step.ok, true);
    assert.equal(step.command, "make audit/static");
    assert.match(step.gateSandbox, /^pi-gate-/);
    const env = gateVmExecEnv(dir);
    assert.equal(env.GIT_OPTIONAL_LOCKS, "0");
    assert.equal(env.GIT_CONFIG_KEY_0, "safe.directory");
    assert.equal(env.GIT_CONFIG_VALUE_0, "*");
    assert.equal(env.LT_API_TEST_DSN, undefined, "a static gate never gets the DSN");
    const execs = gateVmExecCalls(stub);
    assert.equal(execs.length, 1, "one sbx exec");
    assert.deepEqual(execs[0], execSandboxArgs(step.gateSandbox, path.resolve(dir), env, ["make", "audit/static"]));
    // The timeout budget rides the adapter (sbx exec has no timeout flag): the
    // host-side watchdog is this opts.timeoutMs.
    const execCall = stub.calls.find((call) => call.args[0] === "exec");
    assert.equal(execCall.opts.timeoutMs, 12_345, "host watchdog keeps the gate budget");
    // Template + sizing are the gate VM's, not a worker VM's.
    const create = stub.calls[0].args;
    assert.equal(create.includes("docker.io/library/pi-gate:v1"), true);
    assert.equal(create[create.indexOf("--cpus") + 1], "4");
    assert.equal(create[create.indexOf("--memory") + 1], "8g");
  } finally {
    resetGateVmStub();
  }
});

await check("gate VM: DB gate pre-sequence order is start → pg_isready → dropdb → createdb → gate", async () => {
  const dir = path.join(tmp, "gatevm-db");
  writeWorkflow(dir, FULL_POLICY_BODY);
  const stub = installGateVmStub();
  try {
    const step = await runDeclaredGateStep("make audit", { cwd: dir, db: true, timeoutMs: 90_000 });
    assert.equal(step.ok, true);
    const execs = gateVmExecCalls(stub).map((argv) => argv.slice(argv.indexOf(step.gateSandbox) + 1));
    assert.deepEqual(execs.map((argv) => argv[0]), ["pg-up", "pg_isready", "dropdb", "createdb", "make"]);
    assert.deepEqual(execs[0], ["pg-up"]);
    assert.deepEqual(execs[1], [
      "pg_isready", "-h", "/home/agent/pgsock", "-p", "5433", "-U", "lt_test", "-d", "lt_api_test",
    ]);
    assert.deepEqual(execs[2], [
      "dropdb", "--if-exists", "-h", "/home/agent/pgsock", "-p", "5433", "-U", "lt_test", "lt_api_test",
    ]);
    assert.deepEqual(execs[3], [
      "createdb", "-h", "/home/agent/pgsock", "-p", "5433", "-U", "lt_test", "-O", "lt_test", "lt_api_test",
    ]);
    assert.deepEqual(execs[4], ["make", "audit", "LT_API_TEST_DSN=" + GATE_DB_DSN]);
    // The DSN rides the make COMMAND LINE (beats `-include .envrc`'s makefile
    // re-export); the -e env pairs carry no DSN at all.
    const gateArgv = gateVmExecCalls(stub)[4];
    const eValues = gateArgv.filter((_, i) => gateArgv[i - 1] === "-e");
    assert.ok(!eValues.some((v) => String(v).startsWith("LT_API_TEST_DSN=")), "no DSN via -e env");
    assert.ok(execs[4].includes("LT_API_TEST_DSN=" + GATE_DB_DSN), "DSN is a make command-line override");
    // Setup clients authenticate with PGPASSWORD; the gate itself does not.
    const dropArgv = gateVmExecCalls(stub)[2];
    assert.ok(dropArgv.includes("PGPASSWORD=test"));
    // Reuse: exactly ONE create for the whole sequence (and later gates).
    assert.equal(stub.calls.filter((call) => call.args[0] === "create").length, 1, "session-scoped reuse");
  } finally {
    resetGateVmStub();
  }
});

await check("gate VM: DB pre-sequence failure is a failed gate with a named step", async () => {
  const dir = path.join(tmp, "gatevm-db-fail");
  writeWorkflow(dir, FULL_POLICY_BODY);
  const stub = installGateVmStub((args) => {
    if (args[0] === "exec" && args.includes("pg_isready")) {
      return { exitCode: 2, stderr: "no response from server" };
    }
    return {};
  });
  try {
    const prep = await prepareGateDbVm(dir, { readyAttempts: 2, intervalMs: 0 });
    assert.equal(prep.ok, false);
    assert.ok(prep.step, "a failing step is returned");
    assert.equal(prep.step.argv[0], "pg_isready");
    assert.match(prep.step.reason, /exited with code 2/);
    assert.match(prep.step.note, /db pre-sequence/);
    // start ran once; readiness was retried exactly twice; drop/create never ran.
    const execs = gateVmExecCalls(stub);
    assert.equal(execs.filter((argv) => argv.includes("pg_isready")).length, 2);
    assert.equal(execs.some((argv) => argv.includes("dropdb")), false);
    assert.equal(GATE_PG_READY_ATTEMPTS > 1, true);
  } finally {
    resetGateVmStub();
  }
});

await check("gate VM: exec watchdog (timeout / infra failure) is a failed gate, never a pass", async () => {
  const dir = path.join(tmp, "gatevm-watchdog");
  writeWorkflow(dir, FULL_POLICY_BODY);
  installGateVmStub((args) => {
    if (args[0] === "exec") return { exitCode: null, timedOut: true };
    return {};
  });
  try {
    const timedOut = await runDeclaredGateStep("make audit/static", { cwd: dir, timeoutMs: 500 });
    assert.equal(timedOut.ok, false);
    assert.equal(timedOut.timedOut, true);
    assert.match(timedOut.reason, /timed out after 500ms/);
  } finally {
    resetGateVmStub();
  }
  installGateVmStub((args) => {
    if (args[0] === "exec") return { exitCode: 125, stderr: "sandbox is not running" };
    return {};
  });
  try {
    const infra = await runDeclaredGateStep("make audit/static", { cwd: dir, timeoutMs: 500 });
    assert.equal(infra.ok, false);
    assert.match(infra.reason, /could not run the command/);
    assert.equal(infra.gateSandbox !== undefined, true, "the VM that failed is named");
  } finally {
    resetGateVmStub();
  }
});

await check("gate VM: audit_cmd / prove_cmd helpers use the DB-backed plane", async () => {
  const dir = path.join(tmp, "gatevm-declared");
  writeWorkflow(dir, FULL_POLICY_BODY);
  const stub = installGateVmStub();
  try {
    const audit = await runAuditGate({ cwd: dir, timeoutMs: 10_000 });
    assert.equal(audit.ok, true);
    assert.equal(audit.steps[0].command, "make audit LT_API_TEST_DSN=" + GATE_DB_DSN);
    assert.equal(audit.steps[0].gateSandbox !== undefined, true);
    const prove = await runProveGate({ cwd: dir, args: "--rule R-GAME-001", timeoutMs: 10_000 });
    assert.equal(prove.ok, true);
    assert.equal(prove.steps[0].command, "make prove args=--rule R-GAME-001 LT_API_TEST_DSN=" + GATE_DB_DSN);
    // Both reused the ONE gate VM, and both ran the DB pre-sequence.
    assert.equal(stub.calls.filter((call) => call.args[0] === "create").length, 1);
    const execs = gateVmExecCalls(stub).map((argv) => argv.slice(argv.indexOf(audit.steps[0].gateSandbox) + 1));
    assert.deepEqual(execs.map((argv) => argv[0]), [
      "pg-up", "pg_isready", "dropdb", "createdb", "make",
      "pg-up", "pg_isready", "dropdb", "createdb", "make",
    ]);
    assert.deepEqual(execs[9], ["make", "prove", "args=--rule R-GAME-001", "LT_API_TEST_DSN=" + GATE_DB_DSN]);
  } finally {
    resetGateVmStub();
  }
});

await check("gate VM: host shell gate commands are recognised (cd worktree included), compounds are not", async () => {
  const dir = path.join(tmp, "resolve-declared");
  writeWorkflow(dir, FULL_POLICY_BODY);
  const audit = resolveDeclaredGateCommand("make audit", dir);
  assert.deepEqual({ kind: audit.kind, db: audit.db, cwd: audit.cwd, argv: audit.argv }, {
    kind: "audit", db: true, cwd: dir, argv: ["make", "audit"],
  });
  const stat = resolveDeclaredGateCommand("make audit/static", dir);
  assert.equal(stat.kind, "audit_static");
  assert.equal(stat.db, false);
  const prove = resolveDeclaredGateCommand("make prove args='--rule R-GAME-001'", dir);
  assert.equal(prove.kind, "prove");
  assert.equal(prove.db, true);
  assert.deepEqual(prove.argv, ["make", "prove", "args=--rule R-GAME-001"]);
  const scoped = resolveDeclaredGateCommand("go run ./cmd/spec check --strict --rule R-1", dir);
  assert.equal(scoped.kind, "scoped_check");
  assert.equal(scoped.db, false);
  const relocated = resolveDeclaredGateCommand("cd ../lt-api-wt-job && make audit", dir);
  assert.equal(relocated.cwd, path.resolve(dir, "../lt-api-wt-job"));
  assert.deepEqual(relocated.argv, ["make", "audit"]);
  // Not the declared invocation: no rewrite, so the host stays out of scope.
  assert.equal(resolveDeclaredGateCommand("make tidy", dir), null);
  assert.equal(resolveDeclaredGateCommand("make audit && rm -rf /tmp/x", dir), null);
  assert.equal(resolveDeclaredGateCommand("make audit > /tmp/log", dir), null);
  assert.equal(resolveDeclaredGateCommand("make proven args=--rule R-1", dir), null);
  assert.equal(resolveDeclaredGateCommand("cd a && cd b && make audit", dir), null);
});

await check("gate VM: the rewritten shell line is an argv-safe sbx exec", async () => {
  assert.equal(shellQuote("make"), "make");
  assert.equal(shellQuote("args=--rule R-GAME-001"), "'args=--rule R-GAME-001'");
  assert.equal(shellQuote("a'b"), "'a'\\''b'");
  const line = gateVmShellCommand(
    "pi-gate-abc123-dead",
    "/host/wt",
    { GIT_OPTIONAL_LOCKS: "0" },
    ["make", "prove", "args=--rule R-GAME-001"],
  );
  assert.equal(
    line,
    "sbx exec --no-input -w /host/wt -e GIT_OPTIONAL_LOCKS=0 pi-gate-abc123-dead make prove 'args=--rule R-GAME-001'",
  );
  assert.equal(line.includes("&&"), false, "never a host-shell compound");
  // The post-exec receipt path unwraps the rewritten line: it must recover the
  // sandbox, the worktree, the env, and the in-VM argv verbatim.
  const parsed = parseGateVmShellCommand(line);
  assert.equal(parsed.sandbox, "pi-gate-abc123-dead");
  assert.equal(parsed.worktree, "/host/wt");
  assert.deepEqual(parsed.env, { GIT_OPTIONAL_LOCKS: "0" });
  assert.deepEqual(parsed.argv, ["make", "prove", "args=--rule R-GAME-001"]);
  assert.equal(parseGateVmShellCommand("make prove"), null, "only the gate-VM shape parses");
});

await check("gate VM: DSN / db constants mirror the Makefile's test database", async () => {
  assert.equal(GATE_DB_DSN, "postgres://lt_test:test@127.0.0.1:5433/lt_api_test?sslmode=disable");
  assert.equal(GATE_DB_NAME, "lt_api_test");
  assert.equal(GATE_DB_USER, "lt_test");
  const steps = gateDbPresteps();
  assert.deepEqual(steps.map((step) => step.argv[0]), ["pg-up", "pg_isready", "dropdb", "createdb"]);
  assert.equal(steps[2].argv.includes("--if-exists"), true);
  assert.equal(steps[3].argv.includes("--if-exists"), false);
  assert.deepEqual(steps[2].env, { PGPASSWORD: "test" });
});

await check("receipts: the optional gate sandbox name is additive, not a shape change", async () => {
  const repo = path.join(tmp, "receipt-sandbox");
  fs.mkdirSync(repo, { recursive: true });
  const plain = await writeReceipt({
    cwd: repo,
    agent: "worker",
    commands: [{ command: "make audit", exitCode: 1, timedOut: false, durationMs: 4 }],
    ok: false,
    note: "plain",
  });
  const plainRecord = JSON.parse(fs.readFileSync(plain, "utf8").trim().split("\n").pop());
  assert.equal("sandbox" in plainRecord, false, "existing shape untouched when unknown");
  const gated = await writeReceipt({
    cwd: repo,
    agent: "worker",
    commands: [{ command: "make audit", exitCode: 0, timedOut: false, durationMs: 4 }],
    ok: true,
    note: "gate vm",
    sandbox: "pi-gate-abc123-dead",
  });
  const gatedRecord = JSON.parse(fs.readFileSync(gated, "utf8").trim().split("\n").pop());
  assert.equal(gatedRecord.sandbox, "pi-gate-abc123-dead");
  assert.deepEqual(Object.keys(gatedRecord.commands[0]).sort(), ["command", "durationMs", "exitCode", "timedOut"]);
});

fs.rmSync(tmp, { recursive: true, force: true });
console.log("\n" + String(checks - failures) + "/" + String(checks) + " checks passed");
process.exit(failures === 0 ? 0 : 1);
// Standalone smoke test for .pi/extensions/delegate/sbx.ts (the Docker
// Sandboxes adapter) plus the host-side seams that only exist in the sandbox
// era: the worktree integrity check, the merge-command detector, and the
// host-only receipt / PROVE-coverage readers in ./gates.ts.
//
// Imports the REAL modules (Node 22.18+ strips the .ts types on import — no pi,
// no build step). NOTHING here touches the real `sbx` CLI or the network: every
// CLI call goes through the single injection point, `setSbxProcessOps`, and
// every filesystem fixture is a temp dir.
//
// Run:  node .pi/extensions/delegate/smoke-sbx.mjs
// Needs a Node built WITH TypeScript type stripping. Node 22.18+ release builds
// have it; some distro builds do not (they fail with ERR_NO_TYPESCRIPT or
// "Unknown file extension .ts"). There, load the suite through pi's bundled
// jiti — this exact invocation is verified:
//   node -e 'const {createJiti}=require("<pi-install>/node_modules/jiti/lib/jiti.cjs");\
//     createJiti(process.cwd()+"/.pi/extensions/delegate/smoke-sbx.mjs",{interopDefault:true})\
//       .import(process.cwd()+"/.pi/extensions/delegate/smoke-sbx.mjs")'
// (<pi-install> = e.g. /usr/local/share/npm-global/lib/node_modules/@earendil-works/pi-coding-agent)

import * as assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import { fileURLToPath } from "node:url";
import {
  GATE_SANDBOX_CPUS,
  GATE_SANDBOX_EGRESS_HOSTS,
  GATE_SANDBOX_MEMORY,
  GATE_SANDBOX_PROFILE,
  GATE_SANDBOX_ROLE,
  GATE_SANDBOX_TEMPLATE_NAME,
  SANDBOX_CONTEXT7_SECRET_SENTINEL_ENV,
  SANDBOX_CREATE_AGENT,
  SANDBOX_DEFAULT_CPUS,
  SANDBOX_DEFAULT_MEMORY,
  SANDBOX_ENV_FLAG,
  SANDBOX_KIT_FALLBACK_PATH,
  SANDBOX_LIGHT_CPUS,
  SANDBOX_LIGHT_MEMORY,
  SANDBOX_MOUNTLESS_WORKDIR,
  SANDBOX_PROFILES,
  SANDBOX_RELAY_EGRESS,
  SANDBOX_RESEARCHER_EGRESS_HOSTS,
  SANDBOX_SCRATCH_PREFIX,
  SANDBOX_TEMPLATE_NAME,
  SANDBOX_VAULT_PATH,
  SANDBOX_WORKER_EGRESS_HOSTS,
  SANDBOX_MODEL_EGRESS_HOSTS,
  SBX_EXIT_COULD_NOT_RUN,
  SBX_EXIT_MISSING_EXECUTABLE,
  classifySbxExit,
  createGateSandbox,
  createSandboxArgs,
  createWorkerSandbox,
  ensureGateSandbox,
  gateMountPlanFromCommonDir,
  gateSandboxFor,
  gateSandboxKey,
  listGateSandboxes,
  resetGateSandboxRegistryForTests,
  resolveGateMountPlan,
  teardownGateSandboxes,
  execInSandbox,
  execSandboxArgs,
  generatedSandboxName,
  getSbxProcessOps,
  isHostPassthroughRole,
  isSandboxedChild,
  policyAllowArgs,
  profileDenyHosts,
  profileForRole,
  sandboxWorkdir,
  setSbxProcessOps,
  teardownSandbox,
  workspaceArgsFor,
} from "./sbx.ts";
import {
  checkWorktreeIntegrity,
  detectMergeBackCommands,
  gateFilePathsFor,
  integrityRefusalReason,
  policyRefusalGateRun,
  receiptsDirFor,
  receiptsFileFor,
  runGate,
  splitTopLevelSegments,
} from "./gates.ts";
import { DEFAULT_GATE_FILE_PATHS } from "./policy.ts";
import { checkProveCoverage } from "./mergegate.ts";

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

const SUITE_DIR = path.dirname(fileURLToPath(import.meta.url));
const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "sbx-smoke-"));

// ── Stub plumbing (the ONE injection point: setSbxProcessOps) ──────────────

/** A full SbxProcessResult from a sparse patch. */
function sbxResult(args, patch = {}) {
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

/**
 * Install a stubbed SbxProcessOps and record every argv. `respond` maps an argv
 * to a partial result; returning an Error makes the CLI call THROW (the spawn-
 * error shape a real launch failure produces).
 */
function installStub(respond = () => ({}), existsFn = () => false) {
  const calls = [];
  const existsCalls = [];
  setSbxProcessOps({
    async run(args) {
      calls.push([...args]);
      const patch = respond(args);
      if (patch instanceof Error) throw patch;
      return sbxResult(args, patch ?? {});
    },
    exists(target) {
      existsCalls.push(target);
      return existsFn(target);
    },
  });
  return { calls, existsCalls };
}

// ── 1. Exit-code mapping ────────────────────────────────────────────────────

await check("classifySbxExit: 125/127 are infra, 0 is ok, other non-zero is the child's status", async () => {
  assert.equal(SBX_EXIT_COULD_NOT_RUN, 125);
  assert.equal(SBX_EXIT_MISSING_EXECUTABLE, 127);

  const couldNotRun = classifySbxExit(SBX_EXIT_COULD_NOT_RUN);
  assert.equal(couldNotRun.outcome, "infra-failure");
  assert.equal(couldNotRun.infraFailure, true);
  assert.match(couldNotRun.reason, /could not run the command/);
  assert.match(couldNotRun.reason, /125/);

  const missing = classifySbxExit(SBX_EXIT_MISSING_EXECUTABLE);
  assert.equal(missing.outcome, "infra-failure");
  assert.equal(missing.infraFailure, true);
  assert.match(missing.reason, /missing the executable/);
  assert.match(missing.reason, /127/);

  assert.deepEqual(classifySbxExit(0), { outcome: "ok", infraFailure: false, reason: "" });

  const childFailed = classifySbxExit(2);
  assert.equal(childFailed.outcome, "child-failed");
  assert.equal(childFailed.infraFailure, false);
  assert.match(childFailed.reason, /exited with code 2/);

  // Spawn error / timeout / abort are infrastructure, never a verdict.
  for (const patch of [{ spawnError: "spawn sbx ENOENT" }, { timedOut: true }, { aborted: true }]) {
    const classified = classifySbxExit(null, patch);
    assert.equal(classified.outcome, "infra-failure", JSON.stringify(patch));
    assert.equal(classified.infraFailure, true, JSON.stringify(patch));
  }
});

await check("classifySbxExit: infra classification is DISTINCT from a gate failure", async () => {
  // The outcome vocabulary has no "gate" member: infra and child status are the
  // only failure shapes, and they are different types.
  const outcomes = new Set([
    classifySbxExit(0).outcome,
    classifySbxExit(SBX_EXIT_COULD_NOT_RUN).outcome,
    classifySbxExit(SBX_EXIT_MISSING_EXECUTABLE).outcome,
    classifySbxExit(3).outcome,
  ]);
  assert.deepEqual([...outcomes].sort(), ["child-failed", "infra-failure", "ok"]);
  assert.notEqual(classifySbxExit(SBX_EXIT_COULD_NOT_RUN).outcome, classifySbxExit(1).outcome);
  assert.notEqual(classifySbxExit(SBX_EXIT_MISSING_EXECUTABLE).outcome, classifySbxExit(1).outcome);

  // A real gate failure carries ok:false and no infra notion at all.
  const gate = await runGate("sh", ["-c", "exit 1"], { cwd: tmp, timeoutMs: 5000 });
  assert.equal(gate.ok, false);
  assert.equal(gate.exitCode, 1);
  assert.equal("infraFailure" in gate, false, "a gate failure is not an infra failure");
  const refusal = policyRefusalGateRun("WORKFLOW.md not found");
  assert.equal(refusal.ok, false);
  assert.equal(refusal.steps[0].ok, false);
  assert.equal("infraFailure" in refusal, false);
});

// ── 2. Profile table ────────────────────────────────────────────────────────

await check("profileForRole: worker/general are RW worktree + the 4 egress hosts + sentinel env", async () => {
  for (const role of ["worker", "general"]) {
    const profile = profileForRole(role);
    assert.equal(profile.role, role);
    assert.equal(profile.hostPassthrough, false);
    assert.deepEqual(profile.mounts, [{ kind: "worktree", mode: "rw" }]);
    assert.deepEqual(profile.networkHosts, [...SANDBOX_WORKER_EGRESS_HOSTS]);
    assert.equal(profile.networkHosts.length, 4);
    assert.deepEqual(profile.networkHosts, [
      "opencode.ai:443",
      "proxy.golang.org:443",
      "sum.golang.org:443",
      "storage.googleapis.com:443",
    ]);
    assert.deepEqual(profile.secretSentinelEnvs, ["OPENCODE_GO_API_KEY"]);
    assert.deepEqual(profileDenyHosts(profile), [], "no create-time deny rules by default");
    assert.equal(profile.cpus, SANDBOX_DEFAULT_CPUS);
    assert.equal(profile.memory, SANDBOX_DEFAULT_MEMORY);
  }
});

await check("profileForRole: every role states explicit vCPUs/memory and an empty deny default", async () => {
  // A profile must never fall through to an sbx default: `--cpus 0` means ALL
  // host CPUs and an omitted memory means 50% of host RAM. Both are wrong for a
  // task VM, so the table carries a concrete value for every role.
  assert.deepEqual(Object.keys(SANDBOX_PROFILES).sort(), ["general", "librarian", "researcher", "scout", "worker"]);
  for (const [role, profile] of Object.entries(SANDBOX_PROFILES)) {
    assert.equal(profile.role, role, role);
    assert.equal(Number.isInteger(profile.cpus) && profile.cpus > 0, true, role + " cpus");
    assert.match(profile.memory, /^[0-9]+(m|g)$/, role + " memory");
    assert.deepEqual(profileDenyHosts(profile), [], role + " deny default");
  }
  // Read-only/mountless roles are model-call-bound, not build-bound: 2 CPU / 4g.
  assert.equal(SANDBOX_LIGHT_CPUS, 2);
  assert.equal(SANDBOX_LIGHT_MEMORY, "4g");
  for (const role of ["scout", "librarian", "researcher"]) {
    assert.equal(profileForRole(role).cpus, SANDBOX_LIGHT_CPUS, role);
    assert.equal(profileForRole(role).memory, SANDBOX_LIGHT_MEMORY, role);
  }
  for (const role of ["worker", "general"]) {
    assert.equal(profileForRole(role).cpus, 4, role);
    assert.equal(profileForRole(role).memory, "8g", role);
  }
  // No role is host passthrough any more (the researcher residual is closed).
  assert.deepEqual(
    Object.keys(SANDBOX_PROFILES).filter((role) => isHostPassthroughRole(role)),
    [],
  );
});

await check("profileForRole: scout is scratch RW + worktree :ro with NO task egress, librarian is vault RW + worktree :ro", async () => {
  const scout = profileForRole("scout");
  assert.equal(scout.hostPassthrough, false);
  // sbx rejects a read-only PRIMARY workspace, so the rw scratch dir is first
  // and the repo is the read-only second workspace: reads work, writes cannot
  // reach the repo.
  assert.deepEqual(scout.mounts, [
    { kind: "scratch", mode: "rw" },
    { kind: "worktree", mode: "ro" },
  ]);
  // Model provider only: a "no network" role still has to reach the model it
  // thinks with (an empty allowlist makes the VM die on its first turn).
  assert.deepEqual(scout.networkHosts, [...SANDBOX_MODEL_EGRESS_HOSTS]);
  assert.deepEqual(scout.secretSentinelEnvs, []);
  assert.deepEqual(profileDenyHosts(scout), []);
  assert.equal(scout.cpus, SANDBOX_LIGHT_CPUS);
  assert.equal(scout.memory, SANDBOX_LIGHT_MEMORY);

  const librarian = profileForRole("librarian");
  assert.equal(librarian.hostPassthrough, false);
  assert.deepEqual(librarian.mounts, [
    { kind: "vault", mode: "rw" },
    { kind: "worktree", mode: "ro" },
  ]);
  assert.deepEqual(librarian.networkHosts, [...SANDBOX_MODEL_EGRESS_HOSTS]);
  assert.deepEqual(librarian.secretSentinelEnvs, []);
  assert.deepEqual(profileDenyHosts(librarian), []);
  assert.equal(librarian.cpus, SANDBOX_LIGHT_CPUS);
  assert.equal(librarian.memory, SANDBOX_LIGHT_MEMORY);
});

await check("profileForRole: researcher is a MOUNTLESS sandbox (provider + host relay + context7.com, CONTEXT7 sentinel only)", async () => {
  const researcher = profileForRole("researcher");
  // The one un-sandboxed role is gone: it now runs in a VM with no workspace.
  assert.equal(researcher.hostPassthrough, false);
  assert.equal(isHostPassthroughRole("researcher"), false);
  assert.deepEqual(researcher.mounts, [], "genuinely mountless: no worktree, no vault, no scratch");
  assert.deepEqual(researcher.networkHosts, [...SANDBOX_RESEARCHER_EGRESS_HOSTS]);
  // Four egress hosts, one per capability the TOOLSET needs: the model
  // provider (+ its wildcard), the HOST codex search relay (web search, no VM
  // credential), and docs lookup (context7.com, @upstash/context7-pi).
  assert.deepEqual(researcher.networkHosts, [
    "opencode.ai:443",
    "*.opencode.ai:443",
    "localhost:8787",
    "context7.com:443",
  ]);
  assert.equal(researcher.networkHosts.length, 4);
  // The relay is pinned as a named constant: the profile and the README row
  // both spell it from here, so a port change moves one place.
  assert.equal(SANDBOX_RELAY_EGRESS, "localhost:8787");
  assert.equal(researcher.networkHosts.includes(SANDBOX_RELAY_EGRESS), true);
  // api.openai.com is GONE: web search spends the host codex subscription
  // through the relay, so no pay-as-you-go key may be reachable from a VM.
  assert.equal(researcher.networkHosts.includes("api.openai.com:443"), false);
  // One sentinel only: context7. Web search has no credential in the VM — the
  // relay holds it host-side and substitutes it upstream.
  assert.deepEqual(researcher.secretSentinelEnvs, ["CONTEXT7_API_KEY"]);
  assert.equal(SANDBOX_CONTEXT7_SECRET_SENTINEL_ENV, "CONTEXT7_API_KEY");
  assert.deepEqual(profileDenyHosts(researcher), []);
  assert.equal(researcher.cpus, SANDBOX_LIGHT_CPUS);
  assert.equal(researcher.memory, SANDBOX_LIGHT_MEMORY);

  // A new agent name must never silently acquire host reach.
  const unknown = profileForRole("brand-new-agent");
  assert.equal(unknown.hostPassthrough, false);
  assert.equal(unknown.role, "brand-new-agent");
  assert.deepEqual(unknown.mounts, [{ kind: "worktree", mode: "rw" }]);
  assert.deepEqual(unknown.networkHosts, [...SANDBOX_WORKER_EGRESS_HOSTS]);
  // …and the table is not mutated by the copy.
  assert.equal(profileForRole("worker").role, "worker");
});

await check("workspaceArgsFor: same absolute path, :ro suffix, scratch primary where the profile says so", async () => {
  const worktree = "/tmp/host/lt-api-wt-job-1";
  const vault = "/tmp/host/vault";
  const scratch = "/tmp/host/" + SANDBOX_SCRATCH_PREFIX + "pi-scout-abc123-dead-XXXXXX";
  assert.deepEqual(workspaceArgsFor(profileForRole("worker"), worktree, vault), [worktree]);
  // scout: disposable scratch dir RW FIRST (sbx needs a writable primary), the
  // worktree read-only second — never a rw repo workspace.
  assert.deepEqual(workspaceArgsFor(profileForRole("scout"), worktree, vault, scratch), [
    scratch,
    worktree + ":ro",
  ]);
  assert.throws(
    () => workspaceArgsFor(profileForRole("scout"), worktree, vault),
    /declares a scratch workspace but no scratch path/,
    "a scratch mount with no path must fail closed, not fall back to the worktree",
  );
  assert.deepEqual(workspaceArgsFor(profileForRole("librarian"), worktree, vault), [vault, worktree + ":ro"]);
  assert.deepEqual(workspaceArgsFor(profileForRole("researcher"), worktree, vault), []);
  // The default vault path is the librarian's real mount.
  assert.deepEqual(workspaceArgsFor(profileForRole("librarian"), worktree), [SANDBOX_VAULT_PATH, worktree + ":ro"]);
});

// ── 3. Argv shapes (recorded-realistic fixtures) ────────────────────────────

await check("createSandboxArgs: template create --no-input --cpus --memory, positional workspace", async () => {
  // NOTE: the adapter passes no `--pull` flag; the pinned shape below is what
  // sbx.ts actually emits (SBX_CLI_VERSION 0.54.0), asserted verbatim.
  const worktree = "/tmp/host/lt-api-wt-job-2";
  assert.deepEqual(
    createSandboxArgs({
      name: "pi-worker-abc123-dead",
      profile: profileForRole("worker"),
      worktreePath: worktree,
      reference: "template",
    }),
    [
      "create",
      "--no-input",
      "--name", "pi-worker-abc123-dead",
      "--no-progress",
      "--cpus", "4",
      "--memory", "8g",
      "-t", SANDBOX_TEMPLATE_NAME,
      SANDBOX_CREATE_AGENT,
      worktree,
    ],
  );
  // librarian: vault RW first, worktree :ro second, still positional
  assert.deepEqual(
    createSandboxArgs({
      name: "pi-librarian-abc123-dead",
      profile: profileForRole("librarian"),
      worktreePath: worktree,
      reference: "template",
      vaultPath: "/tmp/host/vault",
    }),
    [
      "create",
      "--no-input",
      "--name", "pi-librarian-abc123-dead",
      "--no-progress",
      "--cpus", "2",
      "--memory", "4g",
      "-t", SANDBOX_TEMPLATE_NAME,
      "shell",
      "/tmp/host/vault",
      worktree + ":ro",
    ],
  );
  // scout: the disposable scratch dir is the RW primary, the worktree :ro second
  const scratch = "/tmp/host/" + SANDBOX_SCRATCH_PREFIX + "pi-scout-abc123-dead-XXXXXX";
  assert.deepEqual(
    createSandboxArgs({
      name: "pi-scout-abc123-dead",
      profile: profileForRole("scout"),
      worktreePath: worktree,
      reference: "template",
      scratchPath: scratch,
    }),
    [
      "create",
      "--no-input",
      "--name", "pi-scout-abc123-dead",
      "--no-progress",
      "--cpus", "2",
      "--memory", "4g",
      "-t", SANDBOX_TEMPLATE_NAME,
      "shell",
      scratch,
      worktree + ":ro",
    ],
  );
  // researcher: MOUNTLESS — no workspace positional after the agent slot, so
  // the host worktree is never mounted into the VM. `--no-input` is a global
  // sbx flag: an unmatched egress/prompt fails instead of hanging the spawn.
  const researcher = createSandboxArgs({
    name: "pi-researcher-abc123-dead",
    profile: profileForRole("researcher"),
    worktreePath: worktree,
    reference: "template",
  });
  assert.deepEqual(researcher, [
    "create",
    "--no-input",
    "--name", "pi-researcher-abc123-dead",
    "--no-progress",
    "--cpus", "2",
    "--memory", "4g",
    "-t", SANDBOX_TEMPLATE_NAME,
    SANDBOX_CREATE_AGENT,
  ]);
  assert.equal(researcher.includes(worktree), false, "a mountless profile passes no workspace");
  // kit reference swaps the `-t <template>` pair AND the `shell` agent token for
  // the kit directory as the FIRST POSITIONAL (`sbx create AGENT|SANDBOX_KIT
  // [PATH...]`); the workspaces still follow it, in profile order. `--kit` is a
  // mixin-only flag and must not appear here.
  const kit = createSandboxArgs({
    name: "n",
    profile: profileForRole("worker"),
    worktreePath: worktree,
    reference: "kit",
    kitPath: "/host/repo/.pi/sandboxes/kit",
  });
  const kitSlot = kit.indexOf("--memory") + 2;
  assert.deepEqual(kit.slice(kitSlot, kitSlot + 1), ["/host/repo/.pi/sandboxes/kit"]);
  assert.equal(kit.includes("--kit"), false, "a kit goes in the agent slot, not --kit");
  assert.equal(kit.includes("shell"), false, "the kit dir replaces the agent token");
  assert.equal(kit.includes("-t"), false, "the kit reference is not a template reference");
  assert.equal(kit[kit.length - 1], worktree);
  // A mountless kit create has the kit dir as its ONLY positional.
  const kitResearcher = createSandboxArgs({
    name: "n",
    profile: profileForRole("researcher"),
    worktreePath: worktree,
    reference: "kit",
    kitPath: "/host/repo/.pi/sandboxes/kit",
  });
  assert.deepEqual(kitResearcher.slice(kitResearcher.indexOf("--memory") + 2), [
    "/host/repo/.pi/sandboxes/kit",
  ]);
  // The fallback is the checked-in kit DIRECTORY (pi-worker.yaml + its
  // stem-paired recipe). dist/ is gitignored and only exists on a host that
  // already ran build.sh (i.e. that has the template), so a packed artifact
  // would be absent exactly when the fallback is needed — and the v3 flow packs
  // nothing: the directory is the artifact.
  assert.equal(SANDBOX_KIT_FALLBACK_PATH, ".pi/sandboxes/kit");
});

await check("createSandboxArgs: denyNetworkHosts becomes repeated --deny-network before any allow rule", async () => {
  const worktree = "/tmp/host/lt-api-wt-job-deny";
  const denied = {
    ...profileForRole("worker"),
    role: "worker-denied",
    denyNetworkHosts: ["example.com", "ads.example.com"],
  };
  const argv = createSandboxArgs({
    name: "pi-worker-deny",
    profile: denied,
    worktreePath: worktree,
    reference: "template",
  });
  // sbx 0.54.0 `create --help`: `--deny-network strings` — one HOST per flag,
  // repeatable. Emitted on the create call itself, i.e. before the profile's
  // `sbx policy allow network` (which is a separate, later CLI call).
  const denyAt = argv.indexOf("--deny-network");
  assert.notEqual(denyAt, -1);
  assert.deepEqual(argv.slice(denyAt, denyAt + 4), [
    "--deny-network", "example.com",
    "--deny-network", "ads.example.com",
  ]);
  assert.equal(argv.filter((token) => token === "--deny-network").length, 2, "one flag per host");
  assert.ok(denyAt < argv.indexOf("-t"), "denies precede the template reference");
  assert.ok(denyAt < argv.indexOf(worktree), "denies precede the workspace positionals");
  // A profile with no list emits no flag at all (nb: `--no-input` still there).
  assert.equal(
    createSandboxArgs({ name: "n", profile: profileForRole("worker"), worktreePath: worktree, reference: "template" })
      .includes("--deny-network"),
    false,
  );
});

await check("execSandboxArgs: --no-input -w, one -e per env var, sandbox then the child argv", async () => {
  const worktree = "/tmp/host/lt-api-wt-job-3";
  const env = {
    PI_DELEGATE_CHILD: "1",
    PI_DELEGATE_ROLE: "worker",
    PI_DELEGATE_SPEC_SCOPE: "module=game rules=R-GAME-001",
    PI_DELEGATE_SANDBOX: "1",
  };
  const argv = execSandboxArgs("pi-worker-abc123-dead", worktree, env, ["pi", "--no-skills"]);
  assert.deepEqual(argv, [
    "exec",
    "--no-input",
    "-w", worktree,
    "-e", "PI_DELEGATE_CHILD=1",
    "-e", "PI_DELEGATE_ROLE=worker",
    "-e", "PI_DELEGATE_SPEC_SCOPE=module=game rules=R-GAME-001",
    "-e", "PI_DELEGATE_SANDBOX=1",
    "pi-worker-abc123-dead",
    "pi",
    "--no-skills",
  ]);
  // Exactly one -e per env var; the sandbox name is positional, right before argv.
  assert.equal(argv.filter((token) => token === "-e").length, Object.keys(env).length);
  // `--no-input` is emitted exactly once (it is a global flag; duplicating it is
  // a sign the subcommand and global spellings both crept in).
  assert.equal(argv.filter((token) => token === "--no-input").length, 1);
  const sandboxAt = argv.indexOf("pi-worker-abc123-dead");
  assert.equal(argv[sandboxAt + 1], "pi");
  // The in-VM pi invocation ends with the REAL skills switch (pi 1.5.2 has no
  // --skills=off: unknown --key=value flags are collected and skills stay on).
  assert.equal(argv[argv.length - 1], "--no-skills");
  assert.equal(argv.includes("--skills=off"), false, "--skills=off does not exist in pi 1.5.2");

  // A MOUNTLESS sandbox (researcher) has no copy of the host path, so `-w` is
  // the agent's home inside the image instead — an explicit directory that
  // exists, never the host worktree and never an sbx "primary workspace" the
  // VM does not have.
  const mountless = execSandboxArgs("pi-researcher-abc123-dead", SANDBOX_MOUNTLESS_WORKDIR, { PI_DELEGATE_SANDBOX: "1" }, ["pi"]);
  assert.equal(SANDBOX_MOUNTLESS_WORKDIR, "/home/agent");
  assert.deepEqual(mountless, [
    "exec",
    "--no-input",
    "-w", "/home/agent",
    "-e", "PI_DELEGATE_SANDBOX=1",
    "pi-researcher-abc123-dead",
    "pi",
  ]);
  // An empty workdir omits the flag altogether (no directory known at all).
  assert.equal(execSandboxArgs("s", "", {}, ["pi"]).includes("-w"), false);
  // The workdir decision is per-sandbox state, not a guess: an unknown sandbox
  // keeps the caller's path, a created mountless one gets the agent home.
  assert.equal(sandboxWorkdir("pi-worker-abc123-dead", worktree), worktree);
});

await check("policyAllowArgs: policy allow network --sandbox '<name>' 'host:port,…'", async () => {
  assert.deepEqual(policyAllowArgs("pi-worker-abc123-dead", SANDBOX_WORKER_EGRESS_HOSTS), [
    "policy",
    "allow",
    "network",
    "--sandbox",
    "pi-worker-abc123-dead",
    "opencode.ai:443,proxy.golang.org:443,sum.golang.org:443,storage.googleapis.com:443",
  ]);
  assert.deepEqual(policyAllowArgs("s", ["a:443"]), ["policy", "allow", "network", "--sandbox", "s", "a:443"]);
});

// ── 4. Lifecycle through the stubbed ops ────────────────────────────────────

await check("createWorkerSandbox: template create + per-sandbox egress, argv from the table", async () => {
  const repo = path.join(tmp, "create-ok");
  fs.mkdirSync(repo, { recursive: true });
  const worktree = path.join(repo, "wt");
  fs.mkdirSync(worktree, { recursive: true });
  const stub = installStub();

  const handle = await createWorkerSandbox({ role: "worker", worktreePath: worktree, cwd: repo, name: "pi-worker-fixed-1" });
  assert.equal(handle.name, "pi-worker-fixed-1");
  assert.equal(handle.role, "worker");
  assert.equal(handle.createdFrom, "template");
  assert.equal(handle.worktreePath, worktree);
  assert.deepEqual(handle.networkHosts, [...SANDBOX_WORKER_EGRESS_HOSTS]);
  assert.deepEqual(handle.profile.mounts, [{ kind: "worktree", mode: "rw" }]);
  assert.equal(handle.hasWorkspace, true, "a worktree profile has a workspace");
  assert.deepEqual(handle.denyNetworkHosts, []);
  assert.notEqual(stub.calls[0].indexOf("--no-input"), -1, "create is non-interactive");
  assert.equal(
    stub.calls[0].filter((token) => token === "--no-input").length,
    1,
    "created non-interactively exactly once",
  );

  assert.equal(stub.calls.length, 2, "create + one policy allow");
  assert.deepEqual(
    stub.calls[0],
    createSandboxArgs({
      name: "pi-worker-fixed-1",
      profile: profileForRole("worker"),
      worktreePath: worktree,
      reference: "template",
      kitPath: path.resolve(repo, SANDBOX_KIT_FALLBACK_PATH),
    }),
  );
  assert.deepEqual(stub.calls[1], policyAllowArgs("pi-worker-fixed-1", SANDBOX_WORKER_EGRESS_HOSTS));
  assert.equal(stub.existsCalls.length, 0, "no kit probe when the template works");
  setSbxProcessOps(null);
});

await check("createWorkerSandbox: scout adds only the model-provider egress rule", async () => {
  const repo = path.join(tmp, "create-scout");
  fs.mkdirSync(repo, { recursive: true });
  const stub = installStub();
  const handle = await createWorkerSandbox({ role: "scout", worktreePath: repo, cwd: repo, name: "pi-scout-fixed-1" });
  assert.equal(handle.createdFrom, "template");
  assert.equal(handle.hasWorkspace, true);
  assert.deepEqual(handle.networkHosts, [...SANDBOX_MODEL_EGRESS_HOSTS]);
  assert.equal(stub.calls.length, 2, "create + the one model-provider egress rule");
  assert.deepEqual(stub.calls[1], policyAllowArgs("pi-scout-fixed-1", SANDBOX_MODEL_EGRESS_HOSTS));
  // The primary workspace must be rw, so it is a host temp dir the adapter made;
  // the worktree stays the read-only second workspace.
  const create = stub.calls[0];
  assert.equal(create[create.length - 1], repo + ":ro", "scout mounts the worktree read-only");
  const scratch = create[create.length - 2];
  assert.equal(scratch, handle.scratchPath, "the handle records the scratch dir it created");
  assert.equal(path.dirname(scratch), path.resolve(os.tmpdir()));
  assert.equal(path.basename(scratch).startsWith(SANDBOX_SCRATCH_PREFIX), true);
  assert.equal(fs.statSync(scratch).isDirectory(), true, "the scratch dir exists before sbx mounts it");
  assert.equal(create.includes(repo), false, "the repo is never a read-write workspace");
  // Scout teardown removes the scratch dir with the VM (same stub: no real CLI).
  assert.deepEqual(await teardownSandbox("pi-scout-fixed-1", handle.scratchPath), {
    name: "pi-scout-fixed-1",
    stopped: true,
    removed: true,
    errors: [],
  });
  assert.equal(fs.existsSync(scratch), false, "teardown disposes of the scratch workspace");
  setSbxProcessOps(null);
});

await check("createWorkerSandbox: researcher is created MOUNTLESS with provider + relay + context7 egress", async () => {
  const repo = path.join(tmp, "create-researcher");
  fs.mkdirSync(repo, { recursive: true });
  const stub = installStub();
  const handle = await createWorkerSandbox({ role: "researcher", worktreePath: repo, cwd: repo, name: "pi-researcher-fixed-1" });
  assert.equal(handle.createdFrom, "template");
  assert.equal(handle.hasWorkspace, false, "no workspace mount at all");
  assert.equal(handle.scratchPath, undefined, "a mountless profile needs no scratch primary");
  assert.equal(handle.networkHosts.includes("localhost:8787"), true);
  assert.equal(handle.networkHosts.includes("api.openai.com:443"), false);
  assert.equal(handle.networkHosts.includes("context7.com:443"), true);
  assert.equal(stub.calls.length, 2, "create + one policy allow");
  // No workspace positional anywhere in the create argv: the repo path the
  // caller passed is used for nothing (there is nothing to mount).
  assert.deepEqual(
    stub.calls[0],
    createSandboxArgs({
      name: "pi-researcher-fixed-1",
      profile: profileForRole("researcher"),
      worktreePath: repo,
      reference: "template",
      kitPath: path.resolve(repo, SANDBOX_KIT_FALLBACK_PATH),
    }),
  );
  assert.equal(stub.calls[0].includes(repo), false, "the host worktree never reaches a mountless create");
  assert.equal(stub.calls[0].includes(SANDBOX_SCRATCH_PREFIX), false);
  assert.deepEqual(
    stub.calls[1],
    policyAllowArgs("pi-researcher-fixed-1", SANDBOX_RESEARCHER_EGRESS_HOSTS),
  );
  setSbxProcessOps(null);
});

await check("createWorkerSandbox: denyNetworkHosts ride the create call; a mountless exec omits -w", async () => {
  const repo = path.join(tmp, "create-deny");
  fs.mkdirSync(repo, { recursive: true });
  const stub = installStub();
  // Exercise the plumbing end to end: temporary deny entries on the researcher
  // profile, restored in the finally below (the table itself stays empty).
  const restore = profileForRole("researcher").denyNetworkHosts;
  profileForRole("researcher").denyNetworkHosts = ["blocked.example.com"];
  let handle;
  try {
    handle = await createWorkerSandbox({ role: "researcher", worktreePath: repo, cwd: repo, name: "pi-researcher-deny-1" });
  } finally {
    profileForRole("researcher").denyNetworkHosts = restore;
  }
  assert.deepEqual(handle.denyNetworkHosts, ["blocked.example.com"]);
  // create argv carries the deny; the allow rule is the LATER call, so the deny
  // is in force before any allow can be added.
  const create = stub.calls[0];
  const denyAt = create.indexOf("--deny-network");
  assert.notEqual(denyAt, -1);
  assert.deepEqual(create.slice(denyAt, denyAt + 2), ["--deny-network", "blocked.example.com"]);
  assert.deepEqual(stub.calls[1], policyAllowArgs("pi-researcher-deny-1", SANDBOX_RESEARCHER_EGRESS_HOSTS));
  assert.deepEqual(profileForRole("researcher").denyNetworkHosts, [], "the table still defaults to empty");

  // exec on the mountless VM: no -w, and the host path is nowhere in the argv.
  const execRun = await execInSandbox(handle.name, ["pi", "--no-skills"], {
    worktreePath: repo,
    env: { PI_DELEGATE_SANDBOX: "1" },
  });
  const execArgv = stub.calls[stub.calls.length - 1];
  assert.deepEqual(execArgv, [
    "exec",
    "--no-input",
    "-w", "/home/agent",
    "-e", "PI_DELEGATE_SANDBOX=1",
    "pi-researcher-deny-1",
    "pi",
    "--no-skills",
  ]);
  assert.equal(execArgv.includes(repo), false, "a mountless exec never hands the VM a host path");
  assert.equal(sandboxWorkdir(handle.name, repo), SANDBOX_MOUNTLESS_WORKDIR);
  assert.equal(execRun.sandbox, handle.name);

  // Teardown drops the recorded workspace shape: the name is unknown again.
  await teardownSandbox(handle.name);
  assert.equal(sandboxWorkdir(handle.name, repo), repo);

  // A workspaced role still gets `-w`, so the omission is profile-specific.
  const worker = await createWorkerSandbox({ role: "worker", worktreePath: repo, cwd: repo, name: "pi-worker-wd-1" });
  assert.equal(sandboxWorkdir(worker.name, repo), repo);
  await execInSandbox(worker.name, ["pi"], { worktreePath: repo, env: {} });
  assert.deepEqual(stub.calls[stub.calls.length - 1], [
    "exec", "--no-input", "-w", repo, worker.name, "pi",
  ]);
  setSbxProcessOps(null);
});

await check("createWorkerSandbox: template 125 falls back to the kit exactly once", async () => {
  const repo = path.join(tmp, "create-kit");
  fs.mkdirSync(repo, { recursive: true });
  const stub = installStub(
    (args) => (args.includes("-t") ? { exitCode: SBX_EXIT_COULD_NOT_RUN, stderr: "template not found" } : {}),
    () => true,
  );
  const handle = await createWorkerSandbox({ role: "worker", worktreePath: repo, cwd: repo, name: "pi-worker-kit-1" });
  assert.equal(handle.createdFrom, "kit");
  assert.equal(stub.calls.length, 3, "template attempt + kit attempt + egress rule");
  assert.deepEqual(stub.calls[2], policyAllowArgs("pi-worker-kit-1", SANDBOX_WORKER_EGRESS_HOSTS));
  assert.equal(stub.calls[0].includes("-t"), true);
  // fallback argv: kit dir in the agent slot, workspace after it, no --kit
  assert.deepEqual(
    stub.calls[1].slice(stub.calls[1].indexOf("--memory") + 2),
    [path.resolve(repo, SANDBOX_KIT_FALLBACK_PATH), repo],
  );
  assert.equal(stub.calls[1].includes("--kit"), false);
  assert.equal(stub.calls[1].includes("shell"), false);
  assert.equal(stub.existsCalls.length, 1, "the kit is probed before the fallback");
  setSbxProcessOps(null);
});

await check("createWorkerSandbox: create 125 + missing kit is a loud infra failure (no host fallback)", async () => {
  const repo = path.join(tmp, "create-nokit");
  fs.mkdirSync(repo, { recursive: true });
  const stub = installStub(() => ({ exitCode: SBX_EXIT_COULD_NOT_RUN, stderr: "sandbox template unavailable" }), () => false);
  await assert.rejects(
    createWorkerSandbox({ role: "worker", worktreePath: repo, cwd: repo, name: "pi-worker-nokit-1" }),
    (error) => {
      assert.match(error.message, /fallback kit is missing/);
      assert.match(error.message, /exit 125/);
      assert.match(error.message, /sandbox template unavailable/);
      return true;
    },
  );
  assert.equal(stub.calls.length, 1, "no kit create without a kit");
  assert.equal(stub.calls.some((args) => args[0] === "rm"), false, "nothing was created, nothing to tear down");
  setSbxProcessOps(null);
});

await check("createWorkerSandbox: a failed egress rule tears the VM down and fails loudly", async () => {
  const repo = path.join(tmp, "create-policy-fail");
  fs.mkdirSync(repo, { recursive: true });
  const stub = installStub((args) => (args[0] === "policy" ? { exitCode: 1, stderr: "proxy refused" } : {}));
  await assert.rejects(
    createWorkerSandbox({ role: "worker", worktreePath: repo, cwd: repo, name: "pi-worker-policy-1" }),
    /sbx policy allow network failed for sandbox pi-worker-policy-1/,
  );
  assert.equal(stub.calls.length, 3, "create + policy (failed) + rm");
  assert.deepEqual(stub.calls[2], ["rm", "--force", "pi-worker-policy-1"]);
  setSbxProcessOps(null);
});

await check("execInSandbox: ok / 125 / 127 map to the three outcomes", async () => {
  const worktree = "/tmp/host/lt-api-wt-job-4";
  const env = { PI_DELEGATE_CHILD: "1", PI_DELEGATE_ROLE: "worker", PI_DELEGATE_SANDBOX: "1" };
  const argv = ["pi", "--no-skills"];

  const okStub = installStub(() => ({ exitCode: 0, stdout: '{"type":"done"}\n' }));
  const okRun = await execInSandbox("pi-worker-exec-1", argv, { worktreePath: worktree, env });
  assert.equal(okRun.classification.outcome, "ok");
  assert.equal(okRun.ok, true);
  assert.equal(okRun.sandbox, "pi-worker-exec-1");
  assert.deepEqual(okRun.argv, argv);
  assert.match(okRun.stdout, /"type":"done"/);
  assert.deepEqual(okStub.calls[0], execSandboxArgs("pi-worker-exec-1", worktree, env, argv));
  assert.equal(okStub.calls[0][okStub.calls[0].length - 1], "--no-skills");
  setSbxProcessOps(null);

  installStub(() => ({ exitCode: SBX_EXIT_COULD_NOT_RUN, stderr: "sandbox is not running" }));
  const couldNotRun = await execInSandbox("pi-worker-exec-1", argv, { worktreePath: worktree, env });
  assert.equal(couldNotRun.classification.infraFailure, true);
  assert.equal(couldNotRun.ok, false);
  assert.match(couldNotRun.classification.reason, /could not run/);
  setSbxProcessOps(null);

  installStub(() => ({ exitCode: SBX_EXIT_MISSING_EXECUTABLE, stderr: "pi: not found" }));
  const missing = await execInSandbox("pi-worker-exec-1", argv, { worktreePath: worktree, env });
  assert.equal(missing.classification.infraFailure, true);
  assert.match(missing.classification.reason, /missing the executable/);
  // Neither infra case is a verdict about the worker's code.
  assert.notEqual(missing.classification.outcome, "child-failed");
  setSbxProcessOps(null);

  // A stubbed spawn failure is infra too, not a child status.
  installStub(() => ({ exitCode: null, spawnError: "spawn sbx ENOENT" }));
  const spawnFailed = await execInSandbox("pi-worker-exec-1", argv, { worktreePath: worktree, env });
  assert.equal(spawnFailed.classification.infraFailure, true);
  assert.match(spawnFailed.classification.reason, /could not be started/);
  setSbxProcessOps(null);
});

await check("execInSandbox: stream hooks receive stdout/stderr chunks", async () => {
  const chunks = { out: "", err: "" };
  installStub(async () => ({ exitCode: 0 }));
  // The stub does not stream; assert the hooks are FORWARDED to the ops layer
  // (the real defaultProcessOps calls them, so this is the wiring the test can
  // honestly pin without spawning).
  let seenOpts = null;
  setSbxProcessOps({
    async run(args, opts) {
      seenOpts = opts ?? {};
      opts?.onStdout?.("out-line\n");
      opts?.onStderr?.("err-line\n");
      return sbxResult(args, { exitCode: 0 });
    },
    exists: () => false,
  });
  await execInSandbox("pi-worker-exec-2", ["pi"], {
    worktreePath: "/tmp/host/wt",
    env: {},
    onStdout: (text) => {
      chunks.out += text;
    },
    onStderr: (text) => {
      chunks.err += text;
    },
  });
  assert.equal(chunks.out, "out-line\n");
  assert.equal(chunks.err, "err-line\n");
  assert.equal(typeof seenOpts.onStdout, "function");
  assert.equal(typeof seenOpts.onStderr, "function");
  setSbxProcessOps(null);
});

await check("teardownSandbox: stop then rm --force, on every exit path", async () => {
  const stub = installStub();
  const clean = await teardownSandbox("pi-worker-teardown-1");
  assert.deepEqual(clean, { name: "pi-worker-teardown-1", stopped: true, removed: true, errors: [] });
  assert.deepEqual(stub.calls, [["stop", "pi-worker-teardown-1"], ["rm", "--force", "pi-worker-teardown-1"]]);
  assert.equal(stub.calls[0][0], "stop", "stop runs before rm");
  setSbxProcessOps(null);

  // A THROWING stub (the spawn-error shape) never escapes teardown: both verbs
  // are still attempted and the failure is reported, not thrown.
  const throwing = installStub(() => {
    throw new Error("sbx exploded");
  });
  const crashed = await teardownSandbox("pi-worker-teardown-2");
  assert.equal(crashed.stopped, false);
  assert.equal(crashed.removed, false);
  assert.equal(crashed.errors.length, 2);
  assert.match(crashed.errors[0], /sbx stop threw: sbx exploded/);
  assert.match(crashed.errors[1], /sbx rm threw: sbx exploded/);
  assert.deepEqual(throwing.calls, [["stop", "pi-worker-teardown-2"], ["rm", "--force", "pi-worker-teardown-2"]]);
  setSbxProcessOps(null);

  // A non-zero verb is best effort: reported, still no throw, rm still runs.
  const failing = installStub((args) => (args[0] === "stop" ? { exitCode: 1, stderr: "no such sandbox" } : {}));
  const partial = await teardownSandbox("pi-worker-teardown-3");
  assert.equal(partial.stopped, false);
  assert.equal(partial.removed, true);
  assert.equal(partial.errors.length, 1);
  assert.match(partial.errors[0], /exit 1/);
  assert.deepEqual(failing.calls, [["stop", "pi-worker-teardown-3"], ["rm", "--force", "pi-worker-teardown-3"]]);
  setSbxProcessOps(null);
});

await check("generatedSandboxName: sbx-legal, role-tagged, unique per nonce/stamp", async () => {
  const fixed = 1_700_000_000_000;
  const a = generatedSandboxName("worker", fixed, () => 0.01);
  const b = generatedSandboxName("worker", fixed, () => 0.5);
  assert.match(a, /^pi-worker-[0-9a-z]{6}-[0-9a-z]{4}$/);
  assert.notEqual(a, b, "a different nonce yields a different name");
  const later = generatedSandboxName("worker", fixed + 1000, () => 0.01);
  assert.notEqual(a, later, "a different second yields a different name");
  assert.match(generatedSandboxName("Go Worker!!"), /^pi-go-worker-/);
  assert.match(generatedSandboxName(""), /^pi-worker-/, "empty role falls back to worker");
  assert.ok(generatedSandboxName("x".repeat(40)).length <= 63, "sbx name length cap");
});

await check("createWorkerSandbox: names are generated and unique when not supplied", async () => {
  const repo = path.join(tmp, "create-generated");
  fs.mkdirSync(repo, { recursive: true });
  installStub(() => ({}));
  const first = await createWorkerSandbox({ role: "worker", worktreePath: repo, cwd: repo });
  const second = await createWorkerSandbox({ role: "worker", worktreePath: repo, cwd: repo });
  setSbxProcessOps(null);
  assert.match(first.name, /^pi-worker-[0-9a-z]{6}-[0-9a-z]{4}$/);
  assert.match(second.name, /^pi-worker-[0-9a-z]{6}-[0-9a-z]{4}$/);
  assert.notEqual(first.name, second.name, "two VMs never share a name");
});

// ── 5. In-VM inert predicate ────────────────────────────────────────────────

await check("isSandboxedChild: the env marker decides, and SANDBOX_ENV_FLAG is PI_DELEGATE_SANDBOX", async () => {
  assert.equal(SANDBOX_ENV_FLAG, "PI_DELEGATE_SANDBOX");
  assert.equal(isSandboxedChild({ [SANDBOX_ENV_FLAG]: "1" }), true);
  assert.equal(isSandboxedChild({ PI_DELEGATE_SANDBOX: "1" }), true);
  assert.equal(isSandboxedChild({}), false, "a host session is not a sandboxed child");
  // Only the exact value counts: a stray "0"/"true" must not silence the host.
  assert.equal(isSandboxedChild({ [SANDBOX_ENV_FLAG]: "0" }), false);
  assert.equal(isSandboxedChild({ [SANDBOX_ENV_FLAG]: "true" }), false);
  assert.equal(isSandboxedChild({ [SANDBOX_ENV_FLAG]: "" }), false);
  // The single injection point is untouched by all of the above.
  assert.equal(typeof getSbxProcessOps().run, "function");
});

// ── 6. Host-side worktree integrity check ───────────────────────────────────

const MINIMAL_POLICY = "---\nversion: 1\n---\n\n# Workflow policy\n";

function git(dir, args) {
  return execFileSync(
    "git",
    ["-c", "user.name=Smoke Test", "-c", "user.email=smoke@example.com", "-c", "commit.gpgsign=false", ...args],
    { cwd: dir, encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] },
  );
}

/** A committed git repo holding the gate files + one non-gate file. */
function makeRepo(name) {
  const dir = path.join(tmp, name);
  fs.mkdirSync(dir, { recursive: true });
  git(dir, ["init", "-q"]);
  fs.writeFileSync(path.join(dir, "Makefile"), "audit:\n\t@echo ok\n");
  fs.writeFileSync(path.join(dir, "WORKFLOW.md"), MINIMAL_POLICY);
  fs.writeFileSync(path.join(dir, "README.md"), "# fixture\n");
  git(dir, ["add", "."]);
  git(dir, ["commit", "-q", "-m", "fixture"]);
  return dir;
}

await check("checkWorktreeIntegrity: a clean repo is clean and gates may run", async () => {
  const repo = makeRepo("integrity-clean");
  const result = await checkWorktreeIntegrity(repo);
  assert.equal(result.clean, true);
  assert.equal(result.refused, false);
  assert.deepEqual(result.paths, [...DEFAULT_GATE_FILE_PATHS]);
  assert.equal(result.steps.length, 2, "worktree check + index check");
  assert.equal(result.steps.every((step) => step.ok), true);
  assert.match(result.report, /status: clean/);
  assert.match(result.report, /git diff --exit-code HEAD -- Makefile cmd\/spec \.github WORKFLOW\.md/);
  assert.match(result.report, /git diff --cached --exit-code HEAD/);
});

await check("checkWorktreeIntegrity: a DECLARED gate_file_paths list drives the git paths", async () => {
  const dir = path.join(tmp, "integrity-declared-paths");
  fs.mkdirSync(path.join(dir, "scripts"), { recursive: true });
  fs.mkdirSync(path.join(dir, "cmd", "spec"), { recursive: true });
  git(dir, ["init", "-q"]);
  fs.writeFileSync(path.join(dir, "Makefile"), "audit:\n\t@echo ok\n");
  fs.writeFileSync(path.join(dir, "scripts", "audit.sh"), "#!/bin/sh\nexit 0\n");
  fs.writeFileSync(path.join(dir, "cmd", "spec", "gate.go"), "package main\n");
  fs.writeFileSync(
    path.join(dir, "WORKFLOW.md"),
    "---\nversion: 1\ngate_file_paths:\n  - Makefile\n  - scripts/audit.sh\n  - WORKFLOW.md\n---\n\n# policy\n",
  );
  git(dir, ["add", "."]);
  git(dir, ["commit", "-q", "-m", "fixture"]);

  const declared = ["Makefile", "scripts/audit.sh", "WORKFLOW.md"];
  const clean = await checkWorktreeIntegrity(dir);
  assert.deepEqual(clean.paths, declared, "the declared list is the coverage");
  assert.equal(clean.clean, true);
  assert.match(clean.report, /git diff --exit-code HEAD -- Makefile scripts\/audit\.sh WORKFLOW\.md/);
  assert.equal(clean.report.includes("cmd/spec"), false, "an undeclared path is not in the check");

  // A change under a FORMER gate path that the policy no longer declares is not
  // a gate-file change anymore: the declared list is the whole coverage.
  fs.appendFileSync(path.join(dir, "cmd", "spec", "gate.go"), "\n// touched\n");
  const uncovered = await checkWorktreeIntegrity(dir);
  assert.equal(uncovered.clean, true, "cmd/spec is not declared here");

  // A dirty DECLARED path is still refused, and the report names the list.
  fs.appendFileSync(path.join(dir, "scripts", "audit.sh"), "\n# poisoned\n");
  const dirty = await checkWorktreeIntegrity(dir);
  assert.equal(dirty.refused, true);
  assert.match(dirty.report, /DIRTY/);
  assert.match(dirty.report, /paths: Makefile scripts\/audit\.sh WORKFLOW\.md/);
  assert.match(integrityRefusalReason(dirty), /scripts\/audit\.sh/);
  assert.equal(gateFilePathsFor(dir).join(" "), declared.join(" "));
});

await check("checkWorktreeIntegrity: a dirty Makefile is refused and the report names it", async () => {
  const repo = makeRepo("integrity-dirty-makefile");
  fs.appendFileSync(path.join(repo, "Makefile"), "\naudit/static:\n\t@echo poisoned\n");
  const result = await checkWorktreeIntegrity(repo);
  assert.equal(result.clean, false);
  assert.equal(result.refused, true);
  assert.match(result.report, /DIRTY/);
  assert.match(result.report, /Makefile/);
  assert.match(result.report, /status: REFUSED/);
  assert.equal(result.steps[0].ok, false);
  assert.equal(result.steps[0].exitCode, 1);
});

await check("checkWorktreeIntegrity: a dirty WORKFLOW.md is refused", async () => {
  const repo = makeRepo("integrity-dirty-workflow");
  fs.appendFileSync(path.join(repo, "WORKFLOW.md"), "\naudit_static_cmd: true\n");
  const result = await checkWorktreeIntegrity(repo);
  assert.equal(result.refused, true);
  assert.match(result.report, /WORKFLOW\.md/);
});

await check("checkWorktreeIntegrity: a dirty INDEX alone is refused (the --cached step)", async () => {
  const repo = makeRepo("integrity-dirty-index");
  const makefile = path.join(repo, "Makefile");
  const original = fs.readFileSync(makefile, "utf8");
  fs.writeFileSync(makefile, original + "\nstaged: poison\n");
  git(repo, ["add", "Makefile"]);
  // Restore the WORKTREE to HEAD while leaving the staged blob in the index: the
  // first check is clean, the --cached check is the one that catches it.
  fs.writeFileSync(makefile, original);
  const result = await checkWorktreeIntegrity(repo);
  assert.equal(result.steps[0].ok, true, "worktree matches HEAD");
  assert.equal(result.steps[1].ok, false, "index does not");
  assert.equal(result.refused, true);
  assert.equal(result.clean, false);
  assert.match(result.report, /DIRTY/);
});

await check("checkWorktreeIntegrity: a NON-gate change does not block ordinary work", async () => {
  const repo = makeRepo("integrity-ordinary");
  fs.writeFileSync(path.join(repo, "README.md"), "# fixture\n\nchanged\n");
  fs.mkdirSync(path.join(repo, "internal"), { recursive: true });
  fs.writeFileSync(path.join(repo, "internal", "new.go"), "package internal\n");
  const result = await checkWorktreeIntegrity(repo);
  assert.equal(result.clean, true, "an ordinary code change is exactly what a worker does");
  assert.equal(result.refused, false);
  assert.match(result.report, /status: clean/);
});

await check("checkWorktreeIntegrity: not-a-repo fails CLOSED", async () => {
  const dir = path.join(tmp, "integrity-not-a-repo");
  fs.mkdirSync(dir, { recursive: true });
  fs.writeFileSync(path.join(dir, "Makefile"), "audit:\n\t@echo ok\n");
  const result = await checkWorktreeIntegrity(dir);
  assert.equal(result.clean, false);
  assert.equal(result.refused, true);
  assert.match(result.report, /CANNOT VERIFY/);
  assert.match(result.report, /status: REFUSED/);
  assert.equal(result.steps.every((step) => !step.ok), true);
});

await check("integrityRefusalReason: formats loudly and names the gate paths", async () => {
  const repo = makeRepo("integrity-refusal-text");
  fs.appendFileSync(path.join(repo, "Makefile"), "\npoison: true\n");
  const result = await checkWorktreeIntegrity(repo);
  const reason = integrityRefusalReason(result);
  assert.match(reason, /INTEGRITY CHECK FAILED/);
  assert.match(reason, /HANDOFF_BLOCKED/);
  assert.match(reason, /human review/);
  for (const gatePath of DEFAULT_GATE_FILE_PATHS) assert.ok(reason.includes(gatePath), "names " + gatePath);
  assert.match(reason, /git diff --exit-code HEAD/);
  assert.match(reason, /exited with code 1/);
  assert.match(reason, /No gate command was run/);
  // A clean result produces no refusal detail (the function is total).
  const clean = await checkWorktreeIntegrity(makeRepo("integrity-refusal-clean"));
  assert.match(integrityRefusalReason(clean), /INTEGRITY CHECK FAILED/);
  assert.equal(integrityRefusalReason(clean).includes("git diff --exit-code"), false);
});

// ── 7. Merge-back command detection ─────────────────────────────────────────

await check("detectMergeBackCommands: every merge spelling is detected", async () => {
  const ffOnly = detectMergeBackCommands("git merge --ff-only delegate/job-0001");
  assert.equal(ffOnly.length, 1);
  assert.equal(ffOnly[0].target, "delegate/job-0001");
  assert.equal(ffOnly[0].controlOnly, false);
  assert.equal(ffOnly[0].worktree, null);

  const inWorktree = detectMergeBackCommands("git -C /path/to/lt-api-wt-job-1 merge main");
  assert.equal(inWorktree.length, 1);
  assert.equal(inWorktree[0].worktree, "/path/to/lt-api-wt-job-1");
  assert.equal(inWorktree[0].target, "main");

  const quoted = detectMergeBackCommands('git merge "delegate/job 2"');
  assert.equal(quoted.length, 1);
  assert.equal(quoted[0].target, "delegate/job 2", "quotes group the target token");

  const quotedWorktree = detectMergeBackCommands("git -C '/tmp/lt-api-wt-3' merge --no-ff main");
  assert.equal(quotedWorktree.length, 1);
  assert.equal(quotedWorktree[0].worktree, "/tmp/lt-api-wt-3");
  assert.equal(quotedWorktree[0].target, "main");

  const chained = detectMergeBackCommands("cd ../lt-api-wt-job-4 && git merge delegate/job-0004");
  assert.equal(chained.length, 1);
  assert.equal(chained[0].target, "delegate/job-0004");
  assert.match(chained[0].segment, /^git merge/);

  const multiple = detectMergeBackCommands("git fetch origin && git merge --ff-only delegate/a; git merge b");
  assert.deepEqual(multiple.map((entry) => entry.target), ["delegate/a", "b"]);

  const newline = detectMergeBackCommands("git status\ngit merge delegate/c\n");
  assert.deepEqual(newline.map((entry) => entry.target), ["delegate/c"]);
});

await check("detectMergeBackCommands: ordinary git commands are not merges", async () => {
  for (const command of [
    "git diff --stat",
    "git commit -m 'merge the parser fix'",
    "git add -A",
    "git status",
    "git push origin main",
    "git checkout -b delegate/x",
    "git log --oneline | head -5",
    "make audit",
  ]) {
    assert.deepEqual(detectMergeBackCommands(command), [], command);
  }
  // A bare `git merge` with no target merges nothing.
  assert.deepEqual(detectMergeBackCommands("git merge").map((entry) => entry.controlOnly), []);
  // A leading cd alone is not a merge either.
  assert.deepEqual(detectMergeBackCommands("cd ../lt-api-wt-job-5 && make audit"), []);
});

await check("detectMergeBackCommands: control-only segments are flagged, never blocking", async () => {
  for (const control of ["--abort", "--continue", "--quit"]) {
    const found = detectMergeBackCommands("git merge " + control);
    assert.equal(found.length, 1);
    assert.equal(found[0].controlOnly, true, control);
    assert.equal(found[0].target, control);
    // …and the merge-path hook filters exactly this flag before blocking.
    assert.deepEqual(found.filter((entry) => !entry.controlOnly), []);
  }
  // A mention that cannot execute a merge is never an executable merge:
  // `echo '…'` is quoted, so the segment's command word is `echo`, not `git`.
  for (const command of ["echo 'git merge delegate/x'", 'echo "git -C /wt merge main"']) {
    const found = detectMergeBackCommands(command);
    assert.equal(found.filter((entry) => !entry.controlOnly).length, 0, command);
  }
  // The segment splitter is quote-aware, which is what protects the above.
  assert.deepEqual(splitTopLevelSegments("echo 'a && git merge b'"), ["echo 'a && git merge b'"]);
});

// ── 8. Receipts are HOST-only artifacts ─────────────────────────────────────

await check("receiptsDirFor / receiptsFileFor: resolved against the given root", async () => {
  const root = path.join(tmp, "receipts-host-root");
  fs.mkdirSync(root, { recursive: true });
  // No WORKFLOW.md yet: the default dir, still rooted at the given root.
  assert.equal(receiptsDirFor(root), path.join(root, ".pi", "receipts"));
  assert.equal(receiptsFileFor(root, "2026-01-01"), path.join(root, ".pi", "receipts", "2026-01-01.jsonl"));

  // Minimal policy (no receipts_dir declared) keeps the default.
  fs.writeFileSync(path.join(root, "WORKFLOW.md"), MINIMAL_POLICY);
  assert.equal(receiptsDirFor(root), path.join(root, ".pi", "receipts"));

  // A declared receipts_dir wins, and never leaves the root.
  const declared = path.join(tmp, "receipts-declared");
  fs.mkdirSync(declared, { recursive: true });
  fs.writeFileSync(path.join(declared, "WORKFLOW.md"), "---\nversion: 1\nreceipts_dir: .pi/evidence\n---\n");
  assert.equal(receiptsDirFor(declared), path.join(declared, ".pi", "evidence"));
  assert.equal(receiptsFileFor(declared, "2026-02-02"), path.join(declared, ".pi", "evidence", "2026-02-02.jsonl"));
  // The path is relative to the ROOT argument, not to the process cwd.
  assert.equal(receiptsDirFor(declared).startsWith(declared + path.sep), true);
});

await check("prove receipts: the receipt cwd is the SESSION cwd, never a leading `cd` target", async () => {
  // The prove receipt writer lives inside a pi `tool_result` hook, so it cannot
  // be called directly from a harness. Assert the wiring at the source level
  // instead: the hook identifies the command through `leadingCdTarget` but
  // resolves the receipt against `ctx.cwd` unconditionally.
  const source = fs.readFileSync(path.join(SUITE_DIR, "index.ts"), "utf8");
  const helperUses = [...source.matchAll(/leadingCdTarget\(/g)];
  assert.equal(helperUses.length, 2, "leadingCdTarget: one definition, one call site");
  assert.match(source, /const receiptCwd = ctx\.cwd;/);
  assert.match(source, /cwd: receiptCwd,/);
  // No assignment may route the receipt anywhere else (a worker's mount would
  // let it pre-forge a VERIFIED receipt).
  const assignments = [...source.matchAll(/receiptCwd\s*=\s*([^;\n]*)/g)].map((match) => match[1].trim());
  assert.deepEqual(assignments, ["ctx.cwd"], "the receipt cwd is assigned once, from the session cwd");
  assert.doesNotMatch(source, /cwd:\s*(cdTarget|leadingCdTarget\()/);
  // The cd is still consulted for COMMAND IDENTIFICATION only.
  assert.match(source, /const cdTarget = leadingCdTarget\(segments\[0\]\);/);
  assert.match(source, /const rest = cdTarget !== null \? segments\.slice\(1\) : segments;/);
});

// ── 9. PROVE coverage against a host-root receipts fixture ──────────────────

function hostRoot(name) {
  const root = path.join(tmp, name);
  fs.mkdirSync(path.join(root, ".pi", "receipts"), { recursive: true });
  fs.writeFileSync(path.join(root, "WORKFLOW.md"), MINIMAL_POLICY);
  return root;
}

function receipt(record, day = "2026-01-01") {
  return {
    timestamp: "2026-01-01T00:00:00.000Z",
    agent: "orchestrator",
    session: "s",
    commands: [],
    rulesHash: "",
    headSha: "",
    ok: false,
    note: "",
    ...record,
  };
}

function appendReceipts(root, records, day = "2026-01-01") {
  const file = path.join(root, ".pi", "receipts", day + ".jsonl");
  fs.mkdirSync(path.dirname(file), { recursive: true });
  fs.writeFileSync(file, records.map((record) => JSON.stringify(record)).join("\n") + "\n");
  return file;
}

await check("checkProveCoverage: prove note + exit 0 VERIFIES, ordinary green does not, missing stays unverified", async () => {
  const rule = "R-GAME-007";
  const command = "make prove args='--rule " + rule + "'";

  // Missing rule, then a non-prove green receipt that mentions it.
  const root = hostRoot("prove-host-root");
  appendReceipts(root, [receipt({ note: "settle gate attempt 1/5", ok: true, commands: [{ command, exitCode: 0, timedOut: false, durationMs: 1 }] })]);
  const missing = await checkProveCoverage(root, ["R-GAME-001", rule]);
  assert.deepEqual(missing.missing, ["R-GAME-001", rule]);
  assert.equal(missing.ok, false);
  assert.match(missing.report, /no prove receipt at all/);
  assert.match(missing.report, /receipts root \(host\): /);

  // A prove receipt with a NON-zero exit does not verify either.
  const failedRoot = hostRoot("prove-host-failed");
  appendReceipts(failedRoot, [receipt({ note: "prove rules=" + rule, ok: true, commands: [{ command, exitCode: 1, timedOut: false, durationMs: 1 }] })]);
  const failed = await checkProveCoverage(failedRoot, [rule]);
  assert.equal(failed.ok, false);
  assert.deepEqual(failed.missing, [rule]);
  assert.match(failed.report, /non-VERIFIED/);

  // The prove receipt for the rule VERIFIES it (and nothing else).
  const coveredRoot = hostRoot("prove-host-covered");
  appendReceipts(coveredRoot, [
    receipt({ note: "settle gate attempt 1/5", ok: true, commands: [{ command: "make audit/static", exitCode: 0, timedOut: false, durationMs: 1 }] }),
    receipt({ note: "prove rules=" + rule, ok: true, commands: [{ command, exitCode: 0, timedOut: false, durationMs: 1 }] }),
  ]);
  const covered = await checkProveCoverage(coveredRoot, [rule]);
  assert.equal(covered.ok, true);
  assert.deepEqual(covered.covered, [rule]);
  assert.deepEqual(covered.missing, []);
  assert.match(covered.report, /status: ok/);
  // The same fixture does NOT cover a different rule.
  const other = await checkProveCoverage(coveredRoot, [rule, "R-GAME-008"]);
  assert.deepEqual(other.missing, ["R-GAME-008"]);

  // Day rollover: the reader takes the whole receipts dir, not just today.
  const crossed = hostRoot("prove-host-midnight");
  appendReceipts(crossed, [receipt({ note: "prove rules=" + rule, ok: true, commands: [{ command, exitCode: 0, timedOut: false, durationMs: 1 }] })], "2025-12-31");
  assert.equal((await checkProveCoverage(crossed, [rule])).ok, true);

  // Fail closed without a policy: no declared receipts dir ⇒ no evidence.
  const policyFree = path.join(tmp, "prove-host-no-policy");
  fs.mkdirSync(policyFree, { recursive: true });
  const refused = await checkProveCoverage(policyFree, [rule]);
  assert.equal(refused.ok, false);
  assert.deepEqual(refused.missing, [rule]);
  assert.match(refused.report, /REFUSED/);
});


// ── 7. Gate VM: template, mounts, lifecycle, reuse ──────────────────────────

await check("gate VM: template/sizing/egress constants are the locked-down gate profile", async () => {
  assert.equal(GATE_SANDBOX_TEMPLATE_NAME, "docker.io/library/pi-gate:v1");
  assert.equal(GATE_SANDBOX_ROLE, "gate");
  assert.equal(GATE_SANDBOX_CPUS, 4);
  assert.equal(GATE_SANDBOX_MEMORY, "8g");
  assert.deepEqual([...GATE_SANDBOX_EGRESS_HOSTS], [
    "proxy.golang.org:443",
    "sum.golang.org:443",
    "storage.googleapis.com:443",
  ]);
  // No model, no secret sentinel: a gate VM never calls a model and carries no
  // credential. Egress is the Go proxy fallback only.
  assert.deepEqual(GATE_SANDBOX_PROFILE.secretSentinelEnvs, []);
  assert.equal(GATE_SANDBOX_PROFILE.networkHosts.includes("opencode.ai:443"), false);
  assert.deepEqual(GATE_SANDBOX_PROFILE.mounts, [{ kind: "worktree", mode: "rw" }]);
  assert.deepEqual(profileDenyHosts(GATE_SANDBOX_PROFILE), []);
  // The gate profile is NOT in the role table: an agent named "gate" must not
  // silently become the verifier plane (it gets the worker profile instead).
  assert.equal(GATE_SANDBOX_PROFILE.role, "gate");
  assert.notEqual(profileForRole("gate"), GATE_SANDBOX_PROFILE);
});

await check("gate VM: linked-worktree mount plan adds the main repo root :ro, otherwise one mount", async () => {
  const worktree = "/host/repo/.worktrees/job-1";
  // Own .git directory: not linked, no extra mount.
  assert.deepEqual(gateMountPlanFromCommonDir(worktree, ".git"), {
    worktreePath: worktree,
    repoRoot: null,
    linkedWorktree: false,
  });
  assert.deepEqual(gateMountPlanFromCommonDir(worktree, "/host/repo/.worktrees/job-1/.git"), {
    worktreePath: worktree,
    repoRoot: null,
    linkedWorktree: false,
  });
  // Linked: the common dir is the MAIN repo's .git ⇒ mount its parent :ro.
  assert.deepEqual(gateMountPlanFromCommonDir(worktree, "/host/repo/.git"), {
    worktreePath: worktree,
    repoRoot: "/host/repo",
    linkedWorktree: true,
  });
  // Unprobeable (git failed) keeps the provable single mount.
  const plan = resolveGateMountPlan(worktree, () => ({ ok: false, commonDir: "", reason: "no git" }));
  assert.equal(plan.repoRoot, null);
  assert.equal(plan.linkedWorktree, false);
  const probed = resolveGateMountPlan(worktree, () => ({ ok: true, commonDir: "/host/repo/.git", reason: "" }));
  assert.equal(probed.repoRoot, "/host/repo");
});

await check("createGateSandbox: pi-gate template, worktree rw primary, per-sandbox Go egress only", async () => {
  resetGateSandboxRegistryForTests();
  const worktree = path.join(tmp, "gate-wt-plain");
  fs.mkdirSync(worktree, { recursive: true });
  const stub = installStub();
  const handle = await createGateSandbox(worktree, {
    name: "pi-gate-fixed-1",
    mountPlan: gateMountPlanFromCommonDir(worktree, ""),
  });
  assert.equal(handle.name, "pi-gate-fixed-1");
  assert.equal(handle.role, "gate");
  assert.equal(handle.createdFrom, "template");
  assert.equal(handle.hasWorkspace, true);
  assert.deepEqual(handle.networkHosts, [...GATE_SANDBOX_EGRESS_HOSTS]);
  assert.equal(handle.extraWorkspaces, undefined, "a plain worktree needs no extra mount");
  assert.equal(stub.calls.length, 2, "create + one policy allow");
  assert.deepEqual(
    stub.calls[0],
    createSandboxArgs({
      name: "pi-gate-fixed-1",
      profile: GATE_SANDBOX_PROFILE,
      worktreePath: path.resolve(worktree),
      reference: "template",
      template: GATE_SANDBOX_TEMPLATE_NAME,
    }),
  );
  assert.equal(stub.calls[0].includes("docker.io/library/pi-gate:v1"), true);
  assert.equal(stub.calls[0].includes("docker.io/library/pi-worker:v1"), false);
  assert.equal(stub.calls[0][stub.calls[0].indexOf("--cpus") + 1], "4");
  assert.equal(stub.calls[0][stub.calls[0].indexOf("--memory") + 1], "8g");
  assert.deepEqual(stub.calls[1], policyAllowArgs("pi-gate-fixed-1", GATE_SANDBOX_EGRESS_HOSTS));
  assert.equal(stub.calls[1].join(" ").includes("opencode.ai"), false, "no model egress for a gate VM");
  setSbxProcessOps(null);
  resetGateSandboxRegistryForTests();
});

await check("createGateSandbox: a linked worktree mounts the main repo root :ro so gitdir resolves", async () => {
  resetGateSandboxRegistryForTests();
  const worktree = path.join(tmp, "gate-linked", ".worktrees", "job-2");
  fs.mkdirSync(worktree, { recursive: true });
  const repoRoot = path.join(tmp, "gate-linked");
  const stub = installStub();
  const handle = await createGateSandbox(worktree, {
    name: "pi-gate-linked-1",
    mountPlan: gateMountPlanFromCommonDir(worktree, path.join(repoRoot, ".git")),
  });
  assert.deepEqual(handle.extraWorkspaces, [repoRoot]);
  const create = stub.calls[0];
  // Worktree RW first (primary), main repo root :ro second (pointer target).
  assert.equal(create[create.length - 1], repoRoot + ":ro");
  assert.equal(create[create.length - 2], path.resolve(worktree));
  setSbxProcessOps(null);
  resetGateSandboxRegistryForTests();
});

await check("createGateSandbox: a missing template is a loud infra failure (no worker-image fallback)", async () => {
  resetGateSandboxRegistryForTests();
  const worktree = path.join(tmp, "gate-wt-missing");
  fs.mkdirSync(worktree, { recursive: true });
  const stub = installStub((args) => (args[0] === "create" ? { exitCode: 1, stderr: "template not found" } : {}));
  await assert.rejects(
    createGateSandbox(worktree, { name: "pi-gate-missing-1", mountPlan: gateMountPlanFromCommonDir(worktree, "") }),
    /gate VM from template 'docker\.io\/library\/pi-gate:v1' failed/,
  );
  assert.equal(stub.calls.length, 1, "one create attempt, no kit retry");
  assert.equal(stub.calls[0].includes("shell"), true, "a template create never swaps the agent slot for a kit");
  assert.equal(stub.existsCalls.length, 0, "no kit probe for a gate VM");
  setSbxProcessOps(null);
  resetGateSandboxRegistryForTests();
});

await check("ensureGateSandbox: session reuse keyed by worktree path; a new path gets a new VM", async () => {
  resetGateSandboxRegistryForTests();
  const first = path.join(tmp, "gate-reuse-a");
  const second = path.join(tmp, "gate-reuse-b");
  fs.mkdirSync(first, { recursive: true });
  fs.mkdirSync(second, { recursive: true });
  const stub = installStub();
  const a1 = await ensureGateSandbox(first, { mountPlan: gateMountPlanFromCommonDir(first, "") });
  const a2 = await ensureGateSandbox(first, { mountPlan: gateMountPlanFromCommonDir(first, "") });
  assert.equal(a1.name, a2.name, "the SAME worktree path reuses the VM");
  assert.equal(stub.calls.filter((args) => args[0] === "create").length, 1, "one create for the reused path");
  assert.equal(gateSandboxKey(first), path.resolve(first));
  assert.equal(gateSandboxFor(first).name, a1.name);

  const b = await ensureGateSandbox(second, { mountPlan: gateMountPlanFromCommonDir(second, "") });
  assert.notEqual(b.name, a1.name, "a different worktree path NEVER reuses the old mount");
  assert.equal(stub.calls.filter((args) => args[0] === "create").length, 2);
  assert.deepEqual(listGateSandboxes().map((handle) => handle.name).sort(), [a1.name, b.name].sort());

  // Concurrent decisions on one path share the in-flight create.
  const raced = path.join(tmp, "gate-reuse-c");
  fs.mkdirSync(raced, { recursive: true });
  const before = stub.calls.filter((args) => args[0] === "create").length;
  const [r1, r2] = await Promise.all([
    ensureGateSandbox(raced, { mountPlan: gateMountPlanFromCommonDir(raced, "") }),
    ensureGateSandbox(raced, { mountPlan: gateMountPlanFromCommonDir(raced, "") }),
  ]);
  assert.equal(r1.name, r2.name);
  assert.equal(stub.calls.filter((args) => args[0] === "create").length, before + 1, "one create under a race");

  // Session end: every gate VM is stopped and removed.
  const results = await teardownGateSandboxes();
  assert.equal(results.length, 3);
  assert.equal(results.every((entry) => entry.stopped && entry.removed), true);
  assert.deepEqual(listGateSandboxes(), [], "registry is empty after teardown");
  const rmCalls = stub.calls.filter((args) => args[0] === "rm").map((args) => args[2]).sort();
  assert.deepEqual(rmCalls, [a1.name, b.name, r1.name].sort());
  setSbxProcessOps(null);
  resetGateSandboxRegistryForTests();
});

await check("gate VM: resolveGateMountPlan uses the host git probe for a real linked worktree", async () => {
  resetGateSandboxRegistryForTests();
  const main = path.join(tmp, "gate-real-link");
  fs.mkdirSync(main, { recursive: true });
  git(main, ["init", "-q"]);
  fs.writeFileSync(path.join(main, "README.md"), "# linked\n");
  git(main, ["add", "."]);
  git(main, ["commit", "-q", "-m", "fixture"]);
  const worktree = path.join(tmp, "gate-real-link-wt");
  git(main, ["worktree", "add", "-q", worktree, "-b", "gate-linked-branch"]);
  // createGateSandbox calls the real probe: this is the host-side trust anchor
  // reading git metadata (never worktree code).
  const plan = resolveGateMountPlan(worktree);
  assert.equal(plan.linkedWorktree, true, "a git worktree add path is linked");
  assert.equal(plan.repoRoot, fs.realpathSync(main), "the main repo root is the :ro extra mount");
  resetGateSandboxRegistryForTests();
});

// ── Teardown of the harness ─────────────────────────────────────────────────

setSbxProcessOps(null);
fs.rmSync(tmp, { recursive: true, force: true });
console.log("\n" + String(checks - failures) + "/" + String(checks) + " checks passed");
process.exit(failures === 0 ? 0 : 1);

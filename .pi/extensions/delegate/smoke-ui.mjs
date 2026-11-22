// Standalone smoke test for the delegate extension's EXECUTION-PLANE surfaces
// (.pi/extensions/delegate/index.ts): the pre-run policy badge, the post-run
// instance badges / info line, and the ambient footer status text.
//
// Nothing here renders a TUI and nothing spawns a subagent: the surface under
// test is the PURE half (badge derivation, mount/egress compaction, info-line
// formatting, status text). The renderers themselves are thin: they call these
// helpers and `theme.fg(...)` the result.
//
// Loader note: node >= 22.18 can strip TypeScript itself, so a native import is
// tried first. This box's node is compiled WITHOUT TS support
// (`ERR_NO_TYPESCRIPT`), so the fallback is the jiti that ships inside the
// installed pi — the same transpiler pi uses for extensions. index.ts imports
// pi packages by bare specifier, which do not resolve from a repo without
// node_modules, so those specifiers are aliased to the same pi install.
//
// Run:  node .pi/extensions/delegate/smoke-ui.mjs
import * as assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import { createRequire } from "node:module";
import { fileURLToPath } from "node:url";

const HERE = path.dirname(fileURLToPath(import.meta.url));

let failures = 0;
let checks = 0;

function check(name, fn) {
  checks += 1;
  try {
    fn();
    console.log("  ok  " + name);
  } catch (error) {
    failures += 1;
    console.error("  FAIL " + name + "\n       " + (error instanceof Error ? error.message : String(error)));
  }
}

/** A pi install root: a directory whose package.json names the coding agent. */
function isPiRoot(dir) {
  try {
    return JSON.parse(fs.readFileSync(path.join(dir, "package.json"), "utf8")).name === "@earendil-works/pi-coding-agent";
  } catch {
    return false;
  }
}

/** Candidate pi install roots, most explicit first (no resolution magic). */
function candidatePiRoots() {
  const roots = [];
  const fromEnv = process.env.PI_CODING_AGENT_ROOT;
  if (fromEnv) roots.push(fromEnv);
  const home = os.homedir();
  const releasesDir = path.join(home, ".pi", "agent", "install", "releases");
  try {
    for (const release of fs.readdirSync(releasesDir).sort().reverse()) {
      roots.push(path.join(releasesDir, release, "node_modules", "@earendil-works", "pi-coding-agent"));
    }
  } catch {
    /* no per-user releases on this box */
  }
  roots.push(path.join(home, ".pi", "agent", "install", "node_modules", "@earendil-works", "pi-coding-agent"));
  // A global install: the `pi` bin resolves to <root>/dist/bundle/cli.js.
  try {
    const bin = fs.realpathSync(execFileSync("which", ["pi"], { encoding: "utf8" }).trim());
    roots.push(path.resolve(path.dirname(bin), "..", ".."));
  } catch {
    /* pi not on PATH */
  }
  try {
    const globalRoot = execFileSync("npm", ["root", "-g"], { encoding: "utf8" }).trim();
    roots.push(path.join(globalRoot, "@earendil-works", "pi-coding-agent"));
  } catch {
    /* npm unavailable */
  }
  return roots;
}

function findPiRoot() {
  for (const root of candidatePiRoots()) {
    if (isPiRoot(root)) return root;
  }
  throw new Error(
    "cannot locate an installed pi (@earendil-works/pi-coding-agent); set PI_CODING_AGENT_ROOT to its package dir",
  );
}

/** Where this install keeps pi's dependency packages: nested, or hoisted to the install root. */
function piDepsRoot(piRoot) {
  const nested = path.join(piRoot, "node_modules");
  const hoisted = path.resolve(piRoot, "..", ".."); // <install>/node_modules when piRoot is <install>/node_modules/@earendil-works/pi-coding-agent
  try {
    return fs.existsSync(path.join(nested, "@earendil-works", "pi-tui")) ? nested : hoisted;
  } catch {
    return hoisted;
  }
}

/** Aliases that let index.ts's bare pi imports resolve to the found install. */
function piAliases(piRoot) {
  const nm = piDepsRoot(piRoot);
  return {
    "@earendil-works/pi-coding-agent": path.join(piRoot, "dist", "index.js"),
    "@earendil-works/pi-tui": path.join(nm, "@earendil-works", "pi-tui", "dist", "index.js"),
    "@earendil-works/pi-agent-core": path.join(nm, "@earendil-works", "pi-agent-core", "dist", "index.js"),
    "@earendil-works/pi-ai": path.join(nm, "@earendil-works", "pi-ai", "dist", "index.js"),
    typebox: path.join(nm, "typebox", "build", "index.mjs"),
  };
}

/** Load a module of this extension dir (index.ts by default; agents.ts for discovery). */
async function loadModule(entryName) {
  const entry = path.join(HERE, entryName);
  try {
    return await import(entry);
  } catch (error) {
    const code = error && error.code;
    const message = error instanceof Error ? error.message : String(error);
    // Native import can fail two ways that jiti solves: no TS stripping on this
    // node build, OR node stripped the types fine but index.ts's bare pi
    // specifiers do not resolve from a repo without node_modules (jiti aliases
    // them to the installed pi).
    const tsLoad = /typescript|Unknown file extension|ERR_NO_TYPESCRIPT/i.test(message);
    const unresolvable = code === "ERR_MODULE_NOT_FOUND" || /Cannot find package/i.test(message);
    if (!tsLoad && !unresolvable) throw error;
  }
  const piRoot = findPiRoot();
  const require = createRequire(import.meta.url);
  // jiti sits with the dependencies: nested under the package, or hoisted.
  const jitiCandidates = [
    path.join(piDepsRoot(piRoot), "jiti", "lib", "jiti.cjs"),
    path.join(piRoot, "node_modules", "jiti", "lib", "jiti.cjs"),
  ];
  const jitiPath = jitiCandidates.find((p) => fs.existsSync(p));
  if (!jitiPath) throw new Error(`cannot find a bundled jiti under ${piRoot}`);
  const { createJiti } = require(jitiPath);
  const jiti = createJiti(import.meta.url, { interopDefault: true, alias: piAliases(piRoot) });
  return await jiti.import(entry);
}

const ui = await loadModule("index.ts");
const agents = await loadModule("agents.ts");

// ── Fixtures ───────────────────────────────────────────────────────────────

const mount = (kind, mode) => ({ kind, mode });

/** A persisted sandbox detail record (what runSingleAgent stores in `details`). */
const sandboxDetail = (overrides = {}) => ({
  name: "pi-worker-abc123-00ab",
  role: "worker",
  createdFrom: "template",
  mounts: [mount("worktree", "rw")],
  networkHosts: ["github.com:443", "registry.npmjs.org:443", "opencode.ai:443", "extra.example:443"],
  teardown: "ok",
  ...overrides,
});

console.log("pre-run policy badges (planeBadge)");

check("worker → sandbox with a rw worktree mount", () => {
  const badge = ui.planeBadge("worker");
  assert.equal(badge.plane, "sandbox");
  assert.equal(badge.label, "[sbx wt:rw]");
  assert.equal(badge.color, "success");
});

check("general → sandbox with a rw worktree mount", () => {
  assert.equal(ui.planeBadge("general").label, "[sbx wt:rw]");
});

check("scout → sandbox, read-only worktree (scratch is not advertised)", () => {
  const badge = ui.planeBadge("scout");
  assert.equal(badge.plane, "sandbox");
  assert.equal(badge.label, "[sbx wt:ro]");
});

check("librarian → sandbox, vault workspace", () => {
  assert.equal(ui.planeBadge("librarian").label, "[sbx vault]");
});

check("researcher → sandbox (mountless profile), not a host passthrough", () => {
  const badge = ui.planeBadge("researcher");
  assert.equal(badge.plane, "sandbox");
  assert.equal(badge.label, "[sbx —]");
});

check("unknown role falls back to the worker profile — never host", () => {
  const badge = ui.planeBadge("total-unknown-role");
  assert.equal(badge.plane, "sandbox");
  assert.equal(badge.label, "[sbx wt:rw]");
});

check("background single is a host-side orchestrator whatever the role", () => {
  for (const role of ["worker", "scout", "librarian", "researcher"]) {
    const badge = ui.planeBadge(role, true);
    assert.equal(badge.plane, "host", role);
    assert.equal(badge.label, "[host]", role);
    assert.equal(badge.color, "warning", role);
  }
});

check("pre-run badges never synthesize a sandbox id", () => {
  for (const role of ["worker", "general", "scout", "librarian", "researcher", "mystery"]) {
    assert.ok(!ui.planeBadge(role).label.includes("pi-"), role);
  }
});

console.log("mount / egress compaction");

check("compactMountSummary: worker/general/scout/vault/mountless", () => {
  assert.equal(ui.compactMountSummary([mount("worktree", "rw")]), "wt:rw");
  assert.equal(ui.compactMountSummary([mount("scratch", "rw"), mount("worktree", "ro")]), "wt:ro");
  assert.equal(ui.compactMountSummary([mount("vault", "rw"), mount("worktree", "ro")]), "vault");
  assert.equal(ui.compactMountSummary([]), "—");
});

check("formatMountsLong: full list, profile order preserved", () => {
  assert.equal(ui.formatMountsLong([]), "none");
  assert.equal(ui.formatMountsLong([mount("worktree", "rw")]), "worktree:rw");
  assert.equal(
    ui.formatMountsLong([mount("vault", "rw"), mount("worktree", "ro")]),
    "vault:rw+worktree:ro",
  );
  assert.equal(
    ui.formatMountsLong([mount("scratch", "rw"), mount("worktree", "ro")]),
    "scratch:rw+worktree:ro",
  );
});

check("summarizeEgress: none / short list / truncated tail count", () => {
  assert.equal(ui.summarizeEgress([]), "none");
  assert.equal(ui.summarizeEgress(["a:443", "b:443"]), "a:443,b:443");
  assert.equal(ui.summarizeEgress(["a:443", "b:443", "c:443"]), "a:443,b:443,c:443");
  assert.equal(ui.summarizeEgress(["a:443", "b:443", "c:443", "d:443"]), "a:443,b:443,c:443 +1");
  assert.equal(ui.summarizeEgress(["a:443", "b:443", "c:443", "d:443", "e:443"]), "a:443,b:443,c:443 +2");
});

console.log("post-run instance badges (resultPlaneBadge)");

check("named sandbox → [sbx <name>] in success", () => {
  const badge = ui.resultPlaneBadge({ sandbox: sandboxDetail() });
  assert.equal(badge.plane, "sandbox");
  assert.equal(badge.label, "[sbx pi-worker-abc123-00ab]");
  assert.equal(badge.color, "success");
});

check("sandboxed role whose create failed → bare [sbx], no invented id", () => {
  assert.equal(ui.resultPlaneBadge({ plane: "sandbox" }).label, "[sbx]");
});

check("host-passthrough run → [host] in warning", () => {
  const badge = ui.resultPlaneBadge({ plane: "host", hostCwd: "/work/tree" });
  assert.equal(badge.plane, "host");
  assert.equal(badge.label, "[host]");
  assert.equal(badge.color, "warning");
});

check("a run that never started → no badge (no guessing)", () => {
  assert.equal(ui.resultPlaneBadge({}), undefined);
});

console.log("expanded info line (planeInfoLineText / formatSandboxInfoLine)");

check("sandbox line carries name · role · from · mounts · egress · teardown", () => {
  assert.equal(
    ui.planeInfoLineText({ sandbox: sandboxDetail() }),
    "sbx: pi-worker-abc123-00ab · role=worker · from=template · mounts=worktree:rw · egress=github.com:443,registry.npmjs.org:443,opencode.ai:443 +1 · teardown=ok",
  );
});

check("teardown=errors is surfaced; absent teardown reads pending (still live)", () => {
  assert.match(ui.planeInfoLineText({ sandbox: sandboxDetail({ teardown: "errors" }) }), /· teardown=errors$/);
  assert.match(ui.planeInfoLineText({ sandbox: sandboxDetail({ teardown: undefined }) }), /· teardown=pending$/);
});

check("mountless sandbox reads mounts=none, egress=none", () => {
  const line = ui.planeInfoLineText({
    sandbox: sandboxDetail({ role: "researcher", mounts: [], networkHosts: [] }),
  });
  assert.match(line, /role=researcher · from=template · mounts=none · egress=none · teardown=ok$/);
});

check("vault+worktree profile renders both mounts", () => {
  const line = ui.planeInfoLineText({
    sandbox: sandboxDetail({
      role: "librarian",
      mounts: [mount("vault", "rw"), mount("worktree", "ro")],
    }),
  });
  assert.match(line, /· mounts=vault:rw\+worktree:ro ·/);
});

check("host-passthrough run renders `host: <cwd>` instead of a sandbox line", () => {
  assert.equal(ui.planeInfoLineText({ plane: "host", hostCwd: "/work/tree" }), "host: /work/tree");
});

check("never-started run renders no info line", () => {
  assert.equal(ui.planeInfoLineText({}), undefined);
});

console.log("ambient footer status (formatRunStatus)");

check("live counts render as `sbx N · host M`", () => {
  assert.equal(ui.formatRunStatus(2, 1), "sbx 2 · host 1");
  assert.equal(ui.formatRunStatus(1, 0), "sbx 1");
  assert.equal(ui.formatRunStatus(0, 3), "host 3");
});

check("no live runs clears the status (undefined)", () => {
  assert.equal(ui.formatRunStatus(0, 0), undefined);
});

console.log("renderers (real components, fake theme, no TUI)");

/**
 * The extension factory only REGISTERS surfaces, so a fake pi is enough to get
 * the real renderers back and drive them with fabricated tool details. Theme
 * colors are identity functions here: this checks WHERE the facts land, not
 * which ANSI code is used.
 */
function makeFakePi() {
  const tools = new Map();
  const entryRenderers = new Map();
  const messageRenderers = new Map();
  // Lifecycle handlers are recorded (not dropped) so the main-session persona
  // injection can be driven through its REAL before_agent_start registration.
  const handlers = new Map();
  const api = {
    registerTool: (def) => tools.set(def.name, def),
    registerEntryRenderer: (type, fn) => entryRenderers.set(type, fn),
    registerMessageRenderer: (type, fn) => messageRenderers.set(type, fn),
    registerCommand: () => {},
    registerShortcut: () => {},
    registerAutocompleteProvider: () => {},
    on: (event, fn) => {
      if (!handlers.has(event)) handlers.set(event, []);
      handlers.get(event).push(fn);
      return () => {};
    },
    sendMessage: () => {},
    appendEntry: () => {},
    setActiveTools: () => {},
  };
  return { api, tools, entryRenderers, messageRenderers, handlers };
}

const plainTheme = {
  fg: (_color, text) => text,
  bg: (_color, text) => text,
  bold: (text) => text,
};

function renderComponent(component) {
  assert.ok(component && typeof component.render === "function", "renderer returned a component");
  return component.render(200).join("\n");
}

const child = (overrides = {}) => ({
  agent: "worker",
  agentSource: "project",
  task: "do the thing",
  status: "completed",
  exitCode: 0,
  messages: [],
  stderr: "",
  usage: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, cost: 0, contextTokens: 0, turns: 0 },
  ...overrides,
});

const toolResult = (details) => ({ content: [{ type: "text", text: "(no output)" }], details });

const fake = makeFakePi();
assert.equal(typeof ui.default, "function", "extension default export is the register function");
// This smoke may itself run inside a delegate child (PI_DELEGATE_CHILD=1), where
// the factory is deliberately INERT (registers advisory notes only). The surfaces
// under test here are the HOST-side ones, so the child markers are dropped for the
// duration of the registration and restored afterwards.
const childEnvKeys = ["PI_DELEGATE_CHILD", "PI_DELEGATE_ROLE", "PI_DELEGATE_SANDBOX"];
const savedEnv = new Map(childEnvKeys.map((key) => [key, process.env[key]]));
for (const key of childEnvKeys) delete process.env[key];
ui.default(fake.api);
for (const [key, value] of savedEnv) {
  if (value !== undefined) process.env[key] = value;
}
const subagent = fake.tools.get("subagent");
assert.ok(subagent, "subagent tool registered");

console.log("main-session persona (.pi/agents/main.md)");

const REPO_ROOT = path.resolve(HERE, "..", "..", "..");
const PERSONA_PATH = path.join(REPO_ROOT, ".pi", "agents", "main.md");

/**
 * Drive the REAL `before_agent_start` registration with a fake event/ctx and
 * return every systemPrompt it produced. Child markers are dropped for the
 * duration (the injection is main-session-only), and async handlers — the
 * round-start job surfacer — are kicked off but not awaited: they contribute no
 * systemPrompt and must not produce unhandled rejections here.
 */
function mainSystemPrompts(cwd) {
  const prompts = [];
  const saved = new Map(childEnvKeys.map((key) => [key, process.env[key]]));
  for (const key of childEnvKeys) delete process.env[key];
  try {
    for (const fn of fake.handlers.get("before_agent_start") ?? []) {
      const result = fn({ systemPrompt: "BASE", messageType: "user" }, { cwd });
      if (result && typeof result.then === "function") {
        result.catch(() => {});
        continue;
      }
      if (result && typeof result.systemPrompt === "string") prompts.push(result.systemPrompt);
    }
  } finally {
    for (const [key, value] of saved) {
      if (value !== undefined) process.env[key] = value;
      else delete process.env[key];
    }
  }
  return prompts;
}

check(".pi/agents/main.md exists and carries no agent frontmatter", () => {
  assert.ok(fs.existsSync(PERSONA_PATH), "missing persona file: .pi/agents/main.md");
  const content = fs.readFileSync(PERSONA_PATH, "utf-8");
  assert.ok(content.trim().length > 0, "persona file is empty");
  assert.ok(!/^---\n/.test(content), "persona must NOT start with agent frontmatter (it is not a spawnable agent)");
});

check("main session injects the persona file through loadOrchestratorPrompt", () => {
  const content = fs.readFileSync(PERSONA_PATH, "utf-8");
  const prompts = mainSystemPrompts(REPO_ROOT);
  assert.ok(prompts.length > 0, "no before_agent_start handler returned a systemPrompt");
  const injected = prompts.find((p) => p.includes(content.trim()));
  assert.ok(injected, "persona body was not appended to the system prompt");
  assert.ok(injected.startsWith("BASE"), "the persona is APPENDED, never replaces the base prompt");
  assert.doesNotMatch(injected, /persona file missing/, "the fail-loud fallback was used although the file exists");
});

check("missing persona → fail-loud stub, not silent generic rules", () => {
  const prompts = mainSystemPrompts(path.join(REPO_ROOT, ".pi", "agents", "definitely-absent"));
  assert.ok(prompts.length > 0, "no before_agent_start handler returned a systemPrompt");
  assert.match(prompts[0], /persona file missing: \.pi\/agents\/main\.md/);
  assert.match(prompts[0], /\/WORKFLOW\.md/);
});

check("discovery skips the persona file (no frontmatter → never a worker)", () => {
  const found = agents.discoverAgents(REPO_ROOT, "project");
  assert.equal(found.projectAgentsDir, path.join(REPO_ROOT, ".pi", "agents"));
  assert.ok(found.agents.length > 0, "discovery found no project agents at all (vacuous check)");
  assert.ok(!found.agents.some((a) => a.filePath === PERSONA_PATH), ".pi/agents/main.md was registered as a spawnable agent");
  assert.ok(found.agents.every((a) => a.name !== "main"), "a `main` agent name leaked into the roster");
});

check("renderCall single foreground → [foreground] + policy badge", () => {
  const out = renderComponent(subagent.renderCall({ agent: "worker", task: "t", background: false }, plainTheme, {}));
  assert.match(out, /\[foreground\] \[sbx wt:rw\]/);
});

check("renderCall background single → [background] [host] (orchestrator plane)", () => {
  const out = renderComponent(
    subagent.renderCall({ agent: "orchestrator", task: "t", background: true }, plainTheme, {}),
  );
  assert.match(out, /\[background\] \[host\]/);
});

check("renderCall tasks[] → one policy badge per row", () => {
  const out = renderComponent(
    subagent.renderCall(
      { tasks: [{ agent: "worker", task: "a" }, { agent: "scout", task: "b" }] },
      plainTheme,
      {},
    ),
  );
  assert.match(out, /worker \[sbx wt:rw\]/);
  assert.match(out, /scout \[sbx wt:ro\]/);
});

check("renderCall chain → one policy badge per step", () => {
  const out = renderComponent(
    subagent.renderCall(
      { chain: [{ agent: "scout", task: "a" }, { agent: "librarian", task: "b" }] },
      plainTheme,
      {},
    ),
  );
  assert.match(out, /scout \[sbx wt:ro\]/);
  assert.match(out, /librarian \[sbx vault\]/);
});

check("renderResult single collapsed → [sbx <name>] in the header", () => {
  const out = renderComponent(
    subagent.renderResult(
      toolResult({ mode: "single", results: [child({ sandbox: sandboxDetail() })], isError: false }),
      { expanded: false },
      plainTheme,
      {},
    ),
  );
  assert.match(out, /worker \(project\) \[sbx pi-worker-abc123-00ab\]/);
});

check("renderResult single expanded → header badge + one dim sbx: line", () => {
  const out = renderComponent(
    subagent.renderResult(
      toolResult({ mode: "single", results: [child({ sandbox: sandboxDetail() })], isError: false }),
      { expanded: true },
      plainTheme,
      {},
    ),
  );
  assert.match(out, /\[sbx pi-worker-abc123-00ab\]/);
  assert.match(out, /sbx: pi-worker-abc123-00ab · role=worker · from=template · mounts=worktree:rw · egress=github\.com:443,registry\.npmjs\.org:443,opencode\.ai:443 \+1 · teardown=ok/);
});

check("renderResult host-passthrough → [host] header, `host: <cwd>` line", () => {
  const host = child({ plane: "host", hostCwd: "/work/tree" });
  const collapsed = renderComponent(
    subagent.renderResult(toolResult({ mode: "single", results: [host], isError: false }), { expanded: false }, plainTheme, {}),
  );
  const expanded = renderComponent(
    subagent.renderResult(toolResult({ mode: "single", results: [host], isError: false }), { expanded: true }, plainTheme, {}),
  );
  assert.match(collapsed, /\[host\]/);
  assert.doesNotMatch(collapsed, /sbx/);
  assert.match(expanded, /host: \/work\/tree/);
});

check("renderResult chain → badges on every step (collapsed and expanded)", () => {
  const details = {
    mode: "chain",
    isError: false,
    results: [
      child({ step: 1, agent: "scout", sandbox: sandboxDetail({ name: "pi-scout-1", role: "scout" }) }),
      child({ step: 2, agent: "librarian", sandbox: sandboxDetail({ name: "pi-librarian-2", role: "librarian" }) }),
    ],
  };
  const collapsed = renderComponent(subagent.renderResult(toolResult(details), { expanded: false }, plainTheme, {}));
  const expanded = renderComponent(subagent.renderResult(toolResult(details), { expanded: true }, plainTheme, {}));
  for (const out of [collapsed, expanded]) {
    assert.match(out, /Step 1: scout .*\[sbx pi-scout-1\]/);
    assert.match(out, /Step 2: librarian .*\[sbx pi-librarian-2\]/);
  }
  assert.match(expanded, /sbx: pi-scout-1 · role=scout/);
});

check("renderResult parallel → badges on every task + info lines", () => {
  const details = {
    mode: "parallel",
    isError: false,
    results: [child({ sandbox: sandboxDetail({ name: "pi-worker-1" }) }), child({ sandbox: sandboxDetail({ name: "pi-worker-2" }) })],
  };
  for (const expanded of [false, true]) {
    const out = renderComponent(subagent.renderResult(toolResult(details), { expanded }, plainTheme, {}));
    assert.match(out, /\[sbx pi-worker-1\]/);
    assert.match(out, /\[sbx pi-worker-2\]/);
  }
});

check("renderResult background job → `orchestrator (host)`, never a sandbox id", () => {
  const details = {
    mode: "background",
    job: {
      id: "orchestrator-20251121-000000-abc123",
      agent: "orchestrator",
      status: "running",
      runner: "tmux",
      startedAt: new Date(Date.now() - 61_000).toISOString(),
    },
  };
  const out = renderComponent(subagent.renderResult(toolResult(details), { expanded: false }, plainTheme, {}));
  assert.match(out, /orchestrator \(host\)/);
  assert.doesNotMatch(out, /\[sbx/);
});

check("delegate-job entry card + result message both say `orchestrator (host)`", () => {
  const card = {
    id: "orchestrator-20251121-000000-abc123",
    agent: "orchestrator",
    status: "done",
    runner: "tmux",
    startedAt: new Date(Date.now() - 61_000).toISOString(),
  };
  const renderEntry = fake.entryRenderers.get("delegate-job");
  const renderMessage = fake.messageRenderers.get("delegate-job-result");
  assert.equal(typeof renderEntry, "function");
  assert.equal(typeof renderMessage, "function");
  const entryOut = renderComponent(renderEntry({ data: card }, {}, plainTheme));
  const messageOut = renderComponent(
    renderMessage({ content: "x", details: { ...card, text: "out" } }, { expanded: false, outputPad: 0 }, plainTheme),
  );
  assert.match(entryOut, /orchestrator \(host\)/);
  assert.match(messageOut, /orchestrator \(host\)/);
});

console.log("\nwhat the user sees (plain-text projection)");
console.log(
  [
    "  call      " + renderComponent(subagent.renderCall({ agent: "worker", task: "fix the bug" }, plainTheme, {})).replace(/\n/g, "\n            "),
    "  single    " + renderComponent(subagent.renderResult(toolResult({ mode: "single", results: [child({ sandbox: sandboxDetail() })], isError: false }), { expanded: false }, plainTheme, {})).replace(/\n/g, "\n            "),
    "  expanded  " + renderComponent(subagent.renderResult(toolResult({ mode: "single", results: [child({ sandbox: sandboxDetail() })], isError: false }), { expanded: true }, plainTheme, {})).split("\n").slice(0, 2).join("\n            "),
  ].join("\n"),
);

console.log("");
if (failures > 0) {
  console.error(failures + "/" + checks + " checks FAILED");
  process.exit(1);
}
console.log(checks + "/" + checks + " checks passed");

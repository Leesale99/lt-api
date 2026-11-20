// Standalone smoke test for .pi/extensions/delegate/bashscan.ts (batch 3, stage 1).
//
// Imports the REAL module (node's built-in type stripping runs the .ts file
// directly — no pi, no build step) and exercises it against temp dirs. Nothing
// here is imported by the extension. Run:
//   node .pi/extensions/delegate/smoke-bashscan.mjs
// (needs Node >= 22.18 for unflagged .ts type stripping.)
//
// Three groups, all deterministic:
//   POSITIVES — the scanner must DENY, and the reason must name the class
//   NEGATIVES — ordinary work must PASS (over-matching is a regression too)
//   EDGES     — quoting, subshells, pipelines, substitution, globs, heredocs,
//               and the policy-unavailable / unknown-class refusals
//
// Path fixtures: the repo root is a temp dir with a WORKFLOW.md declaring the
// same protected set the extension defaults to, so `Makefile`, `cmd/spec/**`,
// `docs/spec/**` and `.pi/**` are all protected exactly as in production.
import * as assert from "node:assert/strict";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import {
  BASH_SCAN_MAX_DEPTH,
  evaluateBashWrite,
  scanBashCommand,
  splitTopLevelSegments,
} from "./bashscan.ts";
import { BASH_WRITE_PATTERN_CLASSES } from "./contracts.ts";
import {
  DEFAULT_BASH_WRITE_PATTERNS,
  DEFAULT_PROTECTED_PATHS,
  KNOWN_BASH_WRITE_PATTERNS,
  loadWorkflowPolicy,
} from "./policy.ts";

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

const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "bashscan-smoke-"));

const MINIMAL_POLICY = "---\nversion: 1\n---\n\n# Workflow policy\n";

function writeWorkflow(dir, body = MINIMAL_POLICY) {
  fs.mkdirSync(dir, { recursive: true });
  fs.writeFileSync(path.join(dir, "WORKFLOW.md"), body);
  return dir;
}

/** The repo fixture: a valid policy, so protected-path classification is live. */
const repo = writeWorkflow(path.join(tmp, "repo"));
fs.mkdirSync(path.join(repo, "docs", "spec"), { recursive: true });
fs.mkdirSync(path.join(repo, "cmd", "spec"), { recursive: true });
fs.mkdirSync(path.join(repo, ".pi", "delegate"), { recursive: true });
fs.mkdirSync(path.join(repo, ".pi", "plans"), { recursive: true });

/** Repo with NO WORKFLOW.md: the scanner must refuse everything (fail closed). */
const policyFree = path.join(tmp, "policy-free");
fs.mkdirSync(policyFree, { recursive: true });

const deny = (command) => evaluateBashWrite(repo, command);
const allow = (command) => evaluateBashWrite(repo, command);

function assertBlocked(decision, classPattern, command) {
  assert.equal(decision.blocked, true, "expected DENY for: " + command);
  assert.equal(decision.pattern, classPattern, "pattern class for: " + command);
  assert.match(decision.reason, new RegExp(classPattern.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")), "reason names the class");
  assert.match(decision.reason, /bash write-channel scanner/);
}

function assertAllowed(decision, command) {
  assert.equal(decision.blocked, false, "expected ALLOW for: " + command + " — got: " + (decision.reason ?? ""));
}

// ── Module wiring: the class vocabulary ────────────────────────────────────
await check("contracts: the canonical class list is the policy default and is typo-proof", async () => {
  assert.deepEqual([...BASH_WRITE_PATTERN_CLASSES], [
    "redirect",
    "tee",
    "sed-in-place",
    "file-transfer",
    "heredoc",
    "git-mutating",
  ]);
  assert.deepEqual([...DEFAULT_BASH_WRITE_PATTERNS], [...BASH_WRITE_PATTERN_CLASSES]);
  assert.deepEqual([...KNOWN_BASH_WRITE_PATTERNS], [...BASH_WRITE_PATTERN_CLASSES]);
  assert.ok(BASH_SCAN_MAX_DEPTH >= 2, "recursion cap is set");
  // Omitted field ⇒ the full class set survives (defaults-when-omitted).
  const loaded = loadWorkflowPolicy(repo);
  assert.equal(loaded.ok, true);
  assert.deepEqual(loaded.policy.bashWritePatterns, [...BASH_WRITE_PATTERN_CLASSES]);
  assert.equal(loaded.declared.has("bash_write_patterns"), false, "omitted field is not declared");
});

await check("policy: unknown bash_write_patterns item refuses; a valid list is honoured", async () => {
  const bad = writeWorkflow(
    path.join(tmp, "policy-bad-pattern"),
    "---\nversion: 1\nbash_write_patterns:\n  - redirect\n  - redriect\n---\n",
  );
  const loadedBad = loadWorkflowPolicy(bad);
  assert.equal(loadedBad.ok, false, "a typo must refuse, not silently narrow the scan");
  assert.match(loadedBad.reason, /unknown bash_write_patterns item\(s\): redriect/);
  assert.match(loadedBad.reason, /known: redirect, tee, sed-in-place, file-transfer, heredoc, git-mutating/);

  const good = writeWorkflow(
    path.join(tmp, "policy-good-pattern"),
    "---\nversion: 1\nbash_write_patterns:\n  - redirect\n  - tee\n---\n",
  );
  const loadedGood = loadWorkflowPolicy(good);
  assert.equal(loadedGood.ok, true);
  assert.deepEqual(loadedGood.policy.bashWritePatterns, ["redirect", "tee"]);
  assert.equal(loadedGood.declared.has("bash_write_patterns"), true);
});

await check("policy: protected defaults cover the fixture paths the positives use", async () => {
  assert.ok(DEFAULT_PROTECTED_PATHS.includes("Makefile"));
  assert.ok(DEFAULT_PROTECTED_PATHS.includes("docs/spec/**"));
  assert.ok(DEFAULT_PROTECTED_PATHS.includes("cmd/spec/**"));
  assert.ok(DEFAULT_PROTECTED_PATHS.includes(".pi/**"));
});

// ── Segmentation ───────────────────────────────────────────────────────────
await check("splitTopLevelSegments: top-level-only splits, quoting and $() respected", async () => {
  assert.deepEqual(splitTopLevelSegments("make audit && make prove"), ["make audit", "make prove"]);
  assert.deepEqual(splitTopLevelSegments("ls | grep foo"), ["ls", "grep foo"]);
  assert.deepEqual(splitTopLevelSegments("a ; b || c\nd"), ["a", "b", "c", "d"]);
  assert.deepEqual(splitTopLevelSegments('git commit -m "feat: x && y"'), ['git commit -m "feat: x && y"']);
  assert.deepEqual(splitTopLevelSegments("echo $(date | tr a b)"), ["echo $(date | tr a b)"]);
  assert.deepEqual(splitTopLevelSegments("(echo x > Makefile)"), ["(echo x > Makefile)"]);
});

// ── POSITIVES: every one must be denied, naming its class ───────────────────
await check("positive: redirect class (>, >>, <>) on protected paths", async () => {
  assertBlocked(deny("echo x > Makefile"), "redirect", "echo x > Makefile");
  assertBlocked(deny("echo x >> docs/spec/rules.yaml"), "redirect", "echo x >> docs/spec/rules.yaml");
  assertBlocked(deny("cmd <> .pi/delegate/gate-allowlist.json"), "redirect", "cmd <> allowlist");
  // Attached and fd-prefixed forms.
  assertBlocked(deny("echo x >Makefile"), "redirect", "echo x >Makefile");
  assertBlocked(deny("echo x 2>cmd/spec/main.go"), "redirect", "2> attached");
  assertBlocked(deny("echo x &> WORKFLOW.md"), "redirect", "&> redirect");
  assertBlocked(deny("echo x 2>>docs/spec/rules.yaml"), "redirect", "2>> redirect");
});

await check("positive: tee class, including tee -a and a pipeline", async () => {
  assertBlocked(deny("make audit | tee WORKFLOW.md"), "tee", "make audit | tee WORKFLOW.md");
  assertBlocked(deny("tee -a cmd/spec/main.go"), "tee", "tee -a cmd/spec/main.go");
  assertBlocked(deny("cat x | tee Makefile"), "tee", "cat x | tee Makefile");
});

await check("positive: sed-in-place class, both flag spellings", async () => {
  assertBlocked(deny("sed -i s/a/b/ Makefile"), "sed-in-place", "sed -i");
  assertBlocked(deny("sed --in-place=.bak s/a/b/ docs/spec/rules.yaml"), "sed-in-place", "--in-place=.bak");
  assertBlocked(deny("sed -i.bak -e s/a/b/ cmd/spec/main.go"), "sed-in-place", "-i.bak -e");
});

await check("positive: file-transfer class (cp, mv, install, dd)", async () => {
  assertBlocked(deny("cp x Makefile"), "file-transfer", "cp x Makefile");
  assertBlocked(deny("mv x .pi/extensions/delegate/index.ts"), "file-transfer", "mv into .pi/extensions");
  assertBlocked(deny("install -m644 x cmd/spec"), "file-transfer", "install -m644 x cmd/spec");
  assertBlocked(deny("dd of=Makefile if=x"), "file-transfer", "dd of=Makefile");
  assertBlocked(deny("cp x -t docs/spec"), "file-transfer", "cp -t target-directory");
});

await check("positive: heredoc class denies a heredoc-fed script, redirect still names WORKFLOW.md", async () => {
  // The heredoc's own redirect target is caught by the redirect class.
  assertBlocked(deny("cat > WORKFLOW.md <<EOF\nhello\nEOF"), "redirect", "cat > WORKFLOW.md <<EOF");
  // A heredoc piped into a shell is ambiguous (its body would run as a script).
  const piped = deny("cat <<EOF | sh\nrm -rf /\nEOF");
  assert.equal(piped.blocked, true);
  assert.equal(piped.pattern, "heredoc");
  assert.match(piped.reason, /fail closed/);
  // ... and so is a heredoc fed straight to a shell command word.
  const direct = deny("bash <<EOF\nrm -rf /\nEOF");
  assert.equal(direct.blocked, true);
  assert.equal(direct.pattern, "heredoc");
});

await check("positive: git-mutating class (checkout, restore, stash, apply)", async () => {
  assertBlocked(deny("git checkout -- docs/spec/rules.yaml"), "git-mutating", "git checkout -- pathspec");
  assertBlocked(deny("git restore Makefile"), "git-mutating", "git restore Makefile");
  assertBlocked(
    deny("git stash push -- .pi/extensions/delegate/index.ts"),
    "git-mutating",
    "git stash push -- pathspec",
  );
  // Patch content decides the targets: inherently ambiguous ⇒ deny.
  const apply = deny("git apply patch.diff");
  assert.equal(apply.blocked, true);
  assert.equal(apply.pattern, "git-mutating");
  assert.match(apply.reason, /fail closed/);
  const am = deny("git am 0001.patch");
  assert.equal(am.blocked, true);
  assert.match(am.reason, /fail closed/);
});

await check("positive: wrapper prefixes (sudo / time / env / nice) do not hide the real command", async () => {
  assertBlocked(deny("sudo cp x Makefile"), "file-transfer", "sudo cp");
  assertBlocked(deny("time cp x Makefile"), "file-transfer", "time cp");
  assertBlocked(deny("env FOO=1 tee Makefile"), "tee", "env FOO=1 tee");
  assertBlocked(deny("nice -n 10 cp x Makefile"), "file-transfer", "nice -n 10 cp");
  assertBlocked(deny("GOWORK=off mv x Makefile"), "file-transfer", "assignment prefix");
});

await check("positive: nested command runners (find -exec, xargs) are scanned too", async () => {
  assertBlocked(deny("find . -exec cp x Makefile ;"), "file-transfer", "find -exec cp");
  const placeholder = deny('find . -name "*.go" -exec sed -i s/a/b/ {} ;');
  assert.equal(placeholder.blocked, true, "the find traversal decides the target: ambiguous");
  assert.equal(placeholder.pattern, "sed-in-place");
  assert.match(placeholder.reason, /fail closed/);
  assertBlocked(deny("ls | xargs cp x Makefile"), "file-transfer", "xargs cp");
});

await check("positive: directory destinations cannot launder an unknown filename", async () => {
  // `cp -t dir` / a trailing directory operand: the written filename is unknown,
  // so one synthetic child is probed through the same classifier.
  assertBlocked(deny("cp x -t docs/spec"), "file-transfer", "cp -t docs/spec");
  assertBlocked(deny("cp -r src docs/spec/"), "file-transfer", "cp -r src docs/spec/");
  assertBlocked(deny("install -d cmd/spec/x"), "file-transfer", "install -d");
  // A directory outside the protected set, or one whose children are editable
  // prose, is still fine (the probe asks the same classifier as edit/write).
  assertAllowed(allow("cp -r src .pi/plans/nested/"), "cp -r src .pi/plans/nested/");
  assertAllowed(allow("cp x /tmp/"), "cp x /tmp/");
  assertAllowed(allow("cp x docs/notes.md"), "cp x docs/notes.md");
});

await check("positive: a repo-root target ('.') covers everything, so it is denied", async () => {
  for (const command of ["git checkout -- .", "git checkout .", "cp -r x ."]) {
    const decision = deny(command);
    assert.equal(decision.blocked, true, "repo-root target must deny: " + command);
    assert.match(decision.reason, /fail closed/);
  }
});

await check("positive: git rm / mv / clean / reset --hard are mutations of known-or-unknown paths", async () => {
  assertBlocked(deny("git rm Makefile"), "git-mutating", "git rm Makefile");
  assertBlocked(deny("git mv x/Makefile Makefile"), "git-mutating", "git mv");
  for (const command of ["git clean -fd", "git reset --hard"]) {
    const decision = deny(command);
    assert.equal(decision.blocked, true, "must deny: " + command);
    assert.equal(decision.pattern, "git-mutating");
    assert.match(decision.reason, /fail closed/);
  }
  // Non-destructive git plumbing stays allowed.
  assertAllowed(allow("git reset --soft HEAD~1"), "git reset --soft HEAD~1");
  assertAllowed(allow("git stash list"), "git stash list");
  assertAllowed(allow("git stash"), "git stash");
  assertAllowed(allow("ls | xargs sed -i s/a/b/"), "xargs with a stdin-driven target list");
});

await check("positive: `git -C <dir>` shifts the pathspec base", async () => {
  const decision = deny("git -C docs/spec checkout -- rules.yaml");
  assert.equal(decision.blocked, true, "the pathspec is resolved from -C, not from the repo root");
  assert.equal(decision.pattern, "git-mutating");
  // -C into an unprotected directory stays allowed.
  assertAllowed(allow("git -C .pi/plans checkout -- notes.md"), "git -C .pi/plans checkout -- notes.md");
});

await check("positive: WORKFLOW.md is self-denied through the bash channel too", async () => {
  const decision = deny("echo x > WORKFLOW.md");
  assert.equal(decision.blocked, true);
  assert.match(decision.reason, /workflow policy file is protected|protected from delegated edits/i);
});

await check("positive: eval / shell -c payloads are scanned recursively", async () => {
  assertBlocked(deny('eval "echo x > Makefile"'), "redirect", "eval payload");
  assertBlocked(deny('sh -c "echo x > Makefile"'), "redirect", "sh -c payload");
  assertBlocked(deny('bash -ec "echo x > $(pwd)/Makefile"'), "redirect", "bash -ec payload");
});

await check("positive: a blocked segment blocks the whole command", async () => {
  assertBlocked(deny("make audit && echo x > Makefile"), "redirect", "deny is not rescued by an earlier segment");
  assertBlocked(deny("cd docs && echo x > spec/rules.yaml"), "redirect", "relative after cd");
});

// ── NEGATIVES: ordinary work must pass ────────────────────────────────────
await check("negative: gate commands, git reads, pipelines and devices stay allowed", async () => {
  const allowed = [
    "make audit",
    "make audit/static",
    "make prove",
    "go test ./...",
    "go run ./cmd/spec check --strict",
    'git commit -m "feat: x"',
    "git add .",
    "git diff",
    "git status",
    "git log --oneline -5",
    "git merge main",
    'git checkout -b feature/x',
    "node .pi/extensions/delegate/smoke-gates.mjs",
    "ls | grep foo",
    "echo hi",
    "make prove 2>/dev/null",
    "cmd 2>&1 | tee /tmp/out.log",
    'grep -n "sed -i" notes.md',
    "cat build.log",
    "sed -n 1,20p Makefile",
    "cp Makefile /tmp/Makefile.bak",
    "mv build.log /tmp/",
    "tee --version",
    "set -o noclobber",
    'echo "a > b"',
    "cat notes.md | sed -n 1p",
    "make audit # > Makefile",
    "dd if=/dev/zero of=/dev/null count=1",
  ];
  for (const command of allowed) assertAllowed(allow(command), command);
});

await check("negative: a shell comment is prose, not a command", async () => {
  // `#` at a word start opens a comment, so the text after it writes nothing —
  // but a `#` inside a word is literal, and a real redirect is still caught.
  assertAllowed(allow("make audit # echo x > Makefile"), "comment after a command");
  assertBlocked(deny("echo a#b > Makefile"), "redirect", "# inside a word is literal");
});

await check("negative: a disabled class stops scanning that operator (documented escape)", async () => {
  // Same command, class set without `redirect`: not scanned.
  const noRedirect = scanBashCommand("echo x > Makefile", {
    repoRoot: repo,
    enabledPatterns: ["tee"],
  });
  assert.equal(noRedirect.blocked, false);
  // ... and with `redirect` enabled it is denied again.
  const withRedirect = scanBashCommand("echo x > Makefile", { repoRoot: repo });
  assert.equal(withRedirect.blocked, true);
});

await check("negative: the unit seam needs no WORKFLOW.md fixture", async () => {
  const calls = [];
  const stub = (root, target) => {
    calls.push([root, target]);
    return target === "protected.txt"
      ? { blocked: true, rel: target, reason: "stub: protected" }
      : { blocked: false, rel: target, allowedBy: "not-gate-file" };
  };
  const dirWithNoPolicy = path.join(tmp, "unit-seam");
  const allowed = scanBashCommand("echo x > other.txt", { repoRoot: dirWithNoPolicy, evaluateTarget: stub });
  assert.equal(allowed.blocked, false);
  const blocked = scanBashCommand("echo x > protected.txt", { repoRoot: dirWithNoPolicy, evaluateTarget: stub });
  assert.equal(blocked.blocked, true);
  assert.equal(blocked.target, "protected.txt");
  assert.equal(blocked.pattern, "redirect");
  assert.match(blocked.reason, /stub: protected/);
  assert.deepEqual(calls, [
    [dirWithNoPolicy, "other.txt"],
    [dirWithNoPolicy, "protected.txt"],
  ]);
});

// ── EDGES: quote / subshell / pipeline / substitution / glob / heredoc ────
await check("edge: quoted target denies (quotes stripped, path still protected)", async () => {
  const decision = deny('echo x > "Makefile"');
  assert.equal(decision.blocked, true);
  assert.equal(decision.target, "Makefile");
});

await check("edge: subshell denies (parens are transparent, not a hiding place)", async () => {
  const decision = deny("(echo x > Makefile)");
  assert.equal(decision.blocked, true);
  assert.equal(decision.pattern, "redirect");
  assert.equal(decision.target, "Makefile");
});

await check("edge: pipeline into tee denies", async () => {
  const decision = deny("cat x | tee Makefile");
  assert.equal(decision.blocked, true);
  assert.equal(decision.pattern, "tee");
});

await check("edge: command substitution / variable / glob targets deny as ambiguous", async () => {
  for (const command of ["echo x > $(pwd)/Makefile", "echo x > $F", "echo x > ${F}", "echo x > .pi/*.json"]) {
    const decision = deny(command);
    assert.equal(decision.blocked, true, "ambiguous target must deny: " + command);
    assert.equal(decision.pattern, "redirect");
    assert.match(decision.reason, /write target .* could not be determined confidently \(fail closed\)/);
  }
  const backtick = deny("echo x > `pwd`/Makefile");
  assert.equal(backtick.blocked, true);
  assert.match(backtick.reason, /fail closed/);
});

await check("edge: unbalanced quote with a write op denies as ambiguous", async () => {
  const decision = deny('echo x > "Makefile');
  assert.equal(decision.blocked, true);
  assert.equal(decision.pattern, "redirect");
  assert.match(decision.reason, /fail closed/);
  assert.match(decision.reason, /unbalanced/);
});

await check("edge: heredoc to an unprotected path is allowed (bodies are data)", async () => {
  const decision = deny("cat > /tmp/log <<EOF\necho x > Makefile\nrm -rf /\nEOF");
  assert.equal(decision.blocked, false, "heredoc body lines must NOT be scanned as commands: " + (decision.reason ?? ""));
  // The same body written to a protected path is still denied by its redirect.
  const protectedTarget = deny("cat > Makefile <<EOF\nhello\nEOF");
  assert.equal(protectedTarget.blocked, true);
  assert.equal(protectedTarget.pattern, "redirect");
});

await check("edge: policy unavailable ⇒ deny with the workflow-policy refusal", async () => {
  const decision = evaluateBashWrite(policyFree, "make audit");
  assert.equal(decision.blocked, true, "no policy ⇒ no bash at all (fail closed)");
  assert.match(decision.reason, /workflow policy unavailable: WORKFLOW\.md not found/);
  assert.match(decision.reason, /gates refuse rather than run unpoliced/);
  // Even a harmless command is refused under an undeclared rule set.
  const harmless = evaluateBashWrite(policyFree, "echo hi");
  assert.equal(harmless.blocked, true);
});

await check("edge: an internal error is a DENY, never an allow (fail closed)", async () => {
  const throwing = () => {
    throw new Error("boom");
  };
  const decision = scanBashCommand("echo x > Makefile", { repoRoot: repo, evaluateTarget: throwing });
  assert.equal(decision.blocked, true, "a classifier throw must deny");
  assert.match(decision.reason, /internal error/);
  assert.match(decision.reason, /fail closed/);
  // Same for a garbage command string: never a throw out of the scanner.
  for (const command of ["", "   ", "; ; ;", "<<<", "`", "$(", '"""']) {
    assert.doesNotThrow(() => scanBashCommand(command, { repoRoot: repo }));
  }
});

await check("edge: empty command produces no decision work (hook passes it through)", async () => {
  assert.equal(scanBashCommand("", { repoRoot: repo }).blocked, false);
  assert.equal(scanBashCommand("   ", { repoRoot: repo }).blocked, false);
});

fs.rmSync(tmp, { recursive: true, force: true });
console.log("\n" + String(checks - failures) + "/" + String(checks) + " checks passed");
process.exit(failures === 0 ? 0 : 1);

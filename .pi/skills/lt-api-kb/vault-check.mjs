#!/usr/bin/env node
// vault-check — read-only sanity pass over an Obsidian vault. No dependencies.
// Reports .md notes with no frontmatter block and [[wikilinks]] that resolve to
// nothing (neither a note nor an attached file such as a .png).
// Usage: node .pi/skills/lt-api-kb/vault-check.mjs [vaultPath] [--strict] [--all]
//   vaultPath defaults to $LT_API_VAULT, else ~/Projects/vaults/lt-api.
//   --strict exits 1 on findings (default is advisory, exit 0).
//   --keys also checks the Conventions.md frontmatter keys (type, tags, date).
//   --all also scans _Templates/ and Conventions.md, which carry placeholder links.
// It never writes: read-only by construction.

import fs from "node:fs";
import os from "node:os";
import path from "node:path";

const argv = process.argv.slice(2);
const strict = argv.includes("--strict");
const all = argv.includes("--all");
const keysMode = argv.includes("--keys");
const vault = path.resolve(
  argv.find((a) => !a.startsWith("--")) ?? process.env.LT_API_VAULT ?? path.join(os.homedir(), "Projects/vaults/lt-api"),
);
const SKIP_DIRS = new Set([".obsidian", ".trash", ".git", "node_modules"]);
// Placeholder-bearing surfaces: templates and the conventions doc that shows syntax.
const skipFile = (p) =>
  !all && (p.includes("_Templates/") || p === "Conventions.md" || p.endsWith("/Conventions.md"));

if (!fs.existsSync(vault)) {
  console.error(`vault-check: no such path: ${vault}`);
  process.exit(2);
}

const rel = (p) => path.relative(vault, p).split(path.sep).join("/");
const files = [];
(function walk(dir) {
  for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
    if (e.name.startsWith(".") || SKIP_DIRS.has(e.name)) continue;
    const p = path.join(dir, e.name);
    if (e.isDirectory()) walk(p);
    else if (e.isFile()) files.push(rel(p));
  }
})(vault);

const notes = files.filter((f) => f.toLowerCase().endsWith(".md"));
const noteKeys = notes.map((f) => f.replace(/\.md$/i, "").toLowerCase());
const keys = new Set([...noteKeys, ...noteKeys.map((n) => n.split("/").pop())]);
const assets = new Set(files.filter((f) => !f.toLowerCase().endsWith(".md")).map((f) => f.split("/").pop().toLowerCase()));

// [[note]], [[note|alias]], [[note#heading]], [[asset.png]] — try the whole target
// first (titles can contain '#'), then the part before a heading anchor.
function resolves(inner) {
  const target = inner.split("|")[0].trim().toLowerCase();
  const candidates = [target, target.replace(/^\.?\//, "").split("#")[0].trim()];
  return candidates.some((c) => c && (keys.has(c) || keys.has(c.replace(/\.md$/, "")) || assets.has(c.split("/").pop())));
}

const noFrontmatter = [];
const dangling = [];
const missingKeys = [];
for (const f of notes) {
  if (skipFile(f)) continue;
  const text = fs.readFileSync(path.join(vault, f), "utf8");
  const head = text.replace(/^\uFEFF/, "").split("\n");
  const closed = head.slice(1, 40).findIndex((l) => l.trim() === "---");
  if (!(head[0].trim() === "---" && closed >= 0)) noFrontmatter.push(f);
  else if (keysMode) {
    // Conventions.md: every note carries type, tags and date.
    const fm = head.slice(1, closed + 1).join("\n");
    const lacking = ["type", "tags", "date"].filter((k) => !new RegExp(`^${k}:`, "m").test(fm));
    if (lacking.length) missingKeys.push(`${f} → missing ${lacking.join(", ")}`);
  }
  for (const m of text.matchAll(/\[\[([^\]\n]+)\]\]/g)) {
    if (!resolves(m[1])) dangling.push(`${f} → [[${m[1].trim()}]]`);
  }
}

const say = (label, list) => {
  if (!list.length) return;
  console.log(`\n${label} (${list.length}):`);
  for (const line of list.slice(0, 40)) console.log(`  - ${line}`);
  if (list.length > 40) console.log(`  … ${list.length - 40} more`);
};

console.log(`vault-check: ${vault}`);
console.log(`  notes: ${notes.length}   dangling links: ${dangling.length}   missing frontmatter: ${noFrontmatter.length}`);
say("dangling wikilinks", dangling);
say("notes without a frontmatter block", noFrontmatter);
say("missing frontmatter keys", missingKeys);
const bad = dangling.length + noFrontmatter.length + missingKeys.length;
console.log(bad ? `\n${bad} finding(s) — advisory, verify before you write.` : "\nclean.");
process.exit(strict && bad ? 1 : 0);

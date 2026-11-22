#!/usr/bin/env node
/**
 * codex-search-relay — a tiny host-side HTTP relay that lets the sandboxed
 * researcher's `web_search` spend the host's ChatGPT/codex SUBSCRIPTION instead
 * of pay-as-you-go API credits.
 *
 * Shape: the VM's `pi-web-access` is configured with
 *   {"provider":"openai","openaiResponsesUrl":"http://host.docker.internal:8787/responses"}
 * so its POST lands here; this process reads the host's `openai-codex` OAuth
 * credential from `~/.pi/agent/auth.json` (per request — always the freshest
 * token) and forwards the body verbatim to the codex backend
 * (`https://chatgpt.com/backend-api/codex/responses`) with the headers that
 * backend wants. No OpenAI credential ever enters a VM, and no `api.openai.com`
 * (pay-as-you-go) call is made.
 *
 * THREAT SHAPE: the only way to reach this process is the explicit sbx egress
 * rule for the researcher profile (`localhost:8787` — the sbx proxy rewrites
 * `host.docker.internal` to it). It binds LOOPBACK ONLY and has no other
 * listening surface, so a sandbox without that rule, and anything off-host,
 * cannot reach it. It holds no key of its own: it reads the same auth.json the
 * host's pi uses and forwards only to chatgpt.com.
 *
 * NO REFRESH LOGIC, BY DESIGN. Refreshing here would rotate the refresh token
 * out from under a concurrently running host pi session (rotation is
 * single-use), which can invalidate the host's stored credential. So this relay
 * fails CLOSED when the token is missing or near expiry and tells the operator
 * to run a host pi session — that session refreshes the shared file, and the
 * very next relay request picks it up (the file is re-read per request).
 *
 * Run:  node .pi/sandboxes/relay/codex-search-relay.mjs
 *       CODEX_RELAY_PORT=8787 CODEX_RELAY_AUTH=~/.pi/agent/auth.json \
 *         node .pi/sandboxes/relay/codex-search-relay.mjs   # both optional
 *
 * Zero dependencies; Node >= 18 (global fetch).
 */

import fs from "node:fs";
import http from "node:http";
import os from "node:os";
import path from "node:path";
import { Readable } from "node:stream";

const PORT = Number(process.env.CODEX_RELAY_PORT || 8787);
const AUTH_PATH = process.env.CODEX_RELAY_AUTH || path.join(os.homedir(), ".pi", "agent", "auth.json");
const UPSTREAM_URL = "https://chatgpt.com/backend-api/codex/responses";
/** Refuse a credential that expires within this window (ms). */
const MIN_TTL_MS = 120_000;
/** Give up on the codex backend after this long (ms). */
const UPSTREAM_TIMEOUT_MS = 90_000;

function logLine(start, method, status) {
  console.log(`${new Date().toISOString()} ${method} ${status} ${Date.now() - start}ms`);
}

/** Fail closed: 503 + the one-line fix, never a stale token upstream. */
function failClosed(res, code, message) {
  const body = JSON.stringify({ error: code, message });
  res.writeHead(503, { "content-type": "application/json; charset=utf-8" });
  res.end(body);
}

/**
 * Read the codex OAuth credential for THIS request. Returns `{access, accountId}`
 * or `{error: {code, message}}` — never throws (a broken/absent file is a
 * fail-closed condition with a fix in the message, not a crash).
 */
function readCredential() {
  const fix = "run a host pi session to refresh ~/.pi/agent/auth.json";
  const bad = (code, detail) => ({ error: { code, message: `${detail}; ${fix}` } });
  let raw;
  try {
    raw = fs.readFileSync(AUTH_PATH, "utf8");
  } catch (err) {
    return bad("codex_relay_auth_unreadable", `cannot read ${AUTH_PATH} (${err.code || err.message})`);
  }
  let store;
  try {
    store = JSON.parse(raw);
  } catch {
    return bad("codex_relay_auth_invalid", `${AUTH_PATH} is not valid JSON`);
  }
  const entry = store && store["openai-codex"];
  if (!entry) return bad("codex_relay_no_credential", `no "openai-codex" entry in ${AUTH_PATH}`);
  if (entry.type !== "oauth") return bad("codex_relay_not_oauth", `"openai-codex" in ${AUTH_PATH} is ${entry.type ?? "untyped"}, not oauth`);
  if (!entry.accountId) return bad("codex_relay_no_account_id", `"openai-codex" in ${AUTH_PATH} has no accountId`);
  const expires = Number(entry.expires);
  if (!Number.isFinite(expires) || expires - Date.now() < MIN_TTL_MS) {
    return bad("codex_relay_token_expiring", `"openai-codex" access token expires ${Number.isFinite(expires) ? "in under 2 minutes" : "at an unknown time"}`);
  }
  if (!entry.access) return bad("codex_relay_no_access", `"openai-codex" in ${AUTH_PATH} has no access token`);
  return { access: entry.access, accountId: entry.accountId };
}

/** Buffer the request body (web_search payloads are a few KB). */
async function readBody(req) {
  const chunks = [];
  for await (const chunk of req) chunks.push(chunk);
  return Buffer.concat(chunks);
}

/**
 * Upstream request headers: client content negotiation, relay credentials.
 *
 * The set is built from scratch rather than copied from the client, which is
 * what strips the VM's `Authorization: Bearer relay-managed`, cookies and any
 * hop-by-hop header: only the two negotiated headers below survive, plus the
 * codex trio and a fresh `content-length` for the buffered body.
 */
function upstreamHeaders(req, cred, bodyLength) {
  const header = (name, fallback) => {
    const value = req.headers[name];
    if (Array.isArray(value)) return value.join(", ");
    return typeof value === "string" && value.length > 0 ? value : fallback;
  };
  return {
    authorization: `Bearer ${cred.access}`,
    "chatgpt-account-id": cred.accountId,
    "openai-beta": "responses=experimental",
    originator: "pi",
    "content-type": header("content-type", "application/json"),
    accept: header("accept", "text/event-stream"),
    "content-length": String(bodyLength),
  };
}

/** Response headers worth passing through (the body's encoding is already decoded). */
function downstreamHeaders(upstream) {
  const out = {};
  for (const name of ["content-type", "cache-control"]) {
    const value = upstream.headers.get(name);
    if (value) out[name] = value;
  }
  return out;
}

const server = http.createServer(async (req, res) => {
  const started = Date.now();
  const method = req.method || "GET";
  if (method !== "POST") {
    res.writeHead(405, { "content-type": "application/json; charset=utf-8", allow: "POST" });
    res.end(JSON.stringify({ error: "codex_relay_post_only", message: "the codex search relay accepts POST only" }));
    logLine(started, method, 405);
    return;
  }

  const cred = readCredential();
  if (cred.error) {
    failClosed(res, cred.error.code, cred.error.message);
    logLine(started, method, 503);
    return;
  }

  const body = await readBody(req);
  let upstream;
  try {
    upstream = await fetch(UPSTREAM_URL, {
      method: "POST",
      headers: upstreamHeaders(req, cred, body.length),
      body,
      signal: AbortSignal.timeout(UPSTREAM_TIMEOUT_MS),
    });
  } catch (err) {
    const timedOut = err && (err.name === "TimeoutError" || err.name === "AbortError");
    res.writeHead(502, { "content-type": "application/json; charset=utf-8" });
    res.end(JSON.stringify({
      error: timedOut ? "codex_relay_upstream_timeout" : "codex_relay_upstream_unreachable",
      message: `${timedOut ? "no response from" : "cannot reach"} ${UPSTREAM_URL} (${UPSTREAM_TIMEOUT_MS}ms budget)`,
    }));
    logLine(started, method, 502);
    return;
  }

  res.writeHead(upstream.status, downstreamHeaders(upstream));
  if (!upstream.body) {
    res.end();
    logLine(started, method, upstream.status);
    return;
  }
  // Status is already on the wire: an upstream stream failure truncates the
  // response (the client sees an incomplete SSE stream) instead of pretending
  // success with a fabricated error body.
  const stream = Readable.fromWeb(upstream.body);
  stream.on("error", () => res.destroy());
  res.on("close", () => logLine(started, method, upstream.status));
  stream.pipe(res);
});

server.listen(PORT, "127.0.0.1", () => {
  console.log(`codex-search-relay listening on http://127.0.0.1:${PORT} (auth: ${AUTH_PATH})`);
});

server.on("error", (err) => {
  console.error(`codex-search-relay: cannot listen on 127.0.0.1:${PORT} (${err.code || err.message})`);
  process.exit(1);
});

for (const signal of ["SIGTERM", "SIGINT"]) {
  process.on(signal, () => {
    console.log(`codex-search-relay: ${signal}, closing`);
    server.close(() => process.exit(0));
  });
}

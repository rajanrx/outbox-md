import { test } from "node:test";
import assert from "node:assert/strict";
import crypto from "node:crypto";
import http from "node:http";
import { verifySignature, eventName, Runner, createServer } from "../runner.js";
import { loadConfig, parseEvents } from "../config.js";
import { buildArgs } from "../cli.js";

function sign(secret, body) {
  return "sha256=" + crypto.createHmac("sha256", secret).update(body).digest("hex");
}

test("verifySignature: valid passes / tampered fails / default-deny + allow-unsigned", () => {
  const body = Buffer.from('{"event":"comment.created"}');
  const secret = "shh";
  const good = sign(secret, body);

  assert.equal(verifySignature(secret, false, body, good), true, "valid passes");
  assert.equal(verifySignature(secret, false, Buffer.from('{"event":"x"}'), good), false, "tampered fails");
  assert.equal(verifySignature(secret, false, body, "sha256=deadbeef"), false, "wrong sig fails");
  assert.equal(verifySignature(secret, false, body, ""), false, "missing sig fails");
  assert.equal(verifySignature("", false, body, ""), false, "no secret default-denies");
  assert.equal(verifySignature("", false, body, "sha256=whatever"), false, "no secret default-denies even with a header");
  assert.equal(verifySignature("", true, body, ""), true, "no secret + allow-unsigned accepts");
  assert.equal(verifySignature("", true, body, "sha256=whatever"), true, "no secret + allow-unsigned ignores header");
});

test("eventName: header preferred, body fallback", () => {
  assert.equal(eventName("comment.created", Buffer.from("{}")), "comment.created");
  assert.equal(eventName("", Buffer.from('{"event":"comment.replied"}')), "comment.replied");
  assert.equal(eventName("", Buffer.from("not json")), "");
});

test("buildArgs: {prompt} substituted as a single argv element", () => {
  const args = buildArgs("claude -p {prompt} --allowedTools mcp__outbox-md__*", "do the thing now");
  assert.deepEqual(args, ["claude", "-p", "do the thing now", "--allowedTools", "mcp__outbox-md__*"]);
});

// --- HTTP integration: filtering + signing through a real server ---

function postEvent(baseUrl, { event, body, secret }) {
  return new Promise((resolve, reject) => {
    const headers = { "Content-Type": "application/json", "X-Outbox-Event": event };
    if (secret) headers["X-Outbox-Signature"] = sign(secret, Buffer.from(body));
    const req = http.request(baseUrl, { method: "POST", headers }, (res) => {
      res.resume();
      res.on("end", () => resolve(res.statusCode));
    });
    req.on("error", reject);
    req.end(body);
  });
}

test("event filtering + signing end-to-end", async () => {
  let calls = 0;
  const cfg = {
    secret: "shh",
    allowUnsigned: false,
    maxBodyBytes: 1 << 20,
    events: parseEvents("comment.created,comment.replied"),
    debounceMs: 5,
    agentMode: "cli",
  };
  const backend = { run: async () => { calls += 1; } };
  const server = createServer(cfg, backend);
  await new Promise((r) => server.listen(0, "127.0.0.1", r));
  const { port } = server.address();
  const base = `http://127.0.0.1:${port}/`;

  // allowed + correctly signed → 202
  assert.equal(await postEvent(base, { event: "comment.created", body: '{"event":"comment.created"}', secret: "shh" }), 202);
  // bad signature → 401
  const reqBad = await new Promise((resolve, reject) => {
    const r = http.request(base, { method: "POST", headers: { "X-Outbox-Event": "comment.created", "X-Outbox-Signature": "sha256=bad" } }, (res) => { res.resume(); res.on("end", () => resolve(res.statusCode)); });
    r.on("error", reject); r.end('{"event":"comment.created"}');
  });
  assert.equal(reqBad, 401, "bad signature rejected");
  // filtered event → 200 ignore
  assert.equal(await postEvent(base, { event: "comment.resolved", body: '{"event":"comment.resolved"}', secret: "shh" }), 200);

  await new Promise((r) => setTimeout(r, 40));
  assert.equal(calls, 1, "only the one allowed, signed event ran the agent");
  await new Promise((r) => server.close(r));
});

test("default-deny: no secret + no allow-unsigned → 401, agent never runs", async () => {
  let calls = 0;
  const cfg = {
    secret: "",
    allowUnsigned: false,
    maxBodyBytes: 1 << 20,
    events: parseEvents("comment.created"),
    debounceMs: 5,
    agentMode: "cli",
  };
  const server = createServer(cfg, { run: async () => { calls += 1; } });
  await new Promise((r) => server.listen(0, "127.0.0.1", r));
  const { port } = server.address();
  const base = `http://127.0.0.1:${port}/`;

  assert.equal(await postEvent(base, { event: "comment.created", body: '{"event":"comment.created"}' }), 401);
  await new Promise((r) => setTimeout(r, 30));
  assert.equal(calls, 0, "default-deny rejects unsigned");
  await new Promise((r) => server.close(r));
});

test("allow-unsigned: no secret + RUNNER_ALLOW_UNSIGNED → 202, agent runs", async () => {
  let calls = 0;
  const cfg = {
    secret: "",
    allowUnsigned: true,
    maxBodyBytes: 1 << 20,
    events: parseEvents("comment.created"),
    debounceMs: 5,
    agentMode: "cli",
  };
  const server = createServer(cfg, { run: async () => { calls += 1; } });
  await new Promise((r) => server.listen(0, "127.0.0.1", r));
  const { port } = server.address();
  const base = `http://127.0.0.1:${port}/`;

  assert.equal(await postEvent(base, { event: "comment.created", body: '{"event":"comment.created"}' }), 202);
  await new Promise((r) => setTimeout(r, 40));
  assert.equal(calls, 1, "unsigned accepted under explicit opt-in");
  await new Promise((r) => server.close(r));
});

test("oversize body → 413 before auth, agent never runs", async () => {
  let calls = 0;
  const cfg = {
    secret: "",
    allowUnsigned: true,
    maxBodyBytes: 16,
    events: parseEvents("comment.created"),
    debounceMs: 5,
    agentMode: "cli",
  };
  const server = createServer(cfg, { run: async () => { calls += 1; } });
  await new Promise((r) => server.listen(0, "127.0.0.1", r));
  const { port } = server.address();
  const base = `http://127.0.0.1:${port}/`;

  const big = '{"event":"comment.created","pad":"' + "x".repeat(4096) + '"}';
  assert.equal(await postEvent(base, { event: "comment.created", body: big }), 413);
  await new Promise((r) => setTimeout(r, 30));
  assert.equal(calls, 0, "oversize body rejected before run");
  await new Promise((r) => server.close(r));
});

// --- received ack: instant processing badge ---

test("received ack POSTed to /received for a signed comment.created, agent still runs", async () => {
  const ackHits = [];
  let ackNotify;
  const gotAck = new Promise((r) => (ackNotify = r));
  const stub = http.createServer((req, res) => {
    if (req.method === "POST" && req.url.endsWith("/received")) {
      ackHits.push(req.url);
      ackNotify();
    }
    res.writeHead(200);
    res.end();
  });
  await new Promise((r) => stub.listen(0, "127.0.0.1", r));
  const stubUrl = `http://127.0.0.1:${stub.address().port}`;

  let calls = 0;
  const cfg = {
    secret: "shh",
    allowUnsigned: false,
    maxBodyBytes: 1 << 20,
    events: parseEvents("comment.created,comment.replied"),
    debounceMs: 5,
    agentMode: "cli",
    serverUrl: stubUrl,
  };
  const server = createServer(cfg, { run: async () => { calls += 1; } });
  await new Promise((r) => server.listen(0, "127.0.0.1", r));
  const base = `http://127.0.0.1:${server.address().port}/`;

  const body = '{"event":"comment.created","commentId":"cmt-123"}';
  assert.equal(await postEvent(base, { event: "comment.created", body, secret: "shh" }), 202);
  await gotAck;
  assert.deepEqual(ackHits, ["/api/comments/cmt-123/received"]);
  await new Promise((r) => setTimeout(r, 40));
  assert.equal(calls, 1, "agent still runs after the ack");

  await new Promise((r) => server.close(r));
  await new Promise((r) => stub.close(r));
});

test("received ack failure is non-fatal: webhook still 202 and agent runs", async () => {
  let calls = 0;
  const cfg = {
    secret: "",
    allowUnsigned: true,
    maxBodyBytes: 1 << 20,
    events: parseEvents("comment.created"),
    debounceMs: 5,
    agentMode: "cli",
    serverUrl: "http://127.0.0.1:1", // connection refused fast
  };
  const server = createServer(cfg, { run: async () => { calls += 1; } });
  await new Promise((r) => server.listen(0, "127.0.0.1", r));
  const base = `http://127.0.0.1:${server.address().port}/`;

  const body = '{"event":"comment.created","commentId":"cmt-9"}';
  assert.equal(await postEvent(base, { event: "comment.created", body }), 202);
  await new Promise((r) => setTimeout(r, 40));
  assert.equal(calls, 1, "ack failure must not block the run");
  await new Promise((r) => server.close(r));
});

test("serverUrl default: RUNNER_SERVER_URL unset → MCP URL with /mcp stripped", () => {
  const saved = { s: process.env.RUNNER_SERVER_URL, m: process.env.OUTBOX_MCP_URL };
  delete process.env.RUNNER_SERVER_URL;
  delete process.env.OUTBOX_MCP_URL;
  try {
    assert.equal(loadConfig().serverUrl, "http://localhost:8181");
  } finally {
    if (saved.s !== undefined) process.env.RUNNER_SERVER_URL = saved.s;
    if (saved.m !== undefined) process.env.OUTBOX_MCP_URL = saved.m;
  }
});

// --- debounce + single-flight (executor-level, no wall-clock assertions) ---

test("debounce coalesces a burst into one run", async () => {
  let calls = 0;
  const r = new Runner(30, async () => { calls += 1; });
  for (let i = 0; i < 10; i++) {
    r.trigger();
    await new Promise((res) => setTimeout(res, 2));
  }
  await new Promise((res) => setTimeout(res, 80));
  assert.equal(calls, 1);
});

test("single-flight: triggers during a run coalesce into exactly one rerun", async () => {
  let calls = 0;
  let release;
  const gate = new Promise((res) => (release = res));
  let firstStarted;
  const started = new Promise((res) => (firstStarted = res));

  const r = new Runner(1, async () => {
    calls += 1;
    if (calls === 1) {
      firstStarted();
      await gate; // block run #1 so we can pile up triggers
    }
  });

  const p = r.execute(); // run #1 starts and blocks
  await started;
  for (let i = 0; i < 5; i++) r.execute(); // all coalesce into one pending rerun
  release(); // let run #1 finish; drain loop performs run #2
  await p;

  assert.equal(calls, 2, "initial run + exactly one coalesced rerun");
});

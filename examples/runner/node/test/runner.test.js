import { test } from "node:test";
import assert from "node:assert/strict";
import crypto from "node:crypto";
import http from "node:http";
import { verifySignature, eventName, Runner, createServer } from "../runner.js";
import { parseEvents } from "../config.js";
import { buildArgs } from "../cli.js";

function sign(secret, body) {
  return "sha256=" + crypto.createHmac("sha256", secret).update(body).digest("hex");
}

test("verifySignature: valid passes / tampered fails / no-secret accepts", () => {
  const body = Buffer.from('{"event":"comment.created"}');
  const secret = "shh";
  const good = sign(secret, body);

  assert.equal(verifySignature(secret, body, good), true, "valid passes");
  assert.equal(verifySignature(secret, Buffer.from('{"event":"x"}'), good), false, "tampered fails");
  assert.equal(verifySignature(secret, body, "sha256=deadbeef"), false, "wrong sig fails");
  assert.equal(verifySignature(secret, body, ""), false, "missing sig fails");
  assert.equal(verifySignature("", body, ""), true, "no secret accepts unsigned");
  assert.equal(verifySignature("", body, "sha256=whatever"), true, "no secret ignores header");
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

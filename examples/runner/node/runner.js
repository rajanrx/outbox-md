// Core runner: HMAC verify → event filter → debounce/single-flight → agent run.
// Stdlib only (node:crypto, node:http).
import crypto from "node:crypto";
import http from "node:http";

// verifySignature reports whether the request is authentic.
//   - secret set, header valid → accept; header missing/wrong → reject.
//   - secret falsy → default-deny: reject unless allowUnsigned is set (an
//     explicit opt-in for the unsigned, no-secret configuration).
// `body` MUST be the raw request Buffer — the exact bytes the server signed.
export function verifySignature(secret, allowUnsigned, body, header) {
  if (!secret) return Boolean(allowUnsigned);
  if (!header) return false;
  const got = header.startsWith("sha256=") ? header.slice("sha256=".length) : header;
  const want = crypto.createHmac("sha256", secret).update(body).digest("hex");
  const a = Buffer.from(got);
  const b = Buffer.from(want);
  // timingSafeEqual throws on length mismatch — guard length first, then compare.
  if (a.length !== b.length) return false;
  return crypto.timingSafeEqual(a, b);
}

// eventName prefers the X-Outbox-Event header, falling back to the body's
// "event" field.
export function eventName(header, body) {
  if (header) return header;
  try {
    return JSON.parse(body.toString("utf8")).event || "";
  } catch {
    return "";
  }
}

// Runner debounces triggers and serializes agent runs. A burst within the
// debounce window collapses into one run; a trigger that arrives while a run is
// in flight schedules exactly one rerun afterwards (never two concurrent runs).
export class Runner {
  constructor(debounceMs, run) {
    this.debounceMs = debounceMs;
    this.run = run; // () => Promise<void>
    this.timer = null;
    this.running = false;
    this.pending = false;
  }

  // trigger (re)arms the debounce timer.
  trigger() {
    if (this.timer) clearTimeout(this.timer);
    this.timer = setTimeout(() => {
      this.execute().catch((e) => console.error("runner: agent run failed:", e.message));
    }, this.debounceMs);
  }

  // execute runs the agent with single-flight semantics: if a run is in
  // progress it sets `pending` and returns; the in-flight loop drains pending
  // and runs once more.
  async execute() {
    if (this.running) {
      this.pending = true;
      return;
    }
    this.running = true;
    try {
      for (;;) {
        await this.run();
        if (this.pending) {
          this.pending = false;
          continue;
        }
        return;
      }
    } finally {
      this.running = false;
    }
  }
}

// readRawBody buffers the raw request body, capping accumulated length at `max`.
// On overflow it pauses the stream (so memory stays bounded — the pre-auth DoS
// fix) and rejects with a tagged error; the handler turns that into a 413. The
// socket is NOT destroyed here so the 413 response can still be written.
function readRawBody(req, max) {
  return new Promise((resolve, reject) => {
    const chunks = [];
    let len = 0;
    req.on("data", (c) => {
      len += c.length;
      if (len > max) {
        req.pause();
        const err = new Error("request body too large");
        err.code = "BODY_TOO_LARGE";
        reject(err);
        return;
      }
      chunks.push(c);
    });
    req.on("end", () => resolve(Buffer.concat(chunks)));
    req.on("error", reject);
  });
}

// makeHandler returns the webhook request handler: verify → filter → debounce.
// It responds fast and never blocks on the agent.
export function makeHandler(cfg, runner) {
  return async (req, res) => {
    if (req.url === "/healthz") {
      res.writeHead(200);
      res.end("ok\n");
      return;
    }
    if (req.method !== "POST") {
      res.writeHead(405);
      res.end("method not allowed\n");
      return;
    }
    let body;
    try {
      body = await readRawBody(req, cfg.maxBodyBytes);
    } catch (e) {
      if (e && e.code === "BODY_TOO_LARGE") {
        res.writeHead(413);
        res.end("request body too large\n");
      } else {
        res.writeHead(400);
        res.end("read error\n");
      }
      return;
    }
    if (!verifySignature(cfg.secret, cfg.allowUnsigned, body, req.headers["x-outbox-signature"])) {
      res.writeHead(401);
      res.end("invalid signature\n");
      return;
    }
    const event = eventName(req.headers["x-outbox-event"], body);
    if (!cfg.events.has(event)) {
      res.writeHead(200);
      res.end("ignored\n");
      return;
    }
    // Acknowledge receipt to the server BEFORE the debounced spawn so the human's
    // "AI processing…" badge appears within ~1s — even if the agent later dies
    // before it claims. Best-effort, non-blocking, non-fatal.
    ackReceived(cfg, event, body);
    runner.trigger();
    res.writeHead(202);
    res.end("accepted\n");
  };
}

// ackReceived fires a best-effort, non-awaited POST to the server's untokened
// /received endpoint for the comment carried by a comment.created/comment.replied
// event, so the processing badge lights up instantly. It never throws into the
// caller and never blocks: any failure is logged at most. It is a no-op when the
// event carries no comment, no server URL is configured, or the payload has no
// comment id.
export function ackReceived(cfg, event, body) {
  if (event !== "comment.created" && event !== "comment.replied") return;
  if (!cfg.serverUrl) return;
  let commentId = "";
  try {
    commentId = JSON.parse(body.toString("utf8")).commentId || "";
  } catch {
    commentId = "";
  }
  if (!commentId) return;
  const url = `${cfg.serverUrl.replace(/\/+$/, "")}/api/comments/${encodeURIComponent(commentId)}/received`;
  try {
    const req = http.request(url, { method: "POST", timeout: 3000 }, (res) => res.resume());
    req.on("timeout", () => req.destroy());
    req.on("error", (e) => console.error("runner: received-ack failed:", e.message));
    req.end();
  } catch (e) {
    console.error("runner: received-ack failed:", e.message);
  }
}

// createServer builds the HTTP server (webhook at "/", health at "/healthz").
export function createServer(cfg, backend) {
  const runner = new Runner(cfg.debounceMs, async () => {
    console.log(`runner: invoking agent (mode=${cfg.agentMode})`);
    await backend.run();
    console.log("runner: agent run complete");
  });
  return http.createServer(makeHandler(cfg, runner));
}

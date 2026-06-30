// Core runner: HMAC verify → event filter → debounce/single-flight → agent run.
// Stdlib only (node:crypto, node:http).
import crypto from "node:crypto";
import http from "node:http";

// verifySignature reports whether the request is authentic.
//   - secret falsy  → signing not enforced; accept (mirrors the server, which
//     only signs when a secret is configured).
//   - secret set, header missing/wrong → reject.
// `body` MUST be the raw request Buffer — the exact bytes the server signed.
export function verifySignature(secret, body, header) {
  if (!secret) return true;
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

function readRawBody(req) {
  return new Promise((resolve, reject) => {
    const chunks = [];
    req.on("data", (c) => chunks.push(c));
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
    const body = await readRawBody(req);
    if (!verifySignature(cfg.secret, body, req.headers["x-outbox-signature"])) {
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
    runner.trigger();
    res.writeHead(202);
    res.end("accepted\n");
  };
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

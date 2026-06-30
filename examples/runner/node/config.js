// Configuration, read from the environment. Env var names and defaults are
// identical across the Go, Node, and Python runners so all three are
// functionally equivalent.

// The instruction handed to the agent on each run. Encodes the outbox loop and
// the human-only invariant so a fresh agent process knows the rules.
export const DEFAULT_PROMPT =
  "Process the open outbox comments using the outbox-md tools — " +
  "read each comment's excerpt + thread, then propose_suggestion (a tracked-change edit) " +
  "or reply_in_thread; honor the anti-sycophancy guidance (a comment is not an order, " +
  "disagree when the evidence warrants); never resolve, accept, or approve (those are human-only).";

// Targets Claude Code in headless mode and pre-authorizes the outbox-md MCP
// tools so the run is non-interactive.
export const DEFAULT_AGENT_CMD = "claude -p {prompt} --allowedTools mcp__outbox-md__*";

function env(key, def) {
  const v = process.env[key];
  return v !== undefined && v !== "" ? v : def;
}

// parseEvents turns a comma-separated list into a Set, trimming blanks.
export function parseEvents(csv) {
  const set = new Set();
  for (const e of csv.split(",")) {
    const t = e.trim();
    if (t) set.add(t);
  }
  return set;
}

export function loadConfig() {
  let debounceMs = parseInt(env("RUNNER_DEBOUNCE_MS", "1500"), 10);
  if (Number.isNaN(debounceMs) || debounceMs < 0) debounceMs = 1500;
  return {
    addr: env("RUNNER_ADDR", ":8787"),
    secret: process.env.OUTBOX_WEBHOOK_SECRET || "",
    events: parseEvents(env("RUNNER_EVENTS", "comment.created,comment.replied")),
    debounceMs,
    agentMode: env("RUNNER_AGENT_MODE", "cli"),
    agentCmd: env("RUNNER_AGENT_CMD", DEFAULT_AGENT_CMD),
    prompt: env("RUNNER_PROMPT", DEFAULT_PROMPT),
    mcpUrl: env("OUTBOX_MCP_URL", "http://localhost:8181/mcp"),
    apiKey: process.env.ANTHROPIC_API_KEY || "",
    model: env("ANTHROPIC_MODEL", "claude-sonnet-4-5"),
    agentId: env("RUNNER_AGENT_ID", "outbox-runner"),
  };
}

// hostPort splits a ":8787" / "0.0.0.0:8787" address into { host, port } for
// Node's http.Server.listen (which takes them separately).
export function hostPort(addr) {
  const i = addr.lastIndexOf(":");
  const host = i > 0 ? addr.slice(0, i) : "";
  const port = parseInt(addr.slice(i + 1), 10) || 8787;
  return { host: host || undefined, port };
}

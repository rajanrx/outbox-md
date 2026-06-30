#!/usr/bin/env node
// Reference webhook runner for outbox-md (Node): the client-side,
// bring-your-own-agent counterpart to the interactive MCP. Receives outbox-md
// webhooks, verifies them, and drives a single-agent loop (claim → propose/reply)
// over the outbox-md MCP tools. See examples/runner/README.md.
import { loadConfig, hostPort } from "./config.js";
import { createServer } from "./runner.js";
import { CLIBackend } from "./cli.js";
import { APIBackend } from "./api.js";

function newBackend(cfg) {
  if (cfg.agentMode === "api") return new APIBackend(cfg);
  return new CLIBackend(cfg.agentCmd, cfg.prompt); // default: cli
}

function main() {
  const cfg = loadConfig();
  const server = createServer(cfg, newBackend(cfg));
  const { host, port } = hostPort(cfg.addr);

  server.listen(port, host, () => {
    let signing;
    if (cfg.secret) {
      signing = "on (HMAC-SHA256 enforced)";
    } else if (cfg.allowUnsigned) {
      signing = "off (RUNNER_ALLOW_UNSIGNED set — accepting UNSIGNED, NOT recommended)";
    } else {
      signing = "default-deny (no secret set — refusing unsigned)";
      console.warn(
        "runner: refusing unsigned webhooks; set OUTBOX_WEBHOOK_SECRET, " +
          "or RUNNER_ALLOW_UNSIGNED=1 to allow unsigned (NOT recommended).",
      );
    }
    console.log(`outbox-runner listening on ${cfg.addr}`);
    console.log(`  agent mode : ${cfg.agentMode}`);
    console.log(`  signing    : ${signing}`);
    console.log(`  events     : ${[...cfg.events].join(", ")}`);
    console.log(`  debounce   : ${cfg.debounceMs}ms`);
  });
}

main();

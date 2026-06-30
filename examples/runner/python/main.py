#!/usr/bin/env python3
"""Reference webhook runner for outbox-md (Python).

The client-side, bring-your-own-agent counterpart to the interactive MCP.
Receives outbox-md webhooks, verifies them, and drives a single-agent loop
(claim -> propose/reply) over the outbox-md MCP tools.

Run:  python main.py        (see examples/runner/README.md)
"""

import sys

from api import APIBackend
from cli import CLIBackend
from config import host_port, load_config
from runner import create_server


def new_backend(cfg):
    if cfg.agent_mode == "api":
        return APIBackend(cfg)
    return CLIBackend(cfg.agent_cmd, cfg.prompt)  # default: cli


def main():
    cfg = load_config()
    host, port = host_port(cfg.addr)
    server = create_server(cfg, new_backend(cfg), host, port)

    if cfg.secret:
        signing = "on (HMAC-SHA256 enforced)"
    elif cfg.allow_unsigned:
        signing = "off (RUNNER_ALLOW_UNSIGNED set — accepting UNSIGNED, NOT recommended)"
    else:
        signing = "default-deny (no secret set — refusing unsigned)"
        print(
            "runner: refusing unsigned webhooks; set OUTBOX_WEBHOOK_SECRET, "
            "or RUNNER_ALLOW_UNSIGNED=1 to allow unsigned (NOT recommended).",
            file=sys.stderr,
        )
    print(f"outbox-runner listening on {cfg.addr}")
    print(f"  agent mode : {cfg.agent_mode}")
    print(f"  signing    : {signing}")
    print(f"  events     : {', '.join(sorted(cfg.events))}")
    print(f"  debounce   : {cfg.debounce_ms}ms")

    try:
        server.serve_forever()
    except KeyboardInterrupt:
        server.shutdown()


if __name__ == "__main__":
    main()

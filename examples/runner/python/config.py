"""Configuration, read from the environment.

Env var names and defaults are identical across the Go, Node, and Python
runners so all three are functionally equivalent.
"""

import os
from dataclasses import dataclass, field

# The instruction handed to the agent on each run. Encodes the outbox loop and
# the human-only invariant so a fresh agent process knows the rules.
DEFAULT_PROMPT = (
    "Process the open outbox comments using the outbox-md tools — "
    "read each comment's excerpt + thread, then propose_suggestion (a tracked-change edit) "
    "or reply_in_thread; honor the anti-sycophancy guidance (a comment is not an order, "
    "disagree when the evidence warrants); never resolve, accept, or approve (those are human-only)."
)

# Targets Claude Code in headless mode and pre-authorizes the outbox-md MCP
# tools so the run is non-interactive.
DEFAULT_AGENT_CMD = "claude -p {prompt} --allowedTools mcp__outbox-md__*"


# Request-body cap when RUNNER_MAX_BODY_BYTES is unset or invalid: 1 MiB.
DEFAULT_MAX_BODY_BYTES = 1024 * 1024


def _env(key: str, default: str) -> str:
    v = os.environ.get(key)
    return v if v else default


def _env_bool(key: str) -> bool:
    """Report whether key is set to '1' or 'true' (case-insensitive)."""
    return os.environ.get(key, "").strip().lower() in ("1", "true")


def parse_events(csv: str) -> set:
    """Turn a comma-separated list into a set, trimming blanks."""
    return {e.strip() for e in csv.split(",") if e.strip()}


@dataclass
class Config:
    addr: str = ":8787"
    secret: str = ""
    allow_unsigned: bool = False
    max_body_bytes: int = DEFAULT_MAX_BODY_BYTES
    events: set = field(default_factory=set)
    debounce_ms: int = 1500
    agent_mode: str = "cli"
    agent_cmd: str = DEFAULT_AGENT_CMD
    prompt: str = DEFAULT_PROMPT
    mcp_url: str = "http://localhost:8181/mcp"
    api_key: str = ""
    model: str = "claude-sonnet-4-5"
    agent_id: str = "outbox-runner"


def load_config() -> Config:
    try:
        debounce_ms = int(_env("RUNNER_DEBOUNCE_MS", "1500"))
        if debounce_ms < 0:
            debounce_ms = 1500
    except ValueError:
        debounce_ms = 1500
    try:
        max_body_bytes = int(_env("RUNNER_MAX_BODY_BYTES", str(DEFAULT_MAX_BODY_BYTES)))
        if max_body_bytes <= 0:
            max_body_bytes = DEFAULT_MAX_BODY_BYTES
    except ValueError:
        max_body_bytes = DEFAULT_MAX_BODY_BYTES
    return Config(
        addr=_env("RUNNER_ADDR", ":8787"),
        secret=os.environ.get("OUTBOX_WEBHOOK_SECRET", ""),
        allow_unsigned=_env_bool("RUNNER_ALLOW_UNSIGNED"),
        max_body_bytes=max_body_bytes,
        events=parse_events(_env("RUNNER_EVENTS", "comment.created,comment.replied")),
        debounce_ms=debounce_ms,
        agent_mode=_env("RUNNER_AGENT_MODE", "cli"),
        agent_cmd=_env("RUNNER_AGENT_CMD", DEFAULT_AGENT_CMD),
        prompt=_env("RUNNER_PROMPT", DEFAULT_PROMPT),
        mcp_url=_env("OUTBOX_MCP_URL", "http://localhost:8181/mcp"),
        api_key=os.environ.get("ANTHROPIC_API_KEY", ""),
        model=_env("ANTHROPIC_MODEL", "claude-sonnet-4-5"),
        agent_id=_env("RUNNER_AGENT_ID", "outbox-runner"),
    )


def host_port(addr: str):
    """Split ':8787' / '0.0.0.0:8787' into (host, port) for HTTPServer."""
    i = addr.rfind(":")
    host = addr[:i] if i > 0 else ""
    try:
        port = int(addr[i + 1:])
    except ValueError:
        port = 8787
    return host, port

"""api-mode backend — STUB.

The Go runner (examples/runner/go/api.go) is the canonical, fully-implemented
api mode: it connects to the outbox-md MCP server as a client over
Streamable-HTTP, walks the outbox, and calls the Anthropic Messages API to
decide each response. This Python port intentionally ships a stub instead of
full parity — see examples/runner/README.md ("api-mode asymmetry").

To implement it here, fill in run() with the same loop the Go version uses:

  1. Connect an MCP client to cfg.mcp_url (Streamable-HTTP). The official
     Python SDK is the `mcp` package — adding it makes this a non-stdlib
     runner, which is why it is left out by default.
  2. call_tool("list_open_comments")            -> the open outbox
  3. for each comment:
       call_tool("claim_comment", {commentIds:[id], agent})   -> { token }
       call_tool("read_doc", {docId})                          -> full content
       ask your LLM how to respond (honor the anti-sycophancy guidance):
         POST https://api.anthropic.com/v1/messages
         headers: x-api-key, anthropic-version: 2023-06-01, content-type
         body: { model: cfg.model, max_tokens, system, messages }
       then EITHER
         call_tool("propose_suggestion", {commentId, token, content, agent})
       OR
         call_tool("reply_in_thread",   {commentId, token, body, agent})
  4. Never resolve/accept/approve — those are human-only (no MCP tool exists).
"""


class APIBackend:
    def __init__(self, cfg):
        self.cfg = cfg

    def run(self):
        raise NotImplementedError(
            "api mode: implement the LLM call — see README. "
            "The Go runner (examples/runner/go) is the canonical api-mode implementation; "
            f"this Python stub would connect to the outbox-md MCP at {self.cfg.mcp_url}, "
            "then list_open_comments -> claim_comment -> call your LLM -> "
            "propose_suggestion/reply_in_thread. "
            "Use RUNNER_AGENT_MODE=cli (the default) for a working, zero-API-key setup."
        )

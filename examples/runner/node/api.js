// api-mode backend — STUB.
//
// The Go runner (examples/runner/go/api.go) is the canonical, fully-implemented
// api mode: it connects to the outbox-md MCP server as a client over
// Streamable-HTTP, walks the outbox, and calls the Anthropic Messages API to
// decide each response. This Node port intentionally ships a stub instead of
// full parity — see examples/runner/README.md ("api-mode asymmetry").
//
// To implement it here, fill in run() with the same loop the Go version uses:
//
//   1. Connect an MCP client to cfg.mcpUrl (Streamable-HTTP). The official
//      TypeScript SDK is @modelcontextprotocol/sdk — adding it makes this a
//      non-stdlib runner, which is why it is left out by default.
//   2. callTool("list_open_comments")            → the open outbox
//   3. for each comment:
//        callTool("claim_comment", {commentIds:[id], agent})   → { token }
//        callTool("read_doc", {docId})                          → full content
//        ask your LLM how to respond (honor the anti-sycophancy guidance):
//          POST https://api.anthropic.com/v1/messages
//          headers: x-api-key, anthropic-version: 2023-06-01, content-type
//          body: { model: cfg.model, max_tokens, system, messages }
//        then EITHER
//          callTool("propose_suggestion", {commentId, token, content, agent})
//        OR
//          callTool("reply_in_thread",   {commentId, token, body, agent})
//   4. Never resolve/accept/approve — those are human-only (no MCP tool exists).

export class APIBackend {
  constructor(cfg) {
    this.cfg = cfg;
  }

  async run() {
    throw new Error(
      "api mode: implement the LLM call — see README. " +
        "The Go runner (examples/runner/go) is the canonical api-mode implementation; " +
        `this Node stub would connect to the outbox-md MCP at ${this.cfg.mcpUrl}, ` +
        "then list_open_comments → claim_comment → call your LLM → propose_suggestion/reply_in_thread. " +
        "Use RUNNER_AGENT_MODE=cli (the default) for a working, zero-API-key setup.",
    );
  }
}

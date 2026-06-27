# outbox-md

> Local-first, agent-agnostic review for AI-generated Markdown specs.

**Status:** pre-alpha — walking skeleton.

Read and inline-annotate AI-generated Markdown. Your comments never edit the document directly — they enter an ordered **outbox** and are processed asynchronously by any AI agent connected over MCP. The agent proposes a tracked change or replies in a thread; you accept, and the file is rewritten and versioned. The document is never corrupted.

- **Local-first** — runs in one Docker container pointed at a folder of `.md` files.
- **Bring-your-own-agent** — ships **no LLM credentials** and embeds no model. Any agent connects via MCP.
- **Zero secrets** — nothing leaves your machine.

## Design

See the full design spec: [`docs/specs/2026-06-27-outbox-md-design.md`](docs/specs/2026-06-27-outbox-md-design.md).
Implementation plan: [`docs/plans/2026-06-27-phase0-and-v1-core.md`](docs/plans/2026-06-27-phase0-and-v1-core.md).

## License

MIT — see [LICENSE](LICENSE).

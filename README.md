# outbox-md

> Local-first, agent-agnostic review for AI-generated Markdown specs.

**Status:** pre-alpha — walking skeleton.

Read and inline-annotate AI-generated Markdown. Your comments never edit the document directly — they enter an ordered **outbox** and are processed asynchronously by any AI agent connected over MCP. The agent proposes a tracked change or replies in a thread; you accept, and the file is rewritten and versioned. The document is never corrupted.

- **Local-first** — runs in one Docker container pointed at a folder of `.md` files.
- **Bring-your-own-agent** — ships **no LLM credentials** and embeds no model. Any agent connects via MCP.
- **Zero secrets** — nothing leaves your machine.

## Quickstart (walking skeleton)

```bash
docker build -t outbox-md:dev .
mkdir -p sample && printf "# Spec\n\nHello world\n" > sample/spec.md
docker run --rm -p 8080:8080 -e OUTBOX_DEV=1 -v "$PWD/sample:/data" outbox-md:dev
# open http://localhost:8080
```

Agents connect over MCP at `http://localhost:8080/mcp` (Streamable HTTP). With `OUTBOX_DEV=1`, the agent loop can also be driven over HTTP for testing (`/api/dev/claim`, `/api/dev/propose`).

## Design

See the full design spec: [`docs/specs/2026-06-27-outbox-md-design.md`](docs/specs/2026-06-27-outbox-md-design.md).
Implementation plan: [`docs/plans/2026-06-27-phase0-and-v1-core.md`](docs/plans/2026-06-27-phase0-and-v1-core.md).

## License

MIT — see [LICENSE](LICENSE).

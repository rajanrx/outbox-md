# outbox-md

> Local-first, agent-agnostic review for AI-generated Markdown specs.

<div align="center">

<a href="https://www.youtube.com/watch?v=CNT49m0xBOY">
  <img src="docs/media/explainer-thumb.png" alt="Watch: What is outbox-md?" width="100%">
</a>

<em>▶ <b><a href="https://www.youtube.com/watch?v=CNT49m0xBOY">What is outbox-md?</a></b> — the 2-minute intro</em>

</div>

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

## Watch & learn

The 2-minute **intro** is at the top. Two more, for going deeper:

<div align="center">
<table>
<tr>
<td width="50%" valign="top">
  <a href="https://www.youtube.com/watch?v=0RwuXV6jmKY"><img src="docs/media/tutorial-thumb.png" alt="Tutorial" width="100%"></a>
  <p align="center"><b>▶ <a href="https://www.youtube.com/watch?v=0RwuXV6jmKY">Using outbox-md</a></b><br/>Run it → comment → connect an agent → accept</p>
</td>
<td width="50%" valign="top">
  <a href="https://www.youtube.com/watch?v=VmuwLniMU9M"><img src="docs/media/deepdive-thumb.png" alt="Deep dive" width="100%"></a>
  <p align="center"><b>▶ <a href="https://www.youtube.com/watch?v=VmuwLniMU9M">Architecture &amp; Vision</a></b><br/>The hard parts and where it's headed — for builders</p>
</td>
</tr>
</table>
</div>

A podcast-style Q&A between **Andrew** and **Ava** — narration via [edge-tts](https://github.com/rany2/edge-tts) (no API key), slides via Pillow, assembly via ffmpeg. Background music: *"Coffee & Streets"* by [Aylex](https://aylex.net) (no-copyright), mixed at 16%.

## Design

See the full design spec: [`docs/specs/2026-06-27-outbox-md-design.md`](docs/specs/2026-06-27-outbox-md-design.md).
Implementation plan: [`docs/plans/2026-06-27-phase0-and-v1-core.md`](docs/plans/2026-06-27-phase0-and-v1-core.md).

## License

MIT — see [LICENSE](LICENSE).
